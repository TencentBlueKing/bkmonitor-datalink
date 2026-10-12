package metric

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestCodecCountersDistinguishSameResponseCompatibility(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observation := observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		QueryCodec: []observability.QueryCodecFacts{
			{Codec: "legacy_json", Negotiation: "disabled", Completion: "full"},
			{Codec: "legacy_json", Negotiation: "opt_in", Completion: "full"},
			{Codec: "shared_json_v1", Negotiation: "opt_in", Completion: "partial"},
			{Codec: "sensitive-value", Negotiation: "sensitive-value", Completion: "sensitive-value"},
		}}
	recorder.Observe(context.Background(), observation)
	for _, labels := range [][]string{{"legacy_json", "disabled", "full"}, {"legacy_json", "opt_in", "full"}, {"shared_json_v1", "opt_in", "partial"}, {"other", "other", "other"}} {
		if got := testutil.ToFloat64(recorder.phaseTwo.queryCodec.responses.WithLabelValues(labels...)); got != 1 {
			t.Fatalf("%v=%v, want one per physical response", labels, got)
		}
	}
	observation.Stage = observability.StageEvaluationCompleted
	recorder.Observe(context.Background(), observation)
	if got := testutil.ToFloat64(recorder.phaseTwo.queryCodec.responses.WithLabelValues("legacy_json", "opt_in", "full")); got != 1 {
		t.Fatal("codec escaped query completion scope")
	}
}
