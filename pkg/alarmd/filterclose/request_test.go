// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
package filterclose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// requests is what ObserveRequest was told, in order.
type requests struct {
	mu   sync.Mutex
	seen []Request
}

func (r *requests) observe(request Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, request)
}

// closes is the results of the real closes told, in order.
func (r *requests) closes() []string { return r.results(false) }

// dryRuns is the results of the dry runs told, in order.
func (r *requests) dryRuns() []string { return r.results(true) }

func (r *requests) results(dryRun bool) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var results []string
	for _, request := range r.seen {
		if request.DryRun == dryRun {
			results = append(results, request.Result)
		}
	}
	return results
}

// Each filtered close request is told with how long it took and its
// result, in the link's batch shape: a page the link answers 200 whose
// items answer 429, 503 or 502 is told by the items' one status; a page
// some of whose items closed is items_partial; a request the link refuses
// whole is told by its status, and one the client gave up on as timeout.
// Dry runs are told ok.
func TestEachFilteredCloseRequestIsToldWithItsResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		// fault is every item's answer, or the half with an even number's.
		fault string
		half  bool
		busy  bool
		// broken is the real close answered 200 with a body cut short.
		broken bool
		want   string
	}{
		{name: "every item 429", fault: "busy", want: RequestItemsBusy},
		{name: "every item 503", fault: "unavailable", want: RequestItemsUnavailable},
		{name: "every item 502", fault: "uncertain_unsaved", want: RequestItems5xx},
		{name: "half the items 503", fault: "unavailable", half: true, want: RequestItemsPartial},
		{name: "every item closed", want: RequestOK},
		{name: "the request 429", busy: true, want: RequestBusy},
		{name: "a 200 that does not decode", broken: true, want: RequestInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 10; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
			if tc.fault != "" {
				h.link.itemFault = func(id string, _ int) string {
					var n int
					_, _ = fmt.Sscanf(id[len(id)-1:], "%d", &n)
					if tc.half && n%2 == 1 {
						return ""
					}
					return tc.fault
				}
			}
			if tc.broken {
				h.link.brokenAnswer = func(route string, n int) bool { return route == "/api/v1/alerts/close" && n == 2 }
			}
			if tc.busy {
				// The dry run, then the real close: the second request on
				// the route.
				h.link.requestBusy = func(route string, n int) bool { return route == "/api/v1/alerts/close" && n == 2 }
			}
			seen := &requests{}
			h.observe = seen.observe
			h.closer(t).Absent(context.Background(), testScope, DecisionAbsent, 3)
			closes := seen.closes()
			if len(closes) == 0 || closes[0] != tc.want {
				t.Fatalf("the real closes were told %v, want the first %s", closes, tc.want)
			}
			for _, request := range seen.seen {
				if request.DryRun && request.Result != RequestOK {
					t.Fatalf("a dry run was told %s", request.Result)
				}
			}
		})
	}
}

// A real close is told with the time it took: a request the client gave up
// on as timeout after its budget, and a page answered with at least its
// alerts' cost (50 at 200us, the page halved after the timeout).
func TestAFilteredCloseIsToldWithItsTime(t *testing.T) {
	h := slowLinkStrategy(t, 100)
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closerWithBudget(t, slowLinkRequest)
	closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
	var timeout *Request
	for i, request := range seen.seen {
		if !request.DryRun && request.Result == RequestTimeout {
			timeout = &seen.seen[i]
		}
	}
	if timeout == nil || timeout.Elapsed < slowLinkRequest {
		t.Fatalf("told %+v: want a page of 100 at %v an alert told timeout after at least %v", seen.seen, slowLinkAlert, slowLinkRequest)
	}
	// Set once the page given up on is served whole (under the link's
	// lock), at a cost a page answers well within its budget.
	h.link.setCloseDelay(200 * time.Microsecond)
	seen.mu.Lock()
	seen.seen = nil
	seen.mu.Unlock()
	closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
	for _, request := range seen.seen {
		if !request.DryRun && request.Result == RequestOK && request.Elapsed >= time.Millisecond {
			return
		}
	}
	t.Fatalf("told %+v: want a closed page told with at least its alerts' cost", seen.seen)
}

// failingLink is the harness's link whose filtered closes fail with err,
// as the client returns it, wrapped.
type failingLink struct {
	Link
	err error
}

func (link failingLink) CloseByFilter(context.Context, []byte, string, json.RawMessage, bool) (linkdmanage.FilterPage, error) {
	return linkdmanage.FilterPage{}, fmt.Errorf("close by filter: %w", link.err)
}

// A request whose answer could not be read, or that alarmd could not build
// and did not send, is told invalid_response, each kind by its own error;
// a request the link did not answer is told transport.
func TestARequestNotReadOrNotSentIsToldApartFromNoAnswer(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{linkdmanage.ErrInvalidResponse, RequestInvalidResponse},
		{linkdmanage.ErrCommandNotObject, RequestInvalidResponse},
		{linkdmanage.ErrInvalidRequest, RequestInvalidResponse},
		{linkdmanage.ErrBodyTooLarge, RequestInvalidResponse},
		{errors.New("dial tcp: connection refused"), RequestTransport},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			h := newHarness(t)
			h.link.add(&fakeAlert{id: alertID(0), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			h.opened(t, "41", "3", nil, fingerprints(0, 1)...)
			seen := &requests{}
			h.observe = seen.observe
			closer := h.closer(t)
			closer.link = failingLink{Link: closer.link, err: tc.err}
			closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
			if len(seen.seen) == 0 || seen.seen[0].Result != tc.want {
				t.Fatalf("told %+v, want the first request %s", seen.seen, tc.want)
			}
		})
	}
}

// A 200 whose body stalls past the request's budget is a timeout, not an
// unreadable answer: told timeout, and as for any real close whose answer
// the client gave up on, the candidates its dry run read are kept to retry
// by id.
func TestA200WhoseBodyStallsIsToldTimeoutAndItsCandidatesKept(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
	// The dry run, then the real close.
	h.link.stallAnswer = func(route string, n int) bool { return route == "/api/v1/alerts/close" && n == 2 }
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closerWithBudget(t, slowLinkRequest)
	closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
	if closes := seen.closes(); len(closes) == 0 || closes[0] != RequestTimeout {
		t.Fatalf("the real closes were told %v, want the first timeout", closes)
	}
	if closer.Stats()[OutcomePageLost] != 1 || closer.PendingSize(context.Background()) != 10 {
		t.Fatalf("pending %d, stats %v: want the dry run's 10 candidates kept", closer.PendingSize(context.Background()), nonZero(closer.Stats()))
	}
}

// Each item answer of a real close lands on its own outcome, status by
// status as alert-close.md lists them: 200, 400 invalid_command, 403
// tenant_mismatch, 409 invalid_transition, version_conflict and
// source_unpublished, 404, 429, 502 (kept to retry by id), 503, and 504
// not_attempted for what the link's budget did not admit. The ones not
// retried are each counted by name.
func TestEachItemAnswerLandsOnItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		fault, outcome string
		notAttempted   bool
	}{
		{"", OutcomeItemClosed, false},
		{"ended", OutcomeItemEndedOtherwise, false},
		{"conflict", OutcomeItemRederived, false},
		{"unpublished", OutcomeItemSourceUnpublished, false},
		{"gone", OutcomeItemNotFound, false},
		{"invalid", OutcomeItemInvalidCommand, false},
		{"foreign", OutcomeItemTenantMismatch, false},
		{"busy", OutcomeItemRederived, false},
		{"uncertain_unsaved", OutcomeItemPending, false},
		{"unavailable", OutcomeItemUnavailable, false},
		{"", OutcomeItemRederived, true},
	} {
		t.Run(tc.outcome+"/"+tc.fault, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 2; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(0, 2)...)
			if tc.fault != "" {
				h.link.itemFault = func(string, int) string { return tc.fault }
			}
			if tc.notAttempted {
				// One attempted, the other enumerated and not admitted.
				h.link.closeBudget, h.link.notAttempted = 1, 1
			}
			closer := h.closer(t)
			closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
			if got := closer.Stats()[tc.outcome]; got == 0 {
				t.Fatalf("stats %v, want %s", nonZero(closer.Stats()), tc.outcome)
			}
		})
	}
}

// A 200 whose later candidate query failed (error_code query_failed) is
// counted, told query_failed, and its items are answered: the dry run's
// ids are closed, and the job goes on with the same filter (has_more) and
// closes every alert once.
func TestAnAnswerWhoseLaterQueryFailedKeepsItsItems(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 30; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 30)...)
	h.link.queryFailAfter = 10
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("word %s, stats %v: want the job done", word, nonZero(closer.Stats()))
	}
	stats := closer.Stats()
	if stats[OutcomeLinkQueryFailed] == 0 || stats[OutcomeItemClosed] != 30 {
		t.Fatalf("stats %v: want query_failed counted and all 30 closed", nonZero(stats))
	}
	if dry := seen.dryRuns(); len(dry) == 0 || dry[0] != RequestQueryFailed {
		t.Fatalf("the dry runs were told %v, want the first query_failed", dry)
	}
	if closes := seen.closes(); len(closes) != 3 {
		t.Fatalf("the real closes were told %v, want one per 10 ids the dry runs read", closes)
	}
}

// An item a real close answered 502 result_uncertain may have closed the
// alert with its outputs unfinished: it is kept, and the next round's retry
// by id sends the very command the filtered close did - the same
// operation_id - which completes it. Nothing else is closed again.
func TestAnUncertainItemIsCompletedByIdWithTheSameOperation(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 5)...)
	h.link.itemFault = func(id string, n int) string {
		if id == alertID(2) && n == 0 {
			return "uncertain_saved"
		}
		return ""
	}
	closer := h.closer(t)
	closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
	if stats := closer.Stats(); stats[OutcomeItemPending] != 1 || closer.PendingSize(context.Background()) != 1 {
		t.Fatalf("stats %v: want the uncertain item kept", nonZero(stats))
	}
	if alert := h.link.alert(alertID(2)); alert.closes != 1 || alert.completed {
		t.Fatalf("the fault did not leave the close saved with its outputs unfinished: %+v", alert)
	}
	h.link.mu.Lock()
	operations := len(h.link.operations)
	h.link.mu.Unlock()
	closer.RetryPending(context.Background(), 100)
	h.link.mu.Lock()
	after := len(h.link.operations)
	h.link.mu.Unlock()
	if alert := h.link.alert(alertID(2)); alert.closes != 1 || !alert.completed || after != operations || operations != 1 {
		t.Fatalf("closes %d completed %v, operations %d -> %d (stats %v): want the one operation completed by id",
			alert.closes, alert.completed, operations, after, nonZero(closer.Stats()))
	}
	for i := 0; i < 5; i++ {
		if alert := h.link.alert(alertID(i)); alert.closes != 1 {
			t.Fatalf("%s closed %d times", alert.id, alert.closes)
		}
	}
}

// A dry run whose answer runs past the client's bound - more ids than it
// holds - is its own word and request result, not link_failed and not an
// unreadable answer: nothing is closed, and the reading says the filter
// matches past what alarmd reads.
func TestADryRunPastTheClientsBoundIsNamed(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 100; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 100)...)
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closer(t)
	bounded, err := linkdmanage.New(linkdmanage.Options{BaseURL: h.base, Secret: "example-secret", Username: "alarmd", Client: &http.Client{}, MaxBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	closer.link = bounded
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeLinkAnswerTooLarge {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if stats := closer.Stats(); stats[OutcomeLinkFailed] != 0 || stats[OutcomeExpectExceeded] != 0 {
		t.Fatalf("stats %v: want only link_answer_too_large", nonZero(stats))
	}
	if dry := seen.dryRuns(); len(dry) != 1 || dry[0] != RequestAnswerTooLarge || len(seen.closes()) != 0 {
		t.Fatalf("dry runs %v, closes %v: want one answer_too_large and no close", dry, seen.closes())
	}
	if closed := closedAlerts(t, h, 100); closed != 0 {
		t.Fatalf("closed %d", closed)
	}
}
