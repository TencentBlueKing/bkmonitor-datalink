package detect

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTraditionalDecimalUnitSequentialRoundingBoundary(t *testing.T) {
	config := map[string]any{"days": 1, "method": "gt", "ratio": 0, "shock": 460962397203.8692, "data_unit": "decmbytes", "algorithm_unit": "", "precision": 6}
	plan := compileNamedInputPlan(t, strategy.DetectorKindYearRoundRange, config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	c, _ := plan.Levels()[0].Algorithms()[0].TraditionalComparisonConfig()
	previous := float64(1)
	if got := evaluateTraditionalComparison(strategy.DetectorKindYearRoundRange, c, 460962.39720386924, map[int64]*float64{86400: &previous}); got != pureDetectionNormal {
		t.Fatalf("got %s; Python sequential conversion is equal, not greater", got)
	}
}
