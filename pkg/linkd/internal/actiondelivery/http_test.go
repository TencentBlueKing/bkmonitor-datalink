// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActionHTTPSenderRequiresMatchingCeleryReceipt(t *testing.T) {
	q := actionTask(t, "tenant", 1).Request
	ok, _ := json.Marshal(confirmed(q))
	invisible := confirmed(q)
	invisible.TaskID = ""
	notVisible, _ := json.Marshal(invisible)
	foreign := confirmed(q)
	foreign.TenantID = "foreign"
	foreignJSON, _ := json.Marshal(foreign)
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		retry      bool
	}{
		{"queued", 200, string(ok), "", false}, {"missing task", 200, string(notVisible), "response_invalid", false},
		{"foreign receipt", 200, string(foreignJSON), "response_invalid", false}, {"empty", 200, `{}`, "response_invalid", false},
		{"extra JSON", 200, string(ok) + `{}`, "response_invalid", false}, {"large", 200, strings.Repeat("x", 65537), "response_too_large", false},
		{"unauthorized", 401, "private-token", "remote_unauthorized", false}, {"identity conflict", 409, "private", "identity_conflict", false},
		{"unavailable", 503, "private", "remote_unavailable", true}, {"not confirmed accepted", 202, string(ok), "remote_rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Internal-Token") != "Bearer secret" || len(r.Header.Values("X-Bk-Tenant-Id")) != 0 {
					t.Error("protocol or authentication mismatch")
				}
				var got Request
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.Hash() != q.Hash() {
					t.Error("request not frozen")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			sender, e := NewHTTPSender()
			if e != nil {
				t.Fatal(e)
			}
			defer sender.Close()
			ack, e := sender.Send(t.Context(), Destination{Endpoint: server.URL, InternalToken: "secret"}, q)
			if tc.code == "" {
				if e != nil || ack.ValidateFor(q) != nil {
					t.Fatal(e)
				}
				return
			}
			var f Failure
			if !errors.As(e, &f) || f.Code != tc.code || f.Retryable != tc.retry || strings.Contains(e.Error(), "private") {
				t.Fatal(f, e)
			}
		})
	}
}

func TestActionHTTPSenderDoesNotFollowRedirectsOrLeakCredentials(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	sender, e := NewHTTPSender()
	if e != nil {
		t.Fatal(e)
	}
	defer sender.Close()
	q := actionTask(t, "tenant", 1).Request
	if _, e = sender.Send(t.Context(), Destination{Endpoint: redirect.URL, InternalToken: "secret"}, q); e == nil || followed.Load() {
		t.Fatal("redirect followed", e)
	}
	for _, endpoint := range []string{"file:///private", target.URL + "?token=secret", target.URL + "#fragment", "http://user:secret@localhost/"} {
		if _, e = sender.Send(t.Context(), Destination{Endpoint: endpoint, InternalToken: "secret"}, q); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid destination accepted or leaked", e)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = sender.Send(ctx, Destination{Endpoint: target.URL, InternalToken: "secret"}, q); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
