package detect

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTraditionalContinuousHistoryIndexKeepsExactAndDuplicateSemantics(t *testing.T) {
	config := map[string]any{"ceil": 20, "ceil_interval": 32, "fetch_type": "avg", "data_unit": "short", "algorithm_unit": "", "precision": 6}
	plan := compileNamedInputPlan(t, strategy.DetectorKindAdvancedRingRatio, config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	algorithm := plan.Levels()[0].Algorithms()[0]
	for _, duplicate := range []bool{false, true} {
		// Provider order need not match increasing offset order. Include an
		// unrelated point and ensure it cannot affect the average.
		records := []contract.CanonicalRecordV2{namedRecord(t, 604799, "100000", nil)}
		for i := 32; i >= 1; i-- {
			records = append(records, namedRecord(t, 604800-int64(i)*60, "100", nil))
		}
		if duplicate {
			records = append(records, namedRecord(t, 604740, "0", nil))
		}
		bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{"primary": {namedRecord(t, 604800, "150", nil)}, "ring_history_32": records})
		got := (traditionalComparisonDetector{algorithm.Kind()}).Evaluate(context.Background(), algorithm, execution.SeriesEvaluationInputRequest{Inputs: bindings}, primary)
		want := FactResultAnomalous
		if duplicate {
			want = FactResultError
		}
		if got.result != want {
			t.Fatalf("duplicate=%v result=%+v want=%s", duplicate, got, want)
		}
	}
}
