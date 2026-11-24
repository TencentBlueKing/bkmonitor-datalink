package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

type slowLegacyRefresh struct{ calls int }

func (s *slowLegacyRefresh) Refresh(ctx context.Context, _, _ string, _ []int64) error {
	s.calls++
	<-ctx.Done()
	return ctx.Err()
}

func TestEffectiveMaintenanceSlowLegacyDoesNotStarveSnapshotPlan(t *testing.T) {
	modern := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(18, 0), maintenanceMatches)
	legacy := newMaintenanceTestFixture(t, "", maintenanceTime(18, 0), nil)
	legacyPlan := legacy.m.catalog.(*maintenanceTestCatalog).plans[0]
	modernPlan := modern.m.catalog.(*maintenanceTestCatalog).plans[0]
	modern.m.catalog = &maintenanceTestCatalog{plans: []controlplane.MaintenancePlan{legacyPlan, modernPlan}}
	slow := &slowLegacyRefresh{}
	modern.m.legacyCache = slow
	modern.m.step(context.Background())
	if slow.calls != 1 || len(modern.closed()) != 1 {
		t.Fatalf("slow calls=%d modern closes=%d", slow.calls, len(modern.closed()))
	}
}

func TestEffectiveMaintenancePartialPlanDoesNotClose(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(18, 0), maintenanceMatches)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	catalog.plans[0].CloseUnavailable = true
	f.m.step(context.Background())
	if requests := f.stub.requests("/api/v1/alerts/close"); len(requests) != 0 {
		t.Fatalf("partial plan asked the link to close: %v", requests)
	}
}

type deadlineLegacyRefresh struct{ left time.Duration }

func (d *deadlineLegacyRefresh) Refresh(ctx context.Context, _, _ string, _ []int64) error {
	if deadline, ok := ctx.Deadline(); ok {
		d.left = time.Until(deadline)
	}
	return nil
}

// The maintenance loop bounds its refresh round as a resolution does: half
// of what the step has left, at most a second.
func TestEffectiveMaintenanceRefreshTakesWhatTheStepCanSpare(t *testing.T) {
	f := newMaintenanceTestFixture(t, "", maintenanceTime(18, 0), nil)
	refresh := &deadlineLegacyRefresh{}
	f.m.legacyCache = refresh
	f.m.step(context.Background())
	if refresh.left > time.Second || refresh.left < 900*time.Millisecond {
		t.Fatalf("the refresh round had %v, want about a second of the step's five", refresh.left)
	}
}
