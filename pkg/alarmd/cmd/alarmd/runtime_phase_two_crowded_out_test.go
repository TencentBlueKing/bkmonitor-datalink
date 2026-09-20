package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// TestDispatchCrowdedOutIsCountedAndIsNotASkip is the test that had to fail
// before this change and pass after it.
//
// The walk reaches an object it is still holding from an earlier round and
// takes its turn without a word: no queue entry, no skip reason, nothing. An
// operator looking at a deployment starving this way sees three skip counters
// all reading zero, which is the same thing they would see if nothing were
// wrong at all.
//
// The two halves of the assertion are both load-bearing. That the new counter
// moves says the branch is no longer silent. That the three skip reasons do not
// move says the new count was not obtained by relabelling something that was
// already visible - which is the failure mode of adding a fourth value to a
// closed vocabulary, and the reason this is a separate series.
func TestDispatchCrowdedOutIsCountedAndIsNotASkip(t *testing.T) {
	owned := map[execution.QueryGroupIdentity]walkRunner{
		"query-group-held-active": {},
		"query-group-held-queued": {},
		"query-group-free":        {},
	}
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}

	// Control arm: the same owned set with nothing held. Without it, a zero on
	// the new counter in the treatment arm would also be satisfied by a walk
	// that never reaches anything, and "the deployment is fine" and "the probe
	// is dead" would look identical.
	control := metric.NewRecorder(metric.BuildInfo{})
	free := dueIndexDispatcher(clock, control, 8, 8, owned)
	freeRunners, freeRevision := free.bundle.snapshotScheduledRunners()
	free.fillQueues(freeRunners, freeRevision)
	if len(free.normal)+len(free.delayed) != len(owned) {
		t.Fatalf("the control run queued %d of %d objects; with nothing held every object should get a turn",
			len(free.normal)+len(free.delayed), len(owned))
	}
	for _, holder := range []string{"active", "queued"} {
		if got := counterValue(t, control, "bkmonitor_alarmd_dispatch_crowded_out_total",
			map[string]string{"by": holder}); got != 0 {
			t.Fatalf("control dispatch_crowded_out_total{by=%s} = %v, want 0 when nothing is held", holder, got)
		}
	}

	recorder := metric.NewRecorder(metric.BuildInfo{})
	held := dueIndexDispatcher(clock, recorder, 8, 8, owned)
	held.active["query-group-held-active"] = &phaseTwoQueryGroupLifecycle{}
	held.queued["query-group-held-queued"] = &phaseTwoQueryGroupLifecycle{}

	heldRunners, heldRevision := held.bundle.snapshotScheduledRunners()
	held.fillQueues(heldRunners, heldRevision)

	if queued := len(held.normal) + len(held.delayed); queued != 1 {
		t.Fatalf("queued %d objects, want only the one that was not being held", queued)
	}
	if held.walked != len(owned) {
		t.Fatalf("the walk stopped after %d of %d objects; a taken turn still has to consume one "+
			"or the walk never reaches what is behind it", held.walked, len(owned))
	}

	for _, holder := range []string{"active", "queued"} {
		if got := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_crowded_out_total",
			map[string]string{"by": holder}); got != 1 {
			t.Fatalf("dispatch_crowded_out_total{by=%s} = %v, want 1", holder, got)
		}
	}

	// The invariant this change is published under: it splits nothing off the
	// existing counters. Whatever the three skip reasons read before, they read
	// after.
	for _, reason := range []string{"not_due", "backoff", "query_cooldown"} {
		before := counterValue(t, control, "bkmonitor_alarmd_dispatch_skipped_total",
			map[string]string{"reason": reason})
		after := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_skipped_total",
			map[string]string{"reason": reason})
		if before != after {
			t.Fatalf("dispatch_skipped_total{%s} moved from %v to %v; a crowded-out turn is not a skip "+
				"and must not be counted as one", reason, before, after)
		}
	}
}

// TestDispatchCrowdedOutNamesWhichHolderTookTheTurn keeps the two label values
// apart, because they send an operator to different places: active means the
// previous round has not returned and the question is how long rounds take,
// queued means the object is already waiting for a worker and the question is
// capacity. A single boolean would answer neither.
func TestDispatchCrowdedOutNamesWhichHolderTookTheTurn(t *testing.T) {
	owned := map[execution.QueryGroupIdentity]walkRunner{"query-group-a": {}}
	clock := &dueIndexClock{at: time.Unix(1_000, 0)}

	for _, testCase := range []struct {
		holder string
		hold   func(*phaseTwoRunnerDispatcher)
	}{
		{holder: "active", hold: func(d *phaseTwoRunnerDispatcher) {
			d.active["query-group-a"] = &phaseTwoQueryGroupLifecycle{}
		}},
		{holder: "queued", hold: func(d *phaseTwoRunnerDispatcher) {
			d.queued["query-group-a"] = &phaseTwoQueryGroupLifecycle{}
		}},
	} {
		t.Run(testCase.holder, func(t *testing.T) {
			recorder := metric.NewRecorder(metric.BuildInfo{})
			dispatcher := dueIndexDispatcher(clock, recorder, 8, 8, owned)
			testCase.hold(dispatcher)
			runners, revision := dispatcher.bundle.snapshotScheduledRunners()
			dispatcher.fillQueues(runners, revision)

			if got := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_crowded_out_total",
				map[string]string{"by": testCase.holder}); got != 1 {
				t.Fatalf("dispatch_crowded_out_total{by=%s} = %v, want 1", testCase.holder, got)
			}
			other := "queued"
			if testCase.holder == "queued" {
				other = "active"
			}
			if got := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_crowded_out_total",
				map[string]string{"by": other}); got != 0 {
				t.Fatalf("dispatch_crowded_out_total{by=%s} = %v, want 0; the holder that did not take "+
					"the turn must not be credited with it", other, got)
			}
		})
	}
}
