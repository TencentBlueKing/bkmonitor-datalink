package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func retainedSlot(group string, evaluation, following int64, retained uint64) observability.Observation {
	return observability.Observation{Stage: observability.StageSlotCompleted, Result: observability.ResultSuccess,
		Duration: time.Millisecond, DurationKnown: true, FollowingSlot: following,
		Trace:           observability.TraceFields{QueryGroupKey: group, EvaluationTime: evaluation},
		SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: retained}}
}

// The observation memory line reserves for the retained pool what the owned
// Query Groups were measured to retain and a quarter more, within the pool's
// ceiling and never under what it holds; the ceiling until every owned group
// has a reading, and again once the reading is stale. From that unmeasured
// ceiling it drops to the first complete reading at once; from a measured
// reservation it rises at once and comes down by at most the ceiling over
// ten minutes.
func TestTheRetainedReservationIsTheOwnedGroupsMeasuredPeak(t *testing.T) {
	const ceiling = 1000
	now := time.Unix(3600, 0)
	clock := func() time.Time { return now }
	census := observability.NewRetainedPeakCensus(5*time.Minute, clock)
	census.SetReplayAge(10 * time.Minute)
	source := newWorkerCostSource(census, nil, clock)
	owned := []execution.QueryGroupIdentity{"qg-hourly", "qg-minute"}
	source.boundByOwned(func() []execution.QueryGroupIdentity { return owned })
	reserve := func(held uint64, want uint64, why string) {
		t.Helper()
		if got := source.RetainedReservation(ceiling, held); got != want {
			t.Fatalf("%s: %d, want %d", why, got, want)
		}
	}
	reserve(0, ceiling, "before any reading")
	census.Observe(context.Background(), retainedSlot("qg-hourly", 3600, 7200, 100))
	source.Costs()
	reserve(0, ceiling, "one owned group unmeasured")
	census.Observe(context.Background(), retainedSlot("qg-minute", 3600, 3660, 60))
	source.Costs()
	reserve(0, 200, "the first complete reading, at once: 160 and a quarter")
	reserve(250, 250, "holding 250")
	// Twenty minutes after the hourly Slot its peak still counts: the census
	// holds it until its next Slot. The minute group's 60 went with its own
	// two periods and the replay age; its latest Slot retained 40.
	now = time.Unix(3600, 0).Add(20 * time.Minute)
	census.Observe(context.Background(), retainedSlot("qg-minute", 4800, 4860, 40))
	source.Costs()
	reserve(0, 175, "twenty minutes after the hourly Slot: (100 + 40) and a quarter")
	// A heavier Slot raises it at once.
	census.Observe(context.Background(), retainedSlot("qg-minute", 4860, 4920, 900))
	source.Costs()
	reserve(0, ceiling, "peaks 100 and 900")
	// The heavy minute group handed over leaves the hourly one, 125 with its
	// quarter; from a measured reservation that comes down a tenth of the
	// ceiling a minute.
	owned = []execution.QueryGroupIdentity{"qg-hourly"}
	source.Costs()
	reserve(0, ceiling, "the handover's instant")
	now = now.Add(time.Minute)
	source.Costs()
	reserve(0, 900, "a minute after")
	now = now.Add(9 * time.Minute)
	source.Costs()
	reserve(0, 125, "ten minutes after")
	// The same reading, three heartbeats and a second later, is stale.
	now = now.Add(rosterPeakStale + time.Second)
	reserve(0, ceiling, "a stale reading")
	// From that unmeasured ceiling a fresh reading drops it at once.
	source.Costs()
	reserve(0, 125, "a fresh reading after a stale one")
	// A group taken over and not yet run here is unmeasured.
	owned = append(owned, "qg-new")
	source.Costs()
	reserve(0, ceiling, "a new owned group")
	// Nothing owned is no measurement either.
	owned = nil
	source.Costs()
	reserve(0, ceiling, "nothing owned")
}
