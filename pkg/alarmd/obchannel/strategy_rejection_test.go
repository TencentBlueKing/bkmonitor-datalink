// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// strategy.get, which a session reads, gives each Plan whose Query Group
// this replica kept a refusal for the provider's sanitized words; a Plan
// whose group this replica kept none for carries nothing, and the route the
// answer was read from never had them.
func TestStrategyGetCarriesTheKeptRejectionOfEachPlan(t *testing.T) {
	body := `{"strategy_id":"101","line":"策略 101","plans":[{"query_group":"q1","existence":"present"},{"query_group":"q2","existence":"present"}]}`
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	kept := fleet.KeptRejection{Class: "VM_SAMPLE_LIMIT", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", HTTPStatus: 400,
		Excerpt: `{"error":"cannot select more than 10000000 samples; password=<redacted>"}`, At: time.Unix(1_700_000_000, 0).UTC()}
	c := testChannel(t, &testAuth{}, NativeOperationsWith(native, NativeOptions{Rejections: func(group string) (fleet.KeptRejection, bool) {
		return kept, group == "q1"
	}})...)
	status, out := call(t, c, envelope(c, "invoke", "strategy.get", Params{"strategy_id": "101"}))
	if status != 200 {
		t.Fatalf("status %d: %+v", status, out)
	}
	plans := out.Result.(map[string]any)["plans"].([]any)
	rejection, _ := plans[0].(map[string]any)["rejection"].(map[string]any)
	if rejection["excerpt"] != kept.Excerpt || rejection["class"] != "VM_SAMPLE_LIMIT" || rejection["http_status"] != float64(400) ||
		rejection["trace_id"] != kept.TraceID {
		t.Fatalf("q1 rejection %v", rejection)
	}
	if _, found := plans[1].(map[string]any)["rejection"]; found {
		t.Fatal("a plan whose group kept no refusal got one")
	}
}
