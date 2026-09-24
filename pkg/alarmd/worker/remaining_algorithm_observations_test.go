package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestHistoricalDependencyPointsUseBoundedLabel(t *testing.T) {
	for _, name := range []string{"history_60", "history_604800", "history_172800"} {
		point, ok := observedDependencyPoint(name)
		if !ok || point != observability.AlgorithmDependencyPointHistorical {
			t.Fatalf("%s mapped to %q, %t", name, point, ok)
		}
	}
	for _, name := range []string{"history_", "history_0", "history_-1", "history_arbitrary_input", "unknown"} {
		if _, ok := observedDependencyPoint(name); ok {
			t.Fatalf("unrecognized dependency %q accepted", name)
		}
	}
}
