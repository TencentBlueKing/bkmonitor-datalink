// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// fakeLink is the link's three routes over alerts in memory, written from
// the link's contracts and handlers at b7e5beddf (docs/reference/contracts/
// alert-search.md, alert-close.md; internal/controlplane/api/
// alert_search.go, close_filter.go, close.go; internal/lifecycle/
// processor.go CloseAlert): the request fields and their bounds, unknown
// fields refused, the server's tenant and status=active added to a
// filtered close, pages in alert id order with a next cursor, the per-item
// statuses, already_closed for the same command and invalid_transition for
// another, a single close's errors as text. Its query evaluator covers
// what alarmd sends: bool (filter, must_not, should with
// minimum_should_match), term, terms, exists, every value compared as the
// keyword the link indexes, and range on create_at (date_nanos).
//
// Faults are injected per request or per item, and every close is counted
// per alert, so a test can say each alert was closed exactly once.
type fakeLink struct {
	t  *testing.T
	mu sync.Mutex

	alerts map[string]*fakeAlert
	sorted []string
	// operations is each operation id's command, field for field: the link
	// refuses the same id with other content.
	operations map[string]string
	requests   map[string]int

	// requestBusy answers a whole request 429 when it returns true, given
	// the route and the request's number on it.
	requestBusy func(route string, n int) bool
	// attemptBudget is how many items one filtered close attempts before
	// the rest answer 504 not_attempted; 0 is no bound.
	attemptBudget int
	// itemFault is an item's injected answer: "busy" (429, nothing saved),
	// "conflict" (409 version_conflict, nothing saved), "uncertain_saved"
	// (502, the close saved), "uncertain_unsaved" (502, nothing saved), or
	// "" for none. n is how many times the alert was attempted before.
	itemFault func(alertID string, n int) string
	// beforeClose runs when a filtered close (not a dry run) arrives,
	// before it selects its candidates, under the lock. request is the
	// close's own request, whose context ends when the caller stops waiting.
	beforeClose func(link *fakeLink, request *http.Request)
	// loseAnswer, when it returns true for a real close (its number among
	// them), runs the close and then answers 502 with no items: the
	// answer lost after the closes were saved.
	loseAnswer func(n int) bool
	realCloses int
	// closeBodies is every filtered close's (not a dry run's) filter.
	closeBodies []string
	unsupported bool
}

type fakeAlert struct {
	id, tenant, source, status, severity, fingerprint string
	labels, dimensions                                map[string]string
	extra                                             json.RawMessage
	// created is when the link opened it; add sets fakeOpenedAt when zero.
	created time.Time
	// closes counts saved closes; endOperation is the saved command.
	closes       int
	endOperation string
	attempts     int
	// completed is a close the link answered 200 for: its outputs done.
	completed bool
}

func newFakeLink(t *testing.T) *fakeLink {
	return &fakeLink{t: t, alerts: map[string]*fakeAlert{}, operations: map[string]string{}, requests: map[string]int{}}
}

func (link *fakeLink) add(alert *fakeAlert) {
	link.mu.Lock()
	defer link.mu.Unlock()
	if alert.status == "" {
		alert.status = "active"
	}
	if alert.tenant == "" {
		alert.tenant = "tenant-a"
	}
	if alert.source == "" {
		alert.source = "built_in_bk"
	}
	if alert.created.IsZero() {
		alert.created = fakeOpenedAt
	}
	link.alerts[alert.id] = alert
	link.sorted = nil
}

// fakeOpenedAt is when an alert that names no other was opened; a test's
// decisions are read after it (fakeDecidedAt).
// The record a test writes starts at fakeRecordStart, recordSettle and
// more before its entries (fakeEntryOpened) and the alerts that name no
// other opening time, so it vouches for them.
// testPeriod is how often a test's strategy is evaluated: a minute, so its
// record vouches recordSettle after its start.
const testPeriod = time.Minute

var (
	fakeOpenedAt    = time.Unix(1_700_000_000, 0)
	fakeDecidedAt   = fakeOpenedAt.Add(time.Hour)
	fakeRecordStart = fakeOpenedAt.Add(-recordSettle - time.Hour)
	fakeEntryOpened = fakeOpenedAt.Add(-time.Minute)
)

func (link *fakeLink) alert(id string) fakeAlert {
	link.mu.Lock()
	defer link.mu.Unlock()
	return *link.alerts[id]
}

// field is an alert's value for a queried field, as the index keeps it.
func (alert *fakeAlert) field(name string) (string, bool) {
	switch {
	case name == fieldTenant:
		return alert.tenant, true
	case name == fieldSource:
		return alert.source, true
	case name == fieldAlertID:
		return alert.id, true
	case name == fieldPrint:
		return alert.fingerprint, alert.fingerprint != ""
	case name == fieldStatus:
		return alert.status, true
	case name == fieldLevel:
		return alert.severity, alert.severity != ""
	case name == fieldCreated:
		return alert.created.UTC().Format(time.RFC3339Nano), !alert.created.IsZero()
	case strings.HasPrefix(name, "labels."):
		value, ok := alert.labels[strings.TrimPrefix(name, "labels.")]
		return value, ok
	case strings.HasPrefix(name, "dimensions."):
		value, ok := alert.dimensions[strings.TrimPrefix(name, "dimensions.")]
		return value, ok
	}
	return "", false
}

func keyword(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

// compile turns a query object into a predicate; an unknown clause is a
// 400 at the link and a test failure here.
func (link *fakeLink) compile(query json.RawMessage) func(*fakeAlert) bool {
	never := func(*fakeAlert) bool { return false }
	var clause map[string]json.RawMessage
	if err := json.Unmarshal(query, &clause); err != nil || len(clause) != 1 {
		link.t.Errorf("query %s is not one clause", query)
		return never
	}
	for kind, body := range clause {
		switch kind {
		case "match_all":
			return func(*fakeAlert) bool { return true }
		case "term":
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(body, &fields)
			for name, value := range fields {
				want := keyword(value)
				return func(alert *fakeAlert) bool {
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
					set[keyword(value)] = true
				}
				return func(alert *fakeAlert) bool {
					got, ok := alert.field(name)
					return ok && set[got]
				}
			}
		case "range":
			var fields map[string]map[string]string
			if json.Unmarshal(body, &fields) != nil || len(fields) != 1 {
				link.t.Errorf("range %s is not one field's bounds", body)
				return never
			}
			for name, bounds := range fields {
				if name != fieldCreated {
					link.t.Errorf("range on %s, the fake ranges create_at only", name)
					return never
				}
				return func(alert *fakeAlert) bool {
					for op, text := range bounds {
						at, err := time.Parse(time.RFC3339Nano, text)
						if err != nil {
							link.t.Errorf("range bound %q: %v", text, err)
							return false
						}
						switch op {
						case "lte":
							if alert.created.After(at) {
								return false
							}
						case "lt":
							if !alert.created.Before(at) {
								return false
							}
						case "gte":
							if alert.created.Before(at) {
								return false
							}
						case "gt":
							if !alert.created.After(at) {
								return false
							}
						default:
							link.t.Errorf("range operator %s", op)
							return false
						}
					}
					return true
				}
			}
		case "exists":
			var field struct {
				Field string `json:"field"`
			}
			_ = json.Unmarshal(body, &field)
			return func(alert *fakeAlert) bool {
				_, ok := alert.field(field.Field)
				return ok
			}
		case "bool":
			var b struct {
				Filter  []json.RawMessage `json:"filter"`
				MustNot []json.RawMessage `json:"must_not"`
				Should  []json.RawMessage `json:"should"`
				Minimum int               `json:"minimum_should_match"`
			}
			if err := json.Unmarshal(body, &b); err != nil {
				link.t.Errorf("bool %s: %v", body, err)
				return never
			}
			compileAll := func(raw []json.RawMessage) []func(*fakeAlert) bool {
				out := make([]func(*fakeAlert) bool, 0, len(raw))
				for _, sub := range raw {
					out = append(out, link.compile(sub))
				}
				return out
			}
			filter, mustNot, should := compileAll(b.Filter), compileAll(b.MustNot), compileAll(b.Should)
			minimum := b.Minimum
			if minimum == 0 && len(b.Filter) == 0 {
				minimum = 1
			}
			return func(alert *fakeAlert) bool {
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
				if len(should) == 0 {
					return true
				}
				hit := 0
				for _, match := range should {
					if match(alert) {
						hit++
					}
				}
				return hit >= minimum
			}
		}
	}
	link.t.Errorf("query clause %s is not one the fake evaluates", query)
	return never
}

// page is the tenant's matching alerts after the cursor, in id order.
func (link *fakeLink) page(tenant string, query json.RawMessage, activeOnly bool, after string, limit int) ([]*fakeAlert, string) {
	if link.sorted == nil {
		for id := range link.alerts {
			link.sorted = append(link.sorted, id)
		}
		sort.Strings(link.sorted)
	}
	match := func(*fakeAlert) bool { return true }
	if len(query) > 0 {
		match = link.compile(query)
	}
	start := sort.SearchStrings(link.sorted, after)
	var out []*fakeAlert
	for _, id := range link.sorted[start:] {
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

func (link *fakeLink) wire(alert *fakeAlert) linkdmanage.Alert {
	labels := map[string]json.RawMessage{}
	for name, value := range alert.labels {
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			labels[name] = json.RawMessage(value)
		} else {
			labels[name], _ = json.Marshal(value)
		}
	}
	dimensions := map[string]json.RawMessage{}
	for name, value := range alert.dimensions {
		dimensions[name], _ = json.Marshal(value)
	}
	if _, tagged := alert.dimensions[contract.NoDataDimensionTag]; tagged {
		// The link keeps the tag as alarmd writes it: the JSON boolean true.
		dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
	}
	return linkdmanage.Alert{AlertID: alert.id, TenantID: alert.tenant, EventSourceID: alert.source, Status: alert.status,
		Severity: alert.severity, Labels: labels, Dimensions: dimensions, ExtraData: alert.extra, CreateAt: alert.created}
}

type fakeCommand struct {
	TenantID        string `json:"bk_tenant_id"`
	OperationID     string `json:"operation_id"`
	OperatorID      string `json:"operator_id"`
	OperatorKind    string `json:"operator_kind"`
	OperationSource string `json:"operation_source"`
	Reason          string `json:"reason"`
	EffectiveAt     string `json:"effective_at"`
}

func (command fakeCommand) valid() bool {
	at, err := time.Parse(time.RFC3339, command.EffectiveAt)
	return command.OperationID != "" && len(command.OperationID) <= 128 && command.OperatorID != "" && command.OperatorKind == "system" &&
		command.OperationSource == "strategy_change" && command.Reason != "" && len(command.Reason) <= 256 && err == nil &&
		!at.After(time.Now().Add(5*time.Minute))
}

// closeOne is CloseAlert: status and error code, under the lock.
func (link *fakeLink) closeOne(command fakeCommand, id string) (int, string, bool) {
	identity, _ := json.Marshal(command)
	if known, ok := link.operations[command.OperationID]; ok && known != string(identity) {
		return http.StatusConflict, linkdmanage.ConflictInvalidTransition, false
	}
	link.operations[command.OperationID] = string(identity)
	alert, ok := link.alerts[id]
	if !ok || alert.tenant != command.TenantID {
		return http.StatusNotFound, "not_found", false
	}
	fault := ""
	if link.itemFault != nil {
		fault = link.itemFault(id, alert.attempts)
	}
	alert.attempts++
	switch fault {
	case "busy":
		return http.StatusTooManyRequests, "busy", false
	case "conflict":
		return http.StatusConflict, linkdmanage.ConflictVersion, false
	case "uncertain_unsaved":
		return http.StatusBadGateway, "result_uncertain", false
	}
	if alert.status != "active" {
		if alert.status == "closed" && alert.endOperation == string(identity) {
			if fault != "uncertain_saved" {
				alert.completed = true
			}
			return http.StatusOK, "", true
		}
		return http.StatusConflict, linkdmanage.ConflictInvalidTransition, false
	}
	alert.status, alert.endOperation = "closed", string(identity)
	alert.closes++
	if fault == "uncertain_saved" {
		return http.StatusBadGateway, "result_uncertain", false
	}
	alert.completed = true
	return http.StatusOK, "", false
}

var errorMessages = map[string]string{
	linkdmanage.ConflictInvalidTransition: "alert state changed; refresh before another operation",
	linkdmanage.ConflictVersion:           "alert version changed; retry the same operation",
	"busy":                                "alert is being processed; retry the same operation",
	"result_uncertain":                    "close result uncertain; retry the same operation",
	"not_found":                           "alert or source not found",
}

func decodeStrict(r *http.Request, maximum int64, target any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		return errors.New("too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing JSON")
	}
	return nil
}

func (link *fakeLink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	link.mu.Lock()
	defer link.mu.Unlock()
	route := r.URL.Path
	if strings.HasSuffix(route, "/close") && route != linkdmanage.RouteCloseFilter {
		route = "close_one"
	}
	link.requests[route]++
	if !strings.HasPrefix(r.Header.Get("Internal-Token"), "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if link.unsupported && route != "close_one" {
		http.Error(w, "alert search requires Elasticsearch storage", http.StatusNotImplemented)
		return
	}
	if link.requestBusy != nil && link.requestBusy(route, link.requests[route]) {
		http.Error(w, "alert search busy", http.StatusTooManyRequests)
		return
	}
	switch route {
	case linkdmanage.RouteSearch:
		var input struct {
			TenantID string          `json:"bk_tenant_id"`
			Query    json.RawMessage `json:"query"`
			After    string          `json:"after"`
			Limit    int             `json:"limit"`
		}
		if err := decodeStrict(r, linkdmanage.MaxFilterBodyBytes, &input); err != nil || input.TenantID == "" || input.Limit < 1 || input.Limit > 500 {
			http.Error(w, "invalid alert search request", http.StatusBadRequest)
			return
		}
		alerts, next := link.page(input.TenantID, input.Query, false, input.After, input.Limit)
		page := linkdmanage.SearchPage{Items: []linkdmanage.Alert{}, Next: next}
		for _, alert := range alerts {
			page.Items = append(page.Items, link.wire(alert))
		}
		_ = json.NewEncoder(w).Encode(page)
	case linkdmanage.RouteCloseFilter:
		var input struct {
			fakeCommand
			Filter json.RawMessage `json:"filter"`
			After  string          `json:"after"`
			Limit  int             `json:"limit"`
			DryRun bool            `json:"dry_run"`
		}
		if err := decodeStrict(r, linkdmanage.MaxFilterBodyBytes, &input); err != nil || input.TenantID == "" || len(input.Filter) == 0 ||
			input.Limit < 1 || input.Limit > 500 || (!input.DryRun && !input.valid()) {
			http.Error(w, "invalid filtered close request", http.StatusBadRequest)
			return
		}
		if !input.DryRun {
			link.closeBodies = append(link.closeBodies, string(input.Filter))
		}
		if !input.DryRun && link.beforeClose != nil {
			link.beforeClose(link, r)
		}
		alerts, next := link.page(input.TenantID, input.Filter, true, input.After, input.Limit)
		page := linkdmanage.FilterPage{Items: []linkdmanage.Item{}, Next: next, DryRun: input.DryRun}
		lost := false
		if !input.DryRun {
			link.realCloses++
			lost = link.loseAnswer != nil && link.loseAnswer(link.realCloses)
		}
		for i, alert := range alerts {
			item := linkdmanage.Item{AlertID: alert.id, Status: http.StatusOK}
			switch {
			case input.DryRun:
			case link.attemptBudget > 0 && i >= link.attemptBudget:
				item.Status, item.ErrorCode = http.StatusGatewayTimeout, "not_attempted"
			default:
				status, code, already := link.closeOne(input.fakeCommand, alert.id)
				item.Status, item.ErrorCode, item.AlreadyClosed = status, code, already
			}
			if item.Status == http.StatusOK && !input.DryRun {
				page.Closed++
			} else if item.Status != http.StatusOK {
				page.Failed++
			}
			page.Items = append(page.Items, item)
		}
		if lost {
			http.Error(w, "upstream answer lost", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(page)
	case "close_one":
		var command fakeCommand
		if err := decodeStrict(r, linkdmanage.MaxCloseBodyBytes, &command); err != nil || !command.valid() {
			http.Error(w, "invalid close command", http.StatusBadRequest)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/"), "/close")
		status, code, already := link.closeOne(command, id)
		if status != http.StatusOK {
			http.Error(w, errorMessages[code], status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"alert": map[string]string{"alert_id": id}, "already_closed": already})
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// harness is a Closer wired to a fake link over HTTP and a real Redis.
type harness struct {
	link   *fakeLink
	client *linkdmanage.Client
	redis  *redistest.Instance
	prefix string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	link := newFakeLink(t)
	http := httptest.NewServer(link)
	t.Cleanup(http.Close)
	client, err := linkdmanage.New(linkdmanage.Options{BaseURL: http.URL, Secret: "example-secret", Username: "alarmd", Client: http.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{link: link, client: client, redis: server, prefix: "alarmd-test"}
}

// closer is a new Closer, as a new leader or a restarted process builds
// one: nothing in memory, the same Redis.
func (h *harness) closer(t *testing.T) *Closer {
	t.Helper()
	commands, err := NewCommands(h.redis.Client(), h.prefix)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := NewPending(h.redis.Client(), h.prefix, MaxPending)
	if err != nil {
		t.Fatal(err)
	}
	closer, err := New(Options{Link: h.client, Commands: commands, Pending: pending, Record: h.record(t)})
	if err != nil {
		t.Fatal(err)
	}
	return closer
}

// record is the store of alarmd's episode record on the harness's Redis.
func (h *harness) record(t *testing.T) *openepisodes.Store {
	t.Helper()
	store, err := openepisodes.NewStore(h.redis.Client(), h.prefix)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// opened records episodes of the strategy at a version as a worker does
// after the broker's ACK: each fingerprint's ABNORMAL envelope through
// openepisodes.ChangeOf into the store. target, when given, is the Plan's
// target identity dimensions and the series' values of them.
func (h *harness) opened(t *testing.T, strategy, version string, target map[string]string, fingerprints ...string) {
	t.Helper()
	revision, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var changes []openepisodes.Change
	for _, fingerprint := range fingerprints {
		event := contract.TriggerEventV1{EventKind: contract.TriggerEventAbnormal, TenantID: testScope.TenantID,
			PlanRef: contract.RuntimePlanRefV1{StrategyID: strategy}, StrategyRef: &contract.StrategySnapshotRef{Revision: revision},
			DedupeMD5: fingerprint, RecordRef: contract.TriggerRecordRefV1{Dimensions: map[string]json.RawMessage{}}}
		for name, value := range target {
			event.TargetDimensions = append(event.TargetDimensions, name)
			event.RecordRef.Dimensions[name], _ = json.Marshal(value)
		}
		change, ok := openepisodes.ChangeOf(event, fakeEntryOpened)
		if !ok {
			t.Fatalf("no change for %s", fingerprint)
		}
		changes = append(changes, change)
	}
	// The record started at fakeRecordStart, long enough before for it to
	// vouch for what it holds (recordSettle).
	h.redis.Client().HSetNX(context.Background(), h.prefix+":openepisodes:v1:"+testScope.TenantID+":"+strategy, openepisodes.SinceField,
		strconv.FormatInt(fakeRecordStart.Unix(), 10))
	if err := h.record(t).Apply(context.Background(), changes, nil); err != nil {
		t.Fatal(err)
	}
}

// fingerprints are n distinct fingerprints from start.
func fingerprints(start, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%032x", start+i)
	}
	return out
}

var testScope = Scope{TenantID: "tenant-a", EventSourceID: "built_in_bk", StrategyID: "41"}

func alertID(n int) string { return fmt.Sprintf("alert-%06d", n) }
