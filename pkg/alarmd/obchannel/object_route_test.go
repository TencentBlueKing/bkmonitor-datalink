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
	"strings"
	"sync"
	"testing"
	"time"
)

// objectRoutes answers the object route as a replica that tracks neither
// object's row: the healthy one only names its holder, the listed one has its
// row from the view.
func objectRoutes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/objects/qg-healthy":
			_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "facts": []any{}, "tracked_by": "pod-a", "tracked_by_as_of": "2026-10-09T10:00:00Z"})
		case "/api/objects/qg-listed":
			row := map[string]any{"query_group": "qg-listed", "kind": "SOURCE_BLOCKED"}
			_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "facts": []any{row}, "anomaly": row})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// An object read that the answering replica can only answer with the holder's
// name is asked again of the Query Group's lease holder, untold: the caller
// gets the holder's live row in one call. A listed object is answered where
// it lands, with no extra hop. When the routed read fails, the local answer
// stands, with the holder's name and the failure said.
func TestAnObjectReadIsRoutedToItsHolderOnlyWhenTheAnswerIsElsewhere(t *testing.T) {
	var mu sync.Mutex
	var routed []Invocation
	fail := false
	c, err := New(Options{Auth: &sessionAuth{}, EnvironmentID: "test", Replica: "pod-b", Build: "test",
		Operations: NativeOperations(objectRoutes()), Now: func() time.Time { return time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC) },
		Route: func(_ context.Context, invocation Invocation) Response {
			mu.Lock()
			routed = append(routed, invocation)
			mu.Unlock()
			if fail {
				return Response{Status: "error", Error: &Failure{Code: "target_unavailable", Message: "lease holder did not answer"}}
			}
			return Response{Status: "ok", Result: map[string]any{"tracked": map[string]any{"listed": false, "replica": "pod-a"}},
				Evidence: Evidence{Complete: true}, Meta: Meta{Version: Version, EnvironmentID: "test", AnsweredBy: "pod-a"}}
		}})
	if err != nil {
		t.Fatal(err)
	}
	_, out := call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "qg-healthy"}))
	result, _ := out.Result.(map[string]any)
	if len(routed) != 1 || routed[0].Target.OwnerQueryGroup != "qg-healthy" || result == nil || result["tracked"] == nil {
		t.Fatalf("healthy object: routed %+v, result %+v; want one read routed by its Query Group's lease and the holder's row", routed, out.Result)
	}
	// The routed answer still speaks for the caller's session.
	if out.Meta.Session == nil || out.Meta.Session.ID != "test-only" {
		t.Fatalf("routed answer session = %+v, want the caller's", out.Meta.Session)
	}
	_, out = call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "qg-listed"}))
	if len(routed) != 1 || out.Result.(map[string]any)["anomaly"] == nil {
		t.Fatalf("listed object: routed %d times, result %+v; want it answered where it landed", len(routed), out.Result)
	}
	fail = true
	_, out = call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "qg-healthy"}))
	result, _ = out.Result.(map[string]any)
	said := strings.Join(out.Evidence.Limitations, " ")
	if result == nil || result["tracked_by"] != "pod-a" || out.Evidence.Complete || out.Status != "partial" || !strings.Contains(said, "target_unavailable") {
		t.Fatalf("failed route: result %+v evidence %+v; want the local answer with the holder named and the failure said", out.Result, out.Evidence)
	}
}
