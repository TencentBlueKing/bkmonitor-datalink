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
			previous, previousID := int64(-1), ""
			for index, p := range state.Mutation.Points {
				if index > 0 && (p.SourceTime < previous ||
					(p.SourceTime == previous && p.RecordID <= previousID)) {
					t.Fatalf("history is not strictly increasing at index %d: source %d after %d", index, p.SourceTime, previous)
				}
				previous, previousID = p.SourceTime, p.RecordID
			}
			if len(state.Mutation.Points) != len(stored) {
				t.Fatalf("re-evaluating a stored record changed the point count: got %d, want %d",
					len(state.Mutation.Points), len(stored))
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
}
