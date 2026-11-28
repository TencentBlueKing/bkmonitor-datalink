// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pluginruntime

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSynchronousHTTPExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		bad    bool
	}{
		{"success", 200, `{"version":1,"request_id":"r","events":[]}`, false},
		{"busy", 503, `{"error":"private runtime details"}`, true},
		{"timeout", 504, `{"error":"private runtime details"}`, true},
		{"authentication", 401, `{"error":"private runtime details"}`, true},
		{"plugin error", 200, `{"error":{"code":"plugin_error","type":"ValueError"}}`, true},
		{"invalid result", 200, `broken`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer internal-token" {
					t.Error("missing internal authentication")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := NewHTTPClient(server.Client(), server.URL, "internal-token", 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execute(t.Context(), Request{RequestID: "r"})
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.status != http.StatusOK {
				var statusError *HTTPError
				if !errors.As(err, &statusError) || statusError.StatusCode != tc.status || strings.Contains(err.Error(), "private runtime details") {
					t.Fatalf("runtime status lost or response leaked: %v", err)
				}
			}
			if calls != 1 {
				t.Fatal("unexpected automatic retry")
			}
		})
	}
}

func TestExecutionDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var forwarded atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				forwarded.Add(1)
				_, _ = io.WriteString(w, `{"version":1,"events":[]}`)
			}))
			defer target.Close()
			var attempted atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempted.Add(1)
				http.Redirect(w, r, target.URL, status)
			}))
			defer origin.Close()
			shared := origin.Client()
			client, err := NewHTTPClient(shared, origin.URL, "private-token", 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execute(t.Context(), Request{RequestID: "once"})
			var statusError *HTTPError
			if !errors.As(err, &statusError) || statusError.StatusCode != status || attempted.Load() != 1 || forwarded.Load() != 0 {
				t.Fatalf("execution redirected: attempts=%d forwarded=%d error=%v", attempted.Load(), forwarded.Load(), err)
			}
			if shared.CheckRedirect != nil {
				t.Fatal("caller client was modified")
			}
		})
	}
}
