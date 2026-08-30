package uq

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestClientUsesFinalPythonWireContractAndNormalizesMilliseconds(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/query/ts" {
			t.Errorf("path=%s", request.URL.Path)
		}
		if request.Header.Get(headerQuerySource) != "alarmd-shadow" || request.Header.Get(headerTenant) != "tenant" || request.Header.Get(headerSpace) != "bkcc__2" {
			t.Errorf("headers=%v", request.Header)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type=%q", request.Header.Get("Content-Type"))
		}
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip_table1"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}],"status":null,"trace_id":"trace","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "alarmd-shadow", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), validAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData || len(sink.batches) != 1 {
		t.Fatalf("completion=%+v batches=%d", completion, len(sink.batches))
	}
	record, ok := sink.batches[0].Dataset.Record(0)
	if !ok || record.SourceTime() != 1_700_123_456 || record.BusinessID() != "2" {
		t.Fatalf("record=%+v", record)
	}
	queries := got["query_list"].([]any)
	query := queries[0].(map[string]any)
	if query["driver"] != "influxdb" || query["time_field"] != "time" {
		t.Fatalf("query=%v", query)
	}
	function := query["function"].([]any)[0].(map[string]any)
	if position, ok := function["position"].(json.Number); !ok || position.String() != "0" {
		t.Fatalf("function=%v", function)
	}
	condition := query["conditions"].(map[string]any)["field_list"].([]any)[0].(map[string]any)
	_, hasWildcard := condition["is_wildcard"]
	_, hasSuffix := condition["is_suffix"]
	_, hasOffsetForward := query["offset_forward"]
	if hasWildcard || condition["is_prefix"] != true || hasSuffix || hasOffsetForward {
		t.Fatalf("query booleans were not preserved: condition=%v query=%v", condition, query)
	}
	if _, exists := got["order_by"]; exists {
		t.Fatal("order_by must not be sent")
	}
	if _, exists := got["instant"]; exists {
		t.Fatal("instant must not be sent")
	}
}

func TestClientRejectsInvalidCanonicalBoolean(t *testing.T) {
	if _, err := parseQueryBool("is_wildcard", "yes"); err == nil || !strings.Contains(err.Error(), "is_wildcard must be true or false") {
		t.Fatalf("error=%v", err)
	}
}

func TestClientRejectsQueryStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":{"code":"QUERY_ERROR","message":"bad query"},"is_partial":false}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "alarmd-shadow", server.Client())
	if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); err == nil || !strings.Contains(err.Error(), "QUERY_ERROR") {
		t.Fatalf("error=%v", err)
	}
}

func TestClientDeliversLargeResponseOneSeriesAtATime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[`))
		for index := 0; index < 256; index++ {
			if index > 0 {
				_, _ = writer.Write([]byte(","))
			}
			_, _ = writer.Write([]byte(`{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["` + strconv.Itoa(index) + `"],"values":[[1700123456789,1]]}`))
		}
		_, _ = writer.Write([]byte(`],"is_partial":false}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "alarmd-shadow", server.Client())
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), validAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 256 || completion.Delivery.Series != 256 || completion.Delivery.Records != 256 {
		t.Fatalf("batches=%d delivery=%+v", len(sink.batches), completion.Delivery)
	}
	for _, batch := range sink.batches {
		if batch.Dataset.Len() != 1 {
			t.Fatalf("batch records=%d", batch.Dataset.Len())
		}
	}
}

func TestClientAcceptsFullEmpty(t *testing.T) {
	client := fixtureClient(t, http.StatusOK, `{"series":[],"is_partial":false,"result_table_id":[]}`, DefaultLimits())
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateEmpty || completion.Delivery != (execution.SeriesDelivery{}) {
		t.Fatalf("completion=%+v", completion)
	}
}

func TestClientRejectsMalformedNonSuccessAndTimeout(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		client := fixtureClient(t, http.StatusOK, `{"series":[`, DefaultLimits())
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); err == nil {
			t.Fatal("expected malformed response error")
		}
	})
	t.Run("trailing payload", func(t *testing.T) {
		client := fixtureClient(t, http.StatusOK, `{"series":[],"is_partial":false} trailing`, DefaultLimits())
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); err == nil {
			t.Fatal("expected trailing payload error")
		}
	})
	t.Run("non-success", func(t *testing.T) {
		client := fixtureClient(t, http.StatusBadGateway, `upstream failed`, DefaultLimits())
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
		defer server.Close()
		client, _ := NewClient(server.URL, "alarmd-shadow", server.Client())
		attempt := validAttempt(t)
		attempt.DeadlineUnixMilli = time.Now().Add(10 * time.Millisecond).UnixMilli()
		if _, err := client.Execute(context.Background(), attempt, &collectingSink{}); err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestClientEnforcesDecompressedAndSeriesBudgets(t *testing.T) {
	t.Run("decompressed body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Encoding", "gzip")
			compressed := gzip.NewWriter(writer)
			_, _ = compressed.Write([]byte(`{"series":[],"padding":"` + strings.Repeat("x", 1024) + `","is_partial":false}`))
			_ = compressed.Close()
		}))
		defer server.Close()
		client, _ := NewClientWithLimits(server.URL, "alarmd-shadow", server.Client(), Limits{MaxBodyBytes: 256, MaxSeriesBytes: 128, MaxSeries: 10, MaxRecords: 10})
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); !errors.Is(err, ErrResponseBytesExceeded) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("single series", func(t *testing.T) {
		body := `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["` + strings.Repeat("x", 256) + `"],"values":[[1700123456789,1]]}],"is_partial":false}`
		client := fixtureClient(t, http.StatusOK, body, Limits{MaxBodyBytes: 4096, MaxSeriesBytes: 128, MaxSeries: 10, MaxRecords: 10})
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); !errors.Is(err, ErrSeriesBytesExceeded) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestClientEnforcesTotalSeriesAndRecordBudgets(t *testing.T) {
	body := `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["a"],"values":[[1700123456789,1],[1700123457789,2]]},{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["b"],"values":[[1700123456789,1]]}],"is_partial":false}`
	t.Run("series", func(t *testing.T) {
		client := fixtureClient(t, http.StatusOK, body, Limits{MaxBodyBytes: 4096, MaxSeriesBytes: 2048, MaxSeries: 1, MaxRecords: 10})
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); !errors.Is(err, ErrTotalSeriesExceeded) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("records", func(t *testing.T) {
		client := fixtureClient(t, http.StatusOK, body, Limits{MaxBodyBytes: 4096, MaxSeriesBytes: 2048, MaxSeries: 10, MaxRecords: 1})
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); !errors.Is(err, ErrTotalRecordsExceeded) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestRequestMatchesTraceablePythonFinalWireFixture(t *testing.T) {
	// Protocol provenance (identities and timestamps in the fixture are synthetic):
	// - bk-monitor@4827f0d7: alarm_backends.service.access.data.query
	//   get_unify_query_params/_query_unify_query and api.unify_query.default
	//   QueryDataResource.RequestSerializer/perform_request.
	// - bkmonitor-datalink@64898879: pkg/unify-query/structured.QueryTs and
	//   pkg/unify-query/service/http handler request tests.
	// Field presence, omission, nesting and headers are extracted from those sources;
	// this fixture is not represented as a captured production request.
	want, err := os.ReadFile("testdata/python-final-wire-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	second := attempt.Spec.PlanFacts.QueryList[0]
	second.ReferenceName = "b"
	second.TableID = "system.mem"
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append(facts.QueryList, second)
	facts.MetricMerge = "a or b"
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow,
		ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: attempt.Spec.RequiredColumns})
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildRequest(attempt.Spec)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(request)
	if !jsonEqual(got, want) {
		t.Fatalf("request differs from final-wire fixture\ngot=%s\nwant=%s", got, want)
	}
}

func fixtureClient(t *testing.T, status int, body string, limits Limits) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClientWithLimits(server.URL, "alarmd-shadow", server.Client(), limits)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

type collectingSink struct {
	batches []execution.ProviderSeriesBatch
}

func (sink *collectingSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.batches = append(sink.batches, batch)
	return nil
}

func validAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu", FieldName: "usage",
			ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			Functions:       []execution.QueryFunction{{Method: "abs", Position: 0}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0, Window: "60s"},
			Conditions: execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "host", Operator: "eq",
				Values:   []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "127.0.0.1"}},
				Wildcard: "false", Prefix: "true", Suffix: "false"}}}, OffsetForward: "false"}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
		LogicalWindow: execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000},
		ProviderRange: execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000},
		AcceptedRange: execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000}, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return execution.QueryAttempt{Spec: spec, Slot: execution.SlotIdentity{QueryGroup: "group", ScheduleRevision: "schedule", EvaluationTime: 1_700_124_000},
		Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
}
