package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestRecoveryAgeClassificationMatchesQueryFreeBoundary(t *testing.T) {
	schedule := schedulerSchedule(t, 600, 600, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(600, 0), time.Unix(1795, 0), testRecoveryLimits())
	for _, tc := range []struct {
		millis int64
		want   ReplayDisposition
	}{{1794999, ReplayEligible}, {1795000, ReplayExpired}, {1795001, ReplayExpired}} {
		// Real 600-second schedule deadline: 600 + 600 - 5 seconds.
		_, facts, err := source.classifyRecovery(context.Background(), 600, 1195000, time.UnixMilli(tc.millis))
		if err != nil || facts.Disposition != tc.want {
			t.Fatalf("at %d: %+v err=%v want=%s", tc.millis, facts, err, tc.want)
		}
	}
}

func TestDistanceExpiredPrefixRetainsReplayTail(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(700, 0), testRecoveryLimits())
	source.expiredRangeEnabled = true
	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	slot, due, _, err := source.Next(ctx, "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next: due=%v err=%v", due, err)
	}
	if slot.ExpiredRange == nil {
		t.Fatal("distance-expired prefix was left on per-Slot path")
	}
	p := slot.ExpiredRange
	// 660 is the current grid head; 540/600/660 are the three replay Slots.
	if p.Last.Contract.Slot.EvaluationTime != 480 || p.Next != 540 || p.Count != 7 {
		t.Fatalf("unsafe tail: %+v", p)
	}
}

func TestDistanceExpiredPrefixKeepsBoundaryAndOriginalPending(t *testing.T) {
	for _, tc := range []struct {
		name            string
		next            execution.EvaluationTime
		wantRange       bool
		wantDisposition ReplayDisposition
	}{
		{"K successors allow bulk", 420, true, ReplayExpired},
		{"only one expired slot", 480, false, ReplayExpired},
		{"distance K remains replayable", 540, false, ReplayEligible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
			load := foundProgress(tc.next, 60)
			source := newProductionSlotSourceWithRecoveryForTest(t, catalog, load, time.Unix(700, 0), testRecoveryLimits())
			source.expiredRangeEnabled = true
			ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
			slot, due, _, err := source.Next(ctx, "query-group-1")
			if err != nil || !due || (slot.ExpiredRange != nil) != tc.wantRange || slot.Recovery.Disposition != tc.wantDisposition {
				t.Fatalf("slot=%+v due=%v err=%v", slot, due, err)
			}
			if slot.ExpiredRange == nil {
				return
			}
			proof := slot.ExpiredRange.Clone()
			load.Progress.UnfinishedRange = &proof
			catalog.freezeErr = errors.New("Snapshot must not be read for persisted proof")
			restarted := newProductionSlotSourceWithRecoveryForTest(t, catalog, load, time.Unix(2000, 0), testRecoveryLimits())
			resumed, due, _, err := restarted.Next(context.Background(), "query-group-1")
			if err != nil || !due || resumed.ExpiredRange == nil || !resumed.ExpiredRange.Equal(proof) || resumed.ExpiredRange.CompletionKind() != execution.CompletionGapSkipped {
				t.Fatalf("restart changed original distance proof: %+v due=%v err=%v", resumed, due, err)
			}
		})
	}
}
