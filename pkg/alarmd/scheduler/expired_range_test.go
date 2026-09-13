package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestExpiredRangeProductionSourceAgeBoundaryAndDisabledResume(t *testing.T) {
	for _, tc := range []struct {
		at   int64
		last execution.EvaluationTime
	}{{954999, 240}, {955000, 300}, {955001, 300}} {
		t.Run(time.UnixMilli(tc.at).String(), func(t *testing.T) {
			schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
			source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.UnixMilli(tc.at), testRecoveryLimits())
			source.expiredRangeEnabled = true
			ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
			slot, due, _, err := source.Next(ctx, "query-group-1")
			if err != nil || !due || slot.ExpiredRange == nil {
				t.Fatalf("create: %+v %v %v", slot, due, err)
			}
			p := slot.ExpiredRange
			if p.Last.Contract.Slot.EvaluationTime != tc.last || p.Next != tc.last+60 || p.Count != uint32((tc.last-120)/60+1) {
				t.Fatalf("range=%+v", p)
			}
			if len(catalog.requests) != 2 {
				t.Fatalf("freeze calls=%d, want first+last independent of span", len(catalog.requests))
			}
			load := foundProgress(120, 60)
			load.Progress.UnfinishedRange = p
			catalog.freezeErr = controlplane.ErrSnapshotUnavailable
			restarted := newProductionSlotSourceWithRecoveryForTest(t, catalog, load, time.Unix(2000, 0), testRecoveryLimits())
			resumed, due, _, err := restarted.Next(context.Background(), "query-group-1")
			if err != nil || !due || resumed.ExpiredRange == nil || !resumed.ExpiredRange.Equal(*p) {
				t.Fatalf("disabled resume: %+v %v %v", resumed, due, err)
			}
			if len(catalog.requests) != 2 {
				t.Fatal("persisted range attempted Snapshot re-freeze")
			}
		})
	}
}

func TestExpiredRangeSourceRequiresEnabledUnstartedSingleFlight(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		enabled, flight, single bool
	}{{"disabled", false, true, false}, {"without flight", true, false, false}, {"single pending", true, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
			load := foundProgress(120, 60)
			source := newProductionSlotSourceWithRecoveryForTest(t, catalog, load, time.Unix(1000, 0), testRecoveryLimits())
			if tc.single {
				slot, _, _, err := source.Next(context.Background(), "query-group-1")
				if err != nil {
					t.Fatal(err)
				}
				load.Progress.UnfinishedSlot = &execution.UnfinishedSlotProjection{Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets, EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli, KeepUntilUnixMilli: slot.KeepUntilUnixMilli}
			}
			source.expiredRangeEnabled = tc.enabled
			ctx := context.Background()
			if tc.flight {
				ctx = context.WithValue(ctx, rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
			}
			slot, due, _, err := source.Next(ctx, "query-group-1")
			if err != nil || !due || slot.ExpiredRange != nil {
				t.Fatalf("unexpected range %+v %v %v", slot, due, err)
			}
		})
	}
}

func TestExpiredRangeSourceStopsAtRetirementAndBlocksChangedTail(t *testing.T) {
	end := execution.EvaluationTime(277)
	schedule := schedulerSchedule(t, 60, 60, &end, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &end}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(2000, 0), testRecoveryLimits())
	source.expiredRangeEnabled = true
	slot, due, _, err := source.Next(context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1")), "query-group-1")
	if err != nil || !due || slot.ExpiredRange == nil || slot.ExpiredRange.Last.Contract.Slot.EvaluationTime != 240 || slot.ExpiredRange.Next != 277 {
		t.Fatalf("retirement %+v %v %v", slot, due, err)
	}
	load := foundProgress(120, 60)
	load.Progress.UnfinishedRange = slot.ExpiredRange
	changed := execution.EvaluationTime(200)
	catalog.schedules[0].Segment.End = &changed
	resumed := newProductionSlotSourceWithRecoveryForTest(t, catalog, load, time.Unix(3000, 0), testRecoveryLimits())
	_, due, _, err = resumed.Next(context.Background(), "query-group-1")
	var blocked *SourceBlockedError
	if due || err == nil || (!errors.As(err, &blocked) && !errors.Is(err, controlplane.ErrScheduleUnavailable)) {
		t.Fatalf("changed tail due=%v err=%v", due, err)
	}
}
