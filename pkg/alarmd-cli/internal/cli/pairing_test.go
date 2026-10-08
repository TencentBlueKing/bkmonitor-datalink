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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRefresh = "test-refresh-credential-0123456789abcdef"

// pairingServer is an alarmd auth route that pairs: exchange returns a
// renewal credential, refresh rolls it, forget and pair are served. It
// checks a verifier against the challenge the listener announced.
type pairingServer struct {
	t         *testing.T
	mu        sync.Mutex
	p         Profile
	challenge string
	refreshes []string
	forgotten []string
	paired    int
	exchanges []map[string]any
	exchange  func(w http.ResponseWriter) bool
	// unbound answers a verifier without saying the grant was bound to it.
	unbound bool
	// dropRefresh closes the connection on the first renewal without a reply,
	// after the renewal happened.
	dropRefresh bool
	// legacy answers an exchange the way a server from before the binding
	// check does: without bound_to_challenge.
	legacy bool
	// truncateRefresh answers the first renewal 200 with a cut-off body,
	// after the renewal happened.
	truncateRefresh bool
	// channelRefused answers every channel call 401 with this code, as a
	// server does for a session it ended.
	channelRefused string
	// refreshRefused answers every renewal 401 with this code.
	refreshRefused string
	partialInvoke  bool
}

func (s *pairingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	session := func(token, refresh string) map[string]any {
		out := sessionResponse(s.p)
		out["access_token"], out["expires_at"] = token, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		out["refresh_token"], out["pairing_id"], out["pairing"] = refresh, "pairing-one", "paired"
		return out
	}
	switch r.URL.Path {
	case "/ob/api/cli/auth/exchange":
		s.exchanges = append(s.exchanges, body)
		if s.exchange != nil && s.exchange(w) {
			return
		}
		if s.challenge != "" {
			verifier, _ := body["code_verifier"].(string)
			sum := sha256.Sum256([]byte(verifier))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != s.challenge {
				w.WriteHeader(401)
				writeJSON(s.t, w, map[string]any{"status": "error", "error": map[string]string{"code": "grant_invalid_or_expired"}})
				return
			}
		}
		answer := session(testToken, testRefresh)
		if !s.legacy {
			answer["bound_to_challenge"] = body["code_verifier"] != nil && !s.unbound
		}
		writeJSON(s.t, w, answer)
	case "/ob/api/cli/auth/refresh":
		given, _ := body["refresh_token"].(string)
		s.refreshes = append(s.refreshes, given)
		if s.dropRefresh {
			s.dropRefresh = false
			hijacker, _ := w.(http.Hijacker)
			conn, _, _ := hijacker.Hijack()
			conn.Close()
			return
		}
		if s.truncateRefresh {
			s.truncateRefresh = false
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cut-of`))
			return
		}
		if given == "spent" || s.refreshRefused != "" {
			code := "renewal_expired_or_revoked"
			if s.refreshRefused != "" {
				code = s.refreshRefused
			}
			w.WriteHeader(401)
			writeJSON(s.t, w, map[string]any{"status": "error", "error": map[string]string{"code": code}})
			return
		}
		writeJSON(s.t, w, session(testToken+"-renewed", testRefresh+"-next"))
	case "/ob/api/cli/auth/forget":
		given, _ := body["refresh_token"].(string)
		s.forgotten = append(s.forgotten, given)
		writeJSON(s.t, w, map[string]any{"forgotten": true})
	case "/ob/api/cli/auth/pair":
		s.paired++
		writeJSON(s.t, w, map[string]any{"refresh_token": testRefresh + "-upgraded", "pairing_id": "pairing-two"})
	case "/ob/api/cli/session":
		if s.channelRefused != "" {
			w.WriteHeader(401)
			writeJSON(s.t, w, map[string]any{"status": "error", "error": map[string]string{"code": s.channelRefused}})
			return
		}
		writeJSON(s.t, w, map[string]any{"session_id": s.p.SessionID, "revoked": true, "environment_id": s.p.EnvironmentID, "scope": sessionScope, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case "/ob/api/cli/channel":
		if s.channelRefused != "" {
			w.WriteHeader(401)
			writeJSON(s.t, w, map[string]any{"status": "error", "error": map[string]string{"code": s.channelRefused}})
			return
		}
		status := "ok"
		if s.partialInvoke && body["mode"] == "invoke" {
			status = "partial"
		}
		writeJSON(s.t, w, envelope(s.p, status, map[string]any{}))
	default:
		s.t.Errorf("unexpected path %s", r.URL.Path)
		w.WriteHeader(404)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

type listening struct {
	port   int
	origin string
	state  string
	done   chan int
	stdout *bytes.Buffer
	errout *bytes.Buffer
}

// stderr is what the listener wrote to stderr; read it after done.
func (l *listening) stderr() string { return l.errout.String() }

func startListen(t *testing.T, store Store, client *http.Client, base string, timeout string) *listening {
	t.Helper()
	l := &listening{port: freePort(t), state: strings.Repeat("s", 43), done: make(chan int, 1), stdout: &bytes.Buffer{}, errout: &bytes.Buffer{}}
	u, _ := baseURL(base)
	l.origin = u.Scheme + "://" + u.Host
	a := New(strings.NewReader(""), l.stdout, l.errout, "test")
	a.Store, a.HTTP = store, client
	args := []string{"auth", "listen", "--url", base, "--port", strconv.Itoa(l.port), "--state", l.state}
	if timeout != "" {
		args = append(args, "--timeout", timeout)
	}
	go func() { l.done <- a.Run(args) }()
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(l.port)); err == nil {
			c.Close()
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the listener did not come up")
	return nil
}

func (l *listening) call(t *testing.T, method, path, origin string, body any, header map[string]string) (int, http.Header, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	r, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(l.port)+path, reader)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, resp.Header, out
}

func errCode(m map[string]any) string { return stringField(objectField(m, "error"), "code") }

// The loopback login end to end: the page's preflight is answered for the
// entry's origin only, private network access included; the probe gets the
// state back only for the right state; a callback with another state is
// refused without spending the listener; the code is exchanged with the
// verifier of the announced challenge, once; the profile keeps the pairing.
func TestListenTakesOneCodeFromTheEntrysPage(t *testing.T) {
	p := fixtureProfile("")
	server := &pairingServer{t: t}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p = fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	l := startListen(t, store, ts.Client(), p.PublicBaseURL, "")

	status, header, _ := l.call(t, http.MethodOptions, "/ready", l.origin, nil, map[string]string{
		"Access-Control-Request-Method": "GET", "Access-Control-Request-Private-Network": "true"})
	if status != 204 || header.Get("Access-Control-Allow-Origin") != l.origin || header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("preflight %d %v", status, header)
	}
	if status, header, body := l.call(t, http.MethodOptions, "/ready", "https://elsewhere.test", nil, map[string]string{"Access-Control-Request-Method": "GET"}); status != 403 || header.Get("Access-Control-Allow-Origin") != "" || errCode(body) != "origin_denied" {
		t.Fatalf("another origin's preflight %d %v", status, header)
	}
	if status, _, body := l.call(t, http.MethodGet, "/ready?state=wrong-state-0123456789", l.origin, nil, nil); status != 403 || errCode(body) != "state_mismatch" {
		t.Fatalf("probe with another state: %d %v", status, body)
	}
	status, _, ready := l.call(t, http.MethodGet, "/ready?state="+l.state, l.origin, nil, nil)
	challenge := stringField(ready, "code_challenge")
	if status != 200 || stringField(ready, "state") != l.state || len(challenge) != 43 {
		t.Fatalf("probe %d %v", status, ready)
	}
	server.mu.Lock()
	server.challenge = challenge
	server.mu.Unlock()
	if status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": "wrong-state-0123456789", "code": bundle(p)}, nil); status != 403 || errCode(body) != "state_mismatch" {
		t.Fatalf("callback with another state: %d %v", status, body)
	}
	status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil)
	if status != 200 || stringField(body, "environment_id") != p.EnvironmentID || stringField(body, "pairing") != "paired" {
		t.Fatalf("callback %d %v", status, body)
	}
	if code := <-l.done; code != 0 {
		t.Fatalf("listen exit %d: %s", code, l.stdout)
	}
	if strings.Contains(l.stdout.String(), testRefresh) || strings.Contains(l.stdout.String(), testToken) {
		t.Fatal("a credential reached the output")
	}
	if len(server.exchanges) != 1 || server.exchanges[0]["code_verifier"] == nil {
		t.Fatalf("exchanges %v", server.exchanges)
	}
	saved, err := store.get(p.EnvironmentID)
	if err != nil || saved.RefreshToken != testRefresh || saved.Pairing != "paired" || saved.AccessToken != testToken {
		t.Fatalf("saved %+v %v", saved, err)
	}
}

// A second callback, or a probe after the login, is refused by name.
func TestListenRefusesASecondCallback(t *testing.T) {
	server := &pairingServer{t: t}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	var release chan struct{} = make(chan struct{})
	var entered = make(chan struct{}, 1)
	server.exchange = func(w http.ResponseWriter) bool {
		entered <- struct{}{}
		server.mu.Unlock()
		<-release
		server.mu.Lock()
		return false
	}
	l := startListen(t, Store{Dir: t.TempDir()}, ts.Client(), p.PublicBaseURL, "")
	first := make(chan int, 1)
	go func() {
		status, _, _ := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil)
		first <- status
	}()
	<-entered
	if status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil); status != 409 || errCode(body) != "already_completed" {
		t.Fatalf("a callback while one is exchanging: %d %v", status, body)
	}
	close(release)
	if status := <-first; status != 200 {
		t.Fatalf("first callback %d", status)
	}
	<-l.done
}

// Nothing arrives: the listener gives up by name, and the port is freed.
func TestListenTimesOut(t *testing.T) {
	ts := httptest.NewTLSServer(&pairingServer{t: t})
	defer ts.Close()
	l := startListen(t, Store{Dir: t.TempDir()}, ts.Client(), fixtureProfile(ts.URL).PublicBaseURL, "200ms")
	if code := <-l.done; code != 1 || !strings.Contains(l.stdout.String(), "listen_timeout") {
		t.Fatalf("exit %d: %s", code, l.stdout)
	}
	if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(l.port)); err == nil {
		c.Close()
		t.Fatal("the port is still held")
	}
}

// A session about to run out is renewed from the pairing before use; the
// spent credential is replaced; an expired pairing is cleared and named.
func TestSessionsRenewFromThePairing(t *testing.T) {
	server := &pairingServer{t: t}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	p.RefreshToken, p.Pairing = testRefresh, "paired"
	p.ExpiresAt = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
	if err := store.save(p, false); err != nil {
		t.Fatal(err)
	}
	server.p.AccessToken = testToken + "-renewed"
	if code, _, out, _ := run(t, store, ts.Client(), "", "discover", "--env", p.EnvironmentID); code != 0 {
		t.Fatalf("discover %d %s", code, out)
	}
	saved, _ := store.get(p.EnvironmentID)
	if saved.AccessToken != testToken+"-renewed" || saved.RefreshToken != testRefresh+"-next" || len(server.refreshes) != 1 || server.refreshes[0] != testRefresh {
		t.Fatalf("after renewal %+v %v", saved, server.refreshes)
	}
	saved.RefreshToken, saved.ExpiresAt = "spent", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	_ = store.save(saved, false)
	code, result, _, _ := run(t, store, ts.Client(), "", "discover", "--env", p.EnvironmentID)
	if code != 1 || !saysLogInAgain(result, p, "renewal_expired_or_revoked") {
		t.Fatalf("an expired pairing: %d %v", code, result)
	}
	if _, err := store.get(p.EnvironmentID); err == nil {
		t.Fatal("an expired pairing left credentials behind")
	}
}

// A session from before this client is offered for pairing once; logging
// out forgets the pairing on the server.
func TestAnOlderSessionIsPairedOnceAndLogoutForgetsIt(t *testing.T) {
	server := &pairingServer{t: t}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	p.Pairing = ""
	store := Store{Dir: t.TempDir()}
	_ = store.save(p, false)
	for i := 0; i < 2; i++ {
		if code, _, out, _ := run(t, store, ts.Client(), "", "discover", "--env", p.EnvironmentID); code != 0 {
			t.Fatalf("discover %d %s", code, out)
		}
	}
	saved, _ := store.get(p.EnvironmentID)
	if server.paired != 1 || saved.RefreshToken != testRefresh+"-upgraded" || saved.Pairing != "paired" {
		t.Fatalf("upgrade %d %+v", server.paired, saved)
	}
	if code, _, out, _ := run(t, store, ts.Client(), "", "auth", "logout", "--env", p.EnvironmentID); code != 0 {
		t.Fatalf("logout %d %s", code, out)
	}
	if len(server.forgotten) != 1 || server.forgotten[0] != testRefresh+"-upgraded" {
		t.Fatalf("forgotten %v", server.forgotten)
	}
	if _, err := store.get(p.EnvironmentID); err == nil {
		t.Fatal("logout left credentials behind")
	}
}

// An exchange that never reached alarmd's CLI route leaves the code usable
// and says so; one the server refused says to get a new code.
func TestExchangeFailuresSayWhetherTheCodeIsSpent(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(w http.ResponseWriter)
		code   string
		says   string
	}{
		"not_answered": {func(w http.ResponseWriter) { w.WriteHeader(404); _, _ = w.Write([]byte("404 page not found")) }, "exchange_not_answered", "was not used"},
		"refused": {func(w http.ResponseWriter) {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "grant_invalid_or_expired"}})
		}, "grant_invalid_or_expired", "request a new code"},
		"proxy_error": {func(w http.ResponseWriter) { w.WriteHeader(502); _, _ = w.Write([]byte("<html>bad gateway</html>")) }, "exchange_outcome_unknown", "retry once"},
		"busy": {func(w http.ResponseWriter) {
			w.WriteHeader(429)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "auth_rate_limited"}})
		}, "auth_rate_limited", "retry"},
	} {
		t.Run(name, func(t *testing.T) {
			server := &pairingServer{t: t}
			server.exchange = func(w http.ResponseWriter) bool { tc.answer(w); return true }
			ts := httptest.NewTLSServer(server)
			defer ts.Close()
			p := fixtureProfile(ts.URL)
			server.p = p
			code, result, _, _ := run(t, Store{Dir: t.TempDir()}, ts.Client(), bundle(p), "auth", "login")
			if code != 1 || errCode(result) != tc.code || !strings.Contains(stringField(objectField(result, "error"), "message"), tc.says) {
				t.Fatalf("%d %v", code, result)
			}
		})
	}
}

// A loopback exchange whose answer does not say it was bound to this CLI's
// challenge is not kept.
func TestListenKeepsOnlyABoundExchange(t *testing.T) {
	server := &pairingServer{t: t, unbound: true}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	l := startListen(t, store, ts.Client(), p.PublicBaseURL, "2s")
	if status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil); status != 502 || errCode(body) != "grant_not_bound" {
		t.Fatalf("an unbound exchange: %d %v", status, body)
	}
	if _, err := store.get(p.EnvironmentID); err == nil {
		t.Fatal("an unbound exchange was kept")
	}
	<-l.done
}

// The page's Origin is compared as the browser writes it: lowercase host, no
// default port.
func TestTheListenerOriginIsTheBrowsers(t *testing.T) {
	for raw, want := range map[string]string{
		"http://APPS.Example.Test:80/alarmd/": "http://apps.example.test",
		"https://apps.example.test:443/x/":    "https://apps.example.test",
		"https://apps.example.test:8443/x/":   "https://apps.example.test:8443",
		"http://[2001:DB8::1]:8080/alarmd/":   "http://[2001:db8::1]:8080",
	} {
		u, _ := url.Parse(raw)
		if got := browserOrigin(u); got != want {
			t.Errorf("%s: %s, want %s", raw, got, want)
		}
	}
}

// A renewal whose reply was lost is asked again with the same credential,
// which the server answers with the same pair.
func TestARenewalWithALostReplyIsAskedAgain(t *testing.T) {
	server := &pairingServer{t: t, dropRefresh: true}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	p.RefreshToken, p.Pairing = testRefresh, "paired"
	p.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	_ = store.save(p, false)
	server.p.AccessToken = testToken + "-renewed"
	if code, _, out, _ := run(t, store, ts.Client(), "", "discover", "--env", p.EnvironmentID); code != 0 {
		t.Fatalf("discover %d %s", code, out)
	}
	if len(server.refreshes) != 2 || server.refreshes[0] != testRefresh || server.refreshes[1] != testRefresh {
		t.Fatalf("renewals %v", server.refreshes)
	}
}

// A server from before the binding check does not say whether the grant was
// bound: the login is taken, with a note that the protection needs an
// upgrade. A server that says false is refused (TestListenKeepsOnlyABoundExchange).
func TestListenTakesAnOlderServersAnswerWithANote(t *testing.T) {
	server := &pairingServer{t: t, legacy: true}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	l := startListen(t, store, ts.Client(), p.PublicBaseURL, "2s")
	if status, _, body := l.call(t, http.MethodPost, "/callback", l.origin, map[string]string{"state": l.state, "code": bundle(p)}, nil); status != 200 {
		t.Fatalf("an older server's exchange: %d %v", status, body)
	}
	<-l.done
	if _, err := store.get(p.EnvironmentID); err != nil {
		t.Fatal("an older server's exchange was not kept")
	}
	if !strings.Contains(l.stderr(), "predates the challenge binding") {
		t.Fatalf("no note: %q", l.stderr())
	}
}

// A renewal answered with a status and a body that cannot be read is asked
// again with the same credential, like one with no reply.
func TestARenewalWithATruncatedReplyIsAskedAgain(t *testing.T) {
	server := &pairingServer{t: t, truncateRefresh: true}
	ts := httptest.NewTLSServer(server)
	defer ts.Close()
	p := fixtureProfile(ts.URL)
	server.p = p
	store := Store{Dir: t.TempDir()}
	p.RefreshToken, p.Pairing = testRefresh, "paired"
	p.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	_ = store.save(p, false)
	server.p.AccessToken = testToken + "-renewed"
	if code, _, out, _ := run(t, store, ts.Client(), "", "discover", "--env", p.EnvironmentID); code != 0 {
		t.Fatalf("discover %d %s", code, out)
	}
	if len(server.refreshes) != 2 || server.refreshes[0] != testRefresh || server.refreshes[1] != testRefresh {
		t.Fatalf("renewals %v", server.refreshes)
	}
}

// saysLogInAgain is the lapse report: one code whatever the server called
// it, the cause kept, and the way back in -- the login page under the
// environment's own address and the command -- named.
func saysLogInAgain(result map[string]any, p Profile, cause string) bool {
	message := stringField(objectField(result, "error"), "message")
	return errCode(result) == credentialsLapsedCode && strings.Contains(message, "凭据已失效，请重新登录") &&
		strings.Contains(message, cause) && strings.Contains(message, "alarmd-cli auth login --env "+p.EnvironmentID) &&
		strings.Contains(message, strings.TrimSuffix(p.PublicBaseURL, "/")+"/cli")
}

// Every way a credential lapses is reported the same way, with the way back
// in, and never as a bare 401: a session the server ended with no pairing to
// renew from; a session ended whose pairing is gone too; a pairing from
// before the administrator key was rotated; and the same on the full
// diagnosis, which reads the channel on its own path.
func TestEveryLapseSaysLogInAgain(t *testing.T) {
	for _, tc := range []struct {
		name, channel, refresh, cause string
		paired                        bool
		args                          []string
	}{
		{name: "session ended, never paired", channel: "auth_expired_or_revoked", cause: "auth_expired_or_revoked", args: []string{"discover"}},
		{name: "session ended, pairing gone", channel: "auth_expired_or_revoked", refresh: "renewal_expired_or_revoked", paired: true, cause: "auth_expired_or_revoked", args: []string{"discover"}},
		{name: "key rotated", refresh: "renewal_admin_key_rotated", paired: true, cause: "renewal_admin_key_rotated", args: []string{"discover"}},
		{name: "diagnosis, session ended", channel: "auth_expired_or_revoked", cause: "auth_expired_or_revoked", args: []string{"diagnose"}},
		{name: "status, session ended", channel: "auth_expired_or_revoked", cause: "auth_expired_or_revoked", args: []string{"auth", "status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &pairingServer{t: t, channelRefused: tc.channel, refreshRefused: tc.refresh}
			ts := httptest.NewTLSServer(server)
			defer ts.Close()
			p := fixtureProfile(ts.URL)
			server.p = p
			store := Store{Dir: t.TempDir()}
			p.Pairing = "upgrade_refused"
			if tc.paired {
				p.RefreshToken, p.Pairing = testRefresh, "paired"
				// Due for renewal, so the renewal is what fails.
				if tc.channel == "" {
					p.ExpiresAt = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
				}
			}
			if err := store.save(p, false); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, tc.args...), "--env", p.EnvironmentID)
			code, result, out, _ := run(t, store, ts.Client(), "", args...)
			if code == 0 || !saysLogInAgain(result, p, tc.cause) {
				t.Fatalf("exit %d %v %s", code, result, out)
			}
		})
	}
}
