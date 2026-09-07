package metric

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestExpiredRangeGatherSeparatesOperationsAndLogicalSlots(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	observe := func(result string, count uint32) {
		r.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned,
			ExpiredRange: &observability.ExpiredRangeFacts{Result: result, CommittedSlots: count}})
	}
	observe("committed", 17)
	observe("committed", 0) // Already committed response cannot add the range twice.
	r.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned,
		ExpiredRange: &observability.ExpiredRangeFacts{Result: "committed", ReasonCode: contract.ReasonGapSkipped, CommittedSlots: 7}})
	for _, result := range []string{"retrying", "blocked", "error"} {
		observe(result, 999)
	}
	for _, result := range []string{"unknown", "qg-private-id", "FULL_COMPLETED", ""} {
		observe(result, 999)
	}
	r.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned})
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"committed": 3, "retrying": 1, "blocked": 1, "error": 1}
	operations, slots := 0, 0
	for _, family := range families {
		switch family.GetName() {
		case "bkmonitor_alarmd_expired_range_total":
			for _, series := range family.Metric {
				operations++
				if len(series.Label) != 1 || series.Label[0].GetName() != "result" {
					t.Fatalf("range labels=%v", series.Label)
				}
				label := series.Label[0].GetValue()
				expected, ok := want[label]
				if !ok || series.GetCounter().GetValue() != expected {
					t.Fatalf("operation %s=%v expected=%v", label, series.GetCounter().GetValue(), expected)
				}
			}
		case "bkmonitor_alarmd_expired_slots_finalized_total":
			for _, series := range family.Metric {
				slots++
				if len(series.Label) != 1 || series.Label[0].GetName() != "reason" {
					t.Fatalf("logical slots=%v", series)
				}
				expected, ok := map[string]float64{"recovery_expired": 17, "replay_distance_expired": 7}[series.Label[0].GetValue()]
				if !ok || series.GetCounter().GetValue() != expected {
					t.Fatalf("logical slots=%v", series)
				}
			}
		case "bkmonitor_alarmd_progress_completed_total":
			t.Fatal("range fabricated ordinary successful completion")
		}
	}
	if operations != 4 || slots != 2 {
		t.Fatalf("new series=%d+%d, want 4+2", operations, slots)
	}
}
