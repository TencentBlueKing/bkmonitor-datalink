package obchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
)

type testAuth struct {
	calls    atomic.Int32
	err      error
	admitErr error
}

func (a *testAuth) Authenticate(context.Context, string) (cliauth.Session, error) {
	return cliauth.Session{ID: "test-session", EnvironmentID: "test", Scope: cliauth.ScopeReadonly, ExpiresAt: time.Now().Add(time.Hour)}, a.err
}
func (a *testAuth) Admit(_ context.Context, s cliauth.Session, renew bool) (cliauth.Session, error) {
	a.calls.Add(1)
	s.Renewed = renew
	return s, a.admitErr
}
func testChannel(t *testing.T, a *testAuth, ops ...Operation) *Channel {
	t.Helper()
	c, err := New(Options{Auth: a, EnvironmentID: "test", Replica: "replica-1", Build: "test", Operations: ops})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func call(t *testing.T, c *Channel, body any) (int, Response) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/cli/channel", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer test-only")
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("response may be cached")
	}
	var out Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	return w.Code, out
}
func envelope(c *Channel, mode, op string, params Params) map[string]any {
	return map[string]any{"channel_version": Version, "mode": mode, "operation": op, "params": params, "expected_catalog_revision": c.revision, "renew_if_due": true}
}

func TestAdmissionOnlyRenewsValidInvocation(t *testing.T) {
	a := &testAuth{}
	var runs atomic.Int32
	op := Operation{ID: "read", Summary: "Read", Fields: map[string]Field{"id": {Type: "string", MinLength: 1}}, Required: []string{"id"}, Run: func(context.Context, Params) Outcome {
		runs.Add(1)
		return Outcome{Complete: true, Value: map[string]string{"value": "observed"}}
	}}
	c := testChannel(t, a, op)
	for _, tc := range []struct {
		mode     string
		params   Params
		revision string
		status   int
	}{{"discover", nil, "", 200}, {"describe", nil, "", 200}, {"invoke", Params{"unknown": "x"}, c.revision, 400}, {"invoke", Params{"id": ""}, c.revision, 400}, {"invoke", Params{"id": "x"}, "old", 409}} {
		input := envelope(c, tc.mode, "read", tc.params)
		input["expected_catalog_revision"] = tc.revision
		status, out := call(t, c, input)
		if status != tc.status {
			t.Fatalf("%s: %d %+v", tc.mode, status, out)
		}
	}
	if a.calls.Load() != 0 || runs.Load() != 0 {
		t.Fatal("non-invocations counted as activity")
	}
	status, out := call(t, c, envelope(c, "invoke", "read", Params{"id": "x"}))
	if status != 200 || out.Status != "ok" || !out.Meta.Session.Renewed || a.calls.Load() != 1 || runs.Load() != 1 {
		t.Fatalf("invoke: %+v", out)
	}
	a.admitErr = &cliauth.Error{Code: "auth_expired_or_revoked", HTTPStatus: 401}
	status, _ = call(t, c, envelope(c, "invoke", "read", Params{"id": "x"}))
	if status != 401 || runs.Load() != 1 {
		t.Fatal("revoked session executed")
	}
}

// sessionAuth authenticates the bearer token as the session ID, so a test
// can hold two sessions against one channel.
type sessionAuth struct{ testAuth }

func (a *sessionAuth) Authenticate(_ context.Context, token string) (cliauth.Session, error) {
	return cliauth.Session{ID: token, EnvironmentID: "test", Scope: cliauth.ScopeReadonly, ExpiresAt: time.Now().Add(time.Hour)}, a.err
}

func callAs(t *testing.T, c *Channel, token string, body any) (int, http.Header, Response) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/cli/channel", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	var out Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	return w.Code, w.Header(), out
}

// One session spends its own minute's budget of executed invocations and
// no one else's: the thirty-first in a minute is refused with the seconds
// to the turn of the minute, without executing, without renewing, and
// without touching the other session; the next minute starts it over.
// Refused inputs and non-invocations cost nothing.
func TestASessionSpendsOnlyItsOwnInvocationBudget(t *testing.T) {
	a := &sessionAuth{}
	now := time.Date(2026, 1, 1, 0, 0, 20, 0, time.UTC)
	var runs atomic.Int32
	op := Operation{ID: "read", Summary: "Read", Fields: map[string]Field{"id": {Type: "string", MinLength: 1}}, Required: []string{"id"},
		Run: func(context.Context, Params) Outcome { runs.Add(1); return Outcome{Complete: true} }}
	c, err := New(Options{Auth: a, EnvironmentID: "test", Replica: "replica-1", Build: "test", Operations: []Operation{op},
		Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	// Discovery advertises the budget; it and the refused inputs spend none.
	if _, _, out := callAs(t, c, "s1", envelope(c, "discover", "", nil)); out.Result.(map[string]any)["budget"].(map[string]any)["invokes_per_session_per_minute"] != float64(InvokesPerSessionPerMinute) {
		t.Fatalf("discover does not advertise the budget: %+v", out.Result)
	}
	for i := 0; i < 5; i++ {
		if status, _, _ := callAs(t, c, "s1", envelope(c, "invoke", "read", Params{"id": ""})); status != 400 {
			t.Fatalf("refused input status %d", status)
		}
	}
	for i := 0; i < InvokesPerSessionPerMinute; i++ {
		if status, _, out := callAs(t, c, "s1", envelope(c, "invoke", "read", Params{"id": "x"})); status != 200 {
			t.Fatalf("invocation %d of the budget refused: %d %+v", i+1, status, out)
		}
	}
	admitted := a.calls.Load()
	status, header, out := callAs(t, c, "s1", envelope(c, "invoke", "read", Params{"id": "x"}))
	if status != 429 || out.Error == nil || out.Error.Code != "rate_limited" || header.Get("Retry-After") != "40" {
		t.Fatalf("over budget: %d %s %+v", status, header.Get("Retry-After"), out)
	}
	if runs.Load() != int32(InvokesPerSessionPerMinute) || a.calls.Load() != admitted || out.Meta.Session == nil || out.Meta.Session.Renewed {
		t.Fatalf("a refused invocation executed or was admitted: runs %d, admitted %d -> %d", runs.Load(), admitted, a.calls.Load())
	}
	// The other session is untouched.
	if status, _, out := callAs(t, c, "s2", envelope(c, "invoke", "read", Params{"id": "x"})); status != 200 {
		t.Fatalf("another session refused on the first's budget: %d %+v", status, out)
	}
	// The minute turns and the first session starts over.
	now = now.Add(40 * time.Second)
	if status, _, out := callAs(t, c, "s1", envelope(c, "invoke", "read", Params{"id": "x"})); status != 200 {
		t.Fatalf("budget did not start over with the minute: %d %+v", status, out)
	}
}

// The gate forgets sessions whose minute has passed once it holds more than
// it needs to, and never forgets one still in its minute.
func TestTheInvocationGateForgetsPastMinutes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &Channel{options: Options{Now: func() time.Time { return now }}, windows: make(map[string]*sessionWindow)}
	for i := 0; i < sessionWindowSweep; i++ {
		c.allowInvoke(strconv.Itoa(i))
	}
	now = now.Add(time.Minute)
	c.allowInvoke("fresh")
	if len(c.windows) != 1 || c.windows["fresh"] == nil {
		t.Fatalf("%d sessions remembered after their minute passed, want the fresh one alone", len(c.windows))
	}
	for i := 0; i < sessionWindowSweep-1; i++ {
		c.allowInvoke(strconv.Itoa(i))
	}
	c.allowInvoke("also-fresh")
	if len(c.windows) != sessionWindowSweep+1 {
		t.Fatalf("%d sessions, want %d: a sweep in the same minute must forget none", len(c.windows), sessionWindowSweep+1)
	}
}

func TestBudgetsAndStoreOutage(t *testing.T) {
	a := &testAuth{}
	entered := make(chan struct{})
	release := make(chan struct{})
	op := Operation{ID: "wait", Summary: "Bounded read", Run: func(context.Context, Params) Outcome { close(entered); <-release; return Outcome{Complete: true} }}
	c := testChannel(t, a, op)
	done := make(chan struct{})
	go func() { defer close(done); call(t, c, envelope(c, "invoke", "wait", nil)) }()
	<-entered
	status, _ := call(t, c, envelope(c, "invoke", "wait", nil))
	if status != 429 || a.calls.Load() != 1 {
		t.Fatal("busy invocation admitted")
	}
	close(release)
	<-done
	a.err = &cliauth.Error{Code: "auth_store_unavailable", HTTPStatus: 503}
	status, out := call(t, c, envelope(c, "discover", "", nil))
	if status != 503 || out.Error.Code != "auth_store_unavailable" {
		t.Fatalf("outage became login failure: %+v", out)
	}
}

func TestMalformedAndOversizedEnvelopeNeverAdmitted(t *testing.T) {
	a := &testAuth{}
	c := testChannel(t, a)
	for _, raw := range []string{`{"channel_version":"alarmd-ob/v1","mode":"discover","extra":true}`, `{"channel_version":"alarmd-ob/v1","mode":"discover"} {}`, strings.Repeat(" ", MaxRequestBytes) + `{}`} {
		r := httptest.NewRequest("POST", "/api/cli/channel", strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer test-only")
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("got %d", w.Code)
		}
	}
	if a.calls.Load() != 0 {
		t.Fatal("invalid envelope counted as activity")
	}
}

func TestStableCatalogAndConditionalSchema(t *testing.T) {
	ops := StoreOperations(obevidence.New(obevidence.Options{}))
	c := testChannel(t, &testAuth{}, ops...)
	for i := 0; i < 20; i++ {
		other := testChannel(t, &testAuth{}, StoreOperations(nil)...)
		if c.revision != other.revision {
			t.Fatal("catalog depends on map iteration")
		}
	}
	for _, tc := range []struct {
		operation string
		params    Params
		valid     bool
	}{
		{"strategy.config", Params{"view": "source", "strategy_id": "1"}, true},
		{"strategy.config", Params{"view": "source", "strategy_id": "1", "object_digest": strings.Repeat("a", 64)}, false},
		{"strategy.config", Params{"view": "published", "strategy_id": "1", "query_group": "q", "object_digest": strings.Repeat("a", 64)}, true},
		{"strategy.config", Params{"view": "published", "strategy_id": "1"}, false},
		{"store.inspect", Params{"family": "dynamic_config", "fields": []any{"is_access_bk_data"}}, true},
		{"store.inspect", Params{"family": "dynamic_config", "fields": []any{"is_access_bk_data", "is_access_bk_data"}}, false},
		{"store.inspect", Params{"family": "source_strategy", "strategy_id": "18446744073709551616"}, false},
		{"store.inspect", Params{"family": "query_progress", "query_group": "q"}, true},
		{"store.inspect", Params{"family": "query_cooldown", "query_group": "q"}, true},
		{"store.inspect", Params{"family": "query_cooldown"}, false},
		{"store.inspect", Params{"family": "query_cooldown", "query_group": "q", "strategy_id": "1"}, false},
		{"store.inspect", Params{"family": "target_group", "group_id": "a b"}, false},
		{"store.inspect", Params{"family": "target_group", "group_id": "a", "strategy_id": "1"}, false},
	} {
		if err := validate(c.ops[tc.operation], tc.params); (err == nil) != tc.valid {
			t.Fatalf("%s %+v: %v", tc.operation, tc.params, err)
		}
	}
	_, desc := call(t, c, envelope(c, "describe", "store.inspect", nil))
	encoded, _ := json.Marshal(desc.Result)
	for _, word := range []string{"allOf", "additionalProperties", "query_progress", "query_cooldown", "uniqueItems", "max_commands"} {
		if !bytes.Contains(encoded, []byte(word)) {
			t.Fatalf("schema missing %s", word)
		}
	}
}

func TestNativeKeepsDomainFactsAndNoCredentialForwarding(t *testing.T) {
	var gotPath, gotQuery string
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if len(r.Header) != 0 {
			t.Error("channel credentials forwarded to native reader")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"query_group": "q", "complete": false, "gaps": []string{"worker_missing"}, "records_status": "unavailable", "future_field": 17, "state": "blocked"})
	})
	c := testChannel(t, &testAuth{}, NativeOperations(native)...)
	status, out := call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "q", "check": "target_ready", "group": "g"}))
	// Use the server's actual check vocabulary rather than inventing an enum.
	if status == 400 {
		check := c.ops["object.get"].Fields["check"].Enum[0]
		status, out = call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "q", "check": check, "group": "g"}))
	}
	if status != 200 || out.Status != "partial" || out.Evidence.Complete || gotPath != "/api/objects/q" || !strings.Contains(gotQuery, "group=g") {
		t.Fatalf("lost native context: %+v %s %s", out, gotPath, gotQuery)
	}
	result := out.Result.(map[string]any)
	if result["future_field"] != float64(17) || result["state"] != "blocked" {
		t.Fatal("native facts changed")
	}
}

func TestResponseCapAndMissingStoreAreNotSuccess(t *testing.T) {
	c := testChannel(t, &testAuth{}, Operation{ID: "large", Summary: "Large", Run: func(context.Context, Params) Outcome {
		return Outcome{Complete: true, Value: strings.Repeat("a", MaxResponseBytes)}
	}})
	status, out := call(t, c, envelope(c, "invoke", "large", nil))
	if status != 502 || out.Evidence.Complete || out.Result != nil {
		t.Fatalf("oversized evidence: %+v", out)
	}
	c = testChannel(t, &testAuth{}, StoreOperations(nil)...)
	_, out = call(t, c, envelope(c, "invoke", "store.inspect", Params{"family": "source_strategy", "strategy_id": "1"}))
	if out.Status != "partial" {
		t.Fatal("unconfigured source became a healthy result")
	}
	if status := authStatus(errors.New("private error")); status != 503 {
		t.Fatal(status)
	}
}

func TestNativeExplicitCompletenessAndFactTruncation(t *testing.T) {
	for _, body := range []string{
		`{"query_group":"q","view_complete":false,"health":"UNKNOWN","facts_total":0,"facts":[]}`,
		`{"query_group":"q","view_complete":true,"facts_total":2,"facts":[{}]}`,
	} {
		native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		out := invokeNative(context.Background(), native, "/api/objects/q", nil)
		if out.Complete || len(out.Limitations) == 0 {
			t.Fatalf("native incomplete evidence was promoted: %+v", out)
		}
	}
}

type closingBody struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (b closingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b closingBody) Close() error             { b.entered <- struct{}{}; <-b.release; return nil }

func TestRouteSlotsIncludeBodyCleanupAndMethodRejection(t *testing.T) {
	c := testChannel(t, &testAuth{})
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			r := httptest.NewRequest("GET", "/api/cli/channel", nil)
			r.Body = closingBody{entered: entered, release: release}
			w := httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code != 405 {
				t.Errorf("method rejected with %d", w.Code)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	status, _ := call(t, c, envelope(c, "discover", "", nil))
	if status != 429 {
		t.Errorf("cleanup released a slot prematurely: %d", status)
	}
	close(release)
	for i := 0; i < 4; i++ {
		<-done
	}
	status, _ = call(t, c, envelope(c, "discover", "", nil))
	if status != 200 {
		t.Fatalf("cleanup leaked a slot: %d", status)
	}
}

// A degraded object says, at the top of object.get, how much of it is
// degraded: the counts come from the fact's own coverage, through the same
// invoke path the CLI uses. A fact without coverage gets no count, because
// an absent count is not a zero.
func TestObjectGetSummarySaysHowMuchOfTheObjectTheFactCovers(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
		deny []string
	}{
		{"few of many", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","cause_reason":"HISTORY_GAPPED","coverage":{"levels":19643,"short":10,"guarded":6}}}`,
			[]string{"DEGRADED_RUN（HISTORY_GAPPED）", "10/19643", "其中 6 个"}, []string{"事实中的第 1 条"}},
		{"all of them, several facts", `{"query_group":"q","found":true,"facts_total":3,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","coverage":{"levels":250,"short":250,"guarded":250}}}`,
			[]string{"DEGRADED_RUN（COMPLETED_WITH_UNAVAILABLE）", "250/250", "3 条事实中的第 1 条"}, nil},
		{"mostly resumed", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","coverage":{"levels":22,"short":3,"guarded":0,"resumed":227}}}`,
			[]string{"3/22", "另有 227 个序列续跑、0 个未能加载"}, nil},
		{"nothing summarised", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"STATE_READ_TIMEOUT","coverage":{"levels":0,"constrained":249}}}`,
			[]string{"本轮未汇总任何 Level 窗口", "0 个序列续跑、249 个未能加载"}, []string{"/0"}},
		{"windows full", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"QUERY_TIMEOUT","coverage":{"levels":19643,"short":0,"guarded":0}}}`,
			[]string{"19643 个 Level 窗口全满，降级不来自历史窗口"}, []string{"0/19643"}},
		{"no coverage", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"STALLED","reason_code":"none"}}`,
			nil, []string{"窗口", "/0"}},
		{"no fact", `{"query_group":"q","found":true,"facts_total":0,"facts":[]}`, nil, []string{"窗口"}},
		{"filling window, every hole listed", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","cause_reason":"PLAN_REACTIVATED","standing":{"state":"RESULT_UNTRUSTED","action":"WATCH","watch":"WINDOW_FILLING"},"wake":{"known":true,"interval_seconds":60},"coverage":{"levels":2,"short":2,"guarded":2,"windows":[{"key":"s/a/1","series":"a","level":1,"valid":3,"required":5,"end":"2026-09-23T11:00:00Z","holes":[{"at":"2026-09-23T10:56:00Z","cause":"NOT_IN_MEMORY"},{"at":"2026-09-23T10:57:00Z","cause":"NOT_IN_MEMORY"}],"missing_total":2,"unusable_total":0}]}}}`,
			[]string{"2/2 个 Level 窗口未满", "窗口 s/a/1 的 2 个洞在 2026-09-23 11:02Z 全部滑出，这个窗口届时满", "其后一轮（2026-09-23 11:03Z）收敛", "另有 1 个未满窗口没有列出"}, []string{"可信"}},
		{"detecting, no time", `{"query_group":"q","found":true,"facts_total":1,"anomaly":{"kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","standing":{"state":"DETECTING","action":"NONE"},"wake":{"known":true,"interval_seconds":60},"coverage":{"levels":2,"short":1,"guarded":0,"windows":[{"key":"s/a/1","series":"a","level":1,"valid":4,"required":5,"end":"2026-09-23T11:00:00Z","holes":[{"at":"2026-09-23T10:56:00Z","cause":"NOT_IN_MEMORY"}],"missing_total":1,"unusable_total":0}]}}}`,
			[]string{"1/2"}, []string{"滑出窗口"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			c := testChannel(t, &testAuth{}, NativeOperations(native)...)
			status, out := call(t, c, envelope(c, "invoke", "object.get", Params{"query_group": "q"}))
			if status != 200 {
				t.Fatalf("status %d: %+v", status, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out.Summary, w) {
					t.Errorf("summary %q lacks %q", out.Summary, w)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(out.Summary, d) {
					t.Errorf("summary %q has %q", out.Summary, d)
				}
			}
		})
	}
}
