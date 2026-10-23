// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(url string) []Egress {
	return []Egress{{Name: "primary", URL: url, Tenants: []string{"tenant-fixture"}, Spaces: []string{"scope-fixture"}, Endpoints: []Endpoint{{Path: "/query/ts/", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}
}
func request() Request {
	return Request{Egress: "primary", Endpoint: "/query/ts/", Tenant: "tenant-fixture", Space: "scope-fixture", Body: map[string]any{"start_time": "1700000000", "end_time": "1700000060", "query_list": []any{map[string]any{"table_id": "fixture.metric", "field_name": "value"}}}}
}
func code(err error) string {
	var named *Error
	if errors.As(err, &named) {
		return named.Code
	}
	return ""
}

func TestNativeWireAndReceipt(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/query/ts/" || r.Method != http.MethodPost || r.Header.Get("X-Bk-Tenant-Id") != "tenant-fixture" || r.Header.Get("X-Bk-Scope-Space-Uid") != "scope-fixture" || r.Header.Get("Bk-Query-Source") != "alarmd-sre" || r.Header.Get("X-Bkapi-Jwt") != "" {
			t.Errorf("unexpected native wire %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["start_time"] != "1700000000" {
			t.Errorf("body changed: %v", body)
		}
		_, _ = io.WriteString(w, `{"series":[],"trace_id":"synthetic-trace","next_cursor":"next-page"}`)
	}))
	defer s.Close()
	p, err := NewWithQuerySource(fixture(s.URL+"/prefix"), nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	receipt, err := p.Query(context.Background(), request())
	if err != nil || !receipt.Complete || receipt.TraceID != "synthetic-trace" || receipt.ActualURL != s.URL+"/prefix/query/ts/" || receipt.RequestDigest == "" || receipt.FinishedAt.Before(receipt.StartedAt) || !strings.Contains(string(receipt.Native), "next-page") {
		t.Fatalf("receipt=%+v error=%v", receipt, err)
	}
}

func TestNativeLogProjectionPrecedesReceiptOnSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		bklog            bool
		logAlias         bool
		wantError        string
	}{
		{"raw rows", "/query/ts/raw/", `{"list":[{"message":"unmasked-personal-content"}],"total":1,"result_table_options":{"cursor":"private-cursor"}}`, false, false, ""},
		{"nested rows on metric endpoint", "/query/ts/", `{"data":{"list":[{"message":"unmasked-personal-content"}],"total":1},"message":"unmasked-personal-content"}`, false, false, ""},
		{"log aggregation", "/query/ts/", `{"series":[{"tags":{"identity":"unmasked-personal-content"},"values":[[1,2]]}]}`, true, false, ""},
		{"business failure", "/query/ts/", `{"result":false,"error":"unmasked-personal-content","message":"unmasked-personal-content"}`, true, false, "uq_query_error"},
		{"SaaS log alias series", "/query/ts/", `{"series":[{"tags":{"identity":"unmasked-personal-content"},"values":[[1,2]]}]}`, true, true, ""},
		{"SaaS log alias error", "/query/ts/", `{"result":false,"error":"unmasked-personal-content"}`, true, true, "uq_query_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			cfg := fixture(server.URL)
			cfg[0].Endpoints[0].Path = tc.path
			provider, err := NewWithQuerySource(cfg, nil, "alarmd-sre")
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			r := request()
			r.Endpoint = tc.path
			if tc.bklog {
				r.Body["query_list"].([]any)[0].(map[string]any)["DATA_SOURCE"] = "bklog"
				if tc.logAlias {
					r.Body["query_list"].([]any)[0].(map[string]any)["DATA_SOURCE"] = "bk_log_search"
				}
			}
			receipt, err := provider.Query(context.Background(), r)
			if code(err) != tc.wantError || receipt.HTTPStatus != 200 || receipt.Complete || receipt.ContentOmitted != "log_masking_boundary" || receipt.ResponseDigest == "" || receipt.RequestDigest == "" {
				t.Fatalf("log projection lost status or limitation: %+v err=%v", receipt, err)
			}
			encoded, _ := json.Marshal(receipt)
			for _, content := range []string{"unmasked-personal-content", "private-cursor"} {
				if strings.Contains(string(encoded), content) {
					t.Fatalf("unmasked log leaked: %s", encoded)
				}
			}
			if tc.name == "raw rows" || tc.name == "nested rows on metric endpoint" {
				if !strings.Contains(string(receipt.Native), `"list_count":1`) || !strings.Contains(string(receipt.Native), `"total":1`) {
					t.Fatalf("counts lost: %s", receipt.Native)
				}
			}
		})
	}
}

func TestRejectedRequestsNeverReachEgress(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	p, err := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cases := []struct {
		name, code string
		change     func(*Request)
	}{
		{"egress", "uq_egress_unknown", func(r *Request) { r.Egress = "unknown" }},
		{"url", "uq_endpoint_denied", func(r *Request) { r.Endpoint = "http://untrusted.invalid/query/ts/" }},
		{"tenant", "uq_scope_denied", func(r *Request) { r.Tenant = "other-tenant" }},
		{"space", "uq_scope_denied", func(r *Request) { r.Space = "" }},
		{"body scope", "uq_scope_mismatch", func(r *Request) { r.Body["space_uid"] = "other-space" }},
		{"skip", "uq_scope_denied", func(r *Request) { r.Body["skip_space"] = true }},
		{"cache mutation", "uq_scope_denied", func(r *Request) { r.Body["clear_cache"] = true }},
		{"cache alias", "uq_scope_denied", func(r *Request) { r.Body["CLEAR_CACHE"] = true }},
		{"space alias", "uq_scope_mismatch", func(r *Request) { r.Body["Space_UID"] = "other-space" }},
		{"physical storage override", "uq_scope_denied", func(r *Request) {
			r.Body["tsdb_map"] = map[string]any{"a": []any{map[string]any{"db": "outside-scope"}}}
		}},
		{"physical override alias", "uq_scope_denied", func(r *Request) { r.Body["TSDB_MAP"] = map[string]any{} }},
		{"direct SQL", "uq_scope_denied", func(r *Request) {
			r.Body["query_list"].([]any)[0].(map[string]any)["SQL"] = "select * from outside_scope"
		}},
		{"bkdata route", "uq_scope_denied", func(r *Request) { r.Body["query_list"].([]any)[0].(map[string]any)["data_source"] = "bkdata" }},
		{"bkdata route alias", "uq_scope_denied", func(r *Request) { r.Body["query_list"].([]any)[0].(map[string]any)["DATA_SOURCE"] = "bk_data" }},
		{"unsupported typed contract", "uq_contract_unsupported", func(r *Request) { delete(r.Body, "query_list"); r.Body["trace_query_v2"] = map[string]any{} }},
		{"query aliases", "invalid_input", func(r *Request) { r.Body["QUERY_LIST"] = r.Body["query_list"] }},
		{"window", "uq_window_exceeded", func(r *Request) { r.Body["end_time"] = "1700086401" }},
		{"reversed", "uq_window_exceeded", func(r *Request) { r.Body["start_time"] = "1700000061" }},
		{"timestamp unit", "invalid_input", func(r *Request) { r.Body["start_time"] = "1657851600000" }},
		{"numeric timestamp", "invalid_input", func(r *Request) { r.Body["start_time"] = json.Number("1700000000") }},
		{"deadline", "invalid_input", func(r *Request) { r.TimeoutMS = MaxTimeout.Milliseconds() + 1 }},
		{"body bytes", "uq_request_bytes_exceeded", func(r *Request) { r.Body["query_string"] = strings.Repeat("x", MaxBodyBytes) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request()
			tc.change(&r)
			receipt, err := p.Query(context.Background(), r)
			if code(err) != tc.code || receipt.Complete {
				t.Fatalf("error=%v complete=%v", err, receipt.Complete)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected queries reached native egress %d times", calls.Load())
	}
}

func TestNativeScrollAndSearchAfterAreRetained(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["scroll"] != "1m" || body["result_table_options"] == nil {
			t.Errorf("native pagination changed: %+v", body)
		}
		_, _ = io.WriteString(w, `{"data":[],"status":[],"result_table_options":{"cursor":["next"]}}`)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	defer p.Close()
	r := request()
	r.Body["scroll"] = "1m"
	r.Body["result_table_options"] = map[string]any{"fixture.metric": map[string]any{"search_after": []any{"previous"}}}
	if _, err := p.Query(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestResponseFailuresKeepNativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"native error", `{"error":"query failed","trace_id":"synthetic-trace"}`, "uq_query_error", 200},
		{"false result", `{"result":false,"message":"bad query"}`, "uq_query_error", 200},
		{"denied", `{"error":"scope denied"}`, "uq_http_error", 403},
		{"not JSON", `<html>gateway failed</html>`, "uq_response_invalid", 502},
		{"response bound", `{"data":"` + strings.Repeat("x", MaxResponseBytes) + `"}`, "uq_response_bytes_exceeded", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer s.Close()
			p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
			defer p.Close()
			receipt, err := p.Query(context.Background(), request())
			if code(err) != tc.code || receipt.Complete || receipt.HTTPStatus != tc.status {
				t.Fatalf("receipt=%+v error=%v", receipt, err)
			}
			if tc.code == "uq_query_error" && string(receipt.Native) != tc.body {
				t.Error("lost native failure")
			}
		})
	}
}

func TestRedirectDoesNotForwardScope(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), s.Client(), "alarmd-sre")
	defer p.Close()
	receipt, err := p.Query(context.Background(), request())
	if err == nil || receipt.HTTPStatus != 307 || calls.Load() != 0 {
		t.Fatalf("redirect followed receipt=%+v error=%v", receipt, err)
	}
}

func TestIsolatedBudgetAndCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{}`)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Query(ctx, request()); done <- err }()
	<-started
	if _, err := p.Query(context.Background(), request()); code(err) != "uq_busy" {
		t.Fatalf("concurrent query error=%v", err)
	}
	cancel()
	if err := <-done; code(err) != "uq_canceled" {
		t.Fatalf("canceled query error=%v", err)
	}
	close(release)
}

func TestMillisecondsAndTimeout(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer s.Close()
	defer close(release)
	e := fixture(s.URL)
	e[0].Endpoints[0].TimeUnit = "ms"
	p, _ := NewWithQuerySource(e, nil, "alarmd-sre")
	defer p.Close()
	r := request()
	r.Body["start_time"] = "1700000000000"
	r.Body["end_time"] = "1700000060000"
	r.TimeoutMS = 10
	start := time.Now()
	receipt, err := p.Query(context.Background(), r)
	if code(err) != "uq_timeout" || receipt.Complete || time.Since(start) > time.Second {
		t.Fatalf("receipt=%+v error=%v", receipt, err)
	}
}

func TestMillisecondsCannotHideAnActualSecondsLongWindow(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	e := fixture(s.URL)
	e[0].Endpoints[0].TimeUnit = "ms"
	p, _ := NewWithQuerySource(e, nil, "alarmd-sre")
	defer p.Close()
	r := request()
	r.Body["end_time"] = "1702592000" // 30 days under UQ's real 10-digit seconds rule.
	if _, err := p.Query(context.Background(), r); code(err) != "invalid_input" || calls.Load() != 0 {
		t.Fatalf("mismatched wire unit reached UQ: %v calls=%d", err, calls.Load())
	}
	r.Body["start_time"] = "1700000000000"
	r.Body["end_time"] = "1702592000000"
	if _, err := p.Query(context.Background(), r); code(err) != "uq_window_exceeded" || calls.Load() != 0 {
		t.Fatalf("30 day milliseconds window reached UQ: %v calls=%d", err, calls.Load())
	}
}

func TestRegisteredConfigurationValidation(t *testing.T) {
	for _, raw := range []string{"http://user:credential@query.invalid", "http://query.invalid/?token=secret", "file:///tmp/query", "http://query.invalid/#fragment"} {
		if err := Validate(fixture(raw)); err == nil {
			t.Errorf("accepted unsafe URL %s", raw)
		}
	}
	e := fixture("http://query.invalid")
	e[0].Endpoints[0].Path = "/query/../admin"
	if Validate(e) == nil {
		t.Fatal("accepted ambiguous endpoint")
	}
}
