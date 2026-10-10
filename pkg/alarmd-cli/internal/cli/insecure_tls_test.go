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
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExplicitInsecureTLSPersistsOnlyForSelectedEnvironment(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "untrusted_chain"
		if mismatch {
			name = "untrusted_chain_and_wrong_hostname"
		}
		t.Run(name, func(t *testing.T) {
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
			if mismatch {
				if err := s.Certificate().VerifyHostname("localhost"); err == nil {
					t.Fatal("fixture hostname unexpectedly trusted")
				}
				p.PublicBaseURL = strings.Replace(p.PublicBaseURL, "127.0.0.1", "localhost", 1)
			}
			store := Store{t.TempDir()}
			client := &http.Client{}
			if code, _, _, _ := run(t, store, client, bundle(p), "auth", "login"); code != 1 || calls.Load() != 0 {
				t.Fatal("default verification was bypassed")
			}
			if code, _, _, _ := run(t, store, client, bundle(p), "auth", "login", "--insecure-tls"); code != 0 {
				t.Fatal("explicit exception failed")
			}
			current, err := store.get(p.EnvironmentID)
			if err != nil || !current.InsecureTLS {
				t.Fatal("exception not persisted")
			}
			if code, _, _, _ := run(t, store, &http.Client{}, "", "auth", "status", "--env", p.EnvironmentID); code != 0 {
				t.Fatal("later session request did not use saved exception")
			}
			code, out, _, _ := run(t, store, client, "", "discover", "--env", p.EnvironmentID)
			if code != 0 || stringField(objectField(objectField(out, "meta"), "client_transport"), "tls_verification") != "disabled_explicitly" {
				t.Fatal("missing actual transport receipt")
			}
			path := stringField(objectField(out, "meta"), "result_file")
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "disabled_explicitly") {
				t.Fatal("evidence file omitted transport mode")
			}
			_, listed, _, _ := run(t, store, client, "", "profile", "list")
			if objectField(objectField(listed, "profiles"), p.EnvironmentID)["insecure_tls"] != true {
				t.Fatal("profile list omitted exception")
			}
			other := p
			other.EnvironmentID = "env-two"
			if err := store.save(other, false); err != nil {
				t.Fatal(err)
			}
			before := calls.Load()
			if code, _, _, _ := run(t, store, client, "", "discover", "--env", other.EnvironmentID); code != 1 || calls.Load() != before {
				t.Fatal("exception leaked to another environment or the shared client")
			}
			if mismatch {
				return
			}
			// A successful new login with a trusted CA resets the exception.
			certPath := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if code, _, _, _ := run(t, store, client, bundle(p), "auth", "login", "--ca-cert", certPath); code != 0 {
				t.Fatal("strict re-login failed")
			}
			current, err = store.get(p.EnvironmentID)
			if err != nil || current.InsecureTLS || current.CACert != certPath {
				t.Fatal("strict re-login did not reset exception")
			}
			code, out, _, _ = run(t, store, client, "", "discover", "--env", p.EnvironmentID)
			if code != 0 || stringField(objectField(objectField(out, "meta"), "client_transport"), "tls_verification") != "private_ca" {
				t.Fatal("private CA receipt was incorrect")
			}
		})
	}
}

func TestInsecureTLSAppliesOnlyToHTTPSAndDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	if code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, bundle(fixtureProfile(s.URL)), "auth", "login", "--insecure-tls"); code != 1 || redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	if code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, bundle(fixtureProfile(target.URL)), "auth", "login", "--insecure-tls"); code != 2 || redirected.Load() != 0 {
		t.Fatal("HTTP allowed by TLS exception")
	}
}

func TestInsecureTLSCanOnlyBeChosenByLocalLogin(t *testing.T) {
	for _, args := range [][]string{{"discover", "--env", "x", "--insecure-tls"}, {"auth", "login", "--insecure-tls", "--ca-cert", "/test/ca.pem"}, {"auth", "login", "--insecure-tls", "--insecure-tls"}} {
		if code, _, _, _ := run(t, Store{t.TempDir()}, &http.Client{}, "", args...); code != 2 {
			t.Fatalf("accepted %v", args)
		}
	}
	p := fixtureProfile("https://example.com")
	response := sessionResponse(p)
	response["insecure_tls"] = true
	out, err := validateExchange(response, p)
	if err != nil || out.InsecureTLS {
		t.Fatal("server response enabled a local verification exception")
	}
}

func TestDeploymentProtocolChangeRequiresRebind(t *testing.T) {
	p := fixtureProfile("https://example.com")
	store := Store{t.TempDir()}
	if err := store.save(p, false); err != nil {
		t.Fatal(err)
	}
	p.PublicBaseURL = "http://example.com/ob/"
	if err := store.checkBinding(p, false); err == nil {
		t.Fatal("protocol change silently replaced the existing environment binding")
	}
	if err := store.save(p, true); err != nil {
		t.Fatal("explicit deployment protocol change was rejected")
	}
}
