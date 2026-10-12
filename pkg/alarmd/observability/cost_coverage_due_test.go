package observability

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

// A Slot is due in a window when the first evaluation time at or after the
// window's start completes by its end. The deadline landing exactly on the
// end is due, a second later is not; the first evaluation time follows the
// alignment; a short Plan's own completion offset is what it is given; and a
// schedule nobody knows is due in every window.
func TestASlotIsDueWhenItsDeadlineFallsInsideTheWindow(t *testing.T) {
	for name, testCase := range map[string]struct {
		due        costDue
		start, end int64
		want       bool
	}{
		"deadline on the end":           {costDue{interval: 300}, 900, 1200, true},
		"deadline a second past it":     {costDue{interval: 300}, 900, 1199, false},
		"first time after an alignment": {costDue{interval: 60, alignment: 30}, 900, 990, true},
		"aligned one second too late":   {costDue{interval: 60, alignment: 30}, 900, 989, false},
		"an hour's in minutes":          {costDue{interval: 3600}, 900, 1500, false},
		"a short Plan's own offset":     {costDue{interval: 10, offset: 30}, 900, 930, true},
		"one second short of it":        {costDue{interval: 10, offset: 30}, 900, 929, false},
		"no offset means the interval":  {costDue{interval: 60}, 900, 960, true},
		"a schedule nobody knows":       {costDue{}, 900, 901, true},
	} {
		if got := testCase.due.dueIn(testCase.start, testCase.end); got != testCase.want {
			t.Errorf("%s: dueIn(%d, %d) = %v, want %v", name, testCase.start, testCase.end, got, testCase.want)
		}
	}
}

// costDueFixture tracks two groups from before the window began: one on a
// minute, due in every window, and one on an hour, due in none of these.
// The window is two five-minute buckets, 900 to 1440.
// unevaluatedOf reads coverage's UnevaluatedDuePlans by outcome, failing t
// unless it holds every costUnevaluatedOutcomes word once, in that order.
func unevaluatedOf(t *testing.T, coverage CostCoverage) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for i, entry := range coverage.UnevaluatedDuePlans {
		if i >= len(costUnevaluatedOutcomes) || entry.Outcome != costUnevaluatedOutcomes[i] {
			t.Fatalf("unevaluated_due_plans = %+v, want every outcome of %v once, in order", coverage.UnevaluatedDuePlans, costUnevaluatedOutcomes)
		}
		counts[entry.Outcome] = entry.Plans
	}
	if len(counts) != len(costUnevaluatedOutcomes) {
		t.Fatalf("unevaluated_due_plans = %+v, want every outcome of %v", coverage.UnevaluatedDuePlans, costUnevaluatedOutcomes)
	}
	return counts
}

func costDueFixture(t *testing.T, groups ...CostGroup) (*CostSummary, *time.Time) {
	t.Helper()
	now := time.Unix(800, 0)
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: 5 * time.Minute, GroupCapacity: 8, PlanCapacity: 8,
		MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	c.Reconcile(groups, true)
	now = time.Unix(1440, 0)
	return c, &now
}

var (
	costMinute = CostGroup{QueryGroupKey: "minute", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costA},
		Schedules: []CostSchedule{{Plan: costA, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	costHourly = CostGroup{QueryGroupKey: "hourly", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costB},
		Schedules: []CostSchedule{{Plan: costB, IntervalSeconds: 3600, CompletionOffsetSeconds: 3600}}}
)

// ran reports one Slot of the group and, when a Plan is named, that Plan's
// evaluation, all timed.
func ran(c *CostSummary, group string, plan CostPlanIdentity) {
	trace := TraceFields{QueryGroupKey: group, QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}
	c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true, Trace: trace})
	if plan.valid() {
		c.Observe(context.Background(), Observation{Stage: StageEvaluationCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true,
			EvaluationOwner: plan, EvaluationRecordsKnown: true, Trace: trace})
	}
}

// A window is incomplete for a group it should have seen and did not - not
// for one whose next Slot is not due in it. On a live deployment every
// replica tracked a handful of groups on intervals longer than the window,
// had no observation of them, and so read incomplete in every window; the
// flag said nothing. A group or a Plan that was due and left nothing in the
// window is still what makes it incomplete.
func TestAWindowIsIncompleteForWhatWasDueInItAndWentUnseen(t *testing.T) {
	c, now := costDueFixture(t, costMinute, costHourly)
	ran(c, "minute", costA)
	c.Publish(*now)
	coverage := c.Snapshot().Coverage
	if coverage.TrackedGroups != 2 || coverage.ObservedGroups != 1 || coverage.DueGroups != 1 || coverage.UnobservedDueGroups != 0 ||
		coverage.DuePlans != 1 || coverage.UnobservedDuePlans != 0 || coverage.Incomplete {
		t.Fatalf("the hourly group not due in the window = %+v, want complete: nothing due went unseen", coverage)
	}

	c, now = costDueFixture(t, costMinute, costHourly)
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.UnobservedDueGroups != 1 || coverage.UnobservedDuePlans != 1 || !coverage.Incomplete {
		t.Fatalf("the minute group missing from its window = %+v, want it counted and the window incomplete", coverage)
	}

	c, now = costDueFixture(t, costMinute, costHourly)
	ran(c, "minute", CostPlanIdentity{})
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.UnobservedDueGroups != 0 || coverage.UnobservedDuePlans != 1 || !coverage.Incomplete {
		t.Fatalf("a due Plan whose group ran without evaluating it = %+v, want the Plan counted and the window incomplete", coverage)
	}

	// A Plan the roster carried two schedules for - a split Plan, one per
	// piece - is read by the one due most often: it is due in this window
	// when either piece is, and unseen it makes the window incomplete.
	split := costHourly
	split.Schedules = []CostSchedule{costHourly.Schedules[0], {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}
	c, now = costDueFixture(t, costMinute, split)
	ran(c, "minute", costA)
	ran(c, "hourly", CostPlanIdentity{})
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.DuePlans != 2 || coverage.UnobservedDuePlans != 1 || !coverage.Incomplete {
		t.Fatalf("a split Plan with a piece due, unevaluated = %+v, want it due and the window incomplete", coverage)
	}

	// A group the roster carried no schedule for is due in every window: a
	// missing schedule does not read as nothing expected.
	unknown := costHourly
	unknown.Schedules = nil
	c, now = costDueFixture(t, costMinute, unknown)
	ran(c, "minute", costA)
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.DueGroups != 2 || coverage.UnobservedDueGroups != 1 || !coverage.Incomplete {
		t.Fatalf("a group with no schedule, unseen = %+v, want it due and the window incomplete", coverage)
	}
}

// Due is read across the whole window, both buckets: a five-minute group due
// only in the earlier one and unseen makes it incomplete. A group with two
// schedules is due when either is, whichever the roster listed first.
func TestAGroupIsDueAcrossTheWholeWindowAndOnAnyOfItsSchedules(t *testing.T) {
	fiveMinutes := CostGroup{QueryGroupKey: "five", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costB},
		Schedules: []CostSchedule{{Plan: costB, IntervalSeconds: 300, CompletionOffsetSeconds: 300}}}
	c, now := costDueFixture(t, costMinute, fiveMinutes)
	ran(c, "minute", costA)
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.DueGroups != 2 || coverage.UnobservedDueGroups != 1 || !coverage.Incomplete {
		t.Fatalf("a group due in the earlier bucket only, unseen = %+v, want it due and the window incomplete", coverage)
	}

	both := costHourly
	both.Schedules = []CostSchedule{costHourly.Schedules[0], {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}
	c, now = costDueFixture(t, costMinute, both)
	ran(c, "minute", costA)
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.DueGroups != 2 || coverage.UnobservedDueGroups != 1 {
		t.Fatalf("a group with an hourly schedule listed before a minute one = %+v, want it due", coverage)
	}
}

// A group's schedules are kept all or none. Kept in part under the metadata
// bound, a group could be read on the one not due and missed on the one
// that was; with none it is due in every window, which only over-reports.
func TestAGroupsSchedulesAreKeptAllOrNone(t *testing.T) {
	split := CostGroup{QueryGroupKey: "split", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costB},
		Schedules: []CostSchedule{{Plan: costB, IntervalSeconds: 3600, CompletionOffsetSeconds: 3600}, {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	now := time.Unix(800, 0)
	// Room for the group, its one Plan and one schedule of its two.
	bytes := len("split") + len("qsr") + len(costB.TenantID) + len(costB.BusinessID) + len(costB.StrategyID) + costDueBytes
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: 5 * time.Minute, GroupCapacity: 8, PlanCapacity: 8,
		MetadataBytes: bytes, TopN: 2, Now: func() time.Time { return now }})
	c.Reconcile([]CostGroup{split}, true)
	now = time.Unix(1440, 0)
	c.Publish(now)
	if coverage := c.Snapshot().Coverage; coverage.TrackedGroups != 1 || coverage.TrackedPlans != 1 || coverage.DueGroups != 1 || coverage.UnobservedDueGroups != 1 {
		t.Fatalf("a group whose schedules did not all fit = %+v, want it tracked, due in the window and unseen", coverage)
	}
}

// A group whose rounds the scheduler held - a query cooldown - has no work
// in the window, and no cost is missing from it: it and its Plans are held,
// not unseen, and the window is complete. A round with nothing due is not a
// hold, and a group whose only rounds were those is unseen. A held round of
// a group the roster does not track is not cost at all.
func TestAGroupTheSchedulerHeldIsHeldNotMissing(t *testing.T) {
	held := func(c *CostSummary, group, outcome string) {
		c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
			RunOutcome: outcome, Trace: TraceFields{QueryGroupKey: group}})
	}
	c, now := costDueFixture(t, costMinute, costHourly)
	held(c, "minute", "query_cooldown")
	held(c, "elsewhere", "query_cooldown")
	c.Publish(*now)
	coverage := c.Snapshot().Coverage
	if coverage.DueGroups != 1 || coverage.HeldDueGroups != 1 || coverage.UnobservedDueGroups != 0 || unevaluatedOf(t, coverage)["held"] != 1 ||
		coverage.UnobservedDuePlans != 0 || coverage.ObservedGroups != 0 || coverage.UntrackedObservations != 0 || coverage.Incomplete {
		t.Fatalf("a group held by its cooldown all window = %+v, want it and its Plan held, nothing unseen or untracked, the window complete", coverage)
	}

	// A group that ran as well as being held is not itself held; whether its
	// unevaluated due Plan is, the Plan-level rule decides (below).
	c, now = costDueFixture(t, costMinute, costHourly)
	held(c, "minute", "query_cooldown")
	ran(c, "minute", CostPlanIdentity{})
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.HeldDueGroups != 0 || coverage.ObservedGroups != 1 {
		t.Fatalf("a group that ran and was held = %+v, want it observed, not held", coverage)
	}

	c, now = costDueFixture(t, costMinute, costHourly)
	held(c, "minute", "source_not_due")
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.HeldDueGroups != 0 || coverage.UnobservedDueGroups != 1 || !coverage.Incomplete {
		t.Fatalf("a group whose rounds only had nothing due = %+v, want it unseen and the window incomplete", coverage)
	}
}

// Every word the scheduler returns a round with is decided here, held or
// not, and nothing else is: a new word fails this until someone says which
// it is, instead of reading as not held. Each held word, alone, holds a due
// group for the window.
func TestEveryRunOutcomeIsDecidedHeldOrNot(t *testing.T) {
	notHeld := map[string]bool{"execute_returned": true, "source_not_due": true, "panic": true, "other_error": true}
	known := map[string]bool{}
	for _, outcome := range RunOutcomes {
		known[outcome] = true
		if costHeldOutcomes[outcome] == notHeld[outcome] {
			t.Errorf("run outcome %q is held=%v and not held=%v, want exactly one", outcome, costHeldOutcomes[outcome], notHeld[outcome])
		}
	}
	for outcome := range costHeldOutcomes {
		if !known[outcome] {
			t.Errorf("held outcome %q is no run outcome", outcome)
		}
	}
	for outcome := range costHeldOutcomes {
		c, now := costDueFixture(t, costMinute, costHourly)
		c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
			RunOutcome: outcome, Trace: TraceFields{QueryGroupKey: "minute"}})
		c.Publish(*now)
		if coverage := c.Snapshot().Coverage; coverage.HeldDueGroups != 1 || coverage.Incomplete {
			t.Errorf("a group held by %q = %+v, want it held and the window complete", outcome, coverage)
		}
	}
}

// A group held only in the window's earlier bucket is held for the window.
func TestAGroupHeldInTheEarlierBucketIsHeld(t *testing.T) {
	c, now := costDueFixture(t, costMinute, costHourly)
	*now = time.Unix(1000, 0)
	c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
		RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "minute"}})
	*now = time.Unix(1440, 0)
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.HeldDueGroups != 1 || coverage.UnobservedDueGroups != 0 || coverage.Incomplete {
		t.Fatalf("a group held in the earlier bucket only = %+v, want it held and the window complete", coverage)
	}
}

// What was due and went unseen is named, not only counted: a group that did
// nothing is named with its Plan, in key order, each with the group's counts
// over the window; a group that ran and was held, its due Plan unevaluated,
// carries the counts that say so. The sample stops at its bound, a caller's
// copy of it cannot change the cached one, and a window with nothing unseen
// names nothing.
func TestWhatWasDueAndWentUnseenIsNamedWithItsGroupsCounts(t *testing.T) {
	c, now := costDueFixture(t, costMinute, costHourly)
	c.Publish(*now)
	sample := c.Snapshot().Coverage.UnobservedDueSample
	if len(sample) != 2 || sample[0] != (CostDueMiss{Scope: "query_group", QueryGroupKey: "minute", Reason: costMissNoRecord}) ||
		sample[1] != (CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "minute", Plan: costA, Reason: costMissNoRecord}) {
		t.Fatalf("a due group that did nothing = %+v, want the group then its Plan, no record, all counts zero", sample)
	}

	c, now = costDueFixture(t, costMinute, costHourly)
	c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
		RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "minute"}})
	ran(c, "minute", CostPlanIdentity{})
	committed(c, "minute")
	c.Publish(*now)
	sample = c.Snapshot().Coverage.UnobservedDueSample
	want := CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "minute", Plan: costA, Observations: 2, RunReturns: 1, HeldRounds: 1, ProgressCommits: 1,
		Reason: costMissCompletedUnevaluated}
	if len(sample) != 1 || sample[0] != want {
		t.Fatalf("a group that was held and completed a round without its Plan = %+v, want %+v", sample, want)
	}

	// Five groups unseen, each with its Plan: ten misses, the first eight named.
	groups := []CostGroup{}
	for i := 0; i < 5; i++ {
		g := costMinute
		g.QueryGroupKey = string(rune('a' + i))
		groups = append(groups, g)
	}
	c, now = costDueFixture(t, groups...)
	c.Publish(*now)
	snapshot := c.Snapshot()
	sample = snapshot.Coverage.UnobservedDueSample
	if snapshot.Coverage.UnobservedDueGroups+snapshot.Coverage.UnobservedDuePlans != 10 || len(sample) != costDueMissSampleLimit ||
		sample[0].QueryGroupKey != "a" || sample[costDueMissSampleLimit-1] != (CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "d", Plan: costA, Reason: costMissNoRecord}) {
		t.Fatalf("ten misses = groups %d plans %d sample %+v, want the first %d by key", snapshot.Coverage.UnobservedDueGroups,
			snapshot.Coverage.UnobservedDuePlans, sample, costDueMissSampleLimit)
	}
	sample[0].QueryGroupKey = "tampered"
	snapshot.Coverage.UnevaluatedDuePlans[0].Plans = 99
	if coverage := c.Snapshot().Coverage; coverage.UnobservedDueSample[0].QueryGroupKey != "a" || coverage.UnevaluatedDuePlans[0].Plans != 0 {
		t.Fatal("a caller's copy of the sample or the unevaluated counts changed the cached one")
	}

	c, now = costDueFixture(t, costMinute, costHourly)
	ran(c, "minute", costA)
	c.Publish(*now)
	if sample := c.Snapshot().Coverage.UnobservedDueSample; sample != nil {
		t.Fatalf("a window with nothing unseen names %+v", sample)
	}
}

// The sample is kept as the misses are found, never more than its bound:
// twenty misses arriving last first leave exactly the first eight in order,
// and one that sorts after all eight is not kept.
func TestTheSampleKeepsTheFirstMissesAsTheyArrive(t *testing.T) {
	var kept []CostDueMiss
	for i := 19; i >= 0; i-- {
		kept = keepDueMiss(kept, CostDueMiss{Scope: "query_group", QueryGroupKey: string(rune('a' + i))})
		if len(kept) > costDueMissSampleLimit {
			t.Fatalf("kept %d, want at most %d", len(kept), costDueMissSampleLimit)
		}
	}
	if len(kept) != costDueMissSampleLimit {
		t.Fatalf("kept %d, want %d", len(kept), costDueMissSampleLimit)
	}
	for i, miss := range kept {
		if miss.QueryGroupKey != string(rune('a'+i)) {
			t.Fatalf("kept %+v, want a..h in order", kept)
		}
	}
	if after := keepDueMiss(kept, CostDueMiss{Scope: "query_group", QueryGroupKey: "z"}); len(after) != costDueMissSampleLimit || after[costDueMissSampleLimit-1].QueryGroupKey != "h" {
		t.Fatalf("a miss after all eight changed the sample: %+v", after)
	}
}

// Two Plans of one group that differ only by strategy - the usual case, one
// tenant and one business - are kept in strategy order whichever arrives
// first, so which of them the sample names at its bound does not depend on
// the order the roster was walked in.
func TestTwoPlansOfAGroupAreKeptInStrategyOrderWhicheverArrivesFirst(t *testing.T) {
	first := CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "g", Plan: CostPlanIdentity{"tenant", "business", "1001"}}
	second := CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "g", Plan: CostPlanIdentity{"tenant", "business", "1002"}}
	for _, arrival := range [][2]CostDueMiss{{first, second}, {second, first}} {
		kept := keepDueMiss(keepDueMiss(nil, arrival[0]), arrival[1])
		if len(kept) != 2 || kept[0] != first || kept[1] != second {
			t.Fatalf("arriving %s then %s = %+v, want 1001 before 1002", arrival[0].Plan.StrategyID, arrival[1].Plan.StrategyID, kept)
		}
	}
}

// committed reports one round of the group completed and its progress
// committed.
func committed(c *CostSummary, group string) {
	c.Observe(context.Background(), Observation{Stage: StageProgressCommitted, Result: ResultSuccess, ProgressCompletionKind: "FULL_COMPLETED",
		Trace: TraceFields{QueryGroupKey: group, QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}})
}

// A due Plan its group did not evaluate is counted by the round result that
// accounts for it - every result that by definition evaluates nothing - and
// the window stays complete: it saw what happened. It is unseen, and the
// window incomplete, only for what the window should have seen and did not:
// another Plan of the group evaluated and this one not, a round completed
// normally with nothing evaluated (whatever else beside it), a group with
// no record, and records none of which account for it. When several results
// account for it, the first in costUnevaluatedOutcomes names it.
func TestADuePlanItsGroupDidNotEvaluateIsCountedByTheResultThatAccountsForIt(t *testing.T) {
	pair := CostGroup{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costA, costB},
		Schedules: []CostSchedule{{Plan: costA, IntervalSeconds: 60, CompletionOffsetSeconds: 60}, {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	trace := TraceFields{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}
	heldRound := func(c *CostSummary) {
		c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
			RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "pair"}})
	}
	started := func(c *CostSummary) {
		c.Observe(context.Background(), Observation{Stage: StageSlotStarted, Result: ResultStarted, Trace: trace})
	}
	returned := func(c *CostSummary, result Result, err error) {
		c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: result, Err: err, Duration: time.Millisecond, DurationKnown: true, Trace: trace})
	}
	ran := func(c *CostSummary, result Result) { started(c); returned(c, result, nil) }
	commit := func(c *CostSummary, kind string) {
		c.Observe(context.Background(), Observation{Stage: StageProgressCommitted, Result: ResultSuccess, ProgressCompletionKind: kind, Trace: trace})
	}
	evaluated := func(c *CostSummary, plan CostPlanIdentity) {
		c.Observe(context.Background(), Observation{Stage: StageEvaluationCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true,
			EvaluationOwner: plan, EvaluationRecordsKnown: true, Trace: trace})
	}
	refused := errors.New("view not executable")
	for _, testCase := range []struct {
		name    string
		window  func(c *CostSummary)
		outcome string
		seen    int
		reason  string
	}{
		{"held all window", func(c *CostSummary) { heldRound(c) }, "held", 0, ""},
		{"held, a probe returned without a commit", func(c *CostSummary) { heldRound(c); ran(c, ResultSuccess) }, "held", 0, ""},
		{"held, a probe returned not ready", func(c *CostSummary) { heldRound(c); ran(c, ResultRetrying) }, "retrying", 0, ""},
		{"a round the view refused, retrying with its error", func(c *CostSummary) { started(c); returned(c, ResultRetrying, refused) }, "retrying", 0, ""},
		{"never held, a Slot still running", func(c *CostSummary) { started(c) }, "in_flight", 0, ""},
		{"held, a probe still running", func(c *CostSummary) { heldRound(c); started(c) }, "in_flight", 0, ""},
		{"a round that failed", func(c *CostSummary) { ran(c, ResultFailed) }, "failed", 0, ""},
		{"held, the probe's query refused", func(c *CostSummary) { heldRound(c); ran(c, ResultSuccess); commit(c, "COMPLETED_WITH_UNAVAILABLE") }, "unavailable", 0, ""},
		{"the snapshot unavailable", func(c *CostSummary) { ran(c, ResultSuccess); commit(c, "SNAPSHOT_UNAVAILABLE") }, "unavailable", 0, ""},
		{"a span given up", func(c *CostSummary) { ran(c, ResultSuccess); commit(c, "GAP_SKIPPED") }, "gap_skipped", 0, ""},
		{"failed before unavailable", func(c *CostSummary) {
			ran(c, ResultFailed)
			ran(c, ResultSuccess)
			commit(c, "COMPLETED_WITH_UNAVAILABLE")
		}, "failed", 0, ""},
		{"gap_skipped before unavailable", func(c *CostSummary) {
			ran(c, ResultSuccess)
			commit(c, "COMPLETED_WITH_UNAVAILABLE")
			ran(c, ResultSuccess)
			commit(c, "GAP_SKIPPED")
		}, "gap_skipped", 0, ""},
		{"unavailable before retrying", func(c *CostSummary) {
			ran(c, ResultRetrying)
			ran(c, ResultSuccess)
			commit(c, "COMPLETED_WITH_UNAVAILABLE")
		}, "unavailable", 0, ""},
		{"a round completed normally, nothing evaluated", func(c *CostSummary) { ran(c, ResultSuccess); commit(c, "FULL_COMPLETED") }, "", 2, costMissCompletedUnevaluated},
		{"a round completed empty, nothing evaluated", func(c *CostSummary) { ran(c, ResultSuccess); commit(c, "FULL_EMPTY_COMPLETED") }, "", 2, costMissCompletedUnevaluated},
		{"a normal round beside an unavailable one", func(c *CostSummary) {
			heldRound(c)
			ran(c, ResultSuccess)
			commit(c, "COMPLETED_WITH_UNAVAILABLE")
			ran(c, ResultSuccess)
			commit(c, "FULL_COMPLETED")
		}, "", 2, costMissCompletedUnevaluated},
		{"held, and the other Plan evaluated", func(c *CostSummary) { heldRound(c); ran(c, ResultSuccess); evaluated(c, costB) }, "", 1, costMissSiblingEvaluated},
		{"the other Plan evaluated, then a failure", func(c *CostSummary) { evaluated(c, costB); ran(c, ResultFailed) }, "", 1, costMissSiblingEvaluated},
		{"never held, a round returned with nothing committed", func(c *CostSummary) { ran(c, ResultSuccess) }, "", 2, costMissUnexplained},
		{"no record at all", func(c *CostSummary) {}, "", 2, costMissNoRecord},
	} {
		c, now := costDueFixture(t, pair)
		testCase.window(c)
		c.Publish(*now)
		coverage := c.Snapshot().Coverage
		want := map[string]int{}
		for _, outcome := range costUnevaluatedOutcomes {
			want[outcome] = 0
		}
		if testCase.outcome != "" {
			want[testCase.outcome] = 2
		}
		if got := unevaluatedOf(t, coverage); !reflect.DeepEqual(got, want) || coverage.UnobservedDuePlans != testCase.seen ||
			coverage.Incomplete != (testCase.seen > 0) {
			t.Errorf("%s = unevaluated %v unseen %d incomplete %v, want %v unseen %d", testCase.name, coverage.UnevaluatedDuePlans,
				coverage.UnobservedDuePlans, coverage.Incomplete, want, testCase.seen)
			continue
		}
		if testCase.seen > 0 {
			if sample := coverage.UnobservedDueSample; len(sample) == 0 || sample[len(sample)-1].Reason != testCase.reason {
				t.Errorf("%s: sample %+v, want the Plans named %s", testCase.name, sample, testCase.reason)
			}
		}
	}
}

// The window is read whole, both buckets: a round completed normally in the
// earlier bucket, before a cooldown held the later one, leaves the due Plans
// unseen. And a miss in a group never held carries its reason and that
// group's counts, as a miss in a held one does.
func TestAHeldProbeIsReadOverTheWholeWindowAndAMissCarriesItsGroupsCounts(t *testing.T) {
	pair := CostGroup{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costA, costB},
		Schedules: []CostSchedule{{Plan: costA, IntervalSeconds: 60, CompletionOffsetSeconds: 60}, {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	trace := TraceFields{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}
	c, now := costDueFixture(t, pair)
	*now = time.Unix(1000, 0)
	committed(c, "pair")
	*now = time.Unix(1440, 0)
	c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
		RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "pair"}})
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; unevaluatedOf(t, coverage)["held"] != 0 || coverage.UnobservedDuePlans != 2 || !coverage.Incomplete {
		t.Fatalf("a round committed in the earlier bucket, held in the later = unevaluated %v unseen %d, want both Plans unseen",
			coverage.UnevaluatedDuePlans, coverage.UnobservedDuePlans)
	}

	c, now = costDueFixture(t, pair)
	c.Observe(context.Background(), Observation{Stage: StageSlotStarted, Result: ResultStarted, Trace: trace})
	c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true, Trace: trace})
	c.Publish(*now)
	sample := c.Snapshot().Coverage.UnobservedDueSample
	want := CostDueMiss{Scope: "strategy_owned", QueryGroupKey: "pair", Plan: costA, Observations: 2, Attempts: 1, RunReturns: 1,
		Reason: costMissUnexplained}
	if len(sample) != 2 || sample[0] != want {
		t.Fatalf("a never-held group's round that evaluated nothing = %+v, want its Plans named with the group's counts %+v", sample, want)
	}
}

// A probe refused in the earlier bucket and a held round in the later one
// leave the due Plans unavailable, not unseen: the counts are read over both.
func TestAnUnavailableRoundIsReadOverTheWholeWindow(t *testing.T) {
	pair := CostGroup{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costA, costB},
		Schedules: []CostSchedule{{Plan: costA, IntervalSeconds: 60, CompletionOffsetSeconds: 60}, {Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	c, now := costDueFixture(t, pair)
	*now = time.Unix(1000, 0)
	c.Observe(context.Background(), Observation{Stage: StageProgressCommitted, Result: ResultSuccess, ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
		Trace: TraceFields{QueryGroupKey: "pair", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}})
	*now = time.Unix(1440, 0)
	c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
		RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "pair"}})
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; unevaluatedOf(t, coverage)["unavailable"] != 2 || coverage.UnobservedDuePlans != 0 || coverage.Incomplete {
		t.Fatalf("a probe refused in the earlier bucket, held in the later = unevaluated %v unseen %d incomplete %v, want both Plans unavailable",
			coverage.UnevaluatedDuePlans, coverage.UnobservedDuePlans, coverage.Incomplete)
	}
}

// A group that changes revision runs under the next one before the roster
// catches up: its observations are untracked, and the group the roster still
// holds looks as if it left no record. Counted against its key, the miss is
// named revision_changed with how many observations went to the next
// revision - on the group and on its Plan. Once the roster moves, the group
// is tracked since then and named in the partial-window sample as replaced;
// a group new to the roster is named there too, not replaced. A tally the
// group's account could not take for contention is counted as dropped.
func TestAGroupThatChangedRevisionIsNamedNotLeftAsNoRecord(t *testing.T) {
	c, now := costDueFixture(t, costMinute)
	next := TraceFields{QueryGroupKey: "minute", QueryRevision: "q", SnapshotRevision: "s2", ScheduleRevision: "r", EvaluationTime: 1320}
	c.Observe(context.Background(), Observation{Stage: StageSlotStarted, Result: ResultStarted, Trace: next})
	c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true, Trace: next})
	c.Publish(*now)
	coverage := c.Snapshot().Coverage
	sample := coverage.UnobservedDueSample
	if coverage.UntrackedObservations != 2 || coverage.UnobservedDueGroups != 1 || coverage.UnobservedDuePlans != 1 || !coverage.Incomplete ||
		len(sample) != 2 || sample[0].Reason != costMissRevisionChanged || sample[0].OtherRevisionObservations != 2 ||
		sample[1].Reason != costMissRevisionChanged || sample[1].OtherRevisionObservations != 2 {
		t.Fatalf("a group that ran under its next revision = %+v, want it and its Plan named revision_changed with 2 observations", coverage)
	}

	moved := costMinute
	moved.SnapshotRevision = "s2"
	fresh := CostGroup{QueryGroupKey: "fresh", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costB},
		Schedules: []CostSchedule{{Plan: costB, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	*now = time.Unix(1450, 0)
	c.Reconcile([]CostGroup{moved, fresh}, true)
	*now = time.Unix(1460, 0)
	c.Publish(*now)
	snapshot := c.Snapshot()
	want := []CostPartialGroup{{QueryGroupKey: "fresh", TrackedSince: time.Unix(1450, 0), Replaced: false},
		{QueryGroupKey: "minute", TrackedSince: time.Unix(1450, 0), Replaced: true}}
	if snapshot.Coverage.PartialWindowGroups != 2 || !reflect.DeepEqual(snapshot.Coverage.PartialWindowSample, want) {
		t.Fatalf("groups tracked since mid-window = %d %+v, want %+v", snapshot.Coverage.PartialWindowGroups, snapshot.Coverage.PartialWindowSample, want)
	}
	snapshot.Coverage.PartialWindowSample[0].QueryGroupKey = "tampered"
	if c.Snapshot().Coverage.PartialWindowSample[0].QueryGroupKey != "fresh" {
		t.Fatal("a caller's copy of the partial-window sample changed the cached one")
	}
	// The roster reconciles again with nothing changed, as it does every
	// refresh: the group is carried over, still replaced.
	*now = time.Unix(1470, 0)
	c.Reconcile([]CostGroup{moved, fresh}, true)
	c.Publish(*now)
	if got := c.Snapshot().Coverage.PartialWindowSample; !reflect.DeepEqual(got, want) {
		t.Fatalf("after a reconcile that changed nothing = %+v, want %+v", got, want)
	}

	c, now = costDueFixture(t, costMinute)
	account := c.scope.Load().groups["minute"].account
	account.mu.Lock()
	c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true, Trace: next})
	account.mu.Unlock()
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.ContentionDroppedTotal != 1 || coverage.UntrackedObservations != 1 ||
		coverage.UnobservedDueSample[0].Reason != costMissNoRecord {
		t.Fatalf("a tally the account could not take = %+v, want it dropped and counted, the group left unnamed", coverage)
	}
}

// More groups tracked since mid-window than the sample holds: the sample is
// the first costDueMissSampleLimit by key, whatever order they arrived in,
// and the count has them all. A bound read one past the limit would cut the
// sample and insert beyond it, which panics.
func TestThePartialWindowSampleStopsAtItsBound(t *testing.T) {
	now := time.Unix(800, 0)
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: 5 * time.Minute, GroupCapacity: 16, PlanCapacity: 16,
		MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	c.Reconcile(nil, true)
	var groups []CostGroup
	for i := 11; i >= 0; i-- {
		key := fmt.Sprintf("g%02d", i)
		plan := CostPlanIdentity{TenantID: "t", BusinessID: "b", StrategyID: key}
		groups = append(groups, CostGroup{QueryGroupKey: key, QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{plan},
			Schedules: []CostSchedule{{Plan: plan, IntervalSeconds: 60, CompletionOffsetSeconds: 60}}})
	}
	now = time.Unix(1450, 0)
	c.Reconcile(groups, true)
	now = time.Unix(1460, 0)
	c.Publish(now)
	coverage := c.Snapshot().Coverage
	var keys []string
	for _, group := range coverage.PartialWindowSample {
		keys = append(keys, group.QueryGroupKey)
	}
	if coverage.PartialWindowGroups != 12 || fmt.Sprint(keys) != "[g00 g01 g02 g03 g04 g05 g06 g07]" {
		t.Fatalf("twelve groups tracked mid-window = %d %v, want 12 counted and the first eight by key named", coverage.PartialWindowGroups, keys)
	}
}

// A group the scheduler held that also has observations of its key under
// the next revision: the hold accounts for its due Plan, which is held, not
// revision_changed; the untracked observations keep the window incomplete.
func TestAHeldGroupWithObservationsUnderItsNextRevisionIsHeld(t *testing.T) {
	c, now := costDueFixture(t, costMinute)
	c.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageRunnerReturned, Result: ResultTerminal,
		RunOutcome: "query_cooldown", Trace: TraceFields{QueryGroupKey: "minute"}})
	c.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true,
		Trace: TraceFields{QueryGroupKey: "minute", QueryRevision: "q", SnapshotRevision: "s2", ScheduleRevision: "r", EvaluationTime: 1320}})
	c.Publish(*now)
	coverage := c.Snapshot().Coverage
	if unevaluatedOf(t, coverage)["held"] != 1 || coverage.UnobservedDuePlans != 0 || coverage.UntrackedObservations != 1 || !coverage.Incomplete {
		t.Fatalf("a held group with observations under its next revision = %+v, want its Plan held and the window incomplete for the untracked one", coverage)
	}
}

// The partial-window sample keeps the first costDueMissSampleLimit groups by
// key whatever order they arrive in. Ascending, the ninth arrives with the
// sample full and belongs past its end; it is dropped, never inserted there.
// Read directly, so the order is the test's and not a map's.
func TestThePartialWindowSampleKeepsTheFirstGroupsByKeyInAnyArrivalOrder(t *testing.T) {
	keys := make([]string, 12)
	for i := range keys {
		keys[i] = fmt.Sprintf("g%02d", i)
	}
	descending := slices.Clone(keys)
	slices.Reverse(descending)
	interleaved := []string{"g11", "g00", "g10", "g01", "g09", "g02", "g08", "g03", "g07", "g04", "g06", "g05"}
	for name, order := range map[string][]string{"ascending": keys, "descending": descending, "interleaved": interleaved} {
		var kept []CostPartialGroup
		for _, key := range order {
			kept = keepPartialGroup(kept, CostPartialGroup{QueryGroupKey: key})
		}
		var got []string
		for _, group := range kept {
			got = append(got, group.QueryGroupKey)
		}
		if fmt.Sprint(got) != "[g00 g01 g02 g03 g04 g05 g06 g07]" {
			t.Fatalf("%s arrivals kept %v, want the first eight by key", name, got)
		}
	}
}
