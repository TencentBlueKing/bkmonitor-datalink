// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// closedEntry is an entry URL nothing listens on.
func closedEntry(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return "http://" + address
}

// origin is one address serving both entries, as one domain does: the admin
// under /admin/ answering status and body, the deployment's own address
// under /own/ answering the channel as alarmd does. It counts what reaches
// each and the entry each request named.
type origin struct {
	*httptest.Server
	admin, own atomic.Int32
	entries    chan string
}

func newOrigin(t *testing.T, status int, body string) *origin {
	o := &origin{entries: make(chan string, 8)}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/own/") {
			o.own.Add(1)
			o.entries <- r.Header.Get(entryHeader)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(envelope(fixtureProfile("http://unused"), "ok", map[string]any{"answered": "alarmd"}))
			return
		}
		o.admin.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(o.Close)
	return o
}

// profile is a session issued through o's admin entry, with o's own address
// declared as its fallback.
func (o *origin) profile() Profile {
	p := fixtureProfile(o.URL)
	p.PublicBaseURL = o.URL + "/admin/alarmd/"
	p.FallbackBaseURLs = []string{o.URL + "/own/alarmd/"}
	return p
}

func fallbackApp(t *testing.T) (*App, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	a := New(strings.NewReader(""), &stdout, &stderr, "test")
	a.Store, a.HTTP = Store{t.TempDir()}, &http.Client{}
	return a, &stderr
}

// The fallback covers the admin application being down, and nothing else:
// the ingress answering 502/503 with no admin backend behind it, or the admin
// answering that it cannot reach alarmd. A read then goes to the deployment's
// own address on the same domain, says so on the request (X-Alarmd-Entry)
// and in meta.client_entry, and warns. Every other answer is the answer:
// alarmd's own error, a 504 after which alarmd may have run the call. A call
// that executes something never moves by itself; it says where it could go.
func TestARequestMovesOnlyWhenTheAdminItselfIsDown(t *testing.T) {
	adminDown := `{"error":{"code":"alarmd_unreachable","message":"cannot connect","requestId":"r1"}}`
	adminTimeout := `{"error":{"code":"alarmd_timeout","message":"no answer","requestId":"r2"}}`
	alarmdRefusal := `{"status":"error","error":{"code":"business_waiting","message":"x"},"meta":{}}`
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		exec, move bool
		why        string
		hint       bool // an executing call not moved: told where it could go
	}{
		{name: "ingress 503 without an admin backend", status: 503, body: "<html>no healthy upstream</html>", why: fallbackGateway, move: true},
		{name: "ingress 502 without an admin backend", status: 502, body: "bad gateway", why: fallbackGateway, move: true},
		{name: "admin cannot reach alarmd", status: 502, body: adminDown, why: fallbackAdminUnreachable, move: true},
		{name: "admin cannot reach alarmd, exec", status: 502, body: adminDown, exec: true, hint: true},
		{name: "ingress 503, exec", status: 503, body: "upstream down", exec: true, hint: true},
		{name: "admin timed out", status: 504, body: adminTimeout},
		{name: "ingress 504 without JSON", status: 504, body: "gateway timeout"},
		{name: "alarmd's own error", status: 502, body: alarmdRefusal},
		{name: "another JSON 502", status: 502, body: `{"error":{"code":"not_forwarded","message":"x"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newOrigin(t, tc.status, tc.body)
			a, stderr := fallbackApp(t)
			m, _, err := a.requestVia(o.profile(), http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, tc.exec)
			if moved := o.own.Load() == 1; moved != tc.move || o.admin.Load() != 1 {
				t.Fatalf("admin %d own %d, want moved=%v", o.admin.Load(), o.own.Load(), tc.move)
			}
			if !tc.move {
				hinted := strings.Contains(stderr.String(), "re-run with --entry alarmd")
				// The hint is in meta when there is an answer, in the error
				// when there is none (the ingress's bare page).
				available := stringField(objectField(objectField(m, "meta"), "client_entry"), "fallback_available")
				if m == nil && err != nil && strings.Contains(err.Error(), "re-run with --entry alarmd") {
					available = "/own/alarmd/"
				}
				if hinted != tc.hint || (tc.hint && !strings.Contains(available, "/own/alarmd/")) || (!tc.hint && strings.Contains(stderr.String(), "Warning")) {
					t.Fatalf("hint %v (want %v), fallback_available %q, stderr %q", hinted, tc.hint, available, stderr)
				}
				return
			}
			if got := <-o.entries; got != entryAlarmd {
				t.Fatalf("the moved request named entry %q, want %q", got, entryAlarmd)
			}
			client := objectField(objectField(m, "meta"), "client_entry")
			if stringField(client, "entry") != entryAlarmd || stringField(client, "reason") != tc.why || !strings.HasSuffix(stringField(client, "url"), "/own/alarmd/") {
				t.Fatalf("meta.client_entry = %v, want the declared entry for %s", client, tc.why)
			}
			if !strings.Contains(stderr.String(), "Warning: the session's entry did not reach alarmd ("+tc.why+")") {
				t.Fatalf("no warning: %q", stderr)
			}
		})
	}
}

// A domain that cannot be reached at all - nothing listening, no address -
// is an infrastructure failure, and the fallback on the same domain would
// fail the same way: nothing moves, the failure is reported as it was.
func TestANetworkFailureDoesNotMove(t *testing.T) {
	for name, entry := range map[string]string{
		"nothing listening": closedEntry(t),
		"no address":        "http://alarmd-fallback-test.invalid",
	} {
		t.Run(name, func(t *testing.T) {
			a, stderr := fallbackApp(t)
			p := fixtureProfile(entry)
			p.FallbackBaseURLs = []string{entry + "/own/alarmd/"}
			_, _, err := a.requestVia(p, http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, false)
			if err == nil || strings.Contains(stderr.String(), "Warning") {
				t.Fatalf("err %v, stderr %q: want the failure, no fallback", err, stderr)
			}
		})
	}
}

// Without a declared entry nothing moves, and a profile whose entry is on
// another origin (hand-edited, or from before the rule) is not used either.
func TestARequestWithoutASameOriginEntryStaysWhereItWasSent(t *testing.T) {
	o := newOrigin(t, 503, "no healthy upstream")
	a, stderr := fallbackApp(t)
	p := o.profile()
	p.FallbackBaseURLs = nil
	if _, _, _ = a.requestVia(p, http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, false); o.own.Load() != 0 || strings.Contains(stderr.String(), "Warning") {
		t.Fatalf("moved without a declared entry: own %d, stderr %q", o.own.Load(), stderr)
	}
	elsewhere := newOrigin(t, 200, "")
	p.FallbackBaseURLs = []string{elsewhere.URL + "/own/alarmd/"}
	if _, _, _ = a.requestVia(p, http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, false); elsewhere.own.Load() != 0 {
		t.Fatal("moved to an entry on another origin")
	}
	// The session's entry answering is meta.client_entry "session".
	healthy := newOrigin(t, 200, "")
	direct := fixtureProfile(healthy.URL)
	direct.PublicBaseURL = healthy.URL + "/own/alarmd/"
	m, _, err := a.requestVia(direct, http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, false)
	if err != nil || stringField(objectField(objectField(m, "meta"), "client_entry"), "entry") != "session" || <-healthy.entries != "" {
		t.Fatalf("a direct answer: %v %v", m, err)
	}
}

// The fallback is the server's: at most one entry, on the session's own
// origin with another path. Anything else refuses the answer; none declared
// is none.
func TestTheFallbackEntryIsOneSameOriginEntryTheServerDeclared(t *testing.T) {
	p := fixtureProfile("https://ops.example")
	p.PublicBaseURL = "https://ops.example/ops-admin/alarmd/"
	answer := func(fallback any) map[string]any {
		m := sessionResponse(p)
		if fallback != nil {
			m["fallback_base_urls"] = fallback
		}
		return m
	}
	got, err := validateExchange(answer([]any{"https://OPS.example:443/ops-alarmd"}), p)
	if err != nil || len(got.FallbackBaseURLs) != 1 || got.FallbackBaseURLs[0] != "https://ops.example/ops-alarmd/" {
		t.Fatalf("declared: %+v %v", got.FallbackBaseURLs, err)
	}
	if got, err := validateExchange(answer(nil), p); err != nil || got.FallbackBaseURLs != nil {
		t.Fatalf("none declared: %+v %v", got.FallbackBaseURLs, err)
	}
	for name, fallback := range map[string]any{
		"another host":      []any{"https://alarmd.example/ops-alarmd/"},
		"another scheme":    []any{"http://ops.example/ops-alarmd/"},
		"another port":      []any{"https://ops.example:8443/ops-alarmd/"},
		"two entries":       []any{"https://ops.example/ops-alarmd/", "https://ops.example/ops-alarmd-2/"},
		"not a list":        "https://ops.example/ops-alarmd/",
		"not a string":      []any{42},
		"userinfo":          []any{"https://user:pw@ops.example/ops-alarmd/"},
		"query":             []any{"https://ops.example/ops-alarmd/?x=1"},
		"the session's own": []any{p.PublicBaseURL},
	} {
		if _, err := validateExchange(answer(fallback), p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// --entry alarmd sends a call through the entry the server declared, marked
// as such, for an operator re-running an executing call deliberately. It
// needs a declared entry and names no other.
func TestAnOperatorCanSendACallThroughTheDeclaredEntryDeliberately(t *testing.T) {
	o := newOrigin(t, 502, `{"error":{"code":"alarmd_unreachable","message":"x"}}`)
	via, err := onEntry(o.profile(), entryAlarmd)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := fallbackApp(t)
	m, _, err := a.requestVia(via, http.MethodPost, "api/cli/channel", map[string]any{}, 5*time.Second, true)
	client := objectField(objectField(m, "meta"), "client_entry")
	if err != nil || o.admin.Load() != 0 || o.own.Load() != 1 || <-o.entries != entryAlarmd ||
		stringField(client, "entry") != entryAlarmd || stringField(client, "reason") != "requested" {
		t.Fatalf("deliberate call: admin %d own %d meta %v err %v", o.admin.Load(), o.own.Load(), client, err)
	}
	if _, err := onEntry(fixtureProfile(o.URL), entryAlarmd); err == nil {
		t.Fatal("--entry alarmd accepted on a session that declares no entry")
	}
	if _, err := parse([]string{"invoke", "x", "--entry", "https://elsewhere.example/"}); err == nil {
		t.Fatal("--entry accepted a URL")
	}
}

// A session renewed in-band never sees a login answer; every channel answer
// declares its fallback entry, and the CLI keeps it under the same checks as
// a login's. A malformed declaration is ignored, an absent one leaves the
// profile as it was.
func TestAChannelAnswerTeachesTheSessionItsFallbackEntry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared []string // paths on the session's origin, or a full URL
		want     []string
	}{
		{"declared", []string{"/own/alarmd"}, []string{"/own/alarmd/"}},
		{"the session's own entry", []string{"/admin/alarmd/"}, nil},
		{"another origin", []string{"https://elsewhere.example/own/alarmd/"}, nil},
		{"absent", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				answer := envelope(fixtureProfile("http://unused"), "ok", map[string]any{})
				if tc.declared != nil {
					declared := make([]any, 0, len(tc.declared))
					for _, entry := range tc.declared {
						if strings.HasPrefix(entry, "/") {
							entry = base + entry
						}
						declared = append(declared, entry)
					}
					answer["meta"].(map[string]any)["session"].(map[string]any)["fallback_base_urls"] = declared
				}
				_ = json.NewEncoder(w).Encode(answer)
			}))
			defer server.Close()
			base = server.URL
			p := fixtureProfile(server.URL)
			p.PublicBaseURL = server.URL + "/admin/alarmd/"
			a, _ := fallbackApp(t)
			if err := a.Store.save(p, false); err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.channelRequestTimeout("discover", "", nil, "", p, 5*time.Second, false); err != nil {
				t.Fatal(err)
			}
			stored, err := a.Store.get(p.EnvironmentID)
			var want []string
			for _, path := range tc.want {
				want = append(want, server.URL+path)
			}
			if err != nil || !reflect.DeepEqual(stored.FallbackBaseURLs, want) {
				t.Fatalf("stored %v (%v), want %v", stored.FallbackBaseURLs, err, want)
			}
		})
	}
}
