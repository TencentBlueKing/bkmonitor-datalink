package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestCodecEvidenceKeepsExistingQueryCompletedLog(t *testing.T) {
	var out bytes.Buffer
	o := NormalizeObservation(Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultSuccess,
		QueryCodec: []QueryCodecFacts{{Codec: "legacy_json", Negotiation: "opt_in", Completion: "full"}},
	})
	New(ComponentAccess, &out).logObservation(context.Background(), o, LogAdmission{Allowed: true})
	var log struct {
		QueryCodec []QueryCodecFacts `json:"query_codecs"`
		Stage      string            `json:"stage"`
	}
	if err := json.Unmarshal(out.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	if log.Stage != "query_completed" || len(log.QueryCodec) != 1 || log.QueryCodec[0].Negotiation != "opt_in" || log.QueryCodec[0].Codec != "legacy_json" {
		t.Fatalf("missing negotiated legacy evidence %+v", log)
	}
}
