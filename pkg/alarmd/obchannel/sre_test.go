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

func TestRegisteredNativeQueryPreservesScopeFailureAndScrubsReceipt(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Bk-Tenant-Id") != "tenant-fixture" || r.Header.Get("X-Bk-Scope-Space-Uid") != "space-fixture" {
			t.Error("explicit native scope was lost")
		}
		_, _ = io.WriteString(w, `{"result":false,"trace_id":"fixture-private-value","data":{"api_key":"native-credential"},"message":"password=another-credential"}`)
	}))
	defer backend.Close()
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: backend.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"}, Endpoints: []uq.Endpoint{{Path: "/query/ts/", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}, nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	c := testChannel(t, &testAuth{}, SREOperations(SREOptions{Queries: queries, Scrubber: redact.New("fixture-private-value")})...)
	params := Params{"egress": "native", "endpoint": "/query/ts/", "tenant": "tenant-fixture", "space": "space-fixture", "body": map[string]any{"start_time": "1700000000", "end_time": "1700000060", "query_list": []any{map[string]any{"table_id": "fixture.metric", "field_name": "value"}}}}
	status, out := call(t, c, envelope(c, "invoke", "uq.query", params))
	if status != http.StatusBadGateway || out.Error == nil || out.Error.Code != "uq_query_error" || out.Evidence.Complete || calls.Load() != 1 {
		t.Fatalf("native application failure was flattened (HTTP %d): %+v", status, out)
	}
	raw, _ := json.Marshal(out)
	for _, credential := range []string{"fixture-private-value", "native-credential", "another-credential"} {
		if strings.Contains(string(raw), credential) {
			t.Fatalf("native receipt leaked a credential: %s", credential)
		}
	}
	result := out.Result.(map[string]any)
	if result["http_status"] != float64(200) || result["request_digest"] == "" || result["actual_url"] != backend.URL+"/query/ts/" {
		t.Fatalf("native source was lost: %+v", result)
	}
	params["body"].(map[string]any)["TSDB_MAP"] = map[string]any{"a": "override"}
	_, out = call(t, c, envelope(c, "invoke", "uq.query", params))
	if out.Error == nil || out.Error.Code != "uq_scope_denied" || calls.Load() != 1 {
		t.Fatalf("physical route override reached the backend: %+v", out)
	}
}

func TestUnconfiguredSREOperationsStayDiscoverableAndDoNoIO(t *testing.T) {
	ops := SREOperations(SREOptions{})
	c := testChannel(t, &testAuth{}, ops...)
	for _, op := range ops {
		if op.Targetable {
			t.Fatalf("%s unexpectedly routes to a Worker", op.ID)
		}
		_, out := call(t, c, envelope(c, "describe", op.ID, nil))
		if out.Error != nil || out.Result.(map[string]any)["availability"].(map[string]any)["available"] != false {
			t.Fatalf("unconfigured capability was hidden or available: %+v", out)
		}
		if op.ID == "pod.exec" {
			params := Params{"target": map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "u", "container": "web", "container_id": "c", "image_id": "i"}, "argv": []any{"true"}}
			_, out = call(t, c, envelope(c, "invoke", op.ID, params))
			if out.Error == nil || out.Error.Code != "operation_unavailable" {
				t.Fatalf("an execution without a Pod provider was not refused as unavailable: %+v", out)
			}
		}
	}
	// A valid internal evidence identity still cannot execute the new exec op.
	out := c.ExecuteEvidence(context.Background(), Invocation{EnvironmentID: "test", Version: Version, Revision: c.revision, Operation: "pod.exec", Target: Target{Replica: "replica-1"}})
	if out.Error == nil || out.Error.Code != "operation_not_internal_evidence" {
		t.Fatalf("internal execution path accepted exec: %+v", out)
	}
}
