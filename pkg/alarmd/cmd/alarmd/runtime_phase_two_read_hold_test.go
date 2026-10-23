package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

type runtimeTestReadHoldControl struct {
	values map[execution.QueryGroupIdentity][]byte
	reads  [][]execution.QueryGroupIdentity
	writes int
	// failRead answers one group's read with an error of its own, as Redis
	// answers one key of a pipeline; failBatch fails every read that names
	// the group, as a pipeline whose replies never came.
	failRead  map[execution.QueryGroupIdentity]error
	failBatch map[execution.QueryGroupIdentity]error
}

func (c *runtimeTestReadHoldControl) ReadControlBatch(_ context.Context, groups []execution.QueryGroupIdentity, _ string) ([]ownership.ControlRead, error) {
	c.reads = append(c.reads, append([]execution.QueryGroupIdentity(nil), groups...))
	for _, qg := range groups {
		if err := c.failBatch[qg]; err != nil {
			return nil, err
		}
	}
	result := make([]ownership.ControlRead, len(groups))
	for i, qg := range groups {
		if err := c.failRead[qg]; err != nil {
			result[i] = ownership.ControlRead{Err: err}
			continue
		}
		raw, found := c.values[qg]
		result[i] = ownership.ControlRead{Raw: append([]byte(nil), raw...), Missing: !found}
	}
	return result, nil
}
func (c *runtimeTestReadHoldControl) FencedCompareAndSet(_ context.Context, r ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	c.writes++
	raw, found := c.values[r.Fence.QueryGroup]
	if r.ExpectedMissing == found || !bytes.Equal(r.Expected, raw) {
		return ownership.FencedCASConflict, nil
	}
	c.values[r.Fence.QueryGroup] = append([]byte(nil), r.Value...)
	return ownership.FencedCASApplied, nil
}
func runtimeTestHolds(t *testing.T) (*productionReadHolds, *runtimeTestReadHoldControl, *time.Time) {
	t.Helper()
	at := time.Unix(1800, 0)
	store := &runtimeTestReadHoldControl{values: map[execution.QueryGroupIdentity][]byte{}}
	h := &productionReadHolds{now: func() time.Time { return at }, groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{}}
	h.cfg.PhaseTwo.Worker.ID = "worker"
	var err error
	h.controller, err = readhold.NewController(readhold.Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute, Now: h.now, Owner: h.owner})
	if err != nil {
		t.Fatal(err)
	}
	return h, store, &at
}
func runtimeTestHoldSchedule(t *testing.T, qg execution.QueryGroupIdentity) execution.FrozenQueryGroupSchedule {
	t.Helper()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 55}
	rev, _ := execution.DerivePlanScheduleRevision(spec)
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}
	plans := []execution.FrozenPlanSchedule{{Identity: plan, Spec: spec, ScheduleRevision: rev}}
	groupRev, _ := execution.DeriveQueryGroupScheduleRevision(plans)
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{Publication: execution.SnapshotPublicationRef{PublicationEpoch: 1, SnapshotRevision: "snapshot"}, QueryGroup: qg, QueryRevision: "query", ScheduleRevision: groupRev, Start: 60}, Plans: plans}
}
func TestRuntimeOwnedRestoreIsolatesBadGroups(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	raw, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 120000})
	c.values["good"] = raw
	c.values["bad"] = []byte(`{"hold_ms":-1,"since_slot":1}`)
	// Opened here: only a group this Worker has opened is restored.
	for _, qg := range []execution.QueryGroupIdentity{"bad", "good", "zero"} {
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
	}
	h.restore(context.Background(), []execution.QueryGroupIdentity{"bad", "good", "zero"})
	// The corrupt record restores its group marked, so the next write
	// replaces it -- it would otherwise keep the group unrestored for a week.
	if bad := h.controller.Inspect("bad"); !bad.Loaded || !bad.Corrupt || !h.controller.Inspect("good").Loaded ||
		h.controller.Inspect("good").Corrupt || !h.controller.Inspect("zero").Loaded {
		t.Fatal("a corrupt group invalidated good batch entries, or was not restored marked")
	}
	h.restore(context.Background(), []execution.QueryGroupIdentity{"bad", "good", "zero"})
	if len(c.reads) != 1 {
		t.Fatalf("restored groups were reread: %+v", c.reads)
	}
}
func TestRuntimePreparedZeroHoldDoesNotReadOrCreateKey(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	qg := execution.QueryGroupIdentity("zero")
	schedule := runtimeTestHoldSchedule(t, qg)
	session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h.bind(qg, session)
	h.groups[qg].prepared = schedule.Segment
	spec := readhold.GroupSpec{QueryGroup: qg, Plans: []readhold.PlanRef{{Key: schedule.Plans[0].Key()}}, SettlingWait: 30 * time.Second, HoldLimit: 10 * time.Minute}
	if err := h.controller.Configure(spec); err != nil {
		t.Fatal(err)
	}
	h.restore(context.Background(), []execution.QueryGroupIdentity{qg})
	lease, _ := session.Current()
	for _, slot := range []execution.EvaluationTime{1200, 1260, 1320} {
		if got, err := h.SlotReadHold(context.Background(), schedule, slot, lease.Fence); err != nil || got != 0 {
			t.Fatalf("prepared h0 read: %s %v", got, err)
		}
	}
	if page := h.groupPage(nil, "", 200); len(page.Groups) != 1 || !page.Groups[0].HoldKnown || page.Groups[0].ReadHoldMillis != 0 {
		t.Fatalf("successfully seeded h0 was not known: %+v", page)
	}
	if len(c.reads) != 1 || c.writes != 0 || len(c.values) != 0 {
		t.Fatalf("h0 normal path made storage calls: reads=%d writes=%d keys=%d", len(c.reads), c.writes, len(c.values))
	}
	*at = at.Add(2 * time.Minute)
	if _, err := h.owner(qg); !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("expired owner still admitted: %v", err)
	}
	h.forget(qg, session)
	if len(h.groups) != 0 || h.controller.Inspect(qg).Loaded {
		t.Fatal("release kept the owner's controller record")
	}
}
func TestRuntimeLookbackPagerUsesOnlyOwnedMemory(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	raw, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 120000})
	c.values["good"] = raw
	c.values["bad"] = []byte(`{"hold_ms":-1,"since_slot":1}`)
	for _, qg := range []execution.QueryGroupIdentity{"bad", "good", "zero"} {
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
	}
	h.restore(context.Background(), []execution.QueryGroupIdentity{"bad", "good", "zero"})
	handler := withLookbackAPI(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }), nil, lookbackStanding{}, h, h.now)
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	first := request(http.MethodGet, "/api/lookback?limit=1")
	if first.Code != 200 {
		t.Fatalf("GET status %d", first.Code)
	}
	var page lookbackAPIReading
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Next != "bad" || len(page.Groups) != 1 || page.Groups[0].HoldKnown {
		t.Fatalf("bad hold was reported as known: %+v", page)
	}
	second := request(http.MethodGet, "/api/lookback?limit=1&after=bad")
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Groups[0].QueryGroup != "good" || !page.Groups[0].HoldKnown || page.Groups[0].ReadHoldMillis != 120000 {
		t.Fatalf("good page: %+v", page)
	}
	if len(c.reads) != 1 || c.writes != 0 {
		t.Fatal("pager issued storage calls")
	}
	if request(http.MethodGet, "/api/lookback?after="+strings.Repeat("x", 257)).Code != 400 {
		t.Fatal("unbounded cursor accepted")
	}
	op := cliLookbackOperation(nil, lookbackStanding{}, h)
	outcome := op.Run(context.Background(), obchannel.Params{"limit": json.Number("1")})
	if outcome.Complete || len(outcome.Next) != 1 {
		t.Fatal("CLI truncated page gave no next call")
	}
	following := op.Run(context.Background(), outcome.Next[0].Params).Value.(cliLookbackReading)
	if following.ReadHolds.Groups[0].QueryGroup != "good" {
		t.Fatal("CLI cursor reread first page")
	}
	// A group with no record holds nothing, whether or not a Slot of it was
	// prepared yet: one whose hold has only ever been zero keeps none.
	if page := h.groupPage(nil, "good", 200); len(page.Groups) != 1 || !page.Groups[0].HoldKnown || page.Groups[0].ReadHoldMillis != 0 {
		t.Fatalf("missing, unseeded h0 was not reported as a known zero: %+v", page)
	}
	*at = at.Add(2 * time.Minute)
	if h.groupPage(nil, "", 200).Total != 0 || len(h.fleetFacts()) != 0 {
		t.Fatal("expired owner reported a current hold")
	}
	if request(http.MethodPost, "/api/lookback").Code != 405 || request(http.MethodGet, "/api/lookback?limit=201").Code != 400 || request(http.MethodGet, "/other").Code != 418 {
		t.Fatal("method, budget, or route forwarding changed")
	}
}

// A retired group that retires without closing its hold is counted where
// the read hold's counters are exported, and every predecessor reason and
// clamp source is exported at zero beside it.
func TestARetireCloseFailureIsExportedWithTheReadHoldCounters(t *testing.T) {
	h, _, _ := runtimeTestHolds(t)
	h.links = map[string]uint64{}
	engine, err := lookback.New(lookback.Options{Now: h.now,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	h.RetireCloseFailed(errors.New("closing failed"))
	stats := productionLookbackStats(engine, h)
	if stats.ReadHoldRetireCloseFailed != 1 {
		t.Fatalf("retire close failures exported %d, want 1", stats.ReadHoldRetireCloseFailed)
	}
	if len(stats.ReadHoldPredecessors) != len(readhold.PredecessorReasons)+len(readhold.LinkSkipReasons) || len(stats.ReadHoldClamped) != len(readhold.ClampSources) {
		t.Fatalf("not every reason and source exported: %v %v", stats.ReadHoldPredecessors, stats.ReadHoldClamped)
	}
}

// The read hold counts by source are the group page's own facts: a corrupt
// or unseeded record is a hold not yet known, a known one is counted held
// when more than none and at its limit when marked so, and the largest
// known one is kept. A group the lookback has not seen is under other; a
// group this process no longer holds is not counted.
func TestRuntimeReadHoldGroupsCountWhatTheGroupPageKnows(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	held, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 120000})
	limited, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 600000, AtLimit: true, LimitMillis: 600000})
	// A change chosen and not yet frozen: the hold the next Slot is frozen with.
	pending := int64(90000)
	raised, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 0, PendingHoldMillis: &pending})
	c.values["good"], c.values["limited"], c.values["raised"] = held, limited, raised
	c.values["bad"] = []byte(`{"hold_ms":-1,"since_slot":1}`)
	groups := []execution.QueryGroupIdentity{"bad", "good", "limited", "raised", "zero"}
	for _, qg := range groups {
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
	}
	h.restore(context.Background(), groups)
	counts := h.groupsBySource(nil)
	// Unknown is the record that did not decode; the group with none holds
	// nothing.
	want := map[string]lookback.ReadHoldGroups{lookback.SourceOther: {Held: 3, AtLimit: 1, Unknown: 1, MaxMillis: 600000, MaxKnown: true}}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts %+v, want %+v", counts, want)
	}
	page := h.groupPage(nil, "", 200)
	known := 0
	for _, row := range page.Groups {
		if row.HoldKnown {
			known++
		}
	}
	if entry := counts[lookback.SourceOther]; len(page.Groups)-known != entry.Unknown {
		t.Fatalf("counts %+v disagree with the group page %+v", entry, page)
	}
	// Published, the record that did not decode is listed unknown, and the
	// group with none is left out: holding nothing.
	facts := h.fleetFacts()
	if bad, listed := facts["bad"]; !listed || !bad.Unknown || bad.Millis != 0 {
		t.Fatalf("the undecodable record published %+v (listed %t), want it unknown", bad, listed)
	}
	if zero, listed := facts["zero"]; listed {
		t.Fatalf("the group with no record published %+v, want it left out", zero)
	}
	// One group's hold, as an overdue episode reads it, by the same rule.
	for qg, want := range map[string]struct {
		millis int64
		known  bool
	}{"good": {120000, true}, "limited": {600000, true}, "raised": {90000, true}, "bad": {0, false}, "zero": {0, true}} {
		if millis, known := h.holdOf(qg); millis != want.millis || known != want.known {
			t.Fatalf("hold of %s %d %t, want %d %t", qg, millis, known, want.millis, want.known)
		}
	}
	*at = at.Add(2 * time.Minute)
	if counts := h.groupsBySource(nil); counts != nil {
		t.Fatalf("groups no longer held were counted: %+v", counts)
	}
}

// A group holding on a measured arrival age publishes the time_delay that
// would need no hold: the arrival age less the settling wait, aligned up to
// the data step -- also at the limit, where the hold cannot cover it, and
// after a lowering, whose arrival age rests on three matching earlier
// reads. A hold with no measured arrival age, a predecessor's bound,
// suggests nothing; nor does a group holding nothing, or one whose query is
// not known yet. Nor does one whose next Slot holds nothing - its latest
// Slot frozen above none by a transition alone, or a lowering to none
// chosen - whatever its latest Slot got: the suggestion is about the hold
// going on, and a READ_HELD row stands only on a suggestion. The hold's
// first frozen Slot rides beside it.
func TestRuntimeAMeasuredHoldSuggestsTheTimeDelayThatNeedsNone(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	chosen, zero := int64(99_000), int64(0)
	plan := execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}}
	records := map[execution.QueryGroupIdentity]readhold.Record{
		"measured":  {SinceSlot: 1_790_000_000, HoldMillis: 99_000, ArrivalAgeMillis: 189_000},
		"limited":   {SinceSlot: 1_790_000_000, HoldMillis: 600_000, ArrivalAgeMillis: 990_000, AtLimit: true, LimitMillis: 600_000},
		"lowered":   {SinceSlot: 1_790_000_000, HoldMillis: 40_000, ArrivalAgeMillis: 130_000, Lowered: true},
		"fallback":  {SinceSlot: 1_790_000_000, HoldMillis: 600_000},
		"inherited": {SinceSlot: 1_790_000_000, HoldMillis: 120_000, ArrivalAgeMillis: 80_000},
		"chosen":    {SinceSlot: 1_790_000_000, PendingHoldMillis: &chosen, ArrivalAgeMillis: 189_000},
		"transitiononly": {SinceSlot: 1_790_000_000, HoldMillis: 120_000, PendingHoldMillis: &zero, ArrivalAgeMillis: 189_000,
			Transitions: []readhold.Transition{{Key: plan, DeadlineMillis: 1_790_000_200_000}}},
		"lowertozero": {SinceSlot: 1_790_000_000, HoldMillis: 40_000, PendingHoldMillis: &zero, ArrivalAgeMillis: 130_000, Lowered: true},
		"idle":        {SinceSlot: 1_790_000_000, ArrivalAgeMillis: 50_000},
		"unprepared":  {SinceSlot: 1_790_000_000, HoldMillis: 99_000, ArrivalAgeMillis: 189_000},
	}
	var groups []execution.QueryGroupIdentity
	for qg, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		c.values[qg] = raw
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
		groups = append(groups, qg)
	}
	h.restore(context.Background(), groups)
	h.mu.Lock()
	for qg, group := range h.groups {
		if qg != "unprepared" {
			group.queryRoute, group.queryDelay, group.queryStep, group.settlingWait, group.settled = "route", time.Minute, time.Minute, 30*time.Second, true
		}
	}
	h.mu.Unlock()
	facts := h.fleetFacts()
	for qg, want := range map[string]struct {
		millis, since, delay, suggested int64
	}{
		"measured": {99_000, 1_790_000_000, 60, 180},
		"limited":  {600_000, 1_790_000_000, 60, 960},
		"lowered":  {40_000, 1_790_000_000, 60, 120},
		"fallback": {600_000, 1_790_000_000, 60, 0},
		// A hold not from this arrival age -- an inherited bound -- beside
		// one that needs no more than the delay: nothing to move it to.
		"inherited": {120_000, 1_790_000_000, 60, 0},
		// Chosen and not yet frozen: no Slot has held, so the hold read is
		// the last Slot's none; the suggestion already stands.
		"chosen": {0, 0, 60, 180},
		// The next Slot holds nothing: no time_delay to move it to.
		"transitiononly": {120_000, 1_790_000_000, 60, 0},
		"lowertozero":    {40_000, 1_790_000_000, 60, 0},
		"idle":           {0, 0, 60, 0},
		"unprepared":     {99_000, 1_790_000_000, 0, 0},
	} {
		got, found := facts[qg]
		settling := int64(30)
		if qg == "unprepared" {
			settling = 0
		}
		if !found || got.Millis != want.millis || got.HeldSince != want.since || got.DelaySeconds != want.delay || got.SuggestedDelaySeconds != want.suggested ||
			got.SettlingWaitSeconds != settling {
			t.Errorf("%s: facts %+v (found %t), want hold %d since %d delay %d suggested %d", qg, got, found, want.millis, want.since, want.delay, want.suggested)
		}
	}
	// The rows the replica's tracker makes of these facts, every group seen
	// evaluating here.
	tracker := fleet.NewTracker(nil, "worker", func() time.Time { return *at })
	for _, qg := range groups {
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
			ExecuteOutcome: "COMPLETED", Result: observability.ResultSuccess,
			Trace: observability.TraceFields{QueryGroupKey: string(qg), StrategyID: "101", BusinessID: "2", EvaluationTime: at.Unix()},
		})
	}
	var held []string
	for _, row := range tracker.ReadHeld(facts) {
		held = append(held, row.QueryGroup)
	}
	sort.Strings(held)
	if want := []string{"limited", "lowered", "measured"}; !reflect.DeepEqual(held, want) {
		t.Fatalf("READ_HELD rows for %v, want %v: none for a group whose next Slot holds nothing", held, want)
	}
}

// Preparing a group's Slot through the production wiring keeps, beside its
// time_delay, the data step its query's lookback aligns to and the settling
// wait its hold is reckoned beyond: what its suggestion is computed from.
func TestRuntimeAPreparedGroupKeepsWhatItsSuggestionIsReckonedBy(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	holds := f.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	group := holds.groups[f.queryGroup]
	var kept productionReadHoldGroup
	if group != nil {
		kept.queryDelay, kept.queryStep, kept.settlingWait, kept.settled = group.queryDelay, group.queryStep, group.settlingWait, group.settled
	}
	holds.mu.Unlock()
	object, err := f.repository.LoadQueryGroupObject(ctx, f.initialSchedule.Segment.ObjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := holds.spec(ctx, f.initialSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if group == nil || !kept.settled || kept.queryStep != time.Duration(object.QueryPlan.StepMillis)*time.Millisecond || kept.queryStep <= 0 ||
		kept.settlingWait != spec.SettlingWait || kept.queryDelay != spec.Delay {
		t.Fatalf("kept delay %v step %v settling %v (settled %t), want the spec's %v and %v and the query's step %d ms",
			kept.queryDelay, kept.queryStep, kept.settlingWait, kept.settled, spec.Delay, spec.SettlingWait, object.QueryPlan.StepMillis)
	}
}

// A Plan detected more often than its query aggregates has a schedule
// shorter than its data step. Its hold is still lowered by the data step:
// the early read computes its candidate by that step, and the controller
// drops evidence whose candidate differs from its own, so a controller
// lowering by the schedule would never lower such a group's hold.
func TestRuntimeAHoldIsLoweredByTheQuerysDataStepNotTheSchedules(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	holds := f.bundle.dependencies.ReadHolds
	object, err := f.repository.LoadQueryGroupObject(ctx, f.initialSchedule.Segment.ObjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	dataStep := time.Duration(object.QueryPlan.StepMillis) * time.Millisecond
	faster := f.initialSchedule
	faster.Plans = append([]execution.FrozenPlanSchedule(nil), faster.Plans...)
	for index := range faster.Plans {
		faster.Plans[index].Spec.EvaluationIntervalSeconds = int64(dataStep/time.Second) / 4
	}
	spec, err := holds.spec(ctx, faster)
	if err != nil {
		t.Fatal(err)
	}
	if dataStep <= 0 || spec.Step != dataStep {
		t.Fatalf("lowering step %v for Plans every %ds, want the query's data step %v", spec.Step, faster.Plans[0].Spec.EvaluationIntervalSeconds, dataStep)
	}
	hold := 4 * dataStep
	if early, controller := execution.LoweredReadHold(hold, dataStep), execution.LoweredReadHold(hold, spec.Step); early != controller {
		t.Fatalf("the early read lowers %v to %v and the controller to %v", hold, early, controller)
	}
}

// A held group's suggested time_delay is rounded as its delay was: to the
// data step, or for a query read unaligned to the shorter schedule step its
// delay was rounded to. Rounded to the data step, a group detected every
// fifteen seconds would be told a delay its read-early advice does not give,
// and the larger of the two wins.
func TestRuntimeAHeldGroupsSuggestionIsRoundedAsItsDelayWas(t *testing.T) {
	record := readhold.Record{ArrivalAgeMillis: 40_000}
	for _, test := range []struct {
		unit time.Duration
		want int64
	}{{unit: 0, want: 60}, {unit: time.Minute, want: 60}, {unit: 15 * time.Second, want: 45}} {
		basis := readHoldBasis{delay: 30 * time.Second, step: time.Minute, unit: test.unit}
		if got := basis.suggestion(record); got != test.want {
			t.Fatalf("unit %v: suggestion %d, want %d", test.unit, got, test.want)
		}
	}
}
