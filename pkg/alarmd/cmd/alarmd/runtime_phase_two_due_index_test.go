package main

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// dueIndexClock is a hand-advanced clock, because every question here is about
// which second a bound falls on.
type dueIndexClock struct{ at time.Time }

func (clock *dueIndexClock) now() time.Time { return clock.at }

// dueIndexDispatcher builds the same synthetic dispatcher the walk tests use,
// with a real Recorder attached so the metrics can be read back, and a clock the
// test drives.
func dueIndexDispatcher(
	clock *dueIndexClock,
	recorder *metric.Recorder,
	readyCapacity, recoveryCapacity int,
	owned map[execution.QueryGroupIdentity]walkRunner,
) *phaseTwoRunnerDispatcher {
	var cfg config.Config
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = readyCapacity
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = recoveryCapacity
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: clock.now, Recorder: recorder},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		assigned:     make(map[execution.QueryGroupIdentity]struct{}),
	}
	bundle.mu.Lock()
	for queryGroup, runner := range owned {
		bundle.setRunnerLocked(queryGroup, &phaseTwoQueryGroupLifecycle{runner: runner})
		bundle.assigned[queryGroup] = struct{}{}
	}
	bundle.mu.Unlock()
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.generation = 1
	return dispatcher
}

// seedDueIndex writes a bound for every owned Query Group directly, so a test
// can put the index into a state the dispatcher would take many rounds to reach.
func seedDueIndex(dispatcher *phaseTwoRunnerDispatcher, bound scheduler.RunnerDueBound, at time.Time) {
	runners, _ := dispatcher.bundle.snapshotScheduledRunners()
	for _, scheduled := range runners {
		dispatcher.dueIndex.Record(scheduled.queryGroup, scheduled.lifecycle,
			dispatcher.dueIndex.versionEpoch, bound, at)
	}
}

func counterValue(t *testing.T, recorder *metric.Recorder, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.Metric {
			if !sampleHasLabels(sample, labels) {
				continue
			}
			if sample.Counter != nil {
				return sample.Counter.GetValue()
			}
			if sample.Gauge != nil {
				return sample.Gauge.GetValue()
			}
		}
		t.Fatalf("metric %s has no series with labels %v", name, labels)
	}
	t.Fatalf("metric %s is not registered", name)
	return 0
}

func sampleHasLabels(sample *dto.Metric, labels map[string]string) bool {
	for name, value := range labels {
		found := false
		for _, pair := range sample.Label {
			if pair.GetName() == name && pair.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func dispatchOrder(queue []phaseTwoQueuedRunner) []execution.QueryGroupIdentity {
	return queuedNames(queue)
}

// TestDueIndexShadowLeavesDispatchUnchanged is the proof that this commit
// changes nothing about who runs and when.
//
// The index is not consulted by any dispatch decision yet, and "not consulted"
// is hard to demonstrate by absence. So it is demonstrated by contradiction
// instead: one dispatcher is given an index that says every Query Group is an
// hour away from being due, the other is given an index that was never written
// to, and the two are driven through the same walk over the same owned set. If
// any dispatch decision read the index, the two runs would diverge - the
// suppressing one would queue nothing. They must agree on the ready queue, the
// recovery queue, the order within each, and the rotation counts.
//
// When dispatch suppression lands, this test is what stops compiling as a
// no-change claim: the seeded run is then supposed to queue nothing, and the
// assertion has to be rewritten rather than quietly continuing to pass.
func TestDueIndexShadowLeavesDispatchUnchanged(t *testing.T) {
	owned := map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {readyAt: time.Unix(4_000, 0)},
		"query-group-d": {},
	}
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}

	empty := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 2, 4, owned)
	emptyRunners, emptyRevision := empty.bundle.snapshotScheduledRunners()
	empty.fillQueues(emptyRunners, emptyRevision)

	seeded := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 2, 4, owned)
	seedDueIndex(seeded, scheduler.RunnerDueBound{
		NotDueUntilUnix: clock.at.Unix() + 3_600, IntervalSeconds: 60,
	}, clock.at)
	if entries := seeded.dueIndex.Len(); entries != len(owned) {
		t.Fatalf("seeded index holds %d bounds, want %d", entries, len(owned))
	}
	seededRunners, seededRevision := seeded.bundle.snapshotScheduledRunners()
	seeded.fillQueues(seededRunners, seededRevision)

	if got, want := dispatchOrder(seeded.normal), dispatchOrder(empty.normal); !equalQueryGroups(got, want) {
		t.Fatalf("a fully suppressing index changed the ready queue to %v, want %v", got, want)
	}
	if got, want := dispatchOrder(seeded.delayed), dispatchOrder(empty.delayed); !equalQueryGroups(got, want) {
		t.Fatalf("a fully suppressing index changed the recovery queue to %v, want %v", got, want)
	}
	// startedAt is a wall-clock reading and differs between the two runs by
	// construction; the counts are what say the walk made the same decisions.
	if seeded.rotation.offered != empty.rotation.offered ||
		seeded.rotation.queued != empty.rotation.queued ||
		seeded.rotation.deferred != empty.rotation.deferred ||
		seeded.rotation.completed != empty.rotation.completed ||
		seeded.rotation.truncated != empty.rotation.truncated ||
		seeded.walked != empty.walked || seeded.cursor != empty.cursor {
		t.Fatalf("a fully suppressing index changed the rotation to %+v walked=%d cursor=%s, want %+v walked=%d cursor=%s",
			seeded.rotation, seeded.walked, seeded.cursor, empty.rotation, empty.walked, empty.cursor)
	}
	if len(seeded.normal) == 0 {
		t.Fatal("the unsuppressed run queued nothing, so agreeing with it proves nothing")
	}
	// Every Query Group was predicted not due, which is the state the comparison
	// has to be able to see for the falsifier below to mean anything.
	for _, queued := range seeded.normal {
		if queued.scheduled.predictedDue {
			t.Fatalf("%s was predicted due against a bound an hour away", queued.scheduled.queryGroup)
		}
	}
}

func equalQueryGroups(left, right []execution.QueryGroupIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// TestDueIndexPredictionCounterReachesEveryOutcome is what makes the falsifier
// readable.
//
// The series that matters - predicted not due, actually due - is supposed to
// stay at zero forever. A counter whose healthy value is zero cannot be read on
// its own: zero is also what an unregistered metric, an unscraped endpoint and
// a comparison that never runs all look like, and so is a label combination
// that cannot be produced at all. So this test produces it on purpose, from a
// bound that is deliberately later than the truth, and asserts it moves. A
// falsifier that has never been seen to fire cannot be used to conclude
// anything from its silence.
//
// The other three combinations are asserted in the same run, because the four
// are counted by one comparison and a comparison that only counted the
// violation would be silent in exactly the situation where silence is
// ambiguous.
func TestDueIndexPredictionCounterReachesEveryOutcome(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	owned := map[execution.QueryGroupIdentity]walkRunner{
		// Says it found a due Slot when it ran.
		"query-group-due": {bound: scheduler.RunnerDueBound{
			Verdict: scheduler.DueVerdictDue, Executed: true, IntervalSeconds: 60,
		}},
		// Says it found the Slot still in the future.
		"query-group-idle": {bound: scheduler.RunnerDueBound{
			Verdict: scheduler.DueVerdictNotDue, NotDueUntilUnix: 1_060, IntervalSeconds: 60,
		}},
	}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, owned)

	// Round one: nothing is indexed, so both are predicted due. One is, one is
	// not, which fills {due,due} and {due,not_due}.
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	drainQueues(t, dispatcher)

	// Round two: both now carry a bound. The idle one is correctly predicted not
	// due and is still not due, which fills {not_due,not_due}. The due one
	// carries a bound this test forces to be wrong - a full minute later than the
	// Slot it is about to resolve - which is the misprediction the falsifier
	// exists to catch.
	dispatcher.dueIndex.Record("query-group-due", dispatcher.bundle.runners["query-group-due"],
		dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 60, IntervalSeconds: 60}, clock.at)
	dispatcher.beginGeneration()
	runners, revision = dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	drainQueues(t, dispatcher)

	const name = "bkmonitor_alarmd_due_index_prediction_total"
	for _, want := range []struct {
		prediction, actual string
		atLeast            float64
	}{
		{"due", "due", 1},
		{"due", "not_due", 1},
		{"not_due", "not_due", 1},
		{"not_due", "due", 1},
	} {
		got := counterValue(t, recorder, name, map[string]string{
			"prediction": want.prediction, "actual": want.actual,
		})
		if got < want.atLeast {
			t.Errorf("prediction=%s actual=%s counted %v, want at least %v",
				want.prediction, want.actual, got, want.atLeast)
		}
	}
}

// drainQueues dispatches and returns every queued Runner, which is what makes
// the index rewrite its bounds.
func drainQueues(t *testing.T, dispatcher *phaseTwoRunnerDispatcher) {
	t.Helper()
	for len(dispatcher.normal) > 0 || len(dispatcher.delayed) > 0 {
		delayed := len(dispatcher.normal) == 0
		queue := dispatcher.normal
		if delayed {
			queue = dispatcher.delayed
		}
		scheduled := queue[0].scheduled
		dispatcher.markDispatched(scheduled, delayed, delayed)
		dispatcher.handleResult(context.Background(),
			phaseTwoScheduledResult{scheduled: scheduled, ran: true, attempted: true}, true)
	}
}

// TestDueIndexFailsOpenWhenTheHeaderCannotBeRead covers the degraded path the
// design chooses on purpose, and covers it by driving it rather than by
// reasoning about it.
//
// version_unknown is another counter whose healthy value is zero, so the same
// problem applies: without a test that makes it move, a zero reading cannot be
// told apart from a fail-open path that was never wired up. The companion
// series is checked in the same run - unchanged has to grow while the header is
// stable - because that is what says the comparison is happening at all.
func TestDueIndexFailsOpenWhenTheHeaderCannotBeRead(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {},
	})
	lifecycle := dispatcher.bundle.runners["query-group-a"]
	seedBound := scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 600, IntervalSeconds: 60}

	// A stable header leaves the bound alone, and says so.
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-1", known: true})
	dispatcher.beginGeneration()
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch, seedBound, clock.at)
	dispatcher.beginGeneration()
	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at); due {
		t.Fatal("a stable header dropped a bound that is still ten minutes away")
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_version_check_total",
		map[string]string{"result": "unchanged"}); got < 1 {
		t.Fatalf("unchanged header comparisons counted %v, want at least 1: without this, "+
			"a zero unknown count cannot be told apart from a check that never ran", got)
	}

	// A header that cannot be read drops it.
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{known: false})
	dispatcher.beginGeneration()
	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at); !due {
		t.Fatal("an unreadable header left the Query Group parked on a bound it cannot vouch for")
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total",
		map[string]string{"trigger": "version_unknown"}); got < 1 {
		t.Fatalf("version_unknown counted %v, want at least 1", got)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_version_check_total",
		map[string]string{"result": "unknown"}); got < 1 {
		t.Fatalf("unknown header comparisons counted %v, want at least 1", got)
	}
}

// TestDueIndexPublicationDropsScheduleBoundsAndKeepsBackoff covers the one
// direction that can make a Query Group due earlier than its bound says, and
// the one kind of bound a publication must not touch.
//
// A publication can bring a Slot forward, so every bound taken from the
// schedule is dropped. A Runner's own backoff is not a statement about the
// schedule - it is this replica waiting out a failure or a readiness deferral -
// and a publication says nothing about when that wait ends. Dropping it would
// retry the failure immediately on every publication.
func TestDueIndexPublicationDropsScheduleBoundsAndKeepsBackoff(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-scheduled": {},
		"query-group-backoff":   {},
	})
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-1", known: true})
	dispatcher.beginGeneration()

	scheduled := dispatcher.bundle.runners["query-group-scheduled"]
	backoff := dispatcher.bundle.runners["query-group-backoff"]
	dispatcher.dueIndex.Record("query-group-scheduled", scheduled, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 300, IntervalSeconds: 60}, clock.at)
	dispatcher.dueIndex.Record("query-group-backoff", backoff, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 300, Deferred: true, IntervalSeconds: 60}, clock.at)

	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-2", known: true})
	dispatcher.beginGeneration()

	if due, _ := dispatcher.dueIndex.Predict("query-group-scheduled", scheduled, clock.at); !due {
		t.Fatal("a publication left a schedule bound standing, so a Slot it brought forward would wait")
	}
	if due, _ := dispatcher.dueIndex.Predict("query-group-backoff", backoff, clock.at); due {
		t.Fatal("a publication cancelled a Runner's own backoff, which it says nothing about")
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total",
		map[string]string{"trigger": "version_change"}); got < 1 {
		t.Fatalf("version_change counted %v, want at least 1", got)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_version_check_total",
		map[string]string{"result": "changed"}); got < 1 {
		t.Fatalf("changed header comparisons counted %v, want at least 1", got)
	}
}

// TestDueIndexDropsBoundsWrittenUnderASupersededPublication covers the window
// the sweep on its own cannot close: a publication that lands while a round is
// already running.
//
// The round read the schedule under the old publication, so its bound is
// already superseded when it returns - but the sweep has been and gone, and
// writing the bound now would reinstate exactly what the sweep dropped. The
// round is therefore stamped with the publication it started under, and a write
// whose stamp no longer matches is taken as due now.
func TestDueIndexDropsBoundsWrittenUnderASupersededPublication(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {},
	})
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-1", known: true})
	dispatcher.beginGeneration()
	lifecycle := dispatcher.bundle.runners["query-group-a"]
	_, epoch := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at)

	// The publication lands while the round is still out.
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-2", known: true})
	dispatcher.beginGeneration()

	dispatcher.dueIndex.Record("query-group-a", lifecycle, epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 300, IntervalSeconds: 60}, clock.at)
	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at); !due {
		t.Fatal("a bound read under a superseded publication was accepted")
	}
}

// TestDueIndexDropsEntriesForAReplacedLifecycle pins that a Query Group taken
// over again does not inherit the previous owner's bound. This is the one form
// of staleness the publication sweep cannot catch: the inherited bound may well
// still be anchored to the current publication and look perfectly valid.
func TestDueIndexDropsEntriesForAReplacedLifecycle(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {},
	})
	first := dispatcher.bundle.runners["query-group-a"]
	dispatcher.dueIndex.Record("query-group-a", first, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 300, IntervalSeconds: 60}, clock.at)

	replacement := &phaseTwoQueryGroupLifecycle{runner: walkRunner{}}
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.setRunnerLocked("query-group-a", replacement)
	dispatcher.bundle.mu.Unlock()

	_, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.dropStaleQueued(revision)

	if dispatcher.dueIndex.Len() != 0 {
		t.Fatal("the bound written for the previous lifecycle survived the takeover")
	}
	if due, _ := dispatcher.dueIndex.Predict("query-group-a", replacement, clock.at); !due {
		t.Fatal("a Query Group with no bound of its own was not treated as due")
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total",
		map[string]string{"trigger": "ownership"}); got < 1 {
		t.Fatalf("ownership counted %v, want at least 1", got)
	}
}

// TestDueIndexBoundsTheRetiredRecheck pins that a retired Query Group is
// rechecked on a finite timer. Retirement is revocable through a tombstone in
// the timeline, so an infinite bound would suppress a Query Group that has been
// brought back - and a publication sweep only covers the case where the
// revocation arrives as a publication this replica observes.
func TestDueIndexBoundsTheRetiredRecheck(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {},
	})
	lifecycle := dispatcher.bundle.runners["query-group-a"]
	// Retirement happens to a Query Group that has been running, so the entry
	// already exists. A first-ever round is reported as absent instead, because
	// "this replica has never evaluated it" is the stronger statement.
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictDue, Executed: true, IntervalSeconds: 60}, clock.at)
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{Retired: true, Verdict: scheduler.DueVerdictNotDue}, clock.at)

	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle,
		clock.at.Add(time.Duration(dueIndexRetiredRecheckSeconds-1)*time.Second)); due {
		t.Fatal("a retired Query Group was rechecked before its recheck was due")
	}
	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle,
		clock.at.Add(time.Duration(dueIndexRetiredRecheckSeconds)*time.Second)); !due {
		t.Fatalf("a retired Query Group was not rechecked after %d seconds, so a revoked "+
			"retirement would never resume", dueIndexRetiredRecheckSeconds)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total",
		map[string]string{"trigger": "retired_ttl"}); got < 1 {
		t.Fatalf("retired_ttl counted %v, want at least 1", got)
	}
}

// TestDueIndexNeverHoldsBackBacklog pins the safety property the whole design
// rests on, at the level the index sees it.
//
// A Query Group whose cursor is in the past resolves a Slot rather than a
// future bound, so the round it returns carries no bound at all and the entry
// is due now. Recovery, replay, expired ranges and gap closure all arrive here
// the same way. The index cannot suppress them because it is never given
// anything to suppress them with.
func TestDueIndexNeverHoldsBackBacklog(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 4, 4,
		map[execution.QueryGroupIdentity]walkRunner{"query-group-a": {}})
	lifecycle := dispatcher.bundle.runners["query-group-a"]

	// Start from a bound far in the future, so a failure to overwrite it would
	// be visible rather than being masked by an empty index.
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: clock.at.Unix() + 3_600, IntervalSeconds: 60}, clock.at)

	// A round that resolved and ran a Slot. This is what every backlog round is:
	// the cursor was in the past, so Next took the branch that freezes a Slot and
	// established no future bound.
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictDue, Executed: true, IntervalSeconds: 60}, clock.at)

	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at); !due {
		t.Fatal("a Query Group that just executed a backlog Slot was parked")
	}
}

// TestDueIndexMaturedDeferralJoinsTheRecoveryQueue pins where a matured backoff
// goes. The recovery queue is what alternates with the ready queue, and that
// alternation is the only thing keeping recovery from being starved by normal
// work. A backoff that came back as normal would be jumping that rotation.
func TestDueIndexMaturedDeferralJoinsTheRecoveryQueue(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	// The Runner is still reporting the backoff instant it was given, which is
	// what a Runner does between the deferral expiring and its next successful
	// round.
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 4, 4,
		map[execution.QueryGroupIdentity]walkRunner{
			"query-group-a": {readyAt: time.Unix(990, 0)},
		})
	lifecycle := dispatcher.bundle.runners["query-group-a"]
	dispatcher.dueIndex.Record("query-group-a", lifecycle, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 990, Deferred: true, IntervalSeconds: 60}, clock.at)

	if due, _ := dispatcher.dueIndex.Predict("query-group-a", lifecycle, clock.at); !due {
		t.Fatal("a deferral whose instant has passed was still treated as parked")
	}
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	if len(dispatcher.delayed) != 1 || len(dispatcher.normal) != 0 {
		t.Fatalf("a matured deferral queued ready=%v recovery=%v, want it in the recovery queue alone",
			queuedNames(dispatcher.normal), queuedNames(dispatcher.delayed))
	}
	dispatcher.sortDelayed()
	now := clock.now()
	if dispatcher.delayed[0].readyAt.After(now) {
		t.Fatal("the matured deferral is not selectable, so the alternation cannot reach it")
	}
	dispatcher.markDispatched(dispatcher.delayed[0].scheduled, true, true)
	if dispatcher.preferDelayed {
		t.Fatal("dispatching from the recovery queue did not hand the next turn to the ready queue")
	}
}

// TestOverdueWakesReportsOldestFirstWithATrueTotal pins the shape handed to the
// page that shows stuck objects. The order and the untruncated total are both
// part of the answer: a list without the total cannot say whether what it shows
// is all of it, and a reader that has to judge lateness needs the interval each
// wake belongs to.
func TestOverdueWakesReportsOldestFirstWithATrueTotal(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{
			"query-group-a": {}, "query-group-b": {}, "query-group-c": {}, "query-group-d": {},
		})
	for queryGroup, wake := range map[execution.QueryGroupIdentity]int64{
		"query-group-a": 900, "query-group-b": 800, "query-group-c": 950, "query-group-d": 2_000,
	} {
		dispatcher.dueIndex.Record(queryGroup, dispatcher.bundle.runners[queryGroup],
			dispatcher.dueIndex.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: wake, IntervalSeconds: 60},
			time.Unix(wake, 0))
	}

	wakes, total := dispatcher.dueIndex.OverdueWakes(clock.at, 2)
	if total != 3 {
		t.Fatalf("overdue total = %d, want 3 (the Query Group whose wake is still ahead is not overdue)", total)
	}
	if len(wakes) != 2 || wakes[0].QueryGroup != "query-group-b" || wakes[1].QueryGroup != "query-group-a" {
		t.Fatalf("overdue wakes = %+v, want query-group-b then query-group-a", wakes)
	}
	for _, wake := range wakes {
		if wake.IntervalSeconds <= 0 {
			t.Fatalf("wake %+v has no evaluation interval, so how late it is cannot be judged", wake)
		}
	}
}

// TestDueIndexClearingLosesNothingButWork pins that the index is a cache. Every
// recovery path in the design is allowed to empty it, and that is only true if
// emptying it costs extra calls rather than changing any answer.
func TestDueIndexClearingLosesNothingButWork(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 4, 4,
		map[execution.QueryGroupIdentity]walkRunner{"query-group-a": {}, "query-group-b": {}})
	seedDueIndex(dispatcher, scheduler.RunnerDueBound{
		NotDueUntilUnix: clock.at.Unix() + 3_600, IntervalSeconds: 60,
	}, clock.at)

	dispatcher.dueIndex.Clear()
	if dispatcher.dueIndex.Len() != 0 {
		t.Fatal("clearing left bounds behind")
	}
	for queryGroup, lifecycle := range dispatcher.bundle.runners {
		if due, _ := dispatcher.dueIndex.Predict(queryGroup, lifecycle, clock.at); !due {
			t.Fatalf("%s was still parked after the index was cleared", queryGroup)
		}
	}
	// The cleared index is writable again, so clearing is a state the index can
	// be in rather than one it has to be rebuilt from.
	seedDueIndex(dispatcher, scheduler.RunnerDueBound{
		NotDueUntilUnix: clock.at.Unix() + 60, IntervalSeconds: 60,
	}, clock.at)
	if dispatcher.dueIndex.Len() != 2 {
		t.Fatalf("index holds %d bounds after being refilled, want 2", dispatcher.dueIndex.Len())
	}
}

// TestDueIndexRecomputeTriggersAreAllReachable walks the whole trigger
// vocabulary and makes each label move.
//
// The vocabulary is closed, so a label nothing can produce is a label that
// reads as a permanent zero - and a permanent zero is indistinguishable from a
// path that was never wired up. Anything listed here and not asserted is a
// series that cannot be used to conclude anything, so the list and the
// assertions are kept in step deliberately.
func TestDueIndexRecomputeTriggersAreAllReachable(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, recorder, 4, 4, map[execution.QueryGroupIdentity]walkRunner{
		"query-group-a": {}, "query-group-b": {},
	})
	first := dispatcher.bundle.runners["query-group-a"]
	second := dispatcher.bundle.runners["query-group-b"]
	epoch := func() uint64 { return dispatcher.dueIndex.versionEpoch }

	// absent: the first bound ever written for this Query Group.
	dispatcher.dueIndex.Record("query-group-a", first, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_060, IntervalSeconds: 60}, clock.at)
	// attempt: an idle refresh of a bound that already existed.
	dispatcher.dueIndex.Record("query-group-a", first, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_120, IntervalSeconds: 60}, clock.at)
	// execute: the round resolved and ran a Slot, so there is no future bound.
	dispatcher.dueIndex.Record("query-group-a", first, epoch(),
		scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictDue, Executed: true, IntervalSeconds: 60}, clock.at)
	// deferral: the bound came from the Runner's own backoff.
	dispatcher.dueIndex.Record("query-group-a", first, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_030, Deferred: true, IntervalSeconds: 60}, clock.at)
	// retired_ttl: the schedule is retired, so the bound is the bounded recheck.
	dispatcher.dueIndex.Record("query-group-b", second, epoch(),
		scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictDue, Executed: true, IntervalSeconds: 60}, clock.at)
	dispatcher.dueIndex.Record("query-group-b", second, epoch(),
		scheduler.RunnerDueBound{Retired: true, Verdict: scheduler.DueVerdictNotDue}, clock.at)

	// version_change and version_unknown.
	dispatcher.dueIndex.Record("query-group-b", second, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_600, IntervalSeconds: 60}, clock.at)
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{tag: "publication-1", known: true})
	dispatcher.beginGeneration()
	dispatcher.dueIndex.Record("query-group-b", second, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_600, IntervalSeconds: 60}, clock.at)
	dispatcher.controlVersion.Store(&phaseTwoControlVersionSample{known: false})
	dispatcher.beginGeneration()

	// ownership: an entry left behind by a lifecycle this replica no longer owns.
	dispatcher.dueIndex.Record("query-group-a", first, epoch(),
		scheduler.RunnerDueBound{NotDueUntilUnix: 9_000, IntervalSeconds: 60}, clock.at)
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.setRunnerLocked("query-group-a", &phaseTwoQueryGroupLifecycle{runner: walkRunner{}})
	dispatcher.bundle.mu.Unlock()
	_, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.dropStaleQueued(revision)

	for _, trigger := range []string{
		"absent", "attempt", "execute", "deferral", "retired_ttl",
		"version_change", "version_unknown", "ownership",
	} {
		if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_recomputed_total",
			map[string]string{"trigger": trigger}); got < 1 {
			t.Errorf("trigger %s counted %v, want at least 1; a trigger nothing can produce is a "+
				"series that reads as permanently healthy", trigger, got)
		}
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_due_index_entries", nil); got < 1 {
		t.Errorf("due_index_entries = %v, want the bounds actually held", got)
	}
}

// TestDispatcherReadsTheActivationHeaderWithoutBlockingTheLoop pins that the
// header is really being read in a running dispatcher.
//
// Every invalidation rule depends on this one reading. If the poller were never
// started, the version counters would sit at zero, bounds would never be
// dropped on a publication, and nothing in the metrics would say so - the index
// would simply look very effective.
func TestDispatcherReadsTheActivationHeaderWithoutBlockingTheLoop(t *testing.T) {
	var cfg config.Config
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 2
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = 2
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 1
	cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
	control := &fakePhaseTwoControl{versionTag: "publication-1", versionKnown: true}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{
			Config: cfg, Now: time.Now, Recorder: metric.NewRecorder(metric.BuildInfo{}), Control: control,
		},
		runners:  make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		assigned: make(map[execution.QueryGroupIdentity]struct{}),
	}
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher.start(ctx)
	defer dispatcher.stop()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if sample := dispatcher.controlVersion.Load(); sample != nil {
			if !sample.known || sample.tag != "publication-1" {
				t.Fatalf("activation header sample = %+v, want the published tag", sample)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dispatcher never read the activation header, so no publication could ever " +
				"drop a bound and nothing in the metrics would say so")
		}
		time.Sleep(time.Millisecond)
	}
	dispatcher.beginGeneration()
	if got := counterValue(t, bundle.dependencies.Recorder,
		"bkmonitor_alarmd_due_index_version_check_total", map[string]string{"result": "changed"}); got < 1 {
		t.Fatalf("the first header reading was not compared: changed = %v", got)
	}
}

// TestDueIndexReplacesTheEntryOfAPreviousOwnerWholesale covers the case where a
// round for the new owner returns before the owned set has been walked again,
// so the previous owner's entry is still in the index.
//
// Replacing only the map reference would leave the old entry in the heap with
// nothing pointing at it: the lifecycle sweep skips it because the map no
// longer names it, and no later pass would ever remove it. The leak is
// invisible until it is measured, so it is measured.
func TestDueIndexReplacesTheEntryOfAPreviousOwnerWholesale(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 4, 4,
		map[execution.QueryGroupIdentity]walkRunner{"query-group-a": {}})
	previous := dispatcher.bundle.runners["query-group-a"]
	dispatcher.dueIndex.Record("query-group-a", previous, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 9_000, IntervalSeconds: 60}, clock.at)

	current := &phaseTwoQueryGroupLifecycle{runner: walkRunner{}}
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.setRunnerLocked("query-group-a", current)
	dispatcher.bundle.mu.Unlock()
	dispatcher.dueIndex.Record("query-group-a", current, dispatcher.dueIndex.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 1_060, IntervalSeconds: 60}, clock.at)

	if dispatcher.dueIndex.Len() != 1 {
		t.Fatalf("the index names %d Query Groups, want 1", dispatcher.dueIndex.Len())
	}
	dispatcher.dueIndex.mu.Lock()
	pending := len(dispatcher.dueIndex.pending)
	dispatcher.dueIndex.mu.Unlock()
	if pending != 1 {
		t.Fatalf("the wake heap holds %d entries for one Query Group; the previous owner's entry "+
			"is unreachable and nothing will ever remove it", pending)
	}
	// The surviving entry is the new owner's, at the new owner's bound.
	wakes, total := dispatcher.dueIndex.OverdueWakes(time.Unix(1_060, 0), 4)
	if total != 1 || len(wakes) != 1 || !wakes[0].WakeAt.Equal(time.Unix(1_060, 0)) {
		t.Fatalf("overdue wakes = %+v total=%d, want the new owner's bound alone", wakes, total)
	}
}
