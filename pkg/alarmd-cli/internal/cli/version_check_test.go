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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// versionServer is a deployment that serves version (status 200) or the
// status given, and answers login and discover.
func versionServer(t *testing.T, p *Profile, status int, version string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	asked := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ob/api/cli/version":
			asked.Add(1)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(version))
		case "/ob/api/cli/auth/exchange":
			writeJSON(t, w, sessionResponse(*p))
		case "/ob/api/cli/channel":
			writeJSON(t, w, envelope(*p, "ok", map[string]any{"operations": []any{}}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	*p = fixtureProfile(server.URL)
	return server, asked
}

// runAs runs a command as a CLI stamped with version.
func runAs(t *testing.T, version string, store Store, client *http.Client, input string, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := New(strings.NewReader(input), &stdout, &stderr, version)
	a.Store, a.HTTP = store, client
	return a.Run(args), stderr.String()
}

// A release CLI of another version than the deployment serves is told so at
// login and at discover, with the command that installs the matching one
// from this deployment's own address; the command still runs.
func TestACLIOfAnotherVersionIsToldAtLoginAndDiscover(t *testing.T) {
	var p Profile
	server, asked := versionServer(t, &p, http.StatusOK, "0.4.4800\n")
	defer server.Close()
	store := Store{t.TempDir()}
	base := strings.TrimSuffix(p.PublicBaseURL, "/")
	want := "Warning: this alarmd-cli is 0.4.4753 and the deployment serves 0.4.4800. Install the matching one: curl -fsSL " +
		base + "/api/cli/install.sh | sh -s -- --server " + base
	code, stderr := runAs(t, "0.4.4753", store, server.Client(), bundle(p), "auth", "login")
	if code != 0 || !strings.Contains(stderr, want) {
		t.Fatalf("login: code %d stderr %q, want the warning %q", code, stderr, want)
	}
	code, stderr = runAs(t, "0.4.4753", store, server.Client(), "", "discover", "--env", p.EnvironmentID)
	if code != 0 || !strings.Contains(stderr, want) {
		t.Fatalf("discover: code %d stderr %q, want the warning", code, stderr)
	}
	if asked.Load() != 2 {
		t.Fatalf("version read %d times, want once at login and once at discover", asked.Load())
	}
}

// A pre-release is a release too, stamped as the Makefile stamps one
// (x.y.z-<pre-release>): either side being one is still compared.
func TestAPreReleaseOfAnotherVersionIsTold(t *testing.T) {
	for _, c := range []struct{ cli, served string }{
		{"0.2.0-ci.7", "0.2.0-ci.8"},
		{"0.4.4753", "0.4.4800-rc.1"},
		{"0.4.4800-rc.1", "0.4.4753"},
	} {
		var p Profile
		server, _ := versionServer(t, &p, http.StatusOK, c.served+"\n")
		code, stderr := runAs(t, c.cli, Store{t.TempDir()}, server.Client(), bundle(p), "auth", "login")
		server.Close()
		if want := "Warning: this alarmd-cli is " + c.cli + " and the deployment serves " + c.served + "."; code != 0 || !strings.Contains(stderr, want) {
			t.Fatalf("cli %s served %s: code %d stderr %q, want %q", c.cli, c.served, code, stderr, want)
		}
	}
}

// The same version says nothing; a development build does not ask; a
// deployment that serves no version (an older one, or one built without the
// CLI) or answers something that is not a version is not a mismatch.
func TestOnlyAKnownDifferentVersionWarns(t *testing.T) {
	for _, c := range []struct {
		name, cli, served string
		status            int
		asks              bool
	}{
		{"same version", "0.4.4753", "0.4.4753\n", http.StatusOK, true},
		{"development build", "dev", "0.4.4800\n", http.StatusOK, false},
		{"older deployment", "0.4.4753", `{"error":"not found"}`, http.StatusNotFound, true},
		{"no bundle", "0.4.4753", `{"status":"error","error":{"code":"cli_release_unavailable"}}`, http.StatusServiceUnavailable, true},
		{"not a version", "0.4.4753", "<html>proxy</html>", http.StatusOK, true},
		{"a version inside something else", "0.4.4753", "release 0.4.4800", http.StatusOK, true},
		{"the same pre-release", "0.2.0-ci.7", "0.2.0-ci.7\n", http.StatusOK, true},
		{"a leading zero is not a release", "0.4.04800", "0.4.4800\n", http.StatusOK, false},
		{"a refusal that looks like a version", "0.4.4753", "0.4.4800", http.StatusNotFound, true},
		{"too long", "0.4.4753", "0.4.4800" + strings.Repeat(" ", 70), http.StatusOK, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var p Profile
			server, asked := versionServer(t, &p, c.status, c.served)
			defer server.Close()
			code, stderr := runAs(t, c.cli, Store{t.TempDir()}, server.Client(), bundle(p), "auth", "login")
			if code != 0 || strings.Contains(stderr, "Warning: this alarmd-cli") {
				t.Fatalf("code %d stderr %q, want no warning", code, stderr)
			}
			if (asked.Load() > 0) != c.asks {
				t.Fatalf("version read %d times, want asked=%v", asked.Load(), c.asks)
			}
		})
	}
}

// A login from the authorization page is told the same.
func TestAListenLoginOfAnotherVersionIsTold(t *testing.T) {
	server := &pairingServer{t: t, served: "0.4.4800"}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	l := startListenAs(t, "0.4.4753", Store{Dir: t.TempDir()}, ts.Client(), p.PublicBaseURL, "")
	_, _, ready := l.call(t, http.MethodGet, "/ready?state="+l.state, l.origin, nil, nil)
	server.mu.Lock()
	server.challenge = stringField(ready, "code_challenge")
	server.mu.Unlock()
	if status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil); status != 200 {
		t.Fatalf("callback %d %v", status, body)
	}
	if code := <-l.done; code != 0 || !strings.Contains(l.stderr(), "Warning: this alarmd-cli is 0.4.4753 and the deployment serves 0.4.4800.") {
		t.Fatalf("listen exit %d stderr %q, want the warning", code, l.stderr())
	}
}
