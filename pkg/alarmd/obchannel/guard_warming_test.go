// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"net/http"
	"strings"
	"testing"
)

const warmingRow = `{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","cause":"GAP_GUARD_WARMING","cause_reason":"GAP_GUARD_WARMING",` +
	`"coverage":{"levels":6,"short":3,"guarded":3,"windows":[` +
	`{"key":"s/a/1","series":"a","level":1,"valid":7,"required":10,"end":"2026-09-29T11:00:00Z","guarded":true,"guard_reason":"QUERY_UNAVAILABLE","missing_total":3,"unusable_total":0,` +
	`"holes_by":{"answered_without_series":2,"answered_empty":0,"input_incomplete":1,"unusable":0,"primary_unrecorded":0,"not_in_memory":0,"before_this_process":0,"held_by_line":0}},` +
	`{"key":"s/b/1","series":"b","level":1,"valid":8,"required":10,"end":"2026-09-29T11:00:00Z","missing_total":2,"unusable_total":0,` +
	`"holes_by":{"answered_without_series":2,"answered_empty":0,"input_incomplete":0,"unusable":0,"primary_unrecorded":0,"not_in_memory":0,"before_this_process":0,"held_by_line":0}}]}}`

// object.get on an object whose round was filed under GAP_GUARD_WARMING says
// what the guard withholds and whose its guarded windows' missing minutes
// are, in the summary and as its own field; another object gets neither.
func TestObjectGetReadsAWarmingGuard(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		warming bool
	}{
		"warming":     {`{"query_group":"q","found":true,"facts_total":1,"anomaly":` + warmingRow + `}`, true},
		"not warming": {`{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","cause_reason":"HISTORY_GAPPED","coverage":{"levels":6,"short":1}}}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			c := testChannel(t, &testAuth{}, NativeOperations(native)...)
			status, out := call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "q"}))
			if status != 200 {
				t.Fatalf("status %d: %+v", status, out)
			}
			result := out.Result.(map[string]any)
			reading, found := result["guard_warming"].(map[string]any)
			if found != tc.warming || strings.Contains(out.Summary, "只挡 NORMAL") != tc.warming {
				t.Fatalf("guard reading present %v, summary %q; want warming %v", found, out.Summary, tc.warming)
			}
			if !tc.warming {
				return
			}
			minutes, _ := reading["minutes"].(map[string]any)
			if reading["listed"] != float64(1) || reading["guarded"] != float64(3) || minutes["data"] != float64(2) || minutes["incomplete"] != float64(1) {
				t.Fatalf("guard reading = %v, want the one listed guarded window of three: 2 minutes the data's, 1 incomplete", reading)
			}
			if !strings.Contains(out.Summary, "列出的 1/3 个守卫窗口共缺 3 分钟") {
				t.Fatalf("summary %q does not count the guarded window's minutes", out.Summary)
			}
		})
	}
}

// strategy.get puts the reading on each warming plan row and counts them in
// the summary; rows under other causes are left as they came.
func TestStrategyGetReadsEachWarmingRow(t *testing.T) {
	body := `{"strategy_id":"1001","line":"策略 1001","plans":[` +
		`{"query_group":"q1","existence":"present","rows":[` + warmingRow + `]},` +
		`{"query_group":"q2","existence":"present","rows":[{"kind":"DEGRADED_RUN","cause_reason":"HISTORY_GAPPED"}]}]}`
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	status, out := call(t, c, envelope(c, "invoke", "strategy.get", Params{"strategy_id": "1001"}))
	if status != 200 {
		t.Fatalf("status %d: %+v", status, out)
	}
	if !strings.HasPrefix(out.Summary, "策略 1001") || !strings.Contains(out.Summary, "1 个运行对象处于 GAP_GUARD_WARMING") ||
		!strings.Contains(out.Summary, "ABNORMAL 和 RECOVERY 照常判") {
		t.Fatalf("summary %q", out.Summary)
	}
	plans := out.Result.(map[string]any)["plans"].([]any)
	warming := plans[0].(map[string]any)["rows"].([]any)[0].(map[string]any)
	other := plans[1].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if _, found := warming["guard_warming"]; !found {
		t.Fatal("the warming row has no guard reading")
	}
	if _, found := other["guard_warming"]; found {
		t.Fatal("a row under another cause got a guard reading")
	}
}
