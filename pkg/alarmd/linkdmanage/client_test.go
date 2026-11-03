// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package linkdmanage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

const testSecret = "example-secret"

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL, Secret: testSecret, Username: "alarmd", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// A token says it was issued a second before the client's clock and lives
// the link's lifetime from then: the link checks iat with no leeway, so a
// token stamped this second fails on a link whose clock is a fraction
// behind.
func TestATokenIsIssuedASecondEarly(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	client, err := New(Options{BaseURL: "http://192.0.2.10:8090", Secret: testSecret, Username: "alarmd",
		Client: &http.Client{}, Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(client.token(), "Bearer "), ".")
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var fields struct {
		Username string `json:"username"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(claims, &fields); err != nil || fields.IssuedAt != at.Unix()-1 || fields.Username != "alarmd" ||
		fields.Expires != fields.IssuedAt+int64(tokenLifetime/time.Second) {
		t.Fatalf("claims %+v (%v), want iat one second before the clock", fields, err)
	}
}

// A filtered close carries the command's own bytes unchanged after the
// filter and dry_run, and no page or cursor (the link refuses unknown
// fields), so a retry by id sends exactly what the filtered close did; a
// dry run carries the tenant and no command. The answer's has_more, counts
// and top-level error code are read.
func TestAFilteredCloseCarriesTheCommandBytesUnchanged(t *testing.T) {
	var bodies [][]byte
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if r.URL.Path != RouteCloseFilter || !strings.HasPrefix(r.Header.Get("Internal-Token"), "Bearer ") {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"alert_id":"a","status":200,"already_closed":true},{"alert_id":"b","status":504,"error_code":"not_attempted","message":"m"}],"has_more":true,"dry_run":false,"closed":1,"failed":1,"error_code":"query_failed","message":"candidate search failed"}`))
	})
	command := []byte(`{"bk_tenant_id":"tenant-a","operation_id":"op","operator_id":"alarmd","operator_kind":"system","operation_source":"strategy_change","reason":"r","effective_at":"2026-10-27T00:00:00Z"}`)
	filter := json.RawMessage(`{"bool":{"filter":[{"term":{"labels.strategy_id":"1"}}]}}`)
	page, err := client.CloseByFilter(context.Background(), command, "tenant-a", filter, false)
	if err != nil || len(page.Items) != 2 || !page.Items[0].AlreadyClosed || !page.HasMore || page.Closed != 1 || page.Failed != 1 ||
		page.ErrorCode != QueryFailed || page.Items[1].ErrorCode != "not_attempted" || page.Items[1].Message != "m" {
		t.Fatalf("page %+v, %v", page, err)
	}
	if !bytes.HasSuffix(bodies[0], command[1:]) || !bytes.HasPrefix(bodies[0], []byte(`{"filter":`)) {
		t.Fatalf("body %s does not end with the command's bytes", bodies[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal(bodies[0], &decoded); err != nil || decoded["dry_run"] != false {
		t.Fatalf("body %s: %v", bodies[0], err)
	}
	for _, field := range []string{"limit", "after"} {
		if _, sent := decoded[field]; sent {
			t.Fatalf("body %s carries %s, which the link refuses", bodies[0], field)
		}
	}
	if _, err := client.CloseByFilter(context.Background(), nil, "tenant-a", filter, true); err != nil {
		t.Fatal(err)
	}
	var dry map[string]any
	if err := json.Unmarshal(bodies[1], &dry); err != nil || dry["bk_tenant_id"] != "tenant-a" || dry["operation_id"] != nil ||
		dry["limit"] != nil || dry["after"] != nil {
		t.Fatalf("dry run body %s: %v", bodies[1], err)
	}
}

// A single close's 409 is told by the closer's message, the only thing the
// management route answers it with; the search and the filtered close are
// unsupported on 501, 404 and 405, a single close's 404 is the alert's.
func TestTheLinksAnswersAreToldApart(t *testing.T) {
	answers := map[string]struct {
		status  int
		message string
	}{
		"/api/v1/alerts/t/close": {409, "alert state changed; refresh before another operation"},
		"/api/v1/alerts/v/close": {409, "alert version changed; retry the same operation"},
		"/api/v1/alerts/s/close": {409, "event source has no published configuration"},
		"/api/v1/alerts/u/close": {409, "something else"},
		"/api/v1/alerts/n/close": {404, "alert or source not found"},
		RouteSearch:              {501, "alert search requires Elasticsearch storage"},
		RouteCloseFilter:         {405, "method not allowed"},
	}
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		answer := answers[r.URL.Path]
		http.Error(w, answer.message, answer.status)
	})
	ctx := context.Background()
	for id, want := range map[string]string{"t": ConflictInvalidTransition, "v": ConflictVersion, "s": ConflictSourceUnpublished, "u": ConflictUnknown, "n": ""} {
		_, err := client.CloseOne(ctx, id, []byte(`{}`))
		if got := ConflictKind(err); got != want || Unsupported(err) {
			t.Fatalf("%s: %q (unsupported %v), want %q", id, got, Unsupported(err), want)
		}
	}
	if _, err := client.Search(ctx, "tenant-a", json.RawMessage(`{}`), "", 1); !Unsupported(err) || Status(err) != 501 {
		t.Fatalf("search: %v", err)
	}
	if _, err := client.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`{}`), true); !Unsupported(err) {
		t.Fatalf("filtered close: %v", err)
	}
}

// A label is read as the link indexes it: a number and its text are the
// same term.
func TestALabelIsReadAsTheLinkIndexesIt(t *testing.T) {
	alert := Alert{Labels: map[string]json.RawMessage{"a": json.RawMessage(`7`), "b": json.RawMessage(`"7"`),
		"c": json.RawMessage(`1700000000000000`), "d": json.RawMessage(`{"x":1}`), "e": json.RawMessage(`1.5`)}}
	for name, want := range map[string]string{"a": "7", "b": "7", "c": "1700000000000000", "d": "", "e": "", "f": ""} {
		if got, ok := alert.Label(name); got != want || ok != (want != "") {
			t.Fatalf("%s: %q %v, want %q", name, got, ok, want)
		}
	}
}

// A redirect is not followed: Go would carry the Internal-Token header to
// wherever it points. The 3xx is the answer.
func TestARedirectIsNotFollowedWithTheToken(t *testing.T) {
	var leaked []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get("Internal-Token"))
	}))
	t.Cleanup(elsewhere.Close)
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})
	_, err := client.Search(context.Background(), "tenant-a", json.RawMessage(`{"match_all":{}}`), "", 1)
	if Status(err) != http.StatusTemporaryRedirect || len(leaked) != 0 {
		t.Fatalf("err %v, the redirect's target saw %d requests", err, len(leaked))
	}
}

// A request the link answers 400 is not sent: no query or filter object,
// a limit outside 1..500.
func TestARequestTheLinkWouldRefuseIsNotSent(t *testing.T) {
	requests := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { requests++ })
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"nil query":  func() error { _, err := client.Search(ctx, "tenant-a", nil, "", 1); return err },
		"null query": func() error { _, err := client.Search(ctx, "tenant-a", json.RawMessage(`null`), "", 1); return err },
		"limit 0":    func() error { _, err := client.Search(ctx, "tenant-a", json.RawMessage(`{}`), "", 0); return err },
		"limit 501":  func() error { _, err := client.Search(ctx, "tenant-a", json.RawMessage(`{}`), "", 501); return err },
		"nil filter": func() error { _, err := client.CloseByFilter(ctx, nil, "tenant-a", nil, true); return err },
		"array": func() error {
			_, err := client.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`[]`), true)
			return err
		},
	} {
		if err := call(); err != ErrInvalidRequest {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if requests != 0 {
		t.Fatalf("%d requests sent", requests)
	}
}

// Without a secret a request carries no Internal-Token header at all, not
// one with an empty value;
// with one it carries the token. A secret of only blanks is refused.
func TestNoSecretSendsNoTokenHeader(t *testing.T) {
	for secret, want := range map[string]bool{"": false, testSecret: true} {
		var present bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, present = r.Header[http.CanonicalHeaderKey("Internal-Token")]
			_, _ = w.Write([]byte(`{"items":[],"next":"","dry_run":true,"closed":0,"failed":0}`))
		}))
		client, err := New(Options{BaseURL: server.URL, Secret: secret, Username: "alarmd", Client: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		filter := json.RawMessage(`{"bool":{"filter":[{"term":{"labels.strategy_id":"1"}}]}}`)
		if _, err := client.CloseByFilter(context.Background(), nil, "tenant-a", filter, true); err != nil {
			t.Fatalf("secret %q: %v", secret, err)
		}
		server.Close()
		if present != want {
			t.Fatalf("secret given %v: the header was sent %v", secret != "", present)
		}
	}
	if _, err := New(Options{BaseURL: "http://192.0.2.10:8090", Secret: "  ", Username: "alarmd", Client: &http.Client{}}); err == nil {
		t.Fatal("a blank secret was accepted")
	}
}

// A 200 whose body cannot be read is ErrInvalidResponse, and
// ErrAnswerTooLarge as well only when it ran past the client's bound; a close command
// that is not a JSON object is ErrCommandNotObject and not sent, and a link
// that does not answer is neither: each is told apart from the others.
func TestAnAnswerThatCannotBeReadIsToldApartFromNoAnswer(t *testing.T) {
	requests := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"items":[`))
	})
	ctx := context.Background()
	if _, err := client.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`{}`), true); !errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrAnswerTooLarge) || Status(err) != 0 {
		t.Fatalf("a 200 that does not decode: %v", err)
	}
	if _, err := client.Search(ctx, "tenant-a", json.RawMessage(`{}`), "", 1); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("a search answer that does not decode: %v", err)
	}
	sent := requests
	if _, err := client.CloseByFilter(ctx, []byte(`[1]`), "tenant-a", json.RawMessage(`{}`), false); !errors.Is(err, ErrCommandNotObject) || requests != sent {
		t.Fatalf("a command that is not an object: %v, %d requests sent", err, requests-sent)
	}
	// A 200 that breaks off: it promises more than it writes, then the
	// connection is closed.
	cut := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	})
	if _, err := cut.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`{}`), true); !errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrAnswerTooLarge) {
		t.Fatalf("a 200 that breaks off: %v", err)
	}
	// A 200 past the client's bound.
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"next":"` + strings.Repeat("a", 200) + `"}`))
	}))
	t.Cleanup(large.Close)
	bounded, err := New(Options{BaseURL: large.URL, Secret: testSecret, Username: "alarmd", Client: large.Client(), MaxBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`{}`), true); !errors.Is(err, ErrInvalidResponse) || !errors.Is(err, ErrAnswerTooLarge) {
		t.Fatalf("a 200 past the bound: %v", err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	unreachable, err := New(Options{BaseURL: down.URL, Secret: testSecret, Username: "alarmd", Client: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unreachable.CloseByFilter(ctx, nil, "tenant-a", json.RawMessage(`{}`), true); err == nil || errors.Is(err, ErrInvalidResponse) || Status(err) != 0 {
		t.Fatalf("a link that does not answer: %v", err)
	}
}

// A 200 whose body stops coming past the client's timeout is a timeout,
// still: the error keeps the deadline in its chain beside
// ErrInvalidResponse, so a caller that shrinks its page on a timeout does.
func TestA200WhoseBodyStallsPastTheTimeoutIsATimeout(t *testing.T) {
	expired := testwait.Expired(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[`))
		w.(http.Flusher).Flush()
		// Held until the client gives up; the test's deadline only keeps a
		// client that never does from hanging it.
		select {
		case <-r.Context().Done():
		case <-expired:
		}
	}))
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL, Secret: testSecret, Username: "alarmd", Client: &http.Client{Timeout: 100 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CloseByFilter(context.Background(), nil, "tenant-a", json.RawMessage(`{}`), true)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("a 200 whose body stalled: %v, want a deadline and an unreadable answer", err)
	}
}

// A request past the link's body bound is not sent: ErrBodyTooLarge from a
// search, a filtered close and a single close.
func TestARequestPastTheBodyBoundIsNotSent(t *testing.T) {
	requests := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { requests++ })
	ctx := context.Background()
	large := json.RawMessage(`{"term":{"labels.x":"` + strings.Repeat("a", MaxFilterBodyBytes) + `"}}`)
	for name, call := range map[string]func() error{
		"search":         func() error { _, err := client.Search(ctx, "tenant-a", large, "", 1); return err },
		"filtered close": func() error { _, err := client.CloseByFilter(ctx, nil, "tenant-a", large, true); return err },
		"single close": func() error {
			_, err := client.CloseOne(ctx, "alert-1", []byte(`{"reason":"`+strings.Repeat("a", MaxCloseBodyBytes)+`"}`))
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrBodyTooLarge) {
			t.Fatalf("%s: %v, want ErrBodyTooLarge", name, err)
		}
	}
	if requests != 0 {
		t.Fatalf("%d requests sent past the bound", requests)
	}
}
