// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package uq forwards bounded native queries through deployment-registered
// egresses. It does not reconstruct the SaaS query or grant page-user access.
package uq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxBodyBytes     = 48 << 10
	MaxResponseBytes = 1 << 20
	MaxTimeout       = 30 * time.Second
	MaxWindow        = 24 * time.Hour
)

// Endpoint describes the real native wire contract, including its time unit.
// Endpoints are exact paths, not a caller-selected URL or HTTP method.
type Endpoint struct {
	Path       string `yaml:"path" json:"path"`
	StartField string `yaml:"start_field" json:"start_field"`
	EndField   string `yaml:"end_field" json:"end_field"`
	TimeUnit   string `yaml:"time_unit" json:"time_unit"`
}

type Egress struct {
	Name         string        `yaml:"name" json:"name"`
	URL          string        `yaml:"url" json:"url"`
	Tenants      []string      `yaml:"tenants" json:"tenants"`
	Spaces       []string      `yaml:"spaces" json:"spaces"`
	Endpoints    []Endpoint    `yaml:"endpoints" json:"endpoints"`
	TraceTargets []TraceTarget `yaml:"trace_targets,omitempty" json:"trace_targets,omitempty"`
}

type Request struct {
	Egress    string         `json:"egress"`
	Endpoint  string         `json:"endpoint"`
	Tenant    string         `json:"tenant"`
	Space     string         `json:"space"`
	Body      map[string]any `json:"body"`
	TimeoutMS int64          `json:"timeout_ms,omitempty"`
}

type Receipt struct {
	Egress         string          `json:"egress"`
	ActualURL      string          `json:"actual_url"`
	Tenant         string          `json:"tenant"`
	Space          string          `json:"space"`
	Authority      string          `json:"authority"`
	RequestDigest  string          `json:"request_digest"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	HTTPStatus     int             `json:"http_status,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	Native         json.RawMessage `json:"native,omitempty"`
	ResponseBytes  int             `json:"response_bytes"`
	ResponseDigest string          `json:"response_digest,omitempty"`
	ContentOmitted string          `json:"content_omitted,omitempty"`
	Complete       bool            `json:"complete"`
	Limitations    []string        `json:"limitations"`
	TracePage      *TracePage      `json:"trace_page,omitempty"`
}

type Error struct{ Code, Message string }

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Error{code, message} }

type Provider struct {
	egresses    map[string]Egress
	client      *http.Client
	transport   *http.Transport
	slots       chan struct{}
	querySource string
}

func Validate(egresses []Egress) error {
	seen := map[string]bool{}
	for _, e := range egresses {
		if !text(e.Name) || seen[e.Name] {
			return fmt.Errorf("sre.uq.egresses: invalid or duplicate name %q", e.Name)
		}
		seen[e.Name] = true
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "\\\r\n") || strings.Contains(u.Path, "..") {
			return fmt.Errorf("sre.uq.egresses.%s.url: an HTTP(S) base URL without credentials, query or fragment is required", e.Name)
		}
		if len(e.Tenants) == 0 || len(e.Spaces) == 0 || len(e.Endpoints) == 0 {
			return fmt.Errorf("sre.uq.egresses.%s: tenants, spaces and endpoints are required", e.Name)
		}
		for _, values := range [][]string{e.Tenants, e.Spaces} {
			for _, value := range values {
				if !text(value) {
					return fmt.Errorf("sre.uq.egresses.%s: invalid scope value", e.Name)
				}
			}
		}
		paths := map[string]bool{}
		for _, endpoint := range e.Endpoints {
			if !nativePath(endpoint.Path) || paths[endpoint.Path] || endpoint.StartField != "start_time" || endpoint.EndField != "end_time" || (endpoint.TimeUnit != "s" && endpoint.TimeUnit != "ms") {
				return fmt.Errorf("sre.uq.egresses.%s.endpoints: invalid path or time contract", e.Name)
			}
			paths[endpoint.Path] = true
		}
		if err := validateTraceTargets(e); err != nil {
			return err
		}
	}
	return nil
}

// NewWithQuerySource reuses the deployment's existing alarmd UQ source label.
// Native Trace and metric/log queries share the same transport and budget.
func NewWithQuerySource(egresses []Egress, client *http.Client, querySource string) (*Provider, error) {
	if err := Validate(egresses); err != nil {
		return nil, err
	}
	if !text(querySource) {
		return nil, errors.New("sre.uq: a nonempty query source without control characters is required")
	}
	p := &Provider{egresses: map[string]Egress{}, slots: make(chan struct{}, 1), querySource: querySource}
	for _, e := range egresses {
		e.Tenants = append([]string(nil), e.Tenants...)
		e.Spaces = append([]string(nil), e.Spaces...)
		e.Endpoints = append([]Endpoint(nil), e.Endpoints...)
		e.TraceTargets = append([]TraceTarget(nil), e.TraceTargets...)
		p.egresses[e.Name] = e
	}
	if client == nil {
		p.transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, MaxConnsPerHost: 1, MaxIdleConns: 2, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: MaxTimeout, DisableCompression: true}
		client = &http.Client{Transport: p.transport}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p.client = &copyClient
	return p, nil
}

func (p *Provider) Close() {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
}
func (p *Provider) Egresses() []Egress {
	out := make([]Egress, 0, len(p.egresses))
	for _, e := range p.egresses {
		e.Tenants = append([]string(nil), e.Tenants...)
		e.Spaces = append([]string(nil), e.Spaces...)
		e.Endpoints = append([]Endpoint(nil), e.Endpoints...)
		e.TraceTargets = append([]TraceTarget(nil), e.TraceTargets...)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Provider) Query(ctx context.Context, request Request) (receipt Receipt, err error) {
	return p.query(ctx, request, false)
}

func (p *Provider) query(ctx context.Context, request Request, trace bool) (receipt Receipt, err error) {
	receipt = Receipt{Egress: request.Egress, Tenant: request.Tenant, Space: request.Space, Authority: "deployment_operator_readonly; page_user_authorization_not_replayed", StartedAt: time.Now().UTC(), Limitations: []string{}}
	defer func() { receipt.FinishedAt = time.Now().UTC() }()
	// Native log responses precede SaaS topic masking. Retain query/status/count
	// evidence, not log content or value-bearing pagination cursors. The body
	// projection is also applied on failures, whose messages can echo log rows.
	defer func() {
		if !trace && receipt.Native != nil && logContent(request, receipt.Native) {
			digest := sha256.Sum256(receipt.Native)
			receipt.ResponseDigest = hex.EncodeToString(digest[:])
			receipt.ContentOmitted = "log_masking_boundary"
			receipt.Native = logSummary(receipt.Native)
			receipt.Complete = false
			receipt.Limitations = append(receipt.Limitations, "native log content and cursors omitted before topic masking; use the SaaS masked projection")
		}
	}()
	egress, ok := p.egresses[request.Egress]
	if !ok {
		return receipt, fail("uq_egress_unknown", "Egress is not registered in this deployment.")
	}
	if !text(request.Tenant) || !text(request.Space) || !allowed(egress.Tenants, request.Tenant) || !allowed(egress.Spaces, request.Space) {
		return receipt, fail("uq_scope_denied", "Explicit tenant and space must match the registered egress scope.")
	}
	var endpoint *Endpoint
	for i := range egress.Endpoints {
		if egress.Endpoints[i].Path == request.Endpoint {
			endpoint = &egress.Endpoints[i]
			break
		}
	}
	if endpoint == nil {
		return receipt, fail("uq_endpoint_denied", "Native endpoint is not registered for this egress.")
	}
	if request.TimeoutMS < 0 || request.TimeoutMS > MaxTimeout.Milliseconds() {
		return receipt, fail("invalid_input", "UQ timeout exceeds the diagnostic budget.")
	}
	if request.Body == nil {
		return receipt, fail("invalid_input", "A native query object is required.")
	}
	if err := validateScopeBody(request.Body, request.Tenant, request.Space); err != nil {
		return receipt, err
	}
	start, err := timestamp(request.Body[endpoint.StartField], endpoint.TimeUnit)
	if err != nil {
		return receipt, fail("invalid_input", "Native start timestamp does not match the endpoint time contract.")
	}
	end, err := timestamp(request.Body[endpoint.EndField], endpoint.TimeUnit)
	if err != nil {
		return receipt, fail("invalid_input", "Native end timestamp does not match the endpoint time contract.")
	}
	if end.Before(start) || end.Sub(start) > MaxWindow {
		return receipt, fail("uq_window_exceeded", "Native query time range must be ordered and at most 24 hours.")
	}
	body, err := json.Marshal(request.Body)
	if err != nil || len(body) > MaxBodyBytes {
		return receipt, fail("uq_request_bytes_exceeded", "Native query exceeds its request byte budget.")
	}
	digest := sha256.Sum256(body)
	receipt.RequestDigest = hex.EncodeToString(digest[:])
	u, _ := url.Parse(egress.URL)
	u.Path = strings.TrimRight(u.Path, "/") + endpoint.Path
	u.RawPath = ""
	receipt.ActualURL = u.String()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return receipt, fail("uq_busy", "The independent UQ diagnostic query budget is busy.")
	}
	timeout := MaxTimeout
	if request.TimeoutMS > 0 {
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return receipt, fail("uq_request_invalid", "Native request could not be constructed.")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Bk-Tenant-Id", request.Tenant)
	r.Header.Set("X-Bk-Scope-Space-Uid", request.Space)
	r.Header.Set("Bk-Query-Source", p.querySource)
	response, err := p.client.Do(r)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return receipt, fail("uq_timeout", "Native query deadline elapsed.")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return receipt, fail("uq_canceled", "Native query was canceled.")
		}
		return receipt, fail("uq_transport_unavailable", "The registered UQ egress did not answer.")
	}
	defer response.Body.Close()
	receipt.HTTPStatus = response.StatusCode
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	receipt.ResponseBytes = len(data)
	if len(data) > MaxResponseBytes {
		receipt.Limitations = append(receipt.Limitations, "response_byte_budget_exceeded; native JSON was not retained")
		return receipt, fail("uq_response_bytes_exceeded", "Native response exceeds its byte budget; narrow the query or use explicit native pagination.")
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return receipt, fail("uq_timeout", "Native response deadline elapsed.")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return receipt, fail("uq_canceled", "Native response was canceled.")
		}
		return receipt, fail("uq_response_incomplete", "Native response stream did not complete.")
	}
	if !json.Valid(data) {
		return receipt, fail("uq_response_invalid", "Native response is not a JSON value.")
	}
	receipt.Native = append(json.RawMessage(nil), data...)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil || envelope == nil {
		receipt.Native = nil
		return receipt, fail("uq_response_invalid", "Native query response must be a JSON object.")
	}
	_ = json.Unmarshal(envelope["trace_id"], &receipt.TraceID)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return receipt, fail("uq_http_error", "Native query returned a non-success HTTP status; inspect the native receipt.")
	}
	if value, exists := envelope["error"]; exists && string(value) != "null" && string(value) != `""` {
		return receipt, fail("uq_query_error", "Native query returned an application error; inspect the native receipt.")
	}
	if value, exists := envelope["result"]; exists && string(value) == "false" {
		return receipt, fail("uq_query_error", "Native query returned result=false; inspect the native receipt.")
	}
	receipt.Complete = true
	// Successful transport preserves statuses and cursors verbatim. A native
	// result with status/pagination does not prove all data has been read.
	if value, exists := envelope["status"]; exists && string(value) != "[]" && string(value) != "null" && string(value) != "{}" {
		receipt.Complete = false
		receipt.Limitations = append(receipt.Limitations, "native statuses must be checked before inferring data absence")
		if trace {
			return receipt, fail("uq_query_error", "Native Trace query returned nonempty statuses; inspect the native receipt.")
		}
	}
	receipt.Limitations = append(receipt.Limitations, "one native request; pagination and data completeness follow the native response")
	return receipt, nil
}

func logContent(request Request, native json.RawMessage) bool {
	if strings.Contains(request.Endpoint, "/raw") {
		return true
	}
	for key, value := range request.Body {
		if !strings.EqualFold(key, "query_list") {
			continue
		}
		queries, _ := value.([]any)
		for _, item := range queries {
			query, _ := item.(map[string]any)
			for field, value := range query {
				if strings.EqualFold(field, "data_source") && (value == "bklog" || value == "bk_log_search") {
					return true
				}
			}
		}
	}
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(native, &envelope)
	if _, ok := envelope["list"]; ok {
		return true
	}
	var data map[string]json.RawMessage
	_ = json.Unmarshal(envelope["data"], &data)
	_, ok := data["list"]
	return ok
}

func logSummary(native json.RawMessage) json.RawMessage {
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(native, &envelope)
	summary := map[string]any{"content_omitted": "log_masking_boundary"}
	var result bool
	if err := json.Unmarshal(envelope["result"], &result); err == nil {
		summary["result"] = result
	}
	for _, key := range []string{"error", "status"} {
		if value, exists := envelope[key]; exists {
			summary[key+"_present"] = string(value) != "null" && string(value) != `""` && string(value) != "[]" && string(value) != "{}"
		}
	}
	addCounts := func(values map[string]json.RawMessage) {
		for _, key := range []string{"list", "series"} {
			var rows []json.RawMessage
			if err := json.Unmarshal(values[key], &rows); err == nil {
				summary[key+"_count"] = len(rows)
			}
		}
		var total json.Number
		if err := json.Unmarshal(values["total"], &total); err == nil {
			if _, err := total.Int64(); err == nil {
				summary["total"] = total
			}
		}
	}
	addCounts(envelope)
	var data map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &data) == nil {
		addCounts(data)
	}
	projected, _ := json.Marshal(summary)
	return projected
}

func text(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}
func allowed(values []string, value string) bool {
	for _, v := range values {
		if v == "*" || v == value {
			return true
		}
	}
	return false
}
func nativePath(path string) bool {
	// These handlers use the audited QueryTs scope and route resolver. A new
	// endpoint (notably typed Trace or PromQL) needs its own wire contract.
	switch path {
	case "/query/ts", "/query/ts/", "/query/ts/raw", "/query/ts/raw/", "/query/ts/raw_with_scroll", "/query/ts/raw_with_scroll/":
		return true
	}
	return false
}
func timestamp(value any, unit string) (time.Time, error) {
	s, ok := value.(string)
	digits := 10
	if unit == "ms" {
		digits = 13
	}
	// QueryTs uses a string and derives its unit from digit count. Validate
	// that same wire rule before enforcing the diagnostic window budget.
	if !ok || len(s) != digits || strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return time.Time{}, errors.New("QueryTs timestamp digit count does not match its declared unit")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return time.Time{}, errors.New("nonnegative epoch timestamp required")
	}
	if unit == "ms" {
		return time.UnixMilli(n), nil
	}
	if n > 253402300799 {
		return time.Time{}, errors.New("seconds outside supported range")
	}
	return time.Unix(n, 0), nil
}

func validateScopeBody(body map[string]any, tenant, space string) error {
	var queries []any
	for key, value := range body {
		// The downstream Go decoder accepts case-insensitive JSON field names.
		switch strings.ToLower(key) {
		case "bk_tenant_id", "tenant_id", "tenant":
			if value != tenant {
				return fail("uq_scope_mismatch", "Native body tenant differs from the registered request scope.")
			}
		case "space_uid":
			if value != space {
				return fail("uq_scope_mismatch", "Native body space differs from the explicit request scope.")
			}
		case "skip_space", "_skip_space", "__skip_space", "clear_cache":
			if value != false && value != "" && value != nil {
				return fail("uq_scope_denied", "Native administrative or cross-space query controls are not available through this operation.")
			}
		case "tsdb_map":
			// TsDBMap bypasses UQ's space route resolver entirely. Even a
			// syntactically empty override is not part of the public contract.
			return fail("uq_scope_denied", "Direct physical storage overrides are not available through this operation.")
		case "query_list":
			if queries != nil {
				return fail("invalid_input", "Duplicate native query-list aliases are not accepted.")
			}
			queries, _ = value.([]any)
		}
	}
	if len(queries) == 0 {
		return fail("uq_contract_unsupported", "This operation supports structured QueryTs metric/log queries with a nonempty query_list; typed Trace requires its trusted service contract.")
	}
	for _, value := range queries {
		query, ok := value.(map[string]any)
		if !ok {
			return fail("invalid_input", "Native query_list entries must be objects.")
		}
		for key, value := range query {
			switch strings.ToLower(key) {
			case "sql":
				if value != "" && value != nil {
					return fail("uq_scope_denied", "Direct SQL is not available through the scoped diagnostic query operation.")
				}
			case "data_source":
				if value == "bkdata" || value == "bk_data" {
					return fail("uq_scope_denied", "BKData queries require a separate trusted routing contract.")
				}
			}
		}
	}
	return nil
}
