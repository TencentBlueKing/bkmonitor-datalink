// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/internaltoken"
)

func TestClientJWTIdentityAndWorkerIsolation(t *testing.T) {
	verifier, _ := internaltoken.New("client-secret", nil)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/internal/heartbeat" {
			if r.Header.Get("Authorization") != "Bearer worker" || r.Header.Get(internaltoken.HeaderName) != "" {
				t.Error("worker identity changed")
			}
		} else {
			username, err := verifier.VerifyHeader(r.Header)
			if err != nil || username != "service" || r.Header.Get("Authorization") != "" {
				t.Error("invalid management identity")
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := Client{URL: server.URL, JWTSecretKey: "client-secret", JWTUsername: "service"}
	if err := client.Call(t.Context(), "GET", "/api/v1/runtime", nil, nil); err != nil {
		t.Fatal(err)
	}
	worker := Client{URL: server.URL, Token: "worker"}
	if err := worker.Call(t.Context(), "POST", "/internal/heartbeat", nil, nil); err != nil {
		t.Fatal(err)
	}
	client.Token = "worker"
	if err := client.Call(t.Context(), "GET", "/api/v1/runtime", nil, nil); err == nil || calls != 2 {
		t.Fatal("ambiguous credentials accepted")
	}
}

func TestManagementClientDoesNotForwardTokenOnRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("credential forwarded"); w.WriteHeader(204) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := Client{URL: source.URL, JWTSecretKey: "secret"}
	if err := client.Call(t.Context(), "GET", "/api/v1/runtime", nil, nil); err == nil {
		t.Fatal("redirect accepted")
	}
}
