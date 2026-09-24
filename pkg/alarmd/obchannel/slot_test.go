package obchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func slotFixture(t *testing.T) SlotPlan {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq", TenantID: "fixture", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList:   []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu", FieldName: "usage", ReferenceName: "a", Driver: "influxdb", TimeField: "time"}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1700000000, End: 1700000060}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window, ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return SlotPlan{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "fixture-qg", EvaluationTime: 1700000060}, SnapshotRevision: "snapshot", QueryRevision: facts.QueryRevision, ScheduleRevision: "schedule", ScheduleSegmentStart: 1699999000, DuePlanSetDigest: "due"}, ObjectDigest: execution.ObjectDigest(strings.Repeat("c", 64)),
		Prepared: access.PreparedExecution{Queries: []access.PlannedQuery{{Spec: spec, Requirements: []execution.DataRequirement{{RequirementID: "primary", DatasetName: "primary", Role: execution.InputRolePrimary}}}}}}
}

func jsonParams(t *testing.T, params Params) Params {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result Params
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSlotPreviewReferencesAndReadOnlyRequery(t *testing.T) {
	plan := slotFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/query/ts" {
			t.Error("wrong UQ path")
		}
		_, _ = w.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["fixture-host"],"values":[[1700000000000,42]]}],"is_partial":false}`))
	}))
	defer server.Close()
	client, err := uq.NewDiagnosticClient(server.URL, "fixture", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var resolveError error
	ops := SlotOperations(SlotOptions{Resolve: func(context.Context, execution.SlotIdentity) (SlotPlan, error) { return plan, resolveError }, UQ: client,
		Evidence: func(context.Context, execution.SlotIdentity) (SlotEvidence, error) {
			return SlotEvidence{Records: []json.RawMessage{}, Samples: []json.RawMessage{}, Complete: true}, nil
		}})
	p := jsonParams(t, slotParams(slotContext(plan)))
	preview := ops[0].Run(context.Background(), p)
	if preview.Error != nil || !preview.Complete || requests.Load() != 0 || len(preview.Next) != 1 {
		t.Fatalf("preview=%+v requests=%d", preview, requests.Load())
	}
	view := preview.Value.(SlotGetResult)
	if view.Kind != "reconstructed_from_contract" || view.HistoricalInputComplete || len(view.Queries) != 1 {
		t.Fatalf("view=%+v", view)
	}
	params := jsonParams(t, preview.Next[0].Params)
	for _, tc := range []struct{ field, value, code string }{
		{"contract_digest", strings.Repeat("0", 64), "slot_contract_changed"},
		{"physical_query_digest", strings.Repeat("0", 64), "query_reference_unknown"},
		{"request_digest", strings.Repeat("0", 64), "query_request_changed"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			bad := jsonParams(t, params)
			bad[tc.field] = tc.value
			out := ops[1].Run(context.Background(), bad)
			if out.Error == nil || out.Error.Code != tc.code || requests.Load() != 0 {
				t.Fatalf("out=%+v requests=%d", out, requests.Load())
			}
		})
	}
	resolveError = ErrHistoricalContractUnavailable
	missing := ops[1].Run(context.Background(), params)
	if missing.Error == nil || missing.Error.Code != "historical_contract_unavailable" || requests.Load() != 0 {
		t.Fatal(missing)
	}
	resolveError = nil
	out := ops[1].Run(context.Background(), params)
	if out.Error != nil || !out.Complete || requests.Load() != 1 {
		t.Fatalf("out=%+v requests=%d", out, requests.Load())
	}
	result := out.Value.(SlotQueryResult)
	if result.Kind != "requery_now" || result.QueriedAt.IsZero() || len(result.Query.Series) != 1 || result.Query.ReturnedPoints != 1 || string(result.Query.Series[0].Points[0].Value) != "42" {
		t.Fatalf("result=%+v", result)
	}
	// A diagnostic display filter cannot alter the original query semantics.
	params["series_digest"] = result.Query.Series[0].SeriesDigest
	filtered := ops[1].Run(context.Background(), params)
	if !filtered.Complete || filtered.Value.(SlotQueryResult).Query.RequestDigest != result.Query.RequestDigest {
		t.Fatal(filtered)
	}
}

func TestSlotDefaultOwnerAndExplicitReplica(t *testing.T) {
	plan := slotFixture(t)
	client, _ := uq.NewDiagnosticClient("http://uq.invalid", "fixture", &http.Client{})
	ops := SlotOperations(SlotOptions{Resolve: func(context.Context, execution.SlotIdentity) (SlotPlan, error) { return plan, nil }, UQ: client})
	preview := ops[0].Run(context.Background(), jsonParams(t, slotParams(slotContext(plan))))
	params := jsonParams(t, preview.Next[0].Params)
	_, target, err := invocationParams(ops[1], params)
	if err != nil || target.OwnerQueryGroup != "fixture-qg" || target.Replica != "" {
		t.Fatalf("%+v %v", target, err)
	}
	params["replica"] = "other-worker"
	_, target, err = invocationParams(ops[1], params)
	if err != nil || target.Replica != "other-worker" || target.OwnerQueryGroup != "" {
		t.Fatalf("%+v %v", target, err)
	}
	_, target, err = invocationParams(ops[0], jsonParams(t, slotParams(slotContext(plan))))
	if err != nil || target != (Target{}) {
		t.Fatal("shared Slot preview acquired an implicit process target")
	}
	c := testChannel(t, &testAuth{}, ops...)
	if describe(c.ops["slot.query"])["default_owner_parameter"] != "query_group" {
		t.Fatal("default targeting is undiscoverable")
	}
	params["free_query"] = "arbitrary"
	if _, _, err = invocationParams(ops[1], params); err == nil {
		t.Fatal("free UQ query accepted")
	}
	delete(params, "free_query")
	delete(params, "replica")
	status, response := call(t, c, envelope(c, "invoke", "slot.query", params))
	if status != 503 || response.Error == nil || response.Error.Code != "target_routing_unavailable" {
		t.Fatalf("silently executed on entry: %+v", response)
	}
}

func TestObjectNextUsesOnlyKnownSlotIdentity(t *testing.T) {
	p := Params{"query_group": "fixture-qg"}
	var out Outcome
	objectSlotNext(&out, p, map[string]any{"records": []any{
		map[string]any{"slot_identity_known": false, "query_group_key": "fixture-qg", "evaluation_time": json.Number("12")},
		map[string]any{"slot_identity_known": true, "query_group_key": "other", "evaluation_time": json.Number("13")},
		map[string]any{"slot_identity_known": true, "query_group_key": "fixture-qg", "evaluation_time": json.Number("14")},
		map[string]any{"slot_identity_known": true, "query_group_key": "fixture-qg", "evaluation_time": json.Number("14")},
	}})
	if len(out.Next) != 1 || out.Next[0].Operation != "slot.get" || out.Next[0].Params["evaluation_time"] != int64(14) {
		t.Fatal(out.Next)
	}
}
