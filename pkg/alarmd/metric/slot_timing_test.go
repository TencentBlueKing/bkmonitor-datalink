package metric

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestSlotTimingBoundedStagesAndNestedDurations(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, o := range []observability.Observation{
		{Component: observability.ComponentScheduler, Stage: "runner_completed", Duration: 10 * time.Second},
		{Component: observability.ComponentScheduler, Stage: "slot_source_completed", Duration: 7 * time.Second},
		{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Duration: 2 * time.Second},
		{Component: observability.ComponentAccess, Stage: "runner_completed", Duration: time.Hour},
		{Component: observability.ComponentScheduler, Stage: "arbitrary", Duration: time.Hour},
	} {
		r.Observe(context.Background(), o)
	}
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() != "bkmonitor_alarmd_slot_operation_duration_seconds" {
			continue
		}
		found = true
		if len(f.Metric) != 3 {
			t.Fatalf("stage cardinality=%d", len(f.Metric))
		}
		want := map[string]float64{"run_one": 10, "source_next": 7, "execute": 2}
		for _, m := range f.Metric {
			if len(m.Label) != 1 || m.Label[0].GetName() != "stage" {
				t.Fatal("extra label")
			}
			h := m.GetHistogram()
			if h.GetSampleCount() != 1 || h.GetSampleSum() != want[m.Label[0].GetValue()] || len(h.Bucket) != 8 {
				t.Fatalf("bad histogram %v", m)
			}
		}
	}
	if !found {
		t.Fatal("slot operation timing missing")
	}
}
