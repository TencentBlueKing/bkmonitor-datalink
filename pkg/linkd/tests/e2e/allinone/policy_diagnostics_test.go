// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"linkd/internal/policy"
	"linkd/internal/policy/simulation"
)

// 正式进程从Kafka产生已丰富Event；模拟读取真实ES/MySQL事实，结果只在私有内存中推进。
func TestAllInOnePolicyDiagnosticsE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.publish("clip", policy.Suppression, map[string]any{"policy": condition("clip"), "scheme": []any{map[string]any{"type": "clip", "count": 3, "duration": 60, "duration_type": "second"}}}, time.Now().Add(10*time.Minute))
			first := h.send("clip", "policy-a", "diagnostic-one", "same", "clip", "host", "warning", "triggered")
			assertNoAlert(t, first)
			second := h.send("clip", "policy-a", "diagnostic-two", "same", "clip", "host", "warning", "triggered")
			assertNoAlert(t, second)
			var before policy.StatisticsPage
			path := "/api/v1/policies/statistics?bk_tenant_id=clip&type=suppression&ids=enabled-e2e&hours=1"
			h.until("real policy observations", func() bool {
				h.call(http.MethodGet, path, nil, &before)
				return len(before.Items) == 1 && before.Items[0].Matched >= 2
			})
			windows := h.suppressionWindows("clip", "clip")
			if len(windows) != 1 || windows[0].Count == nil || *windows[0].Count != 2 {
				t.Fatal("production counter missing")
			}
			events := len(h.events())
			at := time.Now().UTC()
			third := second.Event.Clone()
			third.EventID = "simulation-third"
			q := simulation.Request{Scope: policy.Scope{TenantID: "clip", Kind: policy.Suppression}, ID: "enabled-e2e", Version: 1, Steps: []simulation.Step{{At: at, EventID: first.Event.EventID}, {At: at.Add(time.Second), EventID: second.Event.EventID}, {At: at.Add(2 * time.Second), Event: &third}, {At: at.Add(time.Minute)}}}
			var result simulation.Response
			h.call(http.MethodPost, "/api/v1/policies/simulate", q, &result)
			if len(result.Steps) != 4 || result.Steps[0].Outcome != "alert_suppressed" || result.Steps[1].Outcome != "alert_suppressed" || result.Steps[2].Outcome != "alert_created" {
				t.Fatalf("simulation from saved facts %+v", result)
			}
			afterWindows := h.suppressionWindows("clip", "clip")
			if len(afterWindows) == len(windows) {
				for i := range windows {
					if afterWindows[i].RetentionMillis > windows[i].RetentionMillis {
						t.Fatal("simulation extended production TTL")
					}
					windows[i].ObservedAtMillis = afterWindows[i].ObservedAtMillis
					windows[i].RetentionMillis = afterWindows[i].RetentionMillis
				}
			}
			if !reflect.DeepEqual(windows, afterWindows) || len(h.events()) != events || len(h.alerts()) != 0 {
				t.Fatal("simulation mutated production state")
			}
			var after policy.StatisticsPage
			h.call(http.MethodGet, path, nil, &after)
			if !reflect.DeepEqual(before.Items, after.Items) {
				t.Fatal("simulation counted as production")
			}
			actual := h.send("clip", "policy-a", "diagnostic-three", "same", "clip", "host", "warning", "triggered")
			id := onlyAlert(t, actual)
			h.expect(id, "firing")
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}
