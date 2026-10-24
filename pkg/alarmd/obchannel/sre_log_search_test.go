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

// log.search answers log text through the channel, every line redacted under
// the deployment's contract: a deployment credential and a credential
// assignment in a line do not come back, a line without one comes back as
// written, the redaction is counted and the contract named, and UQ's SQL in
// its continuation stays out of the answer.
func TestLogSearchAnswersRedactedLogText(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/query/ts/raw" || !strings.Contains(string(body), `"is_search_after":true`) {
			t.Errorf("not a raw search: %s %s", r.URL.Path, body)
		}
		_, _ = io.WriteString(w, `{"total":3,"list":[`+
			`{"_time":1792735140000,"log":"db-migrate: connect mysql://root:pw@db failed, runtime-credential rejected password=hunter2","__ext":{"io_kubernetes_pod":"app-migrate-7-x0","container_name":"db-migrate"}},`+
			`{"_time":1792735080000,"log":"Applying app.0016_backfill_settings...","__ext":{"io_kubernetes_pod":"app-migrate-7-x0","container_name":"db-migrate"}}],`+
			`"result_table_options":{"2_bklog.fixture|doris":{"search_after":[1792735080000,"u2"],"sql":"SELECT log FROM t WHERE x"}}}`)
	}))
	defer backend.Close()
	queries, err := uq.NewWithQuerySource([]uq.Egress{{Name: "native", URL: backend.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"space-fixture"},
		Endpoints: []uq.Endpoint{{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}, nil, "fixture-source")
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	c := testChannel(t, &testAuth{}, SREOperations(SREOptions{Queries: queries, Scrubber: redact.New("runtime-credential")})...)
	params := Params{"egress": "native", "tenant": "tenant-fixture", "space": "space-fixture", "table_id": "2_bklog.fixture",
		"query_string": `__ext.io_kubernetes_pod: "app-migrate-7-x0"`, "lines": 2}
	status, out := call(t, c, envelope(c, "invoke", OpLogSearch, params))
	if status != http.StatusOK || out.Error != nil || !out.Evidence.Complete {
		t.Fatalf("HTTP %d out %+v", status, out)
	}
	raw, _ := json.Marshal(out)
	for _, leaked := range []string{"runtime-credential", "hunter2", "root:pw", "SELECT log"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the answer carries %q: %s", leaked, raw)
		}
	}
	for _, kept := range []string{"Applying app.0016_backfill_settings...", "db-migrate: connect", `"redacted_lines":1`, `"redaction_contract":"` + redact.Contract + `"`, `"next_cursor":"`} {
		if !strings.Contains(string(raw), kept) {
			t.Fatalf("the answer lacks %q: %s", kept, raw)
		}
	}
}
