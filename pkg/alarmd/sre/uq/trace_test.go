// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// All identifiers below are synthetic fixtures, never deployment evidence.
func traceFixture(url string, shared bool) []Egress {
	cfg := fixture(url)
	cfg[0].Endpoints = append(cfg[0].Endpoints, Endpoint{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"})
	table := "101_bkapm.trace_fixture"
	if shared {
		table = "apm_global.shared_trace_fixture"
	}
	cfg[0].TraceTargets = []TraceTarget{{Name: "fixture", Tenant: "tenant-fixture", Space: "scope-fixture", TableID: table, BkBizID: 101, AppName: "app-fixture"}}
	return cfg
}

func traceRequest(shared bool) TraceRequest {
	target := traceFixture("http://native.invalid", shared)[0].TraceTargets[0]
	fields := []any{map[string]any{"field_name": "trace_id", "op": "eq", "value": []any{"synthetic-trace"}}}
	links := []any{}
	if shared {
		fields = append(fields, map[string]any{"field_name": "bk_biz_id", "op": "eq", "value": []any{"101"}}, map[string]any{"field_name": "app_name", "op": "eq", "value": []any{target.AppName}})
		links = []any{"and", "and"}
	}
	return TraceRequest{Egress: "primary", Target: "fixture", Body: map[string]any{
		"start_time": "1700000000", "end_time": "1700000060", "from": 0, "limit": 2,
		"bk_biz_id": target.BkBizID, "space_uid": target.Space, "order_by": []any{"start_time"},
		"query_list": []any{map[string]any{"data_source": "bkapm", "table_id": target.TableID, "time_field": "start_time", "query_string": "*", "conditions": map[string]any{"field_list": fields, "condition_list": links}}},
	}}
}

func traceQuery(r *TraceRequest) map[string]any {
	return r.Body["query_list"].([]any)[0].(map[string]any)
}
func traceCondition(r *TraceRequest, index int) map[string]any {
	return traceQuery(r)["conditions"].(map[string]any)["field_list"].([]any)[index].(map[string]any)
}

func TestTraceNativeMCPWirePreservesSpanUnitsAndQuerySource(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		shared       bool
	}{
		{"exclusive", "bkapm", false},
		{"shared", "bkapm", true},
		{"exclusive alias", "bk_apm", false},
		{"shared alias", "bk_apm", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := traceRequest(tc.shared)
			q := traceQuery(&r)
			q["data_source"] = tc.source
			// Legacy MCP/SpanQuery emits these defaults; a minimal application
			// native body omits them. Both are the same raw Span contract.
			q["driver"], q["reference_name"], q["field_name"] = "influxdb", "a", ""
			q["dimensions"], q["function"], q["order_by"] = []any{}, []any{}, []any{}
			q["time_aggregation"] = map[string]any{}
			q["keep_columns"] = []any{"trace_id", "span_id", "start_time", "end_time", "elapsed_time", "attributes"}
			r.Body["metric_merge"], r.Body["step"], r.Body["timezone"] = "a", "60s", "UTC"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.Path != "/prefix/query/ts/raw" || req.Header.Get("X-Bk-Tenant-Id") != "tenant-fixture" || req.Header.Get("X-Bk-Scope-Space-Uid") != "scope-fixture" || req.Header.Get("Bk-Query-Source") != "fixture-runtime-source" || req.Header.Get("Authorization") != "" || req.Header.Get("X-Bkapi-Jwt") != "" {
					t.Errorf("unexpected native Trace wire: %s %s headers=%v", req.Method, req.URL.Path, req.Header)
				}
				var got, want any
				_ = json.NewDecoder(req.Body).Decode(&got)
				encoded, _ := json.Marshal(r.Body)
				_ = json.Unmarshal(encoded, &want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("native body was rewritten: got=%v want=%v", got, want)
				}
				_, _ = io.WriteString(w, `{"list":[{"trace_id":"synthetic-trace","span_id":"span-fixture","start_time":1700000000123456,"end_time":1700000000123498,"elapsed_time":42,"attributes":{"large_uint":9223372036854775808}}],"total":1,"status":[]}`)
			}))
			defer s.Close()
			p, err := NewWithQuerySource(traceFixture(s.URL+"/prefix", tc.shared), nil, "fixture-runtime-source")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			got, err := p.Trace(context.Background(), r)
			if err != nil || !got.Complete || got.ContentOmitted != "" || got.TracePage == nil || got.TracePage.RequestedID != "synthetic-trace" || got.TracePage.SpanTimeUnit != "us" || got.TracePage.WindowUnit != "s" || got.TracePage.Returned != 1 || got.TracePage.Total != 1 || got.RequestDigest == "" || got.FinishedAt.Before(got.StartedAt) {
				t.Fatalf("receipt=%+v err=%v", got, err)
			}
			for _, value := range []string{"1700000000123456", "1700000000123498", "\"elapsed_time\":42", "9223372036854775808"} {
				if !strings.Contains(string(got.Native), value) {
					t.Fatalf("native Span unit/value lost: %s", got.Native)
				}
			}
			if tc.shared && len(p.Egresses()[0].TraceTargets) != 1 {
				t.Fatal("target missing from discovery")
			}
		})
	}
}

func TestTraceRejectedBodiesDoNoIO(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	p, err := NewWithQuerySource(traceFixture(s.URL, true), nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, tc := range []struct {
		name, code string
		mutate     func(*TraceRequest)
	}{
		{"target", "uq_trace_target_unknown", func(r *TraceRequest) { r.Target = "other-app" }},
		{"body tenant", "uq_scope_mismatch", func(r *TraceRequest) { r.Body["tenant"] = "other-tenant" }},
		{"body space", "uq_scope_mismatch", func(r *TraceRequest) { r.Body["space_uid"] = "other-space" }},
		{"body business", "uq_scope_mismatch", func(r *TraceRequest) { r.Body["bk_biz_id"] = 102 }},
		{"table", "uq_trace_scope_denied", func(r *TraceRequest) { traceQuery(r)["table_id"] = "other.table" }},
		{"datasource", "uq_trace_scope_denied", func(r *TraceRequest) { traceQuery(r)["data_source"] = "bklog" }},
		{"time field", "uq_trace_scope_denied", func(r *TraceRequest) { traceQuery(r)["time_field"] = "end_time" }},
		{"wrong app", "uq_trace_scope_denied", func(r *TraceRequest) { traceCondition(r, 2)["value"] = []any{"other-app"} }},
		{"wrong business", "uq_trace_scope_denied", func(r *TraceRequest) { traceCondition(r, 1)["value"] = []any{"102"} }},
		{"numeric business row", "invalid_input", func(r *TraceRequest) { traceCondition(r, 1)["value"] = []any{101} }},
		{"multi app", "uq_trace_scope_denied", func(r *TraceRequest) { traceCondition(r, 2)["value"] = []any{"app-fixture", "other-app"} }},
		{"non equality", "uq_trace_scope_denied", func(r *TraceRequest) { traceCondition(r, 2)["op"] = "contains" }},
		{"wildcard equality", "uq_trace_scope_denied", func(r *TraceRequest) { traceCondition(r, 2)["is_wildcard"] = true }},
		{"OR scope", "uq_trace_scope_denied", func(r *TraceRequest) {
			traceQuery(r)["conditions"].(map[string]any)["condition_list"] = []any{"or", "and"}
		}},
		{"missing shared scope", "uq_trace_scope_denied", func(r *TraceRequest) { delete(traceQuery(r), "conditions") }},
		{"bad links", "uq_trace_scope_denied", func(r *TraceRequest) { traceQuery(r)["conditions"].(map[string]any)["condition_list"] = []any{"and"} }},
		{"body alias", "uq_contract_unsupported", func(r *TraceRequest) { r.Body["QUERY_LIST"] = r.Body["query_list"] }},
		{"condition alias", "uq_contract_unsupported", func(r *TraceRequest) { traceCondition(r, 2)["Op"] = "ne" }},
		{"SQL", "uq_scope_denied", func(r *TraceRequest) { traceQuery(r)["sql"] = "select * from other_table" }},
		{"physical route", "uq_contract_unsupported", func(r *TraceRequest) { r.Body["tsdb_map"] = map[string]any{} }},
		{"skip space", "uq_contract_unsupported", func(r *TraceRequest) { r.Body["skip_space"] = true }},
		{"cache clear", "uq_contract_unsupported", func(r *TraceRequest) { r.Body["CLEAR_CACHE"] = true }},
		{"typed envelope", "uq_contract_unsupported", func(r *TraceRequest) { r.Body["trace_query_v2"] = map[string]any{} }},
		{"prefix table", "uq_contract_unsupported", func(r *TraceRequest) { traceQuery(r)["is_prefix"] = true }},
		{"query offset", "uq_contract_unsupported", func(r *TraceRequest) { traceQuery(r)["offset"] = "48h" }},
		{"query limit", "uq_contract_unsupported", func(r *TraceRequest) { traceQuery(r)["limit"] = 10000 }},
		{"collapse", "uq_contract_unsupported", func(r *TraceRequest) { traceQuery(r)["collapse"] = map[string]any{"field": "span_id"} }},
		{"aggregation", "uq_contract_unsupported", func(r *TraceRequest) { traceQuery(r)["function"] = []any{map[string]any{"method": "count"}} }},
		{"row limit", "uq_trace_page_exceeded", func(r *TraceRequest) { r.Body["limit"] = 1001 }},
		{"missing limit", "uq_trace_page_exceeded", func(r *TraceRequest) { delete(r.Body, "limit") }},
		{"offset", "invalid_input", func(r *TraceRequest) { r.Body["from"] = -1 }},
		{"fractional limit", "uq_trace_page_exceeded", func(r *TraceRequest) { r.Body["limit"] = json.Number("2.5") }},
		{"timestamp ms", "invalid_input", func(r *TraceRequest) { r.Body["start_time"] = "1700000000000" }},
		{"24h window", "uq_window_exceeded", func(r *TraceRequest) { r.Body["end_time"] = "1700086401" }},
		{"reversed window", "uq_window_exceeded", func(r *TraceRequest) { r.Body["start_time"] = "1700000061" }},
		{"timeout", "invalid_input", func(r *TraceRequest) { r.TimeoutMS = 30001 }},
		{"request budget", "uq_request_bytes_exceeded", func(r *TraceRequest) { traceQuery(r)["query_string"] = strings.Repeat("x", MaxBodyBytes) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := traceRequest(true)
			tc.mutate(&r)
			got, err := p.Trace(context.Background(), r)
			if code(err) != tc.code || got.Complete {
				t.Fatalf("receipt=%+v err=%v want=%s", got, err, tc.code)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("denied input reached egress %d times", calls.Load())
	}
}

func TestTraceRawResponseFailuresAndPartialPages(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		from             int
		complete, more   bool
	}{
		{"empty", `{"list":[],"total":0}`, "", 0, true, false},
		{"complete", `{"list":[{"span_id":"s"}],"total":1}`, "", 0, true, false},
		{"next page", `{"list":[{"span_id":"s"}],"total":3}`, "", 0, false, true},
		{"last offset page", `{"list":[{"span_id":"s"}],"total":3}`, "", 2, false, false},
		{"no current hits with more", `{"list":[],"total":3}`, "", 0, false, true},
		{"error 200", `{"error":"native failed","list":[],"total":0}`, "uq_query_error", 0, false, false},
		{"false result 200", `{"result":false,"list":[],"total":0}`, "uq_query_error", 0, false, false},
		{"status 200", `{"status":[{"error":"storage failed"}],"list":[],"total":0}`, "uq_query_error", 0, false, false},
		{"missing total", `{"list":[]}`, "uq_trace_response_invalid", 0, false, false},
		{"missing list", `{"total":0}`, "uq_trace_response_invalid", 0, false, false},
		{"null list", `{"list":null,"total":0}`, "uq_trace_response_invalid", 0, false, false},
		{"null total", `{"list":[],"total":null}`, "uq_trace_response_invalid", 0, false, false},
		{"negative total", `{"list":[],"total":-1}`, "uq_trace_response_invalid", 0, false, false},
		{"fractional total", `{"list":[],"total":0.5}`, "uq_trace_response_invalid", 0, false, false},
		{"string total", `{"list":[],"total":"0"}`, "uq_trace_response_invalid", 0, false, false},
		{"offset exceeds count", `{"list":[{}],"total":1}`, "uq_trace_response_invalid", 2, false, false},
		{"bad row", `{"list":[null],"total":1}`, "uq_trace_response_invalid", 0, false, false},
		{"rows over budget", `{"list":[{},{},{}],"total":3}`, "uq_trace_response_invalid", 0, false, false},
		{"rows over total", `{"list":[{}],"total":0}`, "uq_trace_response_invalid", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer s.Close()
			p, _ := NewWithQuerySource(traceFixture(s.URL, false), nil, "alarmd-sre")
			defer p.Close()
			r := traceRequest(false)
			r.Body["from"] = tc.from
			got, err := p.Trace(context.Background(), r)
			if code(err) != tc.code || got.Complete != tc.complete || got.TracePage.More != tc.more || got.ContentOmitted != "" || string(got.Native) != tc.body {
				t.Fatalf("receipt=%+v page=%+v err=%v", got, got.TracePage, err)
			}
		})
	}
}

func TestTraceTargetConfigurationAndDiscoveryCopies(t *testing.T) {
	for _, mutate := range []func(*Egress){
		func(e *Egress) { e.TraceTargets[0].Tenant = "other-tenant" },
		func(e *Egress) { e.TraceTargets[0].Space = "other-space" },
		func(e *Egress) { e.TraceTargets[0].TableID = "PREFIX#101_bkapm.trace_" },
		func(e *Egress) { e.TraceTargets[0].TableID = "101_bkapm.trace_*" },
		func(e *Egress) { e.TraceTargets[0].AppName = "" },
		func(e *Egress) { e.TraceTargets[0].BkBizID = 0 },
		func(e *Egress) { e.TraceTargets = append(e.TraceTargets, e.TraceTargets[0]) },
		func(e *Egress) { e.Endpoints = e.Endpoints[:1] },
		func(e *Egress) { e.Endpoints[1].TimeUnit = "ms" },
	} {
		cfg := traceFixture("http://native.invalid", false)
		mutate(&cfg[0])
		if Validate(cfg) == nil {
			t.Fatalf("invalid Trace target accepted: %+v", cfg)
		}
	}
	cfg := traceFixture("http://native.invalid", false)
	p, err := NewWithQuerySource(cfg, nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cfg[0].TraceTargets[0].TableID = "caller.changed"
	first := p.Egresses()
	first[0].TraceTargets[0].AppName = "caller-changed"
	if p.Egresses()[0].TraceTargets[0].TableID == "caller.changed" || p.Egresses()[0].TraceTargets[0].AppName == "caller-changed" {
		t.Fatal("caller mutated registered targets")
	}
	if _, err := NewWithQuerySource(cfg, nil, "bad\nsource"); err == nil {
		t.Fatal("invalid query-source header accepted")
	}
}

func TestTraceAndOldQueryShareTransportBudgetButKeepLogProjection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query/ts/raw" {
			close(started)
			<-release
		}
		_, _ = io.WriteString(w, `{"list":[{"message":"unmasked-log"}],"total":1}`)
	}))
	defer s.Close()
	p, _ := NewWithQuerySource(traceFixture(s.URL, false), nil, "alarmd-sre")
	defer p.Close()
	done := make(chan error, 1)
	go func() { _, err := p.Trace(context.Background(), traceRequest(false)); done <- err }()
	<-started
	if _, err := p.Query(context.Background(), request()); code(err) != "uq_busy" {
		t.Fatalf("Trace/query budget split unexpectedly: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := p.Query(context.Background(), request())
	if err != nil || got.ContentOmitted != "log_masking_boundary" || strings.Contains(string(got.Native), "unmasked-log") || got.TracePage != nil {
		t.Fatalf("old log query changed: %+v err=%v", got, err)
	}
}
