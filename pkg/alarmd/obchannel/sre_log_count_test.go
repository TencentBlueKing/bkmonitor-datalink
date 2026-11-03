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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

// log.count answers groups and counts through the channel: a field value
// holding a deployment credential is scrubbed, the filter's text does not
// come back, nor does UQ's own answer; a content field is refused by name.
func TestLogCountAnswersCountsWithoutLogText(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"count_over_time"`) {
			t.Errorf("not a count query: %s", body)
		}
		_, _ = io.WriteString(w, `{"series":[{"group_keys":["pod"],"group_values":["web-0 runtime-credential"],"values":[[1,40]]},`+
			`{"group_keys":["pod"],"group_values":["web-1"],"values":[[1,2]]}],"is_partial":false,"trace_id":"native-trace"}`)
	}))
	defer backend.Close()
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: backend.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"},
		Endpoints: []uq.Endpoint{{Path: "/query/ts", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}, nil, "fixture-source")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	c := testChannel(t, &testAuth{}, SREOperations(SREOptions{Queries: queries, Scrubber: redact.New("runtime-credential")})...)
	params := Params{"egress": "native", "tenant": "tenant-fixture", "space": "space-fixture", "table_id": "2_bklog.fixture",
		"by": []any{"pod"}, "query_string": "log:*secret-filter-text*"}
	status, out := call(t, c, envelope(c, "invoke", OpLogCount, params))
	if status != http.StatusOK || out.Error != nil || !out.Evidence.Complete {
		t.Fatalf("HTTP %d out %+v", status, out)
	}
	raw, _ := json.Marshal(out)
	for _, kept := range []string{"runtime-credential", "secret-filter-text", "native-trace", "group_values"} {
		if strings.Contains(string(raw), kept) {
			t.Fatalf("the answer carries %q: %s", kept, raw)
		}
	}
	if !strings.Contains(string(raw), `"total":42`) || !strings.Contains(string(raw), `"query_string_bytes":24`) {
		t.Fatalf("answer %s, want the total and the filter's length", raw)
	}
	params["by"] = []any{"log"}
	if _, refused := call(t, c, envelope(c, "invoke", OpLogCount, params)); refused.Error == nil || refused.Error.Code != "log_count_content_field" {
		t.Fatalf("grouping by log: %+v", refused.Error)
	}
}
