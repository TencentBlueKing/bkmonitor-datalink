// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// objectRoute answers /api/objects with a page cut from rows the way the
// route cuts it, and remembers the query it was asked.
type objectRoute struct {
	rows   []map[string]any
	asked  url.Values
	status int
	body   map[string]any
}

func (r *objectRoute) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.asked = req.URL.Query()
	if r.status != 0 {
		w.WriteHeader(r.status)
		_ = json.NewEncoder(w).Encode(r.body)
		return
	}
	offset, limit := 0, 50
	if v := r.asked.Get("offset"); v != "" {
		_ = json.Unmarshal([]byte(v), &offset)
	}
	if v := r.asked.Get("limit"); v != "" {
		_ = json.Unmarshal([]byte(v), &limit)
	}
	page := []map[string]any{}
	for i := offset; i < len(r.rows) && i < offset+limit; i++ {
		page = append(page, r.rows[i])
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"column": r.asked.Get("column"), "order": "oldest", "filtered": r.asked.Get("replica") != "",
		"anomalies": page, "demoted": []any{}, "page": map[string]any{"offset": offset, "limit": limit, "total": len(r.rows)},
		"demoted_total": len(r.rows), "demotion_entries": 463, "demotion_extensions": 6395, "demotion_exits": 1, "demoted_due": 4,
		"health":       "HEALTHY",
		"dependencies": []any{map[string]any{"role": "state_redis", "address": "redis-fixture:6379"}},
		"per_replica":  []any{map[string]any{"replica": "worker-a"}},
	})
}

func objectList(t *testing.T, handler http.Handler) Operation {
	t.Helper()
	for _, op := range NativeOperations(handler) {
		if op.ID == "object.list" {
			return op
		}
	}
	t.Fatal("object.list is not registered")
	return Operation{}
}

func poolRows(n int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"query_group": strings.Repeat("a", 60) + string(rune('a'+i%26)) + "000", "replica": "worker-a",
			"demoted_since": "2026-09-24T05:29:42Z", "query_cooldown": map[string]any{"event": "extended", "failures": 19, "last_query_at": "2026-09-24T06:29:04Z"}}
	}
	return rows
}

func TestObjectListPagesThePoolAndKeepsOnlyWhatThePageIsAbout(t *testing.T) {
	route := &objectRoute{rows: poolRows(120)}
	op := objectList(t, route)
	if op.EvidenceScope != "deployment" || op.Targetable {
		t.Fatalf("object.list reads the merged view, not one process: %+v", op)
	}
	out := op.Run(context.Background(), Params{"column": "demoted", "limit": json.Number("50")})
	if out.Error != nil {
		t.Fatalf("page failed: %+v", out.Error)
	}
	if route.asked.Get("column") != "demoted" || route.asked.Get("offset") != "0" || route.asked.Get("limit") != "50" {
		t.Fatalf("route asked the wrong page: %v", route.asked)
	}
	page := out.Value.(map[string]any)
	rows, _ := page["objects"].([]any)
	if len(rows) != 50 {
		t.Fatalf("rows not under objects: %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["demoted_since"] == nil || row["query_cooldown"] == nil {
		t.Fatalf("a pool row lost the fields the route serves: %v", row)
	}
	for _, dropped := range []string{"dependencies", "per_replica", "anomalies", "demoted"} {
		if _, present := page[dropped]; present {
			t.Fatalf("%s rode along on a page about rows", dropped)
		}
	}
	for _, kept := range []string{"column", "page", "demoted_total", "demotion_entries", "demotion_exits", "demotion_extensions", "demoted_due"} {
		if _, present := page[kept]; !present {
			t.Fatalf("%s missing from the page", kept)
		}
	}
	if out.Summary != "降级池 120 个，本页第 1–50 个；进程启动以来入池 463、延长 6395、出池 1；4 个已过冷却期未出池" {
		t.Fatalf("line: %s", out.Summary)
	}
	if len(out.Next) != 1 || out.Next[0].Operation != "object.list" || out.Next[0].Params["offset"] != 50 || out.Next[0].Params["limit"] != 50 || out.Next[0].Params["column"] != "demoted" {
		t.Fatalf("next page call: %+v", out.Next)
	}

	// The last page says where it ends and offers no further page.
	out = op.Run(context.Background(), Params{"column": "demoted", "offset": json.Number("100"), "limit": json.Number("50")})
	if len(out.Next) != 0 || !strings.HasPrefix(out.Summary, "降级池 120 个，本页第 101–120 个") {
		t.Fatalf("last page: %s %+v", out.Summary, out.Next)
	}
	out = op.Run(context.Background(), Params{"column": "demoted", "offset": json.Number("150")})
	if len(out.Next) != 0 || !strings.HasPrefix(out.Summary, "降级池 120 个，第 150 个之后没有了") {
		t.Fatalf("past the end: %s %+v", out.Summary, out.Next)
	}
}

func TestObjectListCarriesFiltersIntoTheRouteAndTheNextPage(t *testing.T) {
	route := &objectRoute{rows: poolRows(30)}
	op := objectList(t, route)
	out := op.Run(context.Background(), Params{"column": "demoted", "order": "newest", "replica": "worker-a", "strategy": "8999", "business": "2", "limit": json.Number("10")})
	for name, want := range map[string]string{"order": "newest", "replica": "worker-a", "strategy": "8999", "business": "2"} {
		if route.asked.Get(name) != want {
			t.Fatalf("%s not passed to the route: %v", name, route.asked)
		}
	}
	next := out.Next[0].Params
	if next["order"] != "newest" || next["replica"] != "worker-a" || next["strategy"] != "8999" || next["business"] != "2" || next["offset"] != 10 {
		t.Fatalf("next page dropped a filter: %v", next)
	}
	if !strings.Contains(strings.Join(out.Limitations, "\n"), "Rows are filtered") {
		t.Fatalf("a filtered page does not say so: %v", out.Limitations)
	}
	// An unfiltered read sends no empty filters: an empty replica would be refused.
	route.asked = nil
	op.Run(context.Background(), Params{"column": "anomalies"})
	for _, name := range []string{"order", "replica", "strategy", "business"} {
		if _, sent := route.asked[name]; sent {
			t.Fatalf("empty %s sent to the route", name)
		}
	}
}

func TestObjectListRefusalIsAnErrorNotAnEmptyPool(t *testing.T) {
	route := &objectRoute{status: http.StatusBadRequest, body: map[string]any{"error": "no replica named ghost is part of this deployment"}}
	out := objectList(t, route).Run(context.Background(), Params{"column": "demoted", "replica": "ghost"})
	if out.Error == nil || out.Error.Code != "evidence_unavailable" {
		t.Fatalf("refused filter read as a page: %+v", out)
	}
	if value, _ := out.Value.(map[string]any); value["error"] == nil {
		t.Fatalf("the route's reason was dropped: %v", out.Value)
	}
}

func TestObjectListInputBounds(t *testing.T) {
	op := objectList(t, &objectRoute{})
	for _, tc := range []struct {
		params Params
		valid  bool
	}{
		{Params{"column": "demoted"}, true},
		{Params{"column": "by_design", "order": "newest", "offset": json.Number("0"), "limit": json.Number("200")}, true},
		{Params{}, false},
		{Params{"column": "pool"}, false},
		{Params{"column": "demoted", "limit": json.Number("201")}, false},
		{Params{"column": "demoted", "limit": json.Number("0")}, false},
		{Params{"column": "demoted", "offset": json.Number("-1")}, false},
		{Params{"column": "demoted", "order": "random"}, false},
		{Params{"column": "demoted", "replica": ""}, false},
	} {
		if err := validate(op, tc.params); (err == nil) != tc.valid {
			t.Fatalf("params=%v err=%v", tc.params, err)
		}
	}
}

// The rows and the page are read by key from the route's response; the keys
// are the ones fleet's own response type encodes, so a rename there fails
// here rather than turning every page empty.
func TestObjectListReadsTheKeysTheRouteTypeEncodes(t *testing.T) {
	response := fleet.ListResponse{Column: fleet.ColumnDemoted, Page: fleet.Page{Offset: 0, Limit: 1, Total: 3}}
	response.Anomalies = []fleet.Anomaly{{QueryGroup: "qg-fixture"}}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	rows, _ := encoded["anomalies"].([]any)
	page, _ := encoded["page"].(map[string]any)
	if len(rows) != 1 || page["total"] != float64(3) || encoded["column"] != "demoted" {
		t.Fatalf("fleet.ListResponse no longer encodes the keys object.list reads: %s", raw)
	}
	for _, key := range objectListKept {
		if _, present := encoded[key]; !present && !omitEmptyInListResponse[key] {
			t.Fatalf("object.list keeps %q, which fleet.ListResponse does not encode", key)
		}
	}
}

// omitEmptyInListResponse are kept keys fleet omits when they are empty.
var omitEmptyInListResponse = map[string]bool{"replica": true, "strategy": true, "business": true, "last_demotion_exit": true, "demoted_due_oldest_seconds": true, "gaps": true}

func TestObjectListSaysPagesAreSeparateReads(t *testing.T) {
	out := objectList(t, &objectRoute{rows: poolRows(3)}).Run(context.Background(), Params{"column": "demoted"})
	if !strings.Contains(strings.Join(out.Limitations, "\n"), "Each page is a separate read") {
		t.Fatalf("a page does not say it is its own read: %v", out.Limitations)
	}
}

// oversizedRoute answers more than the response budget, as a deployment
// view with a full page of rows can.
type oversizedRoute struct{}

func (oversizedRoute) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{"anomalies":["` + strings.Repeat("x", MaxResponseBytes) + `"]}`))
}

func TestObjectListOverBudgetOffersHalfThePage(t *testing.T) {
	op := objectList(t, oversizedRoute{})
	out := op.Run(context.Background(), Params{"column": "demoted", "offset": json.Number("50"), "limit": json.Number("200"), "replica": "worker-a"})
	if out.Error == nil || out.Error.Code != "response_budget_exceeded" {
		t.Fatalf("oversized page not refused by name: %+v", out)
	}
	if len(out.Next) != 1 || out.Next[0].Params["limit"] != 100 || out.Next[0].Params["offset"] != 50 || out.Next[0].Params["replica"] != "worker-a" {
		t.Fatalf("no half-size retry of the same page: %+v", out.Next)
	}
	out = op.Run(context.Background(), Params{"column": "demoted", "limit": json.Number("1")})
	if len(out.Next) != 0 {
		t.Fatalf("a one-row page cannot be halved: %+v", out.Next)
	}
}
