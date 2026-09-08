package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"strings"
	"testing"
)

func TestLegacyPodCacheObservationKeepsBoundedOutcomes(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, result := range []string{"hit", "miss", "error", "unbounded-pod"} {
		recorder.RecordLegacyPodCache(result)
	}
	wire := scrape(t, recorder)
	for _, result := range []string{"hit", "miss", "error"} {
		if !strings.Contains(wire, `bkmonitor_alarmd_legacy_pod_cache_total{result="`+result+`"} 1`) {
			t.Fatalf("missing %s counter", result)
		}
	}
	if strings.Contains(wire, "unbounded-pod") {
		t.Fatal("high cardinality label accepted")
	}
	normalized := observability.NormalizeObservation(observability.Observation{Component: observability.ComponentRuntime, Stage: observability.StageLegacyPodCache, Result: observability.ResultDegraded})
	if normalized.Component != observability.ComponentRuntime || normalized.Stage != observability.StageLegacyPodCache || normalized.Result != observability.ResultDegraded {
		t.Fatal("fallback diagnostic was normalized away")
	}
}
