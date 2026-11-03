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
	"net/http"
	"strings"
	"testing"
)

// strategy.list without a scope asks exactly what it asked before and still
// says it is not the whole catalog. scope=accepted reads the published
// catalog page by page instead, says nothing of the kind, and offers the
// next page as a call carrying the cursor.
func TestStrategyListReadsTheWholeCatalogWhenAsked(t *testing.T) {
	var asked string
	body := `{"strategies":[]}`
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RequestURI()
		_, _ = w.Write([]byte(body))
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	notWhole := "not the complete source catalog"

	status, out := call(t, c, envelope(c, "invoke", "strategy.list", Params{}))
	if status != 200 || asked != "/api/strategies?action=&limit=50&state=" {
		t.Fatalf("default: status %d asked %q, want the request as before", status, asked)
	}
	if !strings.Contains(strings.Join(out.Evidence.Limitations, " "), notWhole) {
		t.Fatalf("default limitations %v, want it to say it is not the whole catalog", out.Evidence.Limitations)
	}

	body = `{"revision":"rev-1:3","total":5,"strategies":[{"strategy_id":"101","tenant":"t","business":"2","plans":[]}],"next_cursor":"abc"}`
	status, out = call(t, c, envelope(c, "invoke", "strategy.list", Params{"scope": "accepted", "limit": 1}))
	if status != 200 || asked != "/api/objects?limit=1&scope=strategy_catalog" {
		t.Fatalf("accepted: status %d asked %q", status, asked)
	}
	if strings.Contains(strings.Join(out.Evidence.Limitations, " "), notWhole) {
		t.Fatalf("the whole catalog says it is not: %v", out.Evidence.Limitations)
	}
	if len(out.Next) != 1 || out.Next[0].Operation != "strategy.list" || out.Next[0].Params["cursor"] != "abc" ||
		out.Next[0].Params["scope"] != "accepted" || out.Next[0].Params["limit"] != float64(1) && out.Next[0].Params["limit"] != 1 {
		t.Fatalf("next = %+v, want the next page with its cursor", out.Next)
	}
	status, _ = call(t, c, envelope(c, "invoke", "strategy.list", Params{"scope": "accepted", "cursor": "abc"}))
	if status != 200 || asked != "/api/objects?cursor=abc&limit=50&scope=strategy_catalog" {
		t.Fatalf("next page: status %d asked %q", status, asked)
	}
}
