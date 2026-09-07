package uq

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestClientNormalizesNoDimensionSeriesWithStableIdentity(t *testing.T) {
	attempt := noDimensionAttempt(t)
	client := fixtureClient(t, http.StatusOK,
		`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[],"group_values":[],"values":[[1700123456789,12.5],[1700123516789,13.5]]}],"is_partial":false}`,
		DefaultLimits())
	client.now = func() time.Time { return time.Unix(1_700_123_500, 0) }
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), attempt, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 1 {
		t.Fatalf("batches=%d", len(sink.batches))
	}
	batch := sink.batches[0]
	if batch.Dataset.Len() != 2 || batch.Delivery.Series != 1 || batch.Delivery.Records != 2 ||
		completion.Delivery != batch.Delivery {
		t.Fatalf("dataset=%d delivery=%+v completion=%+v", batch.Dataset.Len(), batch.Delivery, completion)
	}
	first, ok := batch.Dataset.Record(0)
	if !ok {
		t.Fatal("first no-dimension record is missing")
	}
	identity := first.DimensionIdentity()
	if len(identity.Fields) != 0 || len(first.Dimensions()) != 0 {
		t.Fatalf("no-dimension series gained synthetic dimensions: identity=%+v dimensions=%v", identity, first.Dimensions())
	}
	const wantIdentityDigest = "4a46e4f597c486e288a78ac81c71fb8db64e6e55e694d6459915c28985c20e0e"
	const wantRecordID = "f9892be76bdb0f7918dd6456aa713c3cfc5548d872d7dd3c1e9728c6b5693cf3"
	const wantDeliveryDigest = "c079c5a5825c416759527d0bc109ae908a05cd57990927463fa7cec2e9ed79c1"
	if identity.Digest != wantIdentityDigest || first.RecordID() != wantRecordID || batch.Delivery.Digest != wantDeliveryDigest {
		t.Fatalf("identity=%q record_id=%q delivery=%q", identity.Digest, first.RecordID(), batch.Delivery.Digest)
	}
	for _, scope := range []struct{ tenant, business string }{{"other", "2"}, {"tenant", "3"}} {
		other, err := contract.DeriveDimensionIdentityDigestV2(scope.tenant, scope.business, []contract.DimensionFieldV2{})
		if err != nil {
			t.Fatal(err)
		}
		if other == identity.Digest {
			t.Fatalf("tenant/business scope collided with no-dimension identity: %+v", scope)
		}
	}
}

// The ProcPort query groups by the dynamic port dimensions that the identity
// contract excludes, so one process with two port rows arrives as two UQ
// series with one SeriesIdentityDigest. The client keeps delivering one batch
// per UQ series and never folds or picks a row: folding is the worker's job
// and follows the fold policy the compiled ProcPort algorithm declares
// (strategy.CompiledAlgorithmPlan.SeriesFoldPolicy), so that every anomalous
// row is preserved. Series of other identities keep their own batch.
func TestClientDeliversSeriesSharingOneIdentityAsSeparateBatches(t *testing.T) {
	series := func(protocol, ip string, values string) string {
		return `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["bind_ip","bk_target_cloud_id","bk_target_ip","display_name","listen","nonlisten","not_accurate_listen","protocol"],` +
			`"group_values":["0.0.0.0","0","` + ip + `","nginx","[80]","[]","[]","` + protocol + `"],"values":[` + values + `]}`
	}
	first := series("tcp", "127.0.0.1", `[1700123456789,1],[1700123516789,1]`)
	other := series("tcp", "127.0.0.2", `[1700123456789,1]`)
	second := series("udp", "127.0.0.1", `[1700123516789,0],[1700123576789,0]`)
	client := fixtureClient(t, http.StatusOK, `{"series":[`+first+`,`+other+`,`+second+`],"is_partial":false}`, DefaultLimits())
	sink := &collectingSink{}

	completion, err := client.Execute(context.Background(), procPortAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 3 {
		t.Fatalf("batches=%d, want one batch per UQ series", len(sink.batches))
	}
	firstRecord, _ := sink.batches[0].Dataset.Record(0)
	otherRecord, _ := sink.batches[1].Dataset.Record(0)
	secondRecord, _ := sink.batches[2].Dataset.Record(0)
	if firstRecord.DimensionIdentity().Digest != secondRecord.DimensionIdentity().Digest ||
		firstRecord.DimensionIdentity().Digest == otherRecord.DimensionIdentity().Digest {
		t.Fatalf("identity digests first=%s other=%s second=%s, want first and second shared",
			firstRecord.DimensionIdentity().Digest, otherRecord.DimensionIdentity().Digest, secondRecord.DimensionIdentity().Digest)
	}
	if string(firstRecord.Dimensions()["protocol"]) != `"tcp"` || string(secondRecord.Dimensions()["protocol"]) != `"udp"` {
		t.Fatalf("row dimensions were altered: first=%s second=%s", firstRecord.Dimensions()["protocol"], secondRecord.Dimensions()["protocol"])
	}
	for index, want := range []uint64{uint64(len(first)), uint64(len(other)), uint64(len(second))} {
		if sink.batches[index].Delivery.Series != 1 || sink.batches[index].Delivery.Bytes != want {
			t.Fatalf("batch %d delivery=%+v, want one series of %d bytes", index, sink.batches[index].Delivery, want)
		}
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData ||
		completion.Delivery.Series != 3 || completion.Delivery.Records != 5 || completion.Stats.Series != 3 {
		t.Fatalf("completion=%+v, want three series with five records", completion)
	}
}

func procPortAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	dimensions := []string{"bind_ip", "bk_target_cloud_id", "bk_target_ip", "display_name", "listen", "nonlisten", "not_accurate_listen", "protocol"}
	facts.QueryList[0].TableID, facts.QueryList[0].FieldName = "system.proc_port", "proc_exists"
	facts.QueryList[0].Functions = []execution.QueryFunction{{Method: "max", Position: 0, Dimensions: dimensions}}
	facts.QueryList[0].Dimensions = dimensions
	facts.Normalization.DatasetContract.IdentityFields = []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	spec := attempt.Spec
	spec.Digest = ""
	spec.PlanFacts = facts
	spec, err = execution.BuildPhysicalQuerySpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec = spec
	return attempt
}

func TestClientMeasuresSeriesPayloadBytesAndAccumulatesCompletion(t *testing.T) {
	first := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["` + strings.Repeat("a", 32<<10) + `"],"values":[[1700123456789,12.5],[1700123516789,13.5]]}`
	second := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["` + strings.Repeat("b", 8<<10) + `"],"values":[[1700123456789,14.5]]}`
	client := fixtureClient(t, http.StatusOK,
		`{"series":[`+first+`,`+second+`],"is_partial":false}`, DefaultLimits())
	sink := &collectingSink{}

	completion, err := client.Execute(context.Background(), validAttempt(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 2 {
		t.Fatalf("batches=%d, want 2", len(sink.batches))
	}
	wantFirst, wantSecond := uint64(len(first)), uint64(len(second))
	if sink.batches[0].Delivery.Bytes != wantFirst || sink.batches[1].Delivery.Bytes != wantSecond {
		t.Fatalf("batch bytes=%d/%d, want %d/%d", sink.batches[0].Delivery.Bytes,
			sink.batches[1].Delivery.Bytes, wantFirst, wantSecond)
	}
	if completion.Delivery.Bytes != wantFirst+wantSecond {
		t.Fatalf("completion bytes=%d, want %d", completion.Delivery.Bytes, wantFirst+wantSecond)
	}
}

func TestClientRejectsInvalidCanonicalBoolean(t *testing.T) {
	if _, err := parseQueryBool("is_wildcard", "yes"); err == nil || !strings.Contains(err.Error(), "is_wildcard must be true or false") {
		t.Fatalf("error=%v", err)
	}
}

func TestClientCompletesDeterministicQueryStatusAsUnavailable(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			body := fmt.Sprintf(`{"series":[],"status":{"code":"QUERY_ERROR","message":"bad query"},"is_partial":%t,"result_table_id":["system.cpu"]}`, partial)
			client := fixtureClient(t, http.StatusOK, body, DefaultLimits())
			completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
			if err != nil || completion.Completeness != execution.CompletenessUnavailable || completion.DataState != execution.DataStateEmpty ||
				len(completion.RouteFacts.Attempts) != 1 || completion.RouteFacts.Attempts[0].Result != execution.RouteAttemptFailed ||
				completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) ||
				completion.RouteFacts.Attempts[0].Detail != "response=status_query_error" ||
				len(completion.RouteFacts.ResultTableIDs) != 1 || completion.RouteFacts.ResultTableIDs[0] != "system.cpu" {
				t.Fatalf("completion=%+v error=%v, want UNAVAILABLE with bounded status detail", completion, err)
			}
			if strings.Contains(fmt.Sprintf("%+v", completion), "bad query") {
				t.Fatalf("status message leaked into completion: %+v", completion)
			}
		})
	}
}

func TestClientClassifiesQueryTsPartialStatus(t *testing.T) {
	for _, partial := range []bool{true, false} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			body := fmt.Sprintf(`{"series":[],"status":{"code":"QUERY_TS_PARTIAL","message":"one route failed"},"is_partial":%t}`, partial)
			client := fixtureClient(t, http.StatusOK, body, DefaultLimits())
			completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
			if err != nil {
				t.Fatal(err)
			}
			if completion.Completeness != execution.CompletenessPartial || completion.DataState != execution.DataStateEmpty {
				t.Fatalf("completion=%+v", completion)
			}
		})
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

func TestClientReportsPartialDataAndEmpty(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		dataState execution.DataState
		batches   int
	}{
		{
			name:      "data",
			body:      `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}],"is_partial":true}`,
			dataState: execution.DataStateData,
			batches:   1,
		},
		{name: "empty", body: `{"series":[],"is_partial":true}`, dataState: execution.DataStateEmpty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient(t, http.StatusOK, test.body, DefaultLimits())
			sink := &collectingSink{}
			completion, err := client.Execute(context.Background(), validAttempt(t), sink)
			if err != nil {
				t.Fatal(err)
			}
			if completion.Completeness != execution.CompletenessPartial || completion.DataState != test.dataState ||
				len(sink.batches) != test.batches || completion.PartialEvidence != nil {
				t.Fatalf("completion=%+v batches=%d", completion, len(sink.batches))
			}
		})
	}
}

func TestClientReportsUnavailableForTransportProtocolAndDeadlineFailures(t *testing.T) {
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
		completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
		if err != nil || completion.Completeness != execution.CompletenessUnavailable || completion.DataState != execution.DataStateUnknown ||
			len(completion.RouteFacts.Attempts) != 1 || completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
			t.Fatalf("completion=%+v error=%v", completion, err)
		}
	})
	t.Run("missing is_partial after data", func(t *testing.T) {
		body := `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}]}`
		client := fixtureClient(t, http.StatusOK, body, DefaultLimits())
		sink := &collectingSink{}
		completion, err := client.Execute(context.Background(), validAttempt(t), sink)
		if err != nil || completion.Completeness != execution.CompletenessUnavailable || completion.DataState != execution.DataStateData ||
			len(sink.batches) != 1 || len(completion.RouteFacts.Attempts) != 1 ||
			completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
			t.Fatalf("completion=%+v batches=%d error=%v", completion, len(sink.batches), err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
		defer server.Close()
		client, _ := NewClient(server.URL, "alarmd-shadow", server.Client())
		attempt := validAttempt(t)
		attempt.DeadlineUnixMilli = time.Now().Add(10 * time.Millisecond).UnixMilli()
		completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
		if err != nil || completion.Completeness != execution.CompletenessUnavailable || completion.DataState != execution.DataStateUnknown ||
			len(completion.RouteFacts.Attempts) != 1 || completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) {
			t.Fatalf("completion=%+v error=%v", completion, err)
		}
	})
}

func TestClientPropagatesCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	client, _ := NewClient(server.URL, "alarmd-shadow", server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Execute(ctx, validAttempt(t), &collectingSink{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
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

func TestRequestPreservesPythonAVGAndRealTimeFunctionShapes(t *testing.T) {
	want, err := os.ReadFile("testdata/python-empty-functions-wire-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	avg := attempt.Spec.PlanFacts.QueryList[0]
	avg.DataSource = ""
	avg.Functions = []execution.QueryFunction{{Method: "mean", Position: 0}}
	avg.TimeAggregation = execution.QueryFunction{Method: "avg_over_time", Window: "60s", Position: 0}
	avg.Conditions = execution.QueryConditions{}
	avg.OffsetForward = ""
	realTime := avg
	realTime.ReferenceName = "b"
	realTime.FieldName = "instant_usage"
	realTime.Functions = []execution.QueryFunction{}
	realTime.TimeAggregation = execution.QueryFunction{}
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = []execution.QueryClause{avg, realTime}
	facts.MetricMerge = "a or b"
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
		LogicalWindow: attempt.Spec.LogicalWindow, ProviderRange: attempt.Spec.ProviderRange,
		AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: attempt.Spec.RequiredColumns})
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildRequest(attempt.Spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, want) {
		t.Fatalf("request lost Python empty function shapes\ngot=%s\nwant=%s", got, want)
	}
}

func TestRequestPreservesPythonEmptyStringConditionValue(t *testing.T) {
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	facts.QueryList[0].Conditions.Fields = append([]execution.QueryConditionField(nil), facts.QueryList[0].Conditions.Fields...)
	facts.QueryList[0].Conditions.Fields[0].Values = []execution.QueryScalar{{Kind: execution.QueryScalarString}}
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{
		PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow,
		ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange,
		RequiredColumns: attempt.Spec.RequiredColumns,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildRequest(attempt.Spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(body.QueryList[0].Conditions.Fields[0])
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"field_name":"host","op":"eq","value":[""],"is_prefix":true}`
	if string(got) != want {
		t.Fatalf("empty string condition wire=%s, want %s", got, want)
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
	return execution.QueryAttempt{Spec: spec, Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1_700_124_000},
		Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
}

func noDimensionAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision = ""
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	facts.QueryList[0].Dimensions = []string{}
	facts.Normalization.DatasetContract.IdentityFields = []string{}
	var err error
	facts, err = execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{
		PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow,
		ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange,
		RequiredColumns: attempt.Spec.RequiredColumns,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}
