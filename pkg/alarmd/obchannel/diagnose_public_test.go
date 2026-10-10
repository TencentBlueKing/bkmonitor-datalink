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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// serve answers one request through handler and returns the status and body.
func serve(t *testing.T, handler http.Handler, target string, header http.Header) (int, []byte) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for key, values := range header {
		request.Header[key] = values
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

// The first page gains the deployment section as its one new key, and every
// byte the page had is still there, in order: a reader of the existing keys
// sees nothing change.
func TestThePublicDiagnosisGainsTheDeploymentSectionAndKeepsEveryByte(t *testing.T) {
	page := map[string]any{"universe": map[string]any{"status": "ok", "count": 83},
		"page": map[string]any{"holds": true, "ids_expected": 20, "rows": 20}, "strategies": []any{}, "next_cursor": "abc_-1"}
	native := diagnoseNative(t, page)
	info := infoOp([]any{map[string]any{"roles": []any{"runtime"}, "address": "a:1", "status": "ok",
		"fields": map[string]any{"maxmemory_policy": "noeviction", "evicted_keys": "0", "maxmemory": "100", "used_memory": "90"}}})
	_, before := serve(t, native, "/api/diagnose?limit=20", nil)
	status, after := serve(t, WithDeploymentSection(native, []Operation{info}), "/api/diagnose?limit=20", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	trimmed := bytes.TrimRight(before, " \t\r\n")
	if !bytes.HasPrefix(after, trimmed[:len(trimmed)-1]) {
		t.Fatalf("the page's own bytes changed:\nbefore %s\nafter  %s", before, after)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, after)
	}
	if len(got) != len(page)+1 {
		t.Fatalf("keys = %d, want the page's %d and deployment", len(got), len(page))
	}
	var deployment struct {
		Findings []DeploymentFinding `json:"findings"`
	}
	if err := json.Unmarshal(got["deployment"], &deployment); err != nil {
		t.Fatal(err)
	}
	if !hasFinding(deployment.Findings, FindingRedisMemoryNearLimit, "") {
		t.Fatalf("findings = %+v, want REDIS_MEMORY_NEAR_LIMIT at 90 of 100", deployment.Findings)
	}
}

// Everything that is not the first page answered whole passes through as it
// was: a later page, a page a follower forwarded to the Leader (the follower
// adds the section), a refusal in the body, an error status, another route.
func TestThePublicDiagnosisLeavesEverythingElseAsItWas(t *testing.T) {
	page := map[string]any{"universe": map[string]any{"status": "ok"}, "page": map[string]any{"holds": true}}
	refusal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "DIAGNOSIS_NOT_WIRED"})
	})
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"reason": "x"})
	})
	forwarded := http.Header{}
	forwarded.Set(fleet.ForwardedHeader(), "1")
	for name, c := range map[string]struct {
		native http.Handler
		target string
		header http.Header
	}{
		"later page": {diagnoseNative(t, page), "/api/diagnose?cursor=abc", nil},
		"forwarded":  {diagnoseNative(t, page), "/api/diagnose", forwarded},
		"refusal":    {refusal, "/api/diagnose", nil},
		"error":      {failing, "/api/diagnose", nil},
		"health":     {diagnoseNative(t, page), "/api/health", nil},
	} {
		wantStatus, want := serve(t, c.native, c.target, c.header)
		gotStatus, got := serve(t, WithDeploymentSection(c.native, []Operation{infoOp(nil)}), c.target, c.header)
		if gotStatus != wantStatus || !bytes.Equal(got, want) {
			t.Errorf("%s: (%d, %s), want (%d, %s)", name, gotStatus, got, wantStatus, want)
		}
	}
}
