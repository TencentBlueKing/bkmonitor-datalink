package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

type slowLegacyRefresh struct{ calls int }

func (s *slowLegacyRefresh) Refresh(ctx context.Context, _, _ string, _ []int64) error {
	s.calls++
	<-ctx.Done()
	return ctx.Err()
}

func TestEffectiveMaintenanceSlowLegacyDoesNotStarveSnapshotPlan(t *testing.T) {
	modern := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(18, 0), []openalerts.Alert{maintenanceAlert("native", "critical")})
	legacy := newMaintenanceTestFixture(t, "", maintenanceTime(18, 0), nil)
	legacyPlan := legacy.m.catalog.(*maintenanceTestCatalog).plans[0]
	modernPlan := modern.m.catalog.(*maintenanceTestCatalog).plans[0]
	modern.m.catalog = &maintenanceTestCatalog{plans: []controlplane.MaintenancePlan{legacyPlan, modernPlan}}
	slow := &slowLegacyRefresh{}
	modern.m.legacyCache = slow
	modern.m.step(context.Background())
	if slow.calls != 1 || len(modern.writer.batches) != 1 {
		t.Fatalf("slow calls=%d modern closes=%d", slow.calls, len(modern.writer.batches))
	}
}

func TestEffectiveMaintenancePartialPlanRetainsTrackingWithoutClosing(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(18, 0), []openalerts.Alert{maintenanceAlert("native", "critical")})
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	catalog.plans[0].CloseUnavailable = true
	f.m.step(context.Background())
	if len(f.writer.batches) != 0 || f.m.cache.Stats().Tracked != 1 {
		t.Fatalf("partial plan closes=%d tracked=%d", len(f.writer.batches), f.m.cache.Stats().Tracked)
	}
}
