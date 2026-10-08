// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "test-session-token-0123456789abcdef"
const testGrant = "test-grant-secret-0123456789abcdef"

func fixtureProfile(base string) Profile {
	return Profile{EnvironmentID: "env-one", EnvironmentName: "One", PublicBaseURL: base + "/ob/", AccessToken: testToken, SessionID: "session-one", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Scope: sessionScope, Pairing: "not_offered"}
}

func sessionResponse(p Profile) map[string]any {
	return map[string]any{"environment_id": p.EnvironmentID, "environment_name": p.EnvironmentName, "public_base_url": p.PublicBaseURL, "access_token": p.AccessToken, "session_id": p.SessionID, "expires_at": p.ExpiresAt, "scope": p.Scope}
}

func statusResponse(p Profile) map[string]any {
	return map[string]any{"environment_id": p.EnvironmentID, "session_id": p.SessionID, "expires_at": p.ExpiresAt, "scope": p.Scope, "renewed": false}
}

func envelope(p Profile, status string, result any) map[string]any {
	return map[string]any{"status": status, "summary": "fixture evidence", "result": result, "evidence": map[string]any{"complete": status == "ok", "limitations": []any{}}, "next_call": []any{}, "meta": map[string]any{"channel_version": channelVersion, "catalog_revision": "revision-one", "environment_id": p.EnvironmentID, "answered_by": "replica-one", "build": "build-one", "request_id": "request-one", "responded_at": time.Now().UTC().Format(time.RFC3339), "session": map[string]any{"session_id": p.SessionID, "expires_at": p.ExpiresAt, "renewed": false}}}
}

func bundle(p Profile) string {
	b := loginBundle{Version: "alarmd-login/v1", EnvironmentID: p.EnvironmentID, EnvironmentName: p.EnvironmentName, PublicBaseURL: p.PublicBaseURL, GrantSecret: testGrant, GrantExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)}
	data, _ := json.Marshal(b)
	return "alarmd-login-v1." + base64.RawURLEncoding.EncodeToString(data)
}

func run(t *testing.T, store Store, client *http.Client, input string, args ...string) (int, map[string]any, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := New(strings.NewReader(input), &stdout, &stderr, "test")
	a.Store, a.HTTP = store, client
	code := a.Run(args)
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("not JSON: %q (%v)", stdout.String(), err)
	}
	if stdout.Len() > stdoutBudget {
		t.Fatalf("stdout exceeds budget: %d", stdout.Len())
	}
	for _, secret := range []string{testToken, testGrant, "alarmd-login-v1."} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatalf("secret leaked to output")
		}
	}
	return code, result, stdout.String(), stderr.String()
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestLoginInvokePartialEvidenceAndExpiry(t *testing.T) {
	var p Profile
	var modes []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ob/api/cli/auth/exchange":
			if r.Header.Get("Authorization") != "" {
				t.Error("exchange sent an existing token")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_secret"] != testGrant || body["environment_id"] != p.EnvironmentID {
				t.Error("invalid exchange body")
			}
			writeJSON(t, w, sessionResponse(p))
		case "/ob/api/cli/channel":
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("missing Bearer")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mode := stringField(body, "mode")
			modes = append(modes, mode)
			if body["channel_version"] != channelVersion {
				t.Error("missing channel version")
			}
			if mode == "describe" {
				if _, ok := body["renew_if_due"]; ok {
					t.Error("describe requested renewal")
				}
				writeJSON(t, w, envelope(p, "ok", map[string]any{"input_schema": map[string]any{}}))
				return
			}
			if body["expected_catalog_revision"] != "revision-one" || body["renew_if_due"] != true {
				t.Error("invoke missing revision or renewal")
			}
			if objectField(body, "params")["strategy_id"] != "42" {
				t.Error("input was not preserved")
			}
			future := p
			future.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			result := envelope(future, "partial", map[string]any{"large": strings.Repeat("x", 40<<10), "unknown_optional": "kept", "nested": map[string]any{"authorization": "Bearer " + testToken, "password": "never-print", "admin_key": "never-print-admin", "Admin-Key": "never-print-admin-hyphen", "note": "echo " + testToken}})
			result["server_extension"] = map[string]any{"new_optional_field": true}
			writeJSON(t, w, result)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p = fixtureProfile(server.URL)
	p.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	store := Store{t.TempDir()}
	if code, _, _, _ := run(t, store, server.Client(), bundle(p), "auth", "login"); code != 0 {
		t.Fatal("login failed")
	}
	input := filepath.Join(t.TempDir(), "input.json")
	_ = os.WriteFile(input, []byte(`{"strategy_id":"42"}`), 0600)
	code, result, _, _ := run(t, store, server.Client(), "", "invoke", "unknown.future.operation", "--env", p.EnvironmentID, "--input", "@"+input)
	if code != 3 || result["status"] != "partial" || result["result_omitted"] != true {
		t.Fatalf("wrong partial response: %d %v", code, result)
	}
	if strings.Join(modes, ",") != "describe,invoke" {
		t.Fatalf("unexpected retries/calls: %v", modes)
	}
	path := stringField(objectField(result, "meta"), "result_file")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || !bytes.Contains(data, []byte("unknown_optional")) || !bytes.Contains(data, []byte("server_extension")) {
		t.Fatal("raw optional data or absolute evidence path lost")
	}
	for _, secret := range []string{testToken, "never-print", "never-print-admin", "never-print-admin-hyphen"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("evidence leaked a secret")
		}
	}
	for _, path := range []string{path, filepath.Join(store.Dir, "profiles.json"), filepath.Join(store.Dir, ".lock")} {
		assertPrivatePath(t, path, false)
	}
	for _, path := range []string{store.Dir, filepath.Join(store.Dir, "results")} {
		assertPrivatePath(t, path, true)
	}
	current, _ := store.get(p.EnvironmentID)
	if current.ExpiresAt == p.ExpiresAt {
		t.Fatal("expired local hint prevented renewal or was not updated")
	}
	files, _ := os.ReadDir(filepath.Join(store.Dir, "results"))
	if len(files) != 1 {
		t.Fatalf("expected one final evidence write, got %d", len(files))
	}
}

func TestURLAndBundleValidation(t *testing.T) {
	for _, raw := range []string{"ftp://example.com", "https://u:p@example.com", "http://u:p@example.com", "https://example.com/?q=x", "https://example.com/?", "https://example.com/#", "https://example.com/a/../b", "https://example.com/%2e%2e/", "https://example.com/%252e%252e/", "https://example.com/a%2fb", "https://example.com/a%5cb", "https://example.com/a\\b"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := baseURL(raw); err == nil {
				t.Fatal("unsafe URL accepted")
			}
		})
	}
	if got, _ := normalizedURL("https://EXAMPLE.com:443/prefix"); got != "https://example.com/prefix/" {
		t.Fatal(got)
	}
	if got, _ := normalizedURL("http://EXAMPLE.com:80/prefix"); got != "http://example.com/prefix/" {
		t.Fatal(got)
	}
	p := fixtureProfile("https://example.com")
	code := bundle(p)
	if _, err := parseBundle(code); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "alarmd-login-v1."))
	decoded = bytes.Replace(decoded, []byte(`"version":`), []byte(`"insecure_tls":true,"version":`), 1)
	if _, err := parseBundle("alarmd-login-v1." + base64.RawURLEncoding.EncodeToString(decoded)); err == nil {
		t.Fatal("unknown bundle field accepted")
	}
}

func TestLoginRejectsUntrustedTLSRedirectAndMismatch(t *testing.T) {
	var p Profile
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, sessionResponse(p)) }))
	defer server.Close()
	p = fixtureProfile(server.URL)
	if code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, bundle(p), "auth", "login"); code != 1 {
		t.Fatal("untrusted TLS accepted")
	}
	for _, mismatch := range []string{"environment", "scope", "url"} {
		t.Run(mismatch, func(t *testing.T) {
			var expected Profile
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := sessionResponse(expected)
				switch mismatch {
				case "environment":
					response["environment_id"] = "wrong"
				case "scope":
					response["scope"] = "observation_write"
				case "url":
					response["public_base_url"] = "https://wrong.invalid/"
				}
				writeJSON(t, w, response)
			}))
			defer s.Close()
			expected = fixtureProfile(s.URL)
			store := Store{t.TempDir()}
			if code, _, _, _ := run(t, store, s.Client(), bundle(expected), "auth", "login"); code != 1 {
				t.Fatal("mismatched session accepted")
			}
			if _, err := store.get(expected.EnvironmentID); err == nil {
				t.Fatal("mismatch persisted credentials")
			}
		})
	}
	redirected := false
	r := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/target" {
			redirected = true
		}
		http.Redirect(w, req, "/target", http.StatusTemporaryRedirect)
	}))
	defer r.Close()
	if code, _, _, _ := run(t, Store{t.TempDir()}, r.Client(), bundle(fixtureProfile(r.URL)), "auth", "login"); code != 1 || redirected {
		t.Fatal("redirect followed")
	}
}

func TestExplicitEnvironmentInputAndRebinding(t *testing.T) {
	store := Store{t.TempDir()}
	p := fixtureProfile("https://old.example.com")
	if err := store.save(p, false); err != nil {
		t.Fatal(err)
	}
	if err := store.use(p.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"discover"}, {"auth", "status"}, {"auth", "logout"}, {"invoke", "x", "--env", p.EnvironmentID, "--input", "[]"}, {"invoke", "x", "--env", p.EnvironmentID, "--input", "{}{}"}, {"auth", "login", "--token", "do-not-use"}} {
		if code, _, _, _ := run(t, store, &http.Client{}, "", args...); code != 2 {
			t.Fatalf("expected input error for %v: %d", args, code)
		}
	}
	next := p
	next.PublicBaseURL = "https://new.example.com/"
	if err := store.checkBinding(next, false); err == nil {
		t.Fatal("origin silently rebound")
	}
	if err := store.save(next, true); err != nil {
		t.Fatal(err)
	}
}

func TestLateResponsesDoNotReplaceNewLogin(t *testing.T) {
	for _, command := range []string{"discover", "logout"} {
		t.Run(command, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var p Profile
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				if command == "logout" {
					writeJSON(t, w, map[string]any{"session_id": p.SessionID, "revoked": true})
				} else {
					updated := p
					updated.ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
					writeJSON(t, w, envelope(updated, "ok", map[string]any{}))
				}
			}))
			defer s.Close()
			p = fixtureProfile(s.URL)
			store := Store{t.TempDir()}
			if err := store.save(p, false); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				args := []string{"discover", "--env", p.EnvironmentID}
				if command == "logout" {
					args = []string{"auth", "logout", "--env", p.EnvironmentID}
				}
				var out bytes.Buffer
				a := New(strings.NewReader(""), &out, io.Discard, "test")
				a.Store, a.HTTP = store, s.Client()
				if code := a.Run(args); code != 0 {
					t.Errorf("late request failed: %s", out.String())
				}
			}()
			<-started
			newSession := p
			newSession.SessionID = "session-two"
			newSession.AccessToken = "new-session-token"
			if err := store.save(newSession, false); err != nil {
				t.Fatal(err)
			}
			close(release)
			wg.Wait()
			current, err := store.get(p.EnvironmentID)
			if err != nil || current != newSession {
				t.Fatalf("late %s replaced session B: %+v %v", command, current, err)
			}
		})
	}
}

func TestRealStatusAndRevocationContracts(t *testing.T) {
	var p Profile
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+p.AccessToken {
			t.Error("missing authentication")
		}
		if r.Method == http.MethodGet {
			writeJSON(t, w, statusResponse(p))
		} else if r.Method == http.MethodDelete {
			writeJSON(t, w, map[string]any{"session_id": p.SessionID, "revoked": true})
		} else {
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer s.Close()
	p = fixtureProfile(s.URL)
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	code, result, _, _ := run(t, store, s.Client(), "", "auth", "status", "--env", p.EnvironmentID)
	if code != 0 || objectField(result, "session")["session_id"] == nil {
		t.Fatalf("valid status rejected: %d %v", code, result)
	}
	code, result, _, _ = run(t, store, s.Client(), "", "auth", "logout", "--env", p.EnvironmentID)
	if code != 0 || result["remote_revocation_confirmed"] != true || result["local_credentials_cleared"] != true {
		t.Fatalf("valid revocation rejected: %d %v", code, result)
	}
}

func TestStatusAndRevocationStillValidateIdentity(t *testing.T) {
	p := fixtureProfile("https://example.com")
	for _, field := range []string{"environment_id", "scope", "session_id", "expires_at"} {
		m := statusResponse(p)
		m[field] = "wrong"
		if _, err := validateStatus(m, p); err == nil {
			t.Errorf("status accepted invalid %s", field)
		}
	}
	for _, response := range []map[string]any{{"session_id": "wrong", "revoked": true}, {"session_id": p.SessionID, "revoked": false}, {"session_id": p.SessionID}} {
		if err := validateRevocation(response, p); err == nil {
			t.Error("invalid revocation accepted")
		}
	}
}

func TestPrivateCAIsBoundToProfileAndKeepsVerification(t *testing.T) {
	var p Profile
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/ob/api/cli/auth/exchange":
			writeJSON(t, w, sessionResponse(p))
		case "/ob/api/cli/session":
			writeJSON(t, w, statusResponse(p))
		case "/ob/api/cli/channel":
			writeJSON(t, w, envelope(p, "ok", map[string]any{}))
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	p = fixtureProfile(s.URL)
	certPath := filepath.Join(t.TempDir(), "private-ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	store := Store{t.TempDir()}
	code, _, _, _ := run(t, store, &http.Client{}, bundle(p), "auth", "login", "--ca-cert", certPath)
	if code != 0 {
		t.Fatal("private CA login failed")
	}
	current, err := store.get(p.EnvironmentID)
	if err != nil || current.CACert != certPath {
		t.Fatal("CA path not saved to profile")
	}
	if code, _, _, _ := run(t, store, &http.Client{}, "", "auth", "status", "--env", p.EnvironmentID); code != 0 {
		t.Fatal("saved CA not used by status")
	}
	if code, _, _, _ := run(t, store, &http.Client{}, "", "discover", "--env", p.EnvironmentID); code != 0 {
		t.Fatal("saved CA not used by channel")
	}
	if calls.Load() != 3 {
		t.Fatal("unexpected calls")
	}
	other := p
	other.EnvironmentID = "env-two"
	_ = store.save(other, false)
	if code, _, _, _ := run(t, store, &http.Client{}, "", "discover", "--env", other.EnvironmentID); code != 1 {
		t.Fatal("private CA leaked into another profile")
	}
	if calls.Load() != 3 {
		t.Fatal("untrusted connection reached handler")
	}
	// httptest's certificate has IP SANs but no localhost DNS SAN.
	if err := s.Certificate().VerifyHostname("localhost"); err == nil {
		t.Fatal("fixture unexpectedly trusts localhost")
	}
	wrongHostname := p
	wrongHostname.PublicBaseURL = strings.Replace(p.PublicBaseURL, "127.0.0.1", "localhost", 1)
	if code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, bundle(wrongHostname), "auth", "login", "--ca-cert", certPath); code != 1 {
		t.Fatal("CA option disabled hostname verification")
	}
	if calls.Load() != 3 {
		t.Fatal("wrong hostname reached handler")
	}
	if err := os.WriteFile(certPath, []byte("not a PEM certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, _, _ := run(t, store, &http.Client{}, "", "discover", "--env", p.EnvironmentID); code != 1 {
		t.Fatal("invalid persisted CA accepted")
	}
	if calls.Load() != 3 {
		t.Fatal("invalid CA sent credentials")
	}
}

func TestMissingOrInvalidPrivateCARejectsBeforeExchange(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); t.Error("CA error reached server") }))
	defer s.Close()
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	_ = os.WriteFile(invalid, []byte("not PEM"), 0600)
	for _, path := range []string{invalid, filepath.Join(t.TempDir(), "missing.pem"), "relative.pem"} {
		code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, bundle(fixtureProfile(s.URL)), "auth", "login", "--ca-cert", path)
		if code == 0 {
			t.Errorf("invalid CA accepted: %s", path)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("credentials sent before CA validation")
	}
}

func TestCASUsesTokenHashAndNewestExpiry(t *testing.T) {
	store := Store{t.TempDir()}
	p := fixtureProfile("https://example.com")
	_ = store.save(p, false)
	newer := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	if err := store.updateExpiry(p, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.updateExpiry(p, p.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	current, _ := store.get(p.EnvironmentID)
	if current.ExpiresAt != newer {
		t.Fatal("expiry moved backwards")
	}
	replaced := p
	replaced.AccessToken = "different-token-same-session-id"
	_ = store.save(replaced, false)
	if ok, err := store.clear(p); err != nil || ok {
		t.Fatal("CAS ignored token hash")
	}
	_, _ = store.clear(replaced)
	_ = store.updateExpiry(replaced, newer)
	if _, err := store.get(p.EnvironmentID); err == nil {
		t.Fatal("late update restored logged out credentials")
	}
}

func TestNetworkLogoutClearsLocalAndReportsUnknown(t *testing.T) {
	s := httptest.NewTLSServer(http.NotFoundHandler())
	p := fixtureProfile(s.URL)
	client := s.Client()
	s.Close()
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	code, result, _, _ := run(t, store, client, "", "auth", "logout", "--env", p.EnvironmentID)
	if code != 1 || result["remote_revocation_confirmed"] != false || result["local_credentials_cleared"] != true {
		t.Fatal(result)
	}
	if _, err := store.get(p.EnvironmentID); err == nil {
		t.Fatal("local credential retained")
	}
	if err := store.checkBinding(fixtureProfile("https://different.example.com"), false); err == nil {
		t.Fatal("logout lost environment binding")
	}
}

func TestCatalogChangedNeverRetriesInvoke(t *testing.T) {
	var p Profile
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, envelope(p, "ok", map[string]any{}))
			return
		}
		m := envelope(p, "error", nil)
		m["error"] = map[string]any{"code": "catalog_changed", "message": "describe again"}
		w.WriteHeader(http.StatusConflict)
		writeJSON(t, w, m)
	}))
	defer s.Close()
	p = fixtureProfile(s.URL)
	store := Store{t.TempDir()}
	_ = store.save(p, false)
	code, result, _, _ := run(t, store, s.Client(), "", "invoke", "future.operation", "--env", p.EnvironmentID)
	if code != 1 || calls != 2 || stringField(objectField(result, "error"), "code") != "catalog_changed" {
		t.Fatal(result, calls)
	}
}

func TestResponseLimitAndWrongChannelEnvironment(t *testing.T) {
	for _, kind := range []string{"too-large", "wrong-env", "unsupported-version"} {
		t.Run(kind, func(t *testing.T) {
			var p Profile
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "too-large" {
					_, _ = fmt.Fprint(w, `{"padding":"`+strings.Repeat("x", maxResponse)+`"}`)
					return
				}
				m := envelope(p, "ok", map[string]any{})
				if kind == "wrong-env" {
					objectField(m, "meta")["environment_id"] = "wrong"
				} else {
					objectField(m, "meta")["channel_version"] = "alarmd-ob/v2"
				}
				writeJSON(t, w, m)
			}))
			defer s.Close()
			p = fixtureProfile(s.URL)
			store := Store{t.TempDir()}
			_ = store.save(p, false)
			if code, _, _, _ := run(t, store, s.Client(), "", "discover", "--env", p.EnvironmentID); code != 1 {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
