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
		if r.URL.Path != "/query/ts/promql" || body["promql"] != "up" || body["query_list"] != nil || body["start"] == nil || body["bk_biz_ids"] == nil {
			t.Errorf("path=%s body=%v", r.URL.Path, body)
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

func TestPollingFTAWireKeepsIntrinsicFilterSeparate(t *testing.T) {
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	q := &facts.QueryList[0]
	q.FieldSemantics = "fta_event_tags/v1"
	q.Conditions = execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "tags.env", Operator: "contains", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}}}}
	q.SourceConditions = &execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "status", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "ABNORMAL"}}}}}
	facts.TSDBMap = map[string][]execution.QueryStorage{"a": {{TableID: "events", StorageID: "17", StorageType: "elasticsearch", DB: "bkfta_event_*_read", Measurement: "__default__", TimeField: execution.QueryTimeField{Name: "time", Type: "date", Unit: "millisecond"}}}}
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow, ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildRequest(attempt.Spec)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(wire, &got)
	clause := got["query_list"].([]any)[0].(map[string]any)
	if clause["field_semantics"] != "fta_event_tags/v1" || clause["source_conditions"] == nil || len(body.QueryList[0].Conditions.Fields) != 1 || body.QueryList[0].SourceConditions.Fields[0].Field != "status" {
		t.Fatalf("wire=%s", wire)
	}
}
