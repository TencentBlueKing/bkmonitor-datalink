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
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const viewActualBody = `{"generated_at":"2026-10-17T00:00:00Z","max_age_seconds":30,` +
	`"row":{"strategy_id":"77","state":"DETECTING","state_word":"在检测","capability_class":"supported","line":"在检测 · 无需处理","punctuality":"late","kb_anchor":"state-DETECTING"},` +
	`"objects":{"total":2,"by_punctuality":{"on_time":1,"late":1}},"check":"SLOTS_OVERDUE"}`

const viewConfigBody = `{"strategy_id":"77","answered_by":"r1","publication":{},"standing":"ACCEPTED","found":true,"retained":true,"line":"",` +
	`"dispositions":[],"plans":[{"query_group":"qg-secret","replica":"r1","existence":"active","rows":[],` +
	`"config":{"redacted":true,"items":[{"plan_id":"plan-secret","schedule":{"interval_seconds":60,"alignment_seconds":0},` +
	`"query":{"source_semantics":["custom/time_series"],"clauses":[{"table_id":"t.secret","field":"f"}]},"target":{"kind":"NONE"},` +
	`"levels":[{"level_id":2,"priority":1,"algorithms":[{"type":"Threshold","version":1,"config_bytes":10}],"trigger":{},"recovery":{}}]}]}}]}`

func viewNative(t *testing.T, actual, config string, configStatus int, asked *[]string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*asked = append(*asked, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/api/diagnose":
			if actual == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"STRATEGY_NOT_IN_UNIVERSE"}`))
				return
			}
			_, _ = w.Write([]byte(actual))
		case "/api/strategies/77":
			w.WriteHeader(configStatus)
			_, _ = w.Write([]byte(config))
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
		}
	})
}

// strategy.view reads the diagnosis's view of the strategy and its frozen
// configuration once each, and answers words: the conclusion line, what it
// should detect, what it does. It is a front read and carries none of the
// configuration's coordinates.
func TestStrategyViewJoinsTheDiagnosisAndTheConfigurationAsWords(t *testing.T) {
	var asked []string
	c := testChannel(t, &testAuth{}, NativeOperations(viewNative(t, viewActualBody, viewConfigBody, 200, &asked))...)
	status, out := call(t, c, envelope(c, "invoke", "strategy.view", Params{"strategy_id": "77"}))
	if status != 200 || len(asked) != 2 || asked[0] != "/api/diagnose?strategy_id=77&view=1" || asked[1] != "/api/strategies/77?include=config" {
		t.Fatalf("status %d asked %v: %+v", status, asked, out)
	}
	raw, _ := json.Marshal(out)
	encoded := string(raw)
	for _, want := range []string{"每 1 分钟", "自定义指标", "预警：静态阈值", "不限定目标（全部）", "全天生效", "迟到", `"total":2`, "在检测 · 无需处理", "SLOTS_OVERDUE", "state-DETECTING"} {
		if !strings.Contains(encoded, want) {
			t.Errorf("the view lacks %q: %s", want, encoded)
		}
	}
	// The channel's envelope names the answering process; the result is
	// what the page shows.
	result, _ := json.Marshal(out.Result)
	if len(result) < 50 {
		t.Fatalf("no result in %s", encoded)
	}
	for _, forbidden := range []string{"qg-secret", "plan-secret", "t.secret", "query_group", "replica", "plan_id", "Threshold"} {
		if strings.Contains(string(result), forbidden) {
			t.Errorf("the view carries %q: %s", forbidden, result)
		}
	}
	if out.Summary != "在检测 · 无需处理" {
		t.Errorf("summary %q, want the conclusion line", out.Summary)
	}
	if !FrontReads["strategy.view"] {
		t.Fatal("strategy.view is not a front read")
	}
}

// A strategy outside the universe is not found, by name; a configuration
// that could not be read leaves the view incomplete with why, never an empty
// "detects nothing".
func TestStrategyViewSaysNotFoundAndAnUnreadConfiguration(t *testing.T) {
	var asked []string
	c := testChannel(t, &testAuth{}, NativeOperations(viewNative(t, "", "", 200, &asked))...)
	_, out := call(t, c, envelope(c, "invoke", "strategy.view", Params{"strategy_id": "77"}))
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "not_found") || len(asked) != 1 {
		t.Fatalf("an unknown strategy answered %s after %v", raw, asked)
	}
	asked = nil
	c = testChannel(t, &testAuth{}, NativeOperations(viewNative(t, viewActualBody, `{"error":"LEADER_UNAVAILABLE"}`, 503, &asked))...)
	status, out := call(t, c, envelope(c, "invoke", "strategy.view", Params{"strategy_id": "77"}))
	raw, _ = json.Marshal(out)
	encoded := string(raw)
	if status != 200 || !strings.Contains(encoded, `"known":false`) || !strings.Contains(encoded, "检测配置暂时读不到") || !strings.Contains(encoded, "迟到") {
		t.Fatalf("an unread configuration answered %d %s", status, encoded)
	}
	if out.Evidence.Complete || !strings.Contains(strings.Join(out.Evidence.Limitations, " "), "应检测未读到") {
		t.Fatalf("an unread configuration read complete: %+v", out.Evidence)
	}
}
