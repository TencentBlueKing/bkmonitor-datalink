// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

func TestNativeTraceDiscoveryDispatchAndSpanCredentialRedaction(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/query/ts/raw" || r.Header.Get("X-Bk-Tenant-Id") != "tenant-fixture" || r.Header.Get("X-Bk-Scope-Space-Uid") != "space-fixture" || r.Header.Get("Bk-Query-Source") != "fixture-source" {
			t.Error("Trace bypassed the existing alarmd UQ wire")
		}
		_, _ = io.WriteString(w, `{"list":[{"trace_id":"synthetic-trace","span_id":"span-fixture","start_time":1700000000123456,"end_time":1700000000123498,"elapsed_time":42,"attributes":{"api_key":"span-credential","statement":"password=sql-credential","large_uint":9223372036854775808},"attributes.api_key":"flat-api-credential","resource.client_secret":"flat-resource-credential","resource.service.name":"fixture-service","resource":{"client_secret":"resource-credential"},"events":[{"attributes":{"Authorization":"Bearer event-credential","http.request.header.Authorization":"flat-event-credential"}}],"links":[{"attributes.private_key":"flat-link-credential","attributes":{"nested":{"private_key":"link-credential"}}}],"span_name":"runtime-credential"}],"total":3,"status":[]}`)
	}))
	defer backend.Close()
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: backend.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"}, Endpoints: []uq.Endpoint{{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}, TraceTargets: []uq.TraceTarget{{Name: "fixture", Tenant: "tenant-fixture", Space: "space-fixture", TableID: "101_bkapm.trace_fixture", BkBizID: 101, AppName: "fixture-app"}}}}, nil, "fixture-source")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	operations := SREOperations(SREOptions{Queries: queries, Scrubber: redact.New("runtime-credential")})
	c := testChannel(t, &testAuth{}, operations...)
	_, description := call(t, c, envelope(c, "describe", "uq.trace", nil))
	desc := description.Result.(map[string]any)
	if desc["availability"].(map[string]any)["available"] != true || desc["limits"].(map[string]any)["max_rows"] != float64(1000) {
		t.Fatalf("Trace description omitted registration or budget: %+v", desc)
	}
	_, discovery := call(t, c, envelope(c, "invoke", "uq.egresses", nil))
	encoded, _ := json.Marshal(discovery)
	if !strings.Contains(string(encoded), `"trace_targets"`) || !strings.Contains(string(encoded), "101_bkapm.trace_fixture") {
		t.Fatalf("registered Trace mapping was hidden: %s", encoded)
	}
	params := Params{"egress": "native", "target": "fixture", "body": map[string]any{"start_time": "1700000000", "end_time": "1700000060", "from": 0, "limit": 2, "query_list": []any{map[string]any{"data_source": "bkapm", "table_id": "101_bkapm.trace_fixture", "time_field": "start_time", "query_string": "*", "conditions": map[string]any{"field_list": []any{map[string]any{"field_name": "trace_id", "op": "eq", "value": []any{"synthetic-trace"}}}, "condition_list": []any{}}}}}}
	status, out := call(t, c, envelope(c, "invoke", "uq.trace", params))
	if status != http.StatusOK || out.Error != nil || out.Evidence.Complete || calls.Load() != 1 {
		t.Fatalf("partial raw Span evidence was flattened (HTTP %d): %+v", status, out)
	}
	raw, _ := json.Marshal(out)
	for _, credential := range []string{"span-credential", "sql-credential", "resource-credential", "event-credential", "link-credential", "runtime-credential", "flat-api-credential", "flat-resource-credential", "flat-event-credential", "flat-link-credential"} {
		if strings.Contains(string(raw), credential) {
			t.Fatalf("Span evidence leaked a credential: %s", credential)
		}
	}
	// Inspect the raw JSON response rather than the test helper's float64
	// decoding to prove microsecond/large-uint values survive channel scrubbing.
	w := httptest.NewRecorder()
	data, _ := json.Marshal(envelope(c, "invoke", "uq.trace", params))
	r := httptest.NewRequest(http.MethodPost, "/api/cli/channel", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer fixture")
	c.ServeHTTP(w, r)
	for _, value := range []string{"1700000000123456", "1700000000123498", "9223372036854775808", `"elapsed_time":42`, `"span_time_unit":"us"`, `"more":true`, `"resource.service.name":"fixture-service"`} {
		if !strings.Contains(w.Body.String(), value) {
			t.Fatalf("native Span values/schema were changed: missing %s in %s", value, w.Body.String())
		}
	}
	params["tenant"] = "other-tenant"
	if status, out = call(t, c, envelope(c, "invoke", "uq.trace", params)); status != 400 || out.Error.Code != "invalid_input" || calls.Load() != 2 {
		t.Fatalf("caller identity override reached UQ: %+v", out)
	}
	out = c.ExecuteEvidence(context.Background(), Invocation{EnvironmentID: "test", Version: Version, Revision: c.revision, Operation: "uq.trace", Target: Target{Replica: "replica-1"}})
	if out.Error == nil || out.Error.Code != "operation_not_internal_evidence" || calls.Load() != 2 {
		t.Fatalf("Trace operation routed through worker evidence RPC: %+v", out)
	}
}

func TestNativeTraceStaysUnavailableWithoutRegisteredApplication(t *testing.T) {
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: "http://native.invalid", Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"}, Endpoints: []uq.Endpoint{{Path: "/query/ts/", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}, nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	c := testChannel(t, &testAuth{}, SREOperations(SREOptions{Queries: queries})...)
	_, out := call(t, c, envelope(c, "describe", "uq.trace", nil))
	if out.Error != nil || out.Result.(map[string]any)["availability"].(map[string]any)["available"] != false {
		t.Fatalf("unregistered Trace target was available: %+v", out)
	}
}
