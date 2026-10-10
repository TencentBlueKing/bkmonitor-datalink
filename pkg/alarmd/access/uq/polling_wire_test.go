package uq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestPollingPromQLWireAndDynamicSeries(t *testing.T) {
	attempt := noDimensionAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = nil
	facts.MetricMerge = ""
	facts.PromQL = &execution.PromQLQuery{Expression: "up", Match: "{job='api'}"}
	facts.Normalization.DatasetContract.DynamicDimensions = true
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow, ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		// bk_biz_ids must be absent: the provider turns it into a bk_biz_id
		// label condition, which a custom-reported metric never matches.
		if r.URL.Path != "/query/ts/promql" || body["promql"] != "up" || body["query_list"] != nil || body["start"] == nil || body["bk_biz_ids"] != nil {
			t.Errorf("path=%s body=%v", r.URL.Path, body)
		}
		if r.Header.Get(headerSpace) == "" {
			t.Errorf("space header missing: the scope has to travel there once it no longer travels in the body")
		}
		_, _ = w.Write([]byte(`{"series":[{"name":"a","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["pod"],"group_values":["one"],"values":[[1700123456789,1]]},{"name":"b","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["pod"],"group_values":["two"],"values":[[1700123456789,2]]}],"is_partial":false}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "alarmd", server.Client())
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), attempt, sink)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessFull || len(sink.batches) != 2 {
		t.Fatalf("completion=%+v batches=%d", completion, len(sink.batches))
	}
	a, _ := sink.batches[0].Dataset.Record(0)
	b, _ := sink.batches[1].Dataset.Record(0)
	if a.DimensionIdentityDigest() == b.DimensionIdentityDigest() || len(a.DimensionIdentity().Fields) != 1 {
		t.Fatal("dynamic identities collapsed")
	}
}

func TestPollingMissingGroupValuesBindExplicitNull(t *testing.T) {
	attempt := validAttempt(t)
	attempt.Spec.PlanFacts.Normalization.Version = "uq-polling-normalization-v1"
	attempt.Spec.PlanFacts.Normalization.DatasetContract.IdentityFields = []string{"host", "zone"}
	source := identityTestSeries([]string{"host", "zone"}, []string{"node-a"})
	batch, _, err := normalizeSeries(attempt.Spec, "result", source, 1_700_123_500)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := batch.Dataset.Record(0)
	if string(record.Dimensions()["zone"]) != "null" || len(record.DimensionIdentity().Fields) != 2 {
		t.Fatalf("record=%+v", record)
	}
	explicit := source
	explicit.GroupValues = append(explicit.GroupValues, json.RawMessage("null"))
	other, _, err := normalizeSeries(attempt.Spec, "result", explicit, 1_700_123_500)
	if err != nil {
		t.Fatal(err)
	}
	otherRecord, _ := other.Dataset.Record(0)
	if record.DimensionIdentityDigest() != otherRecord.DimensionIdentityDigest() {
		t.Fatal("missing group value differs from explicit null")
	}
}
