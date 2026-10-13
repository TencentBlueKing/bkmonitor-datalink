// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const MaxTraceRows = 1000

// TraceTarget is copied from the application's actual Trace datasource mapping.
// TableID is an exact registered result table; no application-name inference,
// prefix search, retention expansion or cross-application discovery is done here.
type TraceTarget struct {
	Name    string `yaml:"name" json:"name"`
	Tenant  string `yaml:"tenant" json:"tenant"`
	Space   string `yaml:"space" json:"space"`
	TableID string `yaml:"table_id" json:"table_id"`
	BkBizID int64  `yaml:"bk_biz_id" json:"bk_biz_id"`
	AppName string `yaml:"app_name" json:"app_name"`
	Shared  bool   `yaml:"shared,omitempty" json:"shared"`
}

type TraceRequest struct {
	Egress    string         `json:"egress"`
	Target    string         `json:"target"`
	Body      map[string]any `json:"body"`
	TimeoutMS int64          `json:"timeout_ms,omitempty"`
}

// TracePage describes this single raw Span page. Complete only concerns the
// fixed application/table/window query, never a global or full-retention trace.
type TracePage struct {
	Target       TraceTarget `json:"target"`
	StartTime    string      `json:"start_time"`
	EndTime      string      `json:"end_time"`
	WindowUnit   string      `json:"window_unit"`
	SpanTimeUnit string      `json:"span_time_unit"`
	From         int64       `json:"from"`
	Limit        int64       `json:"limit"`
	KeepColumns  []string    `json:"keep_columns"`
	Returned     int         `json:"returned"`
	Total        int64       `json:"total"`
	More         bool        `json:"more"`
	RequestedID  string      `json:"requested_trace_id,omitempty"`
}

func validateTraceTargets(e Egress) error {
	seen := map[string]bool{}
	for _, target := range e.TraceTargets {
		if !text(target.Name) || seen[target.Name] || !text(target.Tenant) || target.Tenant == "*" || !text(target.Space) || target.Space == "*" || !allowed(e.Tenants, target.Tenant) || !allowed(e.Spaces, target.Space) || !text(target.TableID) || strings.ContainsAny(target.TableID, "*?#") || !text(target.AppName) || target.AppName == "*" || target.BkBizID == 0 {
			return fmt.Errorf("sre.uq.egresses.%s.trace_targets: an exact named application/table/tenant/space scope is required", e.Name)
		}
		seen[target.Name] = true
		if traceEndpoint(e) == "" {
			return fmt.Errorf("sre.uq.egresses.%s.trace_targets: register /query/ts/raw with second-string timestamps", e.Name)
		}
	}
	return nil
}

func traceEndpoint(e Egress) string {
	for _, endpoint := range e.Endpoints {
		if strings.TrimSuffix(endpoint.Path, "/") == "/query/ts/raw" && endpoint.TimeUnit == "s" {
			return endpoint.Path
		}
	}
	return ""
}

// Trace forwards the existing bkapm QueryTs raw Span contract over the same
// direct UQ HTTP connection and tenant/space headers as other alarmd queries.
func (p *Provider) Trace(ctx context.Context, request TraceRequest) (Receipt, error) {
	egress, ok := p.egresses[request.Egress]
	if !ok {
		return p.query(ctx, Request{Egress: request.Egress}, true)
	}
	var target *TraceTarget
	for i := range egress.TraceTargets {
		if egress.TraceTargets[i].Name == request.Target {
			target = &egress.TraceTargets[i]
			break
		}
	}
	if target == nil {
		return Receipt{Egress: request.Egress, Limitations: []string{}}, fail("uq_trace_target_unknown", "Trace target is not registered in this deployment.")
	}
	page, err := validateTraceBody(request.Body, *target)
	if err != nil {
		return Receipt{Egress: request.Egress, Tenant: target.Tenant, Space: target.Space, Limitations: []string{}}, err
	}
	receipt, err := p.query(ctx, Request{Egress: request.Egress, Endpoint: traceEndpoint(egress), Tenant: target.Tenant, Space: target.Space, Body: request.Body, TimeoutMS: request.TimeoutMS}, true)
	receipt.TracePage = page
	receipt.Limitations = append(receipt.Limitations, "single registered application and exact table within the explicit window; no cross-application or retention expansion; Span timestamps and elapsed_time retain native microseconds")
	if len(page.KeepColumns) != 0 {
		receipt.Limitations = append(receipt.Limitations, "Span columns are projected by the native keep_columns request; omitted fields were not queried")
	}
	if err != nil {
		return receipt, err
	}
	var response map[string]json.RawMessage
	var rows []json.RawMessage
	var totalValue any
	_ = json.Unmarshal(receipt.Native, &response)
	decoder := json.NewDecoder(strings.NewReader(string(response["total"])))
	decoder.UseNumber()
	if json.Unmarshal(response["list"], &rows) != nil || rows == nil || decoder.Decode(&totalValue) != nil {
		receipt.Complete = false
		return receipt, fail("uq_trace_response_invalid", "Raw Span response requires a list and a nonnegative integer total.")
	}
	total, valid := traceInteger(totalValue)
	if !valid || total < 0 || total < int64(len(rows)) || len(rows) > int(page.Limit) || (len(rows) > 0 && (page.From >= total || int64(len(rows)) > total-page.From)) {
		receipt.Complete = false
		return receipt, fail("uq_trace_response_invalid", "Raw Span response count does not match the single-page contract.")
	}
	for _, row := range rows {
		var span map[string]json.RawMessage
		if json.Unmarshal(row, &span) != nil || span == nil {
			receipt.Complete = false
			return receipt, fail("uq_trace_response_invalid", "Raw Span list entries must be objects.")
		}
	}
	page.Returned, page.Total = len(rows), total
	page.More = page.From < total && int64(page.Returned) < total-page.From
	if page.From > 0 || total > int64(page.Returned) {
		receipt.Complete = false
		receipt.Limitations = append(receipt.Limitations, "raw Span page is partial; use an explicit offset in the same fixed query, without automatically expanding scope or window")
	}
	return receipt, nil
}

func validateTraceBody(body map[string]any, target TraceTarget) (*TracePage, error) {
	if body == nil {
		return nil, fail("invalid_input", "A native raw Span query object is required.")
	}
	// This audited raw Span subset prevents per-query time offsets, pagination
	// overrides, prefix/physical routing and typed or aggregated query envelopes.
	if err := traceKeys(body, "query_list", "start_time", "end_time", "from", "limit", "order_by", "bk_biz_id", "space_uid", "bk_tenant_id", "tenant_id", "tenant", "metric_merge", "step", "timezone"); err != nil {
		return nil, err
	}
	if err := validateScopeBody(body, target.Tenant, target.Space); err != nil {
		return nil, err
	}
	if value, exists := body["bk_biz_id"]; exists {
		biz, ok := traceInteger(value)
		if !ok || biz != target.BkBizID {
			return nil, fail("uq_scope_mismatch", "Native business scope differs from the registered Trace target.")
		}
	}
	if value, exists := body["metric_merge"]; exists && value != "" && value != "a" {
		return nil, fail("uq_contract_unsupported", "Raw Span metric_merge must be empty or the single reference a.")
	}
	limit, valid := traceInteger(body["limit"])
	if !valid || limit < 1 || limit > MaxTraceRows {
		return nil, fail("uq_trace_page_exceeded", "Raw Span query requires an explicit limit between 1 and 1000.")
	}
	offset := int64(0)
	if value, exists := body["from"]; exists {
		offset, valid = traceInteger(value)
		if !valid || offset < 0 {
			return nil, fail("invalid_input", "Raw Span offset must be a nonnegative integer.")
		}
	}
	queries, _ := body["query_list"].([]any)
	if len(queries) != 1 {
		return nil, fail("uq_contract_unsupported", "Raw Span diagnostics query one registered application/table per invocation.")
	}
	query, ok := queries[0].(map[string]any)
	if !ok {
		return nil, fail("invalid_input", "Native raw Span query_list entry must be an object.")
	}
	if err := traceKeys(query, "data_source", "table_id", "time_field", "conditions", "query_string", "keep_columns", "driver", "reference_name", "field_name", "dimensions", "function", "time_aggregation", "order_by"); err != nil {
		return nil, err
	}
	if (query["data_source"] != "bkapm" && query["data_source"] != "bk_apm") || query["table_id"] != target.TableID || query["time_field"] != "start_time" {
		return nil, fail("uq_trace_scope_denied", "Raw Span query must use bkapm or bk_apm, the exact registered table and start_time.")
	}
	if value, exists := query["field_name"]; exists && value != "" {
		return nil, fail("uq_contract_unsupported", "Raw Span diagnostics do not query aggregated fields.")
	}
	if value, exists := query["reference_name"]; exists && value != "" && value != "a" {
		return nil, fail("uq_contract_unsupported", "Raw Span diagnostics use one native query reference a.")
	}
	for _, key := range []string{"function", "dimensions", "order_by"} {
		if value, exists := query[key]; exists {
			items, ok := value.([]any)
			if !ok || len(items) != 0 {
				return nil, fail("uq_contract_unsupported", "Raw Span diagnostics do not aggregate, collapse or override query ordering.")
			}
		}
	}
	if value, exists := query["time_aggregation"]; exists {
		items, ok := value.(map[string]any)
		if !ok || len(items) != 0 {
			return nil, fail("uq_contract_unsupported", "Raw Span diagnostics do not apply time aggregation.")
		}
	}
	page := &TracePage{Target: target, StartTime: stringValue(body["start_time"]), EndTime: stringValue(body["end_time"]), WindowUnit: "s", SpanTimeUnit: "us", From: offset, Limit: limit, KeepColumns: []string{}}
	if value, exists := query["keep_columns"]; exists {
		items, ok := value.([]any)
		if !ok {
			return nil, fail("invalid_input", "Native keep_columns must be a string array.")
		}
		for _, item := range items {
			column, ok := item.(string)
			if !ok || !text(column) {
				return nil, fail("invalid_input", "Native keep_columns entries must be nonempty strings.")
			}
			page.KeepColumns = append(page.KeepColumns, column)
		}
	}
	requestedID, err := traceConditions(query["conditions"], target)
	if err != nil {
		return nil, err
	}
	page.RequestedID = requestedID
	return page, nil
}

func traceKeys(object map[string]any, names ...string) error {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	for name := range object {
		if !allowed[name] {
			return fail("uq_contract_unsupported", "Native raw Span fields are outside the bounded diagnostic contract.")
		}
	}
	return nil
}

func traceConditions(value any, target TraceTarget) (string, error) {
	shared := target.Shared || strings.HasPrefix(target.TableID, "apm_global.shared")
	if value == nil && !shared {
		return "", nil
	}
	conditions, ok := value.(map[string]any)
	if !ok {
		return "", fail("uq_trace_scope_denied", "Raw Span conditions must use the native field_list/condition_list contract.")
	}
	if err := traceKeys(conditions, "field_list", "condition_list"); err != nil {
		return "", err
	}
	fields, _ := conditions["field_list"].([]any)
	links, _ := conditions["condition_list"].([]any)
	if len(links) != max(0, len(fields)-1) {
		return "", fail("uq_trace_scope_denied", "Raw Span condition links must match the field count.")
	}
	conjunction := true
	for _, link := range links {
		if link != "and" && link != "or" {
			return "", fail("uq_trace_scope_denied", "Raw Span condition links must be native and/or operators.")
		}
		conjunction = conjunction && link == "and"
	}
	if shared && !conjunction {
		return "", fail("uq_trace_scope_denied", "Shared Trace target isolation requires AND conditions.")
	}
	bizFound, appFound, requestedID := false, false, ""
	for _, value := range fields {
		field, ok := value.(map[string]any)
		if !ok {
			return "", fail("invalid_input", "Native condition entries must be objects.")
		}
		if err := traceKeys(field, "field_name", "value", "op", "is_wildcard", "is_prefix", "is_suffix"); err != nil {
			return "", err
		}
		name, _ := field["field_name"].(string)
		values, _ := field["value"].([]any)
		exact := field["op"] == "eq" && len(values) == 1
		for _, modifier := range []string{"is_wildcard", "is_prefix", "is_suffix"} {
			if value, exists := field[modifier]; exists {
				exact = exact && value == false
			}
		}
		for _, item := range values {
			if _, ok := item.(string); !ok {
				return "", fail("invalid_input", "Native condition values must retain the UQ string-array contract.")
			}
		}
		switch name {
		case "bk_biz_id", "app_name":
			want := target.AppName
			if name == "bk_biz_id" {
				want = strconv.FormatInt(target.BkBizID, 10)
			}
			if !exact || values[0] != want {
				return "", fail("uq_trace_scope_denied", "Trace application/business filters must match the registered target with single-value equality.")
			}
			bizFound = bizFound || name == "bk_biz_id"
			appFound = appFound || name == "app_name"
		case "trace_id":
			if conjunction && exact {
				requestedID, _ = values[0].(string)
			}
		}
	}
	if shared && (!bizFound || !appFound) {
		return "", fail("uq_trace_scope_denied", "Shared Trace table requires registered bk_biz_id and app_name equality predicates.")
	}
	return requestedID, nil
}

func stringValue(value any) string { s, _ := value.(string); return s }

func traceInteger(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v >= math.MinInt64 && v < math.MaxInt64 && math.Trunc(v) == v {
			return int64(v), true
		}
	}
	return 0, false
}
