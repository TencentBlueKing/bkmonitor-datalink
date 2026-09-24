// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runCLI(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(t.Context(), args, strings.NewReader(input), &out, &errOut, "test-version", "test-commit")
	return code, out.String(), errOut.String()
}

func testConfig(t *testing.T, endpoint string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "config.yaml")
	if err := writeSettings(path, settings{CurrentProfile: "test", Profiles: map[string]profile{"test": {URL: endpoint, Username: "operator", Password: "synthetic-password-731", TimeoutSeconds: 30}}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func errorCode(t *testing.T, raw string) string {
	t.Helper()
	var value struct {
		Error cliError `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("invalid error output %q: %v", raw, err)
	}
	return value.Error.Code
}

func TestReadCallPreservesContract(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "synthetic-password-731" {
			t.Error("Basic Auth mismatch")
		}
		if r.URL.Path != "/monitor/local-api/alerts" || r.URL.Query().Get("bk_tenant_id") != "tenant &a" || r.URL.Query().Get("cursor") != "opaque=+/" {
			t.Errorf("unexpected target %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"id":9007199254740993}],"nextCursor":"opaque-next","status":"partial","warnings":["refresh delay"]}`)
	}))
	defer server.Close()
	code, out, errOut := runCLI(t, "", "--config", testConfig(t, server.URL+"/monitor"), "api", "call", "alerts.list", "--query", "bk_tenant_id=tenant &a", "--query", "cursor=opaque=+/")
	if code != 0 || errOut != "" || calls.Load() != 1 {
		t.Fatalf("code=%d calls=%d error=%s", code, calls.Load(), errOut)
	}
	for _, expected := range []string{`9007199254740993`, `"nextCursor":"opaque-next"`, `"status":"partial"`, `"warnings":["refresh delay"]`} {
		if !strings.Contains(out, expected) {
			t.Errorf("missing %s in %s", expected, out)
		}
	}
}

func TestWriteGuardBeforeAnyIO(t *testing.T) {
	t.Setenv("LINKD_CLI_ALLOW_WRITE", "true")
	for _, op := range catalog() {
		if !op.RequiresWrite {
			continue
		}
		t.Run(op.Name, func(t *testing.T) {
			code, out, errOut := runCLI(t, "", "--config", "/does-not-exist/config.yaml", "api", "call", op.Name, "--body-file", "/does-not-exist/body.json")
			if code == 0 || out != "" || errorCode(t, errOut) != "write_confirmation_required" {
				t.Fatalf("guard bypass: %d %s %s", code, out, errOut)
			}
		})
	}
}

func TestDryRunIsOfflineAndRedacted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = io.WriteString(w, `{}`) }))
	defer server.Close()
	body := `{"expected_revision":9007199254740993,"spec":{"event_source_id":"s-1","hooks":[{"password":"backend-secret","apiToken":"private-token"}],"storage":{"url":"https://person:credential@example.com"}}}`
	code, out, errOut := runCLI(t, body, "--config", testConfig(t, server.URL), "api", "call", "event-sources.apply", "--path", "id=s-1", "--body-file", "-", "--dry-run")
	if code != 0 || calls.Load() != 0 {
		t.Fatalf("dry-run %d %d %s", code, calls.Load(), errOut)
	}
	for _, secret := range []string{"backend-secret", "private-token", "credential", "synthetic-password-731"} {
		if strings.Contains(out+errOut, secret) {
			t.Fatalf("leaked secret %q", secret)
		}
	}
	if !strings.Contains(out, `"expected_revision":9007199254740993`) || !strings.Contains(out, `"network_request_sent":false`) {
		t.Fatal(out)
	}
}

func TestValidationNeverSendsInvalidRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = io.WriteString(w, `{}`) }))
	defer server.Close()
	configPath := testConfig(t, server.URL)
	cases := []struct {
		name, op, body string
		args           []string
	}{
		{"unknown API", "http://example.com", "", nil},
		{"secret query", "event-sources.list", "", []string{"--query", "include_secrets=true"}},
		{"duplicate query", "alerts.list", "", []string{"--query", "limit=10", "--query", "limit=20"}},
		{"missing tenant", "alerts.get", "", []string{"--path", "id=a"}},
		{"path traversal", "alerts.get", "", []string{"--path", "id=../close", "--query", "bk_tenant_id=t"}},
		{"encoded traversal", "alerts.get", "", []string{"--path", "id=%2e%2e", "--query", "bk_tenant_id=t"}},
		{"too many items", "alerts.list", "", []string{"--query", "limit=201"}},
		{"invalid integer", "alerts.list", "", []string{"--query", "limit=1e2"}},
		{"invalid date", "metrics.query", "", []string{"--query", "from=yesterday", "--query", "to=now", "--query", "step=1"}},
		{"unregistered body", "alerts.list", `{}`, []string{"--body-file", "-"}},
		{"unknown JSON field", "onemodel.close", `{"bk_tenant_id":"t","cursor":"x","allow_write":true}`, []string{"--body-file", "-"}},
		{"duplicate JSON key", "onemodel.close", `{"bk_tenant_id":"t","cursor":"one","cursor":"two"}`, []string{"--body-file", "-"}},
		{"multiple JSON values", "onemodel.close", `{"bk_tenant_id":"t","cursor":"x"}{}`, []string{"--body-file", "-"}},
		{"missing revision", "event-sources.delete", `{}`, []string{"--path", "id=s", "--allow-write", "--body-file", "-"}},
		{"identity mismatch", "event-sources.apply", `{"expected_revision":1,"spec":{"event_source_id":"other"}}`, []string{"--path", "id=s", "--allow-write", "--body-file", "-"}},
		{"close lacks identity", "alerts.close", `{"bk_tenant_id":"t","reason":"resolved"}`, []string{"--path", "id=a", "--allow-write", "--body-file", "-"}},
		{"preview conflicting input", "enrich.preview", `{"bk_tenant_id":"t","event_source_id":"s","input":{"alert_id":"a","alert":{}}}`, []string{"--body-file", "-"}},
		{"preview wrong tenant", "enrich.preview", `{"bk_tenant_id":"t","event_source_id":"s","input":{"alert":{"bk_tenant_id":"other"}}}`, []string{"--body-file", "-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--config", configPath, "api", "call", tc.op}, tc.args...)
			code, out, errOut := runCLI(t, tc.body, args...)
			if code == 0 || out != "" || errOut == "" {
				t.Fatalf("invalid call accepted %d %s %s", code, out, errOut)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached server: %d", calls.Load())
	}
}

func TestReadonlyPOSTAndConfirmedWrite(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		args                     []string
	}{
		{"onemodel.search", "POST", "/local-api/onemodel/search", `{"bk_tenant_id":"t","model_id":"cw-Host","where":{}}`, nil},
		{"onemodel.related", "POST", "/local-api/onemodel/related", `{"bk_tenant_id":"t","roots":[{"model_id":"cw-Host","model_inst_id":"1"}],"relation":"belongs","direction":"out","query":{"model_id":"cw-Biz"}}`, nil},
		{"onemodel.close", "POST", "/local-api/onemodel/close", `{"bk_tenant_id":"t","cursor":"opaque"}`, nil},
		{"enrich.preview", "POST", "/local-api/enrich/preview", `{"bk_tenant_id":"t","event_source_id":"s","input":{"alert_id":"a"}}`, nil},
		{"event-sources.apply", "PUT", "/local-api/event-sources/s", `{"expected_revision":9007199254740993,"spec":{"event_source_id":"s"}}`, []string{"--path", "id=s", "--allow-write"}},
		{"event-sources.delete", "DELETE", "/local-api/event-sources/s", `{"expected_revision":2}`, []string{"--path", "id=s", "--allow-write"}},
		{"alerts.close", "POST", "/local-api/alerts/a?#/close", `{"bk_tenant_id":"t","operation_id":"581e3d13-c28b-45be-b06e-bc3f21d233cc","reason":"resolved","effective_at":"2026-09-24T00:00:00Z"}`, []string{"--path", "id=a?#", "--allow-write"}},
		{"strategy-audits.start", "POST", "/local-api/strategy-index/audits", `{"event_source_id":"s","hook_name":"index"}`, []string{"--allow-write"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("wrong route: %s %s", r.Method, r.URL)
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				want, _ := decodeJSON([]byte(tc.body))
				got, _ := decodeJSON(raw)
				wantJSON, _ := json.Marshal(want)
				gotJSON, _ := json.Marshal(got)
				if !bytes.Equal(wantJSON, gotJSON) {
					t.Errorf("body changed %s", raw)
				}
				_, _ = io.WriteString(w, `{"status":"partial","next_cursor":"unchanged"}`)
			}))
			defer server.Close()
			args := append([]string{"--config", testConfig(t, server.URL), "api", "call", tc.name, "--body-file", "-"}, tc.args...)
			code, out, errOut := runCLI(t, tc.body, args...)
			if code != 0 || calls.Load() != 1 || !strings.Contains(out, `"next_cursor":"unchanged"`) {
				t.Fatalf("call failed %d %s %s", code, out, errOut)
			}
		})
	}
}

func TestHTTPFailuresNoRetryOrCredentialLeak(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 410, 422, 429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"password=backend-private-secret"}}`)
			}))
			defer server.Close()
			code, out, errOut := runCLI(t, `{"expected_revision":1}`, "--config", testConfig(t, server.URL), "api", "call", "event-sources.delete", "--path", "id=s", "--allow-write", "--body-file", "-")
			if code == 0 || out != "" || calls.Load() != 1 || strings.Contains(errOut, "backend-private-secret") {
				t.Fatalf("unsafe failure %d %s %s", code, out, errOut)
			}
			if status >= 500 && !strings.Contains(errOut, `"outcome_unknown":true`) {
				t.Fatal("missing uncertain outcome", errOut)
			}
		})
	}
}

func TestRedirectNeverFollowed(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { leaked.Add(1); _, _ = io.WriteString(w, `{}`) }))
	defer destination.Close()
	for _, status := range []int{301, 302, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, status) }))
			defer server.Close()
			code, _, errOut := runCLI(t, "", "--config", testConfig(t, server.URL), "api", "call", "server.version")
			if code == 0 || errorCode(t, errOut) != "redirect_refused" || leaked.Load() != 0 {
				t.Fatalf("redirect bypass %d %s", code, errOut)
			}
		})
	}
}

func TestTLSValidation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer server.Close()
	code, _, errOut := runCLI(t, "", "--config", testConfig(t, server.URL), "api", "call", "server.version")
	if code == 0 || errorCode(t, errOut) != "network_error" {
		t.Fatalf("untrusted TLS accepted: %d %s", code, errOut)
	}
}

func TestResponseLimitAndInvalidJSON(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, strings.Repeat(" ", int(responseLimit)+1)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			code, out, errOut := runCLI(t, "", "--config", testConfig(t, server.URL), "api", "call", "server.version")
			if code == 0 || out != "" || errorCode(t, errOut) != "invalid_response" {
				t.Fatalf("invalid response accepted: %d %s", code, errOut)
			}
		})
	}
}

func TestTimeoutCancellationAndParallelCommands(t *testing.T) {
	started := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	op, _ := findOperation("server.version")
	p := profile{URL: server.URL, Username: "u", Password: "long-password", TimeoutSeconds: 1}
	start := time.Now()
	_, _, err := callHTTP(t.Context(), p, op, server.URL, nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout failed %v", err)
	}
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, _, err := callHTTP(ctx, p, op, server.URL, nil); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation ignored")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not exit")
	}
	for i := 0; i < 4; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			code, out, errOut := runCLI(t, "", "api", "describe", "alerts.close")
			if code != 0 || out == "" || errOut != "" {
				t.Fatalf("parallel command failed %s", errOut)
			}
		})
	}
}

func TestPayloadLimitsAndResponseRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"password":"db-secret","apiToken":"token-secret","nested":{"client_key":"private-key","url":"https://user:pass@example.com"},"next_cursor":"keep-me","number":9007199254740993}`)
	}))
	defer server.Close()
	configPath := testConfig(t, server.URL)
	code, out, errOut := runCLI(t, "", "--config", configPath, "api", "call", "server.config")
	if code != 0 {
		t.Fatal(errOut)
	}
	for _, secret := range []string{"db-secret", "token-secret", "private-key", "user:pass"} {
		if strings.Contains(out, secret) {
			t.Fatal("secret leaked", secret)
		}
	}
	if !strings.Contains(out, `"next_cursor":"keep-me"`) || !strings.Contains(out, "9007199254740993") {
		t.Fatal(out)
	}
	code, _, errOut = runCLI(t, strings.Repeat(" ", 4097), "--config", configPath, "api", "call", "alerts.close", "--path", "id=a", "--body-file", "-", "--allow-write")
	if code == 0 || errorCode(t, errOut) != "size_limit" {
		t.Fatal("body limit failed", errOut)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriteResultOutputFailureDoesNotInviteRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"deleted":true}`)
	}))
	defer server.Close()
	var errOut bytes.Buffer
	code := Execute(t.Context(), []string{"--config", testConfig(t, server.URL), "api", "call", "event-sources.delete", "--path", "id=s", "--body-file", "-", "--allow-write"}, strings.NewReader(`{"expected_revision":1}`), failingWriter{}, &errOut, "test", "test")
	if code == 0 || calls.Load() != 1 || errorCode(t, errOut.String()) != "output_error" || !strings.Contains(errOut.String(), `"outcome_unknown":true`) || !strings.Contains(errOut.String(), `"http_status":202`) {
		t.Fatalf("ambiguous completed operation: %d %s", code, errOut.String())
	}
}
