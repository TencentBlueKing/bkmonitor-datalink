package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TestReEvaluatingAStoredRecordDoesNotBreakItsOwnHistory pins the shape that
// made a restart reject the process's own state mutation.
//
// A Slot evaluates a record and writes the state; the process stops before the
// Slot is recorded as finished; on the way back the same record is evaluated
// once more. The history is validated as strictly increasing by (SourceTime,
// RecordID) with equality included, so appending that record a second time
// produced "state history points must be uniquely ordered" - the process
// reporting its own write as invalid. In production this appeared only inside
// restart windows and cleared on its own once the window passed.
//
// Two neighbouring shapes were tried and are deliberately not covered here: a
// record older than the stored history, and two records sharing a SourceTime
// with a descending RecordID. Both are rejected earlier, by the StatePoint
// identity check, so this fixture cannot exercise them; that is a statement
// about the fixture, not a claim that production cannot reach them.
//
// The plan must retain more than one point. With retention 1 the append is
// truncated straight back to a single point and the shape disappears, which is
// how an earlier version of this test reported that nothing reproduced.
func TestReEvaluatingAStoredRecordDoesNotBreakItsOwnHistory(t *testing.T) {
	plan := compiledWindow(t, 3, 3)
	fingerprint := plan.Levels()[0].Fingerprints().Detect
	point := func(id string, sourceTime int64) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}}}
	}
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	stored := []execution.StateHistoryPoint{point("d", 180), point("f", 300)}

	request := requestFixtureForPlan(t, plan, record, stored)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("re-evaluating a stored record was rejected: %v", err)
	}
	mutations := 0
	for _, evaluated := range result.Plans {
		for _, state := range evaluated.StateResults {
			mutations++
			record := recordLeftBehind(t, state.Mutation)
			previous, previousID := int64(-1), ""
			for index, p := range record {
				if index > 0 && (p.SourceTime < previous ||
					(p.SourceTime == previous && p.RecordID <= previousID)) {
					t.Fatalf("history is not strictly increasing at index %d: source %d after %d", index, p.SourceTime, previous)
				}
				previous, previousID = p.SourceTime, p.RecordID
			}
			if len(record) != len(stored) {
				t.Fatalf("re-evaluating a stored record changed the point count: got %d, want %d",
					len(record), len(stored))
			}
		}
	}
	if mutations == 0 {
		t.Fatal("no state mutation was produced, the shape was not exercised")
	}
}

// Summarize is the one place that holds both how much of the window arrived and
// how much was asked for. It had held both and published neither since it was
// written, which is why HISTORY_WARMING reached every consumer -- metrics,
// diagnostics, the deployment page -- as a bare label that a reader could not
// act on: a window one point from converging and a window that can never
// converge produce the identical word on every round.
//
// Driven through Evaluate rather than by calling Summarize directly, because
// the defect was never in Summarize. It was in the wiring between it and
// everything downstream, and a check that calls the source function tests the
// half that was already fine.
func TestEvaluationReportsHowShortTheDetectionWindowWas(t *testing.T) {
	// Window size 2 against a single record: one valid position of the two the
	// level requires, which is the shape of a series younger than its window.
	plan := compiledWindow(t, 2, 1)
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}

	request := requestFixtureForPlan(t, plan, record, nil)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(result.Plans))
	}
	short := result.Plans[0].HistoryCoverage
	if short.Levels == 0 {
		t.Fatal("no window was counted: a Slot that summarised nothing and a Slot whose windows " +
			"were all complete both report zero short, and they are opposite statements")
	}
	if short.Short != 1 {
		t.Fatalf("short = %d, want the one incomplete window counted", short.Short)
	}
	if short.WorstValid != 1 || short.WorstRequired != 2 {
		t.Fatalf("worst pair = %d/%d, want 1/2 -- the shortfall is the whole signal and it has to "+
			"travel as one window's pair", short.WorstValid, short.WorstRequired)
	}

	// And the complete case, which must not read as "nothing measured". A
	// single-point window that the record itself fills.
	full := requestFixtureForPlan(t, compiledWindow(t, 1, 1), record, nil)
	filled, err := newEvaluator(t).Evaluate(context.Background(), full)
	if err != nil {
		t.Fatalf("Evaluate() on a complete window error = %v", err)
	}
	complete := filled.Plans[0].HistoryCoverage
	if complete.Levels == 0 {
		t.Fatal("a complete window was not counted at all")
	}
	if complete.Short != 0 {
		t.Fatalf("short = %d on a window the record fills, want 0", complete.Short)
	}
	if complete.Guarded != 0 {
		t.Fatalf("guarded = %d on a window decided from the window itself, want 0", complete.Guarded)
	}
}

// The number a window is asked for never exceeds what the state keeps for it.
//
// Summarize gives up before walking anything when it is asked for more
// positions than the Level retains, and returns the same shape as a window that
// was walked and found empty -- which is counted as an empty window and rendered
// as 取不到数据, sending a reader to look for a metric that stopped. The real
// condition would be a configuration the store can never satisfy: permanent,
// and invisible behind wording for something transient.
//
// It cannot currently happen, and this is the half of the reason that lives
// here. strategy/compiler.go builds RetentionPoints and RequiredDetectHistoryPoints
// from one variable so they are equal rather than merely ordered, and this is
// the call site that hands that same RequiredDetectHistoryPoints to Summarize as
// the argument it gets compared against. Run over the compiler's real output, so
// a change to how either number is derived fails here rather than turning a dead
// branch into a live one three files away.
//
// The state package pins the other half: that Align refuses to build a window
// for any requirement the refusal would otherwise catch.
func TestNoWindowIsAskedForMorePositionsThanTheStateRetains(t *testing.T) {
	for _, shape := range []struct{ windowSize, requiredAnomalies uint32 }{
		{1, 1}, {2, 1}, {9, 3}, {16, 16},
	} {
		plan := compiledWindow(t, shape.windowSize, shape.requiredAnomalies)
		requirements, err := planLevelRequirements(plan)
		if err != nil {
			t.Fatalf("planLevelRequirements on a %d/%d window: %v", shape.windowSize,
				shape.requiredAnomalies, err)
		}
		levels := plan.Levels()
		if len(requirements) != len(levels) {
			t.Fatalf("requirements = %d for %d levels", len(requirements), len(levels))
		}
		for index, level := range levels {
			asked := level.RequiredDetectHistoryPoints()
			retained := requirements[index].RetentionPoints
			if asked == 0 {
				t.Errorf("level %d asks for no positions at all, which Align would have rejected "+
					"when the window was built", level.Definition().LevelID)
			}
			if retained < asked {
				t.Errorf("level %d is asked for %d positions and retains %d: Summarize refuses to "+
					"walk that window and reports it as one that held no points, which the page "+
					"renders as data that stopped arriving rather than as a window the store can "+
					"never fill", level.Definition().LevelID, asked, retained)
			}
		}
	}
}

// The one fact that says whether a short window is a strategy problem.
//
// A window that stays short for ever is two unrelated situations. A strategy
// whose aggregation dimensions contain something that changes -- a pod name, a
// container, a task id -- gets a *different* series every few rounds; each is
// genuinely new, starts its window from nothing, and is replaced before it can
// fill. A series whose data is missing has been evaluated for hours and is
// short because the points are not there. The first needs the strategy edited,
// the second needs someone to go find the data, and every count published about
// them was identical: same completeness, same shortfall, same rounds counter
// climbing for ever.
//
// Whether this round loaded any history for the series separates them, it is
// decided before the window is summarised, and it was being dropped one line
// later. Driven through Evaluate, because the wiring is the part that was
// missing; Observe itself was always able to count it.
func TestEvaluationSaysWhetherAShortWindowsSeriesWasEverSeenBefore(t *testing.T) {
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}

	// Same short window twice -- one point of the two required -- and the only
	// difference is whether the state load found anything.
	known := requestFixtureForPlan(t, compiledWindow(t, 2, 1), record, nil)
	seenBefore, err := newEvaluator(t).Evaluate(context.Background(), known)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	settled := seenBefore.Plans[0].HistoryCoverage
	if settled.Short != 1 {
		t.Fatalf("short = %d, want the one incomplete window", settled.Short)
	}
	if settled.Fresh != 0 || settled.ShortFresh != 0 {
		t.Fatalf("fresh = %d/%d on a series whose state was found, want 0 and 0: counting a known "+
			"series as new is what sends a reader to edit a strategy whose dimensions are fine",
			settled.Fresh, settled.ShortFresh)
	}

	fresh := requestFixtureForPlan(t, compiledWindow(t, 2, 1), record, nil)
	fresh.State.Items[0].Status = execution.StateMissingWarming
	neverSeen, err := newEvaluator(t).Evaluate(context.Background(), fresh)
	if err != nil {
		t.Fatalf("Evaluate() on a series with no persisted state error = %v", err)
	}
	churning := neverSeen.Plans[0].HistoryCoverage
	if churning.Short != settled.Short || churning.Levels != settled.Levels {
		t.Fatalf("the two runs summarised differently (%+v vs %+v); they are meant to differ in one "+
			"field only, or this proves nothing about that field", churning, settled)
	}
	if churning.Fresh != 1 {
		t.Fatalf("fresh = %d, want 1: no state was loaded for this series, and nothing downstream "+
			"can tell a churning strategy from missing data unless this crosses", churning.Fresh)
	}
	if churning.ShortFresh != 1 {
		t.Fatalf("short fresh = %d, want 1: the short window is the one the question is about, and "+
			"its own count is what carries the answer", churning.ShortFresh)
	}
}

// A Level reporting a verdict it is no longer allowed to revise.
//
// Persisted WARMING or GAPPED is forced onto every later evaluation until the
// loaded history already forms a full window at the *last processed* record --
// not at the record being evaluated. So a window that filled this round keeps
// reporting the older verdict, while the position counts published beside it
// are computed from the window now.
//
// The two halves then disagree on the page, in the direction that costs work: a
// row says the window is short and the counts on the same row say it is full,
// and whoever reads it goes to look at data that is arriving correctly. The
// guard is right to hold -- releasing it would let recovery be decided off a
// window that is complete only because the missing positions aged out -- so
// what has to change is that the held verdict is reported as though it were
// this round's finding.
func TestEvaluationSaysWhenAWindowVerdictWasNotDecidedThisRound(t *testing.T) {
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}

	// A one-point window, which this record fills by itself, under a persisted
	// WARMING whose own last processed moment has nothing in it. The guard
	// cannot converge there, so it holds over a window that is complete here.
	request := requestFixtureForPlan(t, compiledWindow(t, 1, 1), record, nil)
	request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryWarming
	request.State.Items[0].Levels[0].LastProcessedEventTime = 60

	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	coverage := result.Plans[0].HistoryCoverage
	if coverage.Levels != 1 {
		t.Fatalf("levels = %d, want the one window summarised", coverage.Levels)
	}
	if coverage.Short != 0 {
		t.Fatalf("short = %d, want 0: the record fills this window, and that is the point -- the "+
			"counts say complete while the reported verdict says otherwise", coverage.Short)
	}
	if coverage.Guarded != 1 {
		t.Fatalf("guarded = %d, want 1: the verdict came from the persisted guard, not from this "+
			"window, and nothing downstream can tell unless this says so", coverage.Guarded)
	}
}

// An ABNORMAL verdict is counted with the window it was reached on. The
// trigger decides ABNORMAL before it reads completeness and the output
// contract permits exactly that on WARMING and GAPPED history, so the alert
// such a verdict opens cannot close until the window is FULL. How much
// alerting rides on incomplete windows was a claim about the code; this pair
// makes it a reading, and it is the reading a change to that contract would
// be judged against.
func TestEvaluationCountsAbnormalVerdictsWithTheWindowTheyWereReachedOn(t *testing.T) {
	anomalous := func(value string) []contract.CanonicalRecordV2 {
		return []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(value)},
			Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	}
	evaluate := func(windowSize uint32, value string) execution.HistoryCoverage {
		t.Helper()
		result, err := newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, compiledWindow(t, windowSize, 1), anomalous(value), nil))
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		return result.Plans[0].HistoryCoverage
	}

	// One anomalous record against a two-position window: ABNORMAL, and the
	// window is WARMING -- the monotonic case the contract allows.
	if got := evaluate(2, `90`); got.Abnormal != 1 || got.AbnormalOnIncomplete != 1 {
		t.Fatalf("abnormal/incomplete = %d/%d on a WARMING window, want 1/1", got.Abnormal, got.AbnormalOnIncomplete)
	}
	// The same record fills a one-position window: ABNORMAL on FULL.
	if got := evaluate(1, `90`); got.Abnormal != 1 || got.AbnormalOnIncomplete != 0 {
		t.Fatalf("abnormal/incomplete = %d/%d on a FULL window, want 1/0", got.Abnormal, got.AbnormalOnIncomplete)
	}
	// A record under the threshold on a short window is unavailable, not
	// ABNORMAL, and must not be counted as either cell.
	if got := evaluate(2, `10`); got.Abnormal != 0 || got.AbnormalOnIncomplete != 0 {
		t.Fatalf("abnormal/incomplete = %d/%d on a normal record, want 0/0", got.Abnormal, got.AbnormalOnIncomplete)
	}
}

// A record the detection cannot use -- here one with no value under the field
// the strategy names -- goes into the window with no valid bit, and the
// coverage says so and says why. Before this crossed, such a window read as an
// empty one and an empty one read as data not arriving; the record did arrive,
// and the reason is the difference between a strategy naming a field the
// records do not carry and a source that stopped sending.
func TestEvaluationSaysWhichLevelsCouldNotUseTheRecordAndWhy(t *testing.T) {
	missing := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"other": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	result, err := newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, compiledWindow(t, 1, 1), missing, nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	coverage := result.Plans[0].HistoryCoverage
	if coverage.Levels != 1 || coverage.Empty != 1 {
		t.Fatalf("levels/empty = %d/%d, want 1/1: the one window holds a point with no valid bit", coverage.Levels, coverage.Empty)
	}
	if coverage.Unusable != 1 || coverage.UnusableReason != contract.ReasonRequiredValueMissing {
		t.Fatalf("unusable = %d (%q), want 1 with %s: the record arrived and the detection could not use it, "+
			"and that -- not absent data -- is what the empty window is made of", coverage.Unusable, coverage.UnusableReason,
			contract.ReasonRequiredValueMissing)
	}
	// A record the detection can use carries neither.
	usable := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	result, err = newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, compiledWindow(t, 1, 1), usable, nil))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got := result.Plans[0].HistoryCoverage; got.Unusable != 0 || got.UnusableReason != "" {
		t.Fatalf("a usable record reports unusable = %d (%q)", got.Unusable, got.UnusableReason)
	}
}

// recordLeftBehind is the record this mutation writes: the history it was
// built against with the round's points merged in, bounded by the retention
// it carries. The mutation names the addition, so a test about what the
// history looks like afterwards has to ask for the merge rather than read
// Points - reading Points would be asking what the round evaluated, which is
// a different question that happens to have had the same answer before.
func recordLeftBehind(t testing.TB, mutation execution.StateMutation) []execution.StateHistoryPoint {
	t.Helper()
	record, err := execution.MergedHistory(mutation.BaseHistory, mutation.Points, mutation.RetentionPoints)
	if err != nil {
		t.Fatalf("merge the record the mutation leaves behind: %v", err)
	}
	return record
}

// A Slot with more than one record advances a provisional window record by
// record, and that window is the loaded record plus what the Slot has added so
// far - not what the Slot has added on its own.
//
// The mutation names the addition now, so the provisional view has to merge it
// back before the next record reads it. Feeding the addition through as if it
// were the whole record leaves the second record of a Slot looking at a window
// that starts at this Slot: every series with more than one record per Slot
// would report a short window on every round but the first, and warm for ever.
//
// The fixture needs a loaded history and two records, and the assertion needs
// to be the second record's own window. With no loaded history the two shapes
// agree, which is what the batch-bounding test upstream of this one has - it
// starts from a missing record, so it cannot see this at all.
func TestTheSecondRecordOfASlotSeesTheLoadedWindowAndNotOnlyTheSlotsOwnPoints(t *testing.T) {
	plan := compiledWindow(t, 3, 2)
	fingerprint := plan.Levels()[0].Fingerprints().Detect
	stored := func(sourceTime int64) execution.StateHistoryPoint {
		id, err := contract.DeriveRecordIDV2(strings.Repeat("c", 64), sourceTime)
		if err != nil {
			t.Fatalf("derive record id: %v", err)
		}
		return execution.StateHistoryPoint{RecordID: id, SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}}}
	}
	history := []execution.StateHistoryPoint{stored(60), stored(120)}
	records := make([]contract.CanonicalRecordV2, 0, 2)
	for _, sourceTime := range []int64{180, 240} {
		id, err := contract.DeriveRecordIDV2(strings.Repeat("c", 64), sourceTime)
		if err != nil {
			t.Fatalf("derive record id: %v", err)
		}
		records = append(records, contract.CanonicalRecordV2{RecordID: id, SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
			Dimensions:        map[string]json.RawMessage{}, ReceivedTime: sourceTime})
	}
	result, err := newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, plan, records, history))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	coverage := result.Plans[0].HistoryCoverage
	if coverage.Levels != 2 {
		t.Fatalf("the Slot summarised %d windows, want one per record: the fixture must evaluate both "+
			"records or it cannot say what the second one saw", coverage.Levels)
	}
	if coverage.Short != 0 {
		t.Fatalf("%d of the Slot's windows were short (worst %d of %d); both records have three positions "+
			"behind them once the loaded record is counted, and only the second one can lose them",
			coverage.Short, coverage.WorstValid, coverage.WorstRequired)
	}
}
