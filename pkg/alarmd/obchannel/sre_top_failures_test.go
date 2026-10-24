// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

// The channel lists a target's failing routes; a group's message is scrubbed
// of the deployment's credentials and cut on a character boundary, and a
// sampled answer is not complete evidence.
func TestTopFailuresAreScrubbedAndBoundedForTheReader(t *testing.T) {
	message := "runtime-credential password=sql-credential " + strings.Repeat("采集配置不存在", 40)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := map[string]any{"trace_id": "synthetic-trace", "span_name": "/fixture", "resource.service.name": "fixture-service",
			"attributes.http.status_code": 200, "attributes.http.response.code": 101, "attributes.http.response.message": message,
			"start_time": 1700000000123456, "elapsed_time": 42000}
		encoded, _ := json.Marshal(map[string]any{"list": []any{row}, "total": 3})
		_, _ = io.WriteString(w, string(encoded))
	}))
	defer backend.Close()
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: backend.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"},
		Endpoints:    []uq.Endpoint{{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}},
		TraceTargets: []uq.TraceTarget{{Name: "fixture", Tenant: "tenant-fixture", Space: "space-fixture", TableID: "101_bkapm.trace_fixture", BkBizID: 101, AppName: "fixture-app"}}}}, nil, "fixture-source")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	c := testChannel(t, &testAuth{}, SREOperations(SREOptions{Queries: queries, Scrubber: redact.New("runtime-credential")})...)
	_, description := call(t, c, envelope(c, "describe", OpTraceTopFailures, nil))
	if desc := description.Result.(map[string]any); desc["availability"].(map[string]any)["available"] != true || desc["limits"].(map[string]any)["max_pages"] != float64(uq.TopFailuresPages) {
		t.Fatalf("description = %+v", desc)
	}
	status, out := call(t, c, envelope(c, "invoke", OpTraceTopFailures, Params{"egress": "native", "target": "fixture"}))
	if status != http.StatusOK || out.Error != nil || out.Evidence.Complete {
		t.Fatalf("HTTP %d out %+v, want a sampled answer that is not complete", status, out)
	}
	raw, _ := json.Marshal(out)
	for _, credential := range []string{"runtime-credential", "sql-credential"} {
		if strings.Contains(string(raw), credential) {
			t.Fatalf("the message leaked %s", credential)
		}
	}
	groups := out.Result.(map[string]any)["groups"].([]any)
	got := groups[0].(map[string]any)["message"].(string)
	if !utf8.ValidString(got) || len(got) > 203 || !strings.HasSuffix(got, "...") {
		t.Fatalf("message %q (%d bytes), want valid text cut at 200 bytes", got, len(got))
	}
}
