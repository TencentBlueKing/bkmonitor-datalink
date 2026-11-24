// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// stubControlPlane answers the link's management routes: a filtered close
// matches the alerts given, whatever its filter; a search that names one
// strategy and excludes nothing (the alert-by-alert close of a strategy
// alarmd holds no record of) finds the same alerts, active, labelled as the
// search's terms name them; any other search finds nothing; or every route
// answers 501 (storage that cannot search). It records each request's body.
type stubControlPlane struct {
	mu          sync.Mutex
	unsupported bool
	// busy answers every request 429.
	busy    bool
	matches []string
	bodies  map[string][]string
	// onClose, when set, runs as a close that is not a dry run arrives.
	onClose func()
	// itemBusy answers each item of a close that is not a dry run 429:
	// certainly not closed, the matches stay.
	itemBusy bool
	// dryRunDelay and closeDelay are what a dry run and a real close cost
	// the link before it answers.
	dryRunDelay, closeDelay time.Duration
	// closeHasMore answers every real close has_more: the link stopped on
	// its budget (alert-close.md: has_more is no count of what is left).
	closeHasMore bool
	// itemRefused answers each item of a close that is not a dry run 400
	// invalid_command: refused, left open, the matches stay.
	itemRefused bool
	// closeStatus, when set, answers every filtered close - dry run or not
	// - with that status and no body the client reads: 400 refuses the
	// request (link_refused), 500 fails it (link_failed).
	closeStatus int
}

func (stub *stubControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.bodies[r.URL.Path] = append(stub.bodies[r.URL.Path], string(body))
	if stub.unsupported {
		http.Error(w, "alert search requires Elasticsearch storage", http.StatusNotImplemented)
		return
	}
	if stub.busy {
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	switch r.URL.Path {
	case "/api/v1/alerts/search":
		_ = json.NewEncoder(w).Encode(map[string]any{"items": stub.searchItems(body), "next": ""})
	case "/api/v1/alerts/close":
		if stub.closeStatus != 0 {
			http.Error(w, "filtered close answered by the test", stub.closeStatus)
			return
		}
		var request struct {
			DryRun bool `json:"dry_run"`
		}
		_ = json.Unmarshal(body, &request)
		if delay := map[bool]time.Duration{true: stub.dryRunDelay, false: stub.closeDelay}[request.DryRun]; delay > 0 {
			time.Sleep(delay) // fixed-wait: the link's cost per request is the behaviour under test
		}
		items := []map[string]any{}
		status, code := http.StatusOK, ""
		switch {
		case stub.itemBusy && !request.DryRun:
			status = http.StatusTooManyRequests
		case stub.itemRefused && !request.DryRun:
			status, code = http.StatusBadRequest, "invalid_command"
		}
		for _, id := range stub.matches {
			item := map[string]any{"alert_id": id, "status": status}
			if code != "" {
				item["error_code"] = code
			}
			items = append(items, item)
		}
		if !request.DryRun && status == http.StatusOK {
			stub.matches = nil
			if stub.onClose != nil {
				stub.onClose()
			}
		}
		closed, failed := 0, 0
		switch {
		case request.DryRun:
		case status == http.StatusOK:
			closed = len(items)
		default:
			failed = len(items)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "has_more": stub.closeHasMore && !request.DryRun, "dry_run": request.DryRun,
			"closed": closed, "failed": failed})
	default:
		http.NotFound(w, r)
	}
}

// searchItems is the matches as a search of one strategy finds them.
func (stub *stubControlPlane) searchItems(body []byte) []map[string]any {
	var request struct {
		TenantID string `json:"bk_tenant_id"`
		Query    struct {
			Bool struct {
				Filter  []map[string]map[string]any `json:"filter"`
				MustNot []json.RawMessage           `json:"must_not"`
			} `json:"bool"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Query.Bool.MustNot) > 0 {
		return []map[string]any{}
	}
	labels := map[string]any{}
	source := ""
	for _, clause := range request.Query.Bool.Filter {
		for field, value := range clause["term"] {
			if name, ok := strings.CutPrefix(field, "labels."); ok {
				labels[name] = value
			}
			if field == "event_source_id" {
				source, _ = value.(string)
			}
		}
	}
	if labels["strategy_id"] == nil {
		return []map[string]any{}
	}
	items := []map[string]any{}
	for _, id := range stub.matches {
		items = append(items, map[string]any{"alert_id": id, "bk_tenant_id": request.TenantID, "event_source_id": source,
			"status": "active", "labels": labels})
	}
	return items
}

func (stub *stubControlPlane) requests(path string) []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]string(nil), stub.bodies[path]...)
}

// newStubFilterRoute is the production closer, built from configuration as
// the bundle builds it, against the stub and a real runtime Redis.
func newStubFilterRoute(t *testing.T, stub *stubControlPlane) *filterRoute {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	plane := httptest.NewServer(stub)
	t.Cleanup(plane.Close)
	var cfg config.Config
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("link client %v (%v)", client, err)
	}
	closer, err := newFilterCloser(cfg, client, server.Client(), time.Now, nil, nil)
	if err != nil || closer == nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
	return newFilterRoute(cfg, closer)
}

// fakeLinkd is the link's search and filtered close over alerts in memory,
// from the link's contracts (alert-search.md, alert-close.md): a search
// pages the tenant's matching alerts in id order; a filtered close adds
// the tenant and status active to the filter, a dry run lists and a close
// closes each match once. Its evaluator covers what alarmd sends: bool
// (filter, must_not), term, terms, exists, values compared as keywords.
// Every request body is kept per route, and every close per alert.
type fakeLinkd struct {
	t           *testing.T
	mu          sync.Mutex
	alerts      map[string]*fakeLinkdAlert
	bodies      map[string][]string
	unsupported bool
	busy        bool
	// searchBusyAfter, when positive, answers 429 to every search after
	// the first n.
	searchBusyAfter int
	// onClose, when set, runs as a filtered close (dry run or not) arrives,
	// before it is answered.
	onClose func()
	// onSearch, when set, runs as a search arrives, before it is answered.
	onSearch func()
	// closeBusy answers 429 to every filtered close; searches still answer.
	closeBusy bool
	// searchFails, when set, answers 500 to a search whose body it returns
	// true for.
	searchFails func(body string) bool
	// itemBusy, when set, answers 429 to an item of a real close it returns
	// true for: certainly not closed, the alert stays active.
	itemBusy func(id string) bool
	// itemRefused, when set, answers 400 invalid_command to an item of a
	// real close it returns true for: refused, left open.
	itemRefused func(id string) bool
}

type fakeLinkdAlert struct {
	id, tenant, source, status, severity, fingerprint string
	labels, dimensions                                map[string]string
	extra                                             json.RawMessage
	closes                                            int
	reason                                            string
	// created is when the link opened it; add sets an hour ahead of the
	// wall clock when zero: opened after alarmd's record started, as the
	// link opens an alert after the event alarmd recorded.
	created time.Time
}

func newFakeLinkd(t *testing.T) *fakeLinkd {
	return &fakeLinkd{t: t, alerts: map[string]*fakeLinkdAlert{}, bodies: map[string][]string{}}
}

// add holds an alert, active of tenant "system" and source "built_in_bk"
// unless it says otherwise.
func (link *fakeLinkd) add(alert *fakeLinkdAlert) {
	link.mu.Lock()
	defer link.mu.Unlock()
	if alert.status == "" {
		alert.status = "active"
	}
	if alert.tenant == "" {
		alert.tenant = "system"
	}
	if alert.source == "" {
		alert.source = "built_in_bk"
	}
	if alert.created.IsZero() {
		alert.created = time.Now().Add(time.Hour)
	}
	link.alerts[alert.id] = alert
}

// closed is each closed alert's close count.
func (link *fakeLinkd) closed() map[string]int {
	link.mu.Lock()
	defer link.mu.Unlock()
	out := map[string]int{}
	for id, alert := range link.alerts {
		if alert.closes > 0 {
			out[id] = alert.closes
		}
	}
	return out
}

func (link *fakeLinkd) requests(path string) []string {
	link.mu.Lock()
	defer link.mu.Unlock()
	return append([]string(nil), link.bodies[path]...)
}

func (alert *fakeLinkdAlert) field(name string) (string, bool) {
	switch {
	case name == "bk_tenant_id":
		return alert.tenant, true
	case name == "event_source_id":
		return alert.source, true
	case name == "alert_id":
		return alert.id, true
	case name == "fingerprint":
		return alert.fingerprint, alert.fingerprint != ""
	case name == "status":
		return alert.status, true
	case name == "severity":
		return alert.severity, alert.severity != ""
	case strings.HasPrefix(name, "labels."):
		value, ok := alert.labels[strings.TrimPrefix(name, "labels.")]
		return value, ok
	case strings.HasPrefix(name, "dimensions."):
		value, ok := alert.dimensions[strings.TrimPrefix(name, "dimensions.")]
		return value, ok
	}
	return "", false
}

func fakeKeyword(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

// compile turns a query into a predicate; a clause it does not evaluate
// fails the test.
func (link *fakeLinkd) compile(query json.RawMessage) func(*fakeLinkdAlert) bool {
	never := func(*fakeLinkdAlert) bool { return false }
	var clause map[string]json.RawMessage
	if err := json.Unmarshal(query, &clause); err != nil || len(clause) != 1 {
		link.t.Errorf("query %s is not one clause", query)
		return never
	}
	for kind, body := range clause {
		switch kind {
		case "term":
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(body, &fields)
			for name, value := range fields {
				want := fakeKeyword(value)
				return func(alert *fakeLinkdAlert) bool {
					got, ok := alert.field(name)
					return ok && got == want
				}
			}
		case "terms":
			var fields map[string][]json.RawMessage
			_ = json.Unmarshal(body, &fields)
			for name, values := range fields {
				set := map[string]bool{}
				for _, value := range values {
					set[fakeKeyword(value)] = true
				}
				return func(alert *fakeLinkdAlert) bool {
					got, ok := alert.field(name)
					return ok && set[got]
				}
			}
		case "exists":
			var field struct {
				Field string `json:"field"`
			}
			_ = json.Unmarshal(body, &field)
			return func(alert *fakeLinkdAlert) bool {
				_, ok := alert.field(field.Field)
				return ok
			}
		case "range":
			var fields map[string]map[string]string
			if json.Unmarshal(body, &fields) != nil || len(fields) != 1 || fields["create_at"] == nil {
				link.t.Errorf("range %s: the fake ranges create_at only", body)
				return never
			}
			return func(alert *fakeLinkdAlert) bool {
				for op, text := range fields["create_at"] {
					at, err := time.Parse(time.RFC3339Nano, text)
					if err != nil {
						link.t.Errorf("range bound %q: %v", text, err)
						return false
					}
					if (op == "gte" && alert.created.Before(at)) || (op == "lt" && !alert.created.Before(at)) ||
						(op == "lte" && alert.created.After(at)) || (op == "gt" && !alert.created.After(at)) {
						return false
					}
				}
				return true
			}
		case "bool":
			var b struct {
				Filter  []json.RawMessage `json:"filter"`
				MustNot []json.RawMessage `json:"must_not"`
			}
			if err := json.Unmarshal(body, &b); err != nil {
				link.t.Errorf("bool %s: %v", body, err)
				return never
			}
			var filter, mustNot []func(*fakeLinkdAlert) bool
			for _, sub := range b.Filter {
				filter = append(filter, link.compile(sub))
			}
			for _, sub := range b.MustNot {
				mustNot = append(mustNot, link.compile(sub))
			}
			return func(alert *fakeLinkdAlert) bool {
				for _, match := range filter {
					if !match(alert) {
						return false
					}
				}
				for _, match := range mustNot {
					if match(alert) {
						return false
					}
				}
				return true
			}
		}
	}
	link.t.Errorf("query clause %s is not one the fake evaluates", query)
	return never
}

func (link *fakeLinkd) page(tenant string, query json.RawMessage, activeOnly bool, after string, limit int) ([]*fakeLinkdAlert, string) {
	ids := make([]string, 0, len(link.alerts))
	for id := range link.alerts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	match := link.compile(query)
	var out []*fakeLinkdAlert
	for _, id := range ids {
		alert := link.alerts[id]
		if id <= after || alert.tenant != tenant || (activeOnly && alert.status != "active") || !match(alert) {
			continue
		}
		if len(out) == limit {
			return out, out[len(out)-1].id
		}
		out = append(out, alert)
	}
	return out, ""
}

func (link *fakeLinkd) wire(alert *fakeLinkdAlert) map[string]any {
	labels := map[string]json.RawMessage{}
	for name, value := range alert.labels {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil && strconv.FormatInt(n, 10) == value {
			labels[name] = json.RawMessage(value)
		} else {
			labels[name], _ = json.Marshal(value)
		}
	}
	out := map[string]any{"alert_id": alert.id, "bk_tenant_id": alert.tenant, "event_source_id": alert.source,
		"status": alert.status, "severity": alert.severity, "fingerprint": alert.fingerprint, "labels": labels}
	if alert.dimensions != nil {
		out["dimensions"] = alert.dimensions
	}
	if len(alert.extra) > 0 {
		out["extra_data"] = alert.extra
	}
	return out
}

func (link *fakeLinkd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	link.mu.Lock()
	defer link.mu.Unlock()
	link.bodies[r.URL.Path] = append(link.bodies[r.URL.Path], string(body))
	if link.unsupported {
		http.Error(w, "alert search requires Elasticsearch storage", http.StatusNotImplemented)
		return
	}
	if link.busy {
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	if link.searchBusyAfter > 0 && r.URL.Path == "/api/v1/alerts/search" && len(link.bodies[r.URL.Path]) > link.searchBusyAfter {
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	if link.onClose != nil && r.URL.Path == "/api/v1/alerts/close" {
		link.onClose()
	}
	if link.onSearch != nil && r.URL.Path == "/api/v1/alerts/search" {
		link.onSearch()
	}
	if link.searchFails != nil && r.URL.Path == "/api/v1/alerts/search" && link.searchFails(string(body)) {
		http.Error(w, "alert search failed", http.StatusInternalServerError)
		return
	}
	if link.closeBusy && r.URL.Path == "/api/v1/alerts/close" {
		http.Error(w, "alert close busy", http.StatusTooManyRequests)
		return
	}
	switch r.URL.Path {
	case "/api/v1/alerts/search":
		var input struct {
			TenantID string          `json:"bk_tenant_id"`
			Query    json.RawMessage `json:"query"`
			After    string          `json:"after"`
			Limit    int             `json:"limit"`
		}
		if json.Unmarshal(body, &input) != nil || input.TenantID == "" || input.Limit < 1 || input.Limit > 500 {
			http.Error(w, "invalid alert search request", http.StatusBadRequest)
			return
		}
		alerts, next := link.page(input.TenantID, input.Query, false, input.After, input.Limit)
		items := []map[string]any{}
		for _, alert := range alerts {
			items = append(items, link.wire(alert))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next": next})
	case "/api/v1/alerts/close":
		// The link's own request (close_filter.go closeAlertsInput, Linkd
		// c5e2443a3), decoded strictly as it does: no page or cursor.
		var input struct {
			TenantID        string          `json:"bk_tenant_id"`
			OperationID     string          `json:"operation_id"`
			OperatorKind    string          `json:"operator_kind"`
			OperationSource string          `json:"operation_source"`
			OperatorID      string          `json:"operator_id"`
			Reason          string          `json:"reason"`
			EffectiveAt     string          `json:"effective_at"`
			Filter          json.RawMessage `json:"filter"`
			DryRun          bool            `json:"dry_run"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.TenantID == "" || len(input.Filter) == 0 {
			http.Error(w, "invalid filtered close request", http.StatusBadRequest)
			return
		}
		alerts, _ := link.page(input.TenantID, input.Filter, true, "", len(link.alerts)+1)
		items := []map[string]any{}
		closed := 0
		for _, alert := range alerts {
			if !input.DryRun && link.itemBusy != nil && link.itemBusy(alert.id) {
				items = append(items, map[string]any{"alert_id": alert.id, "status": http.StatusTooManyRequests})
				continue
			}
			if !input.DryRun && link.itemRefused != nil && link.itemRefused(alert.id) {
				items = append(items, map[string]any{"alert_id": alert.id, "status": http.StatusBadRequest, "error_code": "invalid_command"})
				continue
			}
			if !input.DryRun {
				alert.status, alert.reason = "closed", input.Reason
				alert.closes++
				closed++
			}
			items = append(items, map[string]any{"alert_id": alert.id, "status": http.StatusOK})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "has_more": false, "dry_run": input.DryRun, "closed": closed})
	default:
		http.NotFound(w, r)
	}
}

// newFakeLinkdRoute is the production closer, built from configuration as
// the bundle builds it, against the fake link and a real runtime Redis.
func newFakeLinkdRoute(t *testing.T, link *fakeLinkd) *filterRoute {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	plane := httptest.NewServer(link)
	t.Cleanup(plane.Close)
	var cfg config.Config
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("link client %v (%v)", client, err)
	}
	closer, err := newFilterCloser(cfg, client, server.Client(), time.Now, nil, nil)
	if err != nil || closer == nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
	return newFilterRoute(cfg, closer)
}

// quietLinkControlPlane is a link control plane holding no alert: every
// search finds nothing and every close by filter matches nothing. A
// deployment on the consumer's protocol has the control plane (the gate's
// copy is built from it, and answers this process's own sends without a
// search), so a fixture that runs that protocol names one.
func quietLinkControlPlane(t *testing.T) config.LinkdControlPlaneConfig {
	t.Helper()
	plane := httptest.NewServer(quietLink)
	t.Cleanup(plane.Close)
	return config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
}

// quietLink answers as a link holding no alert does.
var quietLink = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/alerts/search":
		_, _ = w.Write([]byte(`{"items":[],"next":""}`))
	case "/api/v1/alerts/close":
		var request struct {
			DryRun bool `json:"dry_run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "has_more": false, "dry_run": request.DryRun})
	default:
		http.NotFound(w, r)
	}
})

// sharedQuietLinkControlPlane is one quiet link for the test binary, for a
// fixture built without a test to clean up after (validGoAccessRuntimeConfig):
// the fixtures are a deployment that sends its alerts to the link, which
// does not load without its control plane (config.ErrLinkdControlPlaneRequired).
var sharedQuietLinkControlPlane = sync.OnceValue(func() config.LinkdControlPlaneConfig {
	return config.LinkdControlPlaneConfig{URL: httptest.NewServer(quietLink).URL, JWTSecret: "example-secret"}
})
