// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
package filterclose

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// Times scaled down a hundredfold from production: a request is given up
// after its budget (15 s), a round's absent closes have half the round
// (150 s). slowLinkAlert is a fake link's cost per alert where a test
// needs requests to take time.
const (
	slowLinkAlert   = 10 * time.Millisecond
	slowLinkRequest = 150 * time.Millisecond
	slowLinkRound   = 1500 * time.Millisecond
)

// slowLinkStrategy is strategy 41 at version 3 with n active alerts at a
// link that closes each in slowLinkAlert, and alarmd's own record of all n.
// The link's own budget per request is the test's to set.
func slowLinkStrategy(t *testing.T, n int) *harness {
	t.Helper()
	h := newHarness(t)
	for i := 0; i < n; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, n)...)
	h.link.setCloseDelay(slowLinkAlert)
	return h
}

func closedAlerts(t *testing.T, h *harness, n int) int {
	t.Helper()
	closed := 0
	for i := 0; i < n; i++ {
		switch h.link.alert(alertID(i)).closes {
		case 0:
		case 1:
			closed++
		default:
			t.Fatalf("%s closed %d times", alertID(i), h.link.alert(alertID(i)).closes)
		}
	}
	return closed
}

// realCloses is how many real filtered closes the link served.
func realCloses(h *harness) int {
	h.link.mu.Lock()
	defer h.link.mu.Unlock()
	return h.link.realCloses
}

// A job sends requests while the link answers has_more, each closing what
// the link's own budget allows, until one has no more: one run closes every
// alert once (the link takes no page or cursor; alert-close.md, Linkd
// c5e2443a3). A job that stopped after its first request would close what
// one budget allows.
func TestAJobSendsRequestsUntilTheLinkHasNoMore(t *testing.T) {
	const n, budget = 400, 50
	h := slowLinkStrategy(t, n)
	h.link.setCloseDelay(0)
	h.link.closeBudget = budget
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("word %s, stats %v: want the job done", word, nonZero(closer.Stats()))
	}
	if closed, requests := closedAlerts(t, h, n), realCloses(h); closed != n || requests != n/budget {
		t.Fatalf("closed %d of %d in %d requests, want all in %d", closed, n, requests, n/budget)
	}
}

// The round's time ends a job partway: requests stop when the round does,
// what they closed is closed once, and the rest is left for the next round.
func TestTheRoundsTimeEndsAJobPartway(t *testing.T) {
	const n, budget = 400, 20
	h := slowLinkStrategy(t, n)
	h.link.closeBudget = budget
	closer := h.closer(t)
	round := 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), round) // fixed-wait: the round's time is the behaviour under test
	defer cancel()
	word := closer.Absent(ctx, testScope, DecisionAbsent, 3)
	closed := closedAlerts(t, h, n)
	if word == OutcomeJobDone || closed == 0 || closed >= n {
		t.Fatalf("word %s closed %d of %d, stats %v: want the round to end the job partway", word, closed, n, nonZero(closer.Stats()))
	}
}

// A real close whose answer the client gave up on keeps the candidates its
// dry run read, and they are retried by id (RetryPending): an alert whose
// close was saved no longer matches active, so no filter finds it again,
// and its outputs are completed only by that retry.
func TestARequestTheClientGaveUpOnIsStillRetriedById(t *testing.T) {
	const n = 100
	h := slowLinkStrategy(t, n)
	closer := h.closerWithBudget(t, slowLinkRequest)
	closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
	kept := closer.PendingSize(context.Background())
	if kept == 0 || closer.Stats()[OutcomePageLost] == 0 {
		t.Fatalf("kept %d, stats %v: want the timed-out request's candidates kept", kept, nonZero(closer.Stats()))
	}
	h.link.setCloseDelay(0)
	closer.RetryPending(context.Background(), MaxPending)
	stats := closer.Stats()
	if retried := stats[OutcomePendingClosed] + stats[OutcomePendingAlreadyClosed] + stats[OutcomePendingLeftDecision]; retried == 0 {
		t.Fatalf("stats %v: want the kept candidates retried by id", nonZero(stats))
	}
	for i := 0; i < n; i++ {
		if alert := h.link.alert(alertID(i)); alert.closes > 0 && !alert.completed {
			t.Fatalf("%s closed by the timed-out request and never completed by id", alertID(i))
		}
	}
}

// A job left unsettled (items busy) is run again within the round while
// the round has a request's budget left, and not once it has less: the
// round's other half (the edit close) keeps its time.
func TestAJobIsRunAgainOnlyWhileTheRoundHasARequestLeft(t *testing.T) {
	for _, tc := range []struct {
		name  string
		round time.Duration
		again bool
	}{
		{"a request left", slowLinkRound, true},
		{"less than a request left", slowLinkRequest / 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := slowLinkStrategy(t, 50)
			h.link.setCloseDelay(0)
			h.link.itemFault = func(id string, n int) string {
				if n == 0 && spread(id, 2) {
					return "busy"
				}
				return ""
			}
			closer := h.closerWithBudget(t, slowLinkRequest)
			ctx, cancel := context.WithTimeout(context.Background(), tc.round) // fixed-wait: the round's time is the behaviour under test
			defer cancel()
			closer.UntilProgressStops(ctx, func() Result { return closer.AbsentResult(ctx, testScope, DecisionAbsent, 3) })
			if again := closer.Stats()[OutcomeJobAgain] > 0; again != tc.again {
				t.Fatalf("run again %v, want %v: %v", again, tc.again, nonZero(closer.Stats()))
			}
		})
	}
}

// A job that closed nothing is not run again: a link that is down costs
// one run a round, as before.
func TestAJobThatGotNowhereIsNotRunAgain(t *testing.T) {
	h := slowLinkStrategy(t, 50)
	h.link.setCloseDelay(0)
	h.link.itemFault = func(string, int) string { return "busy" }
	closer := h.closerWithBudget(t, slowLinkRequest)
	ctx, cancel := context.WithTimeout(context.Background(), slowLinkRound) // fixed-wait: the round's time is the behaviour under test
	defer cancel()
	word := closer.UntilProgressStops(ctx, func() Result { return closer.AbsentResult(ctx, testScope, DecisionAbsent, 3) }).Word
	if word != OutcomeJobDoneRederived || closer.Stats()[OutcomeJobAgain] != 0 {
		t.Fatalf("word %s stats %v: want one run, rederived", word, nonZero(closer.Stats()))
	}
}

// The expectation without a page: alarmd's record holds
// 100 of the strategy's episodes, which allows 120 closes; the link holds
// 300. A dry run that enumerated every match and found more than allowed
// refuses; so does one that stopped on its budget (has_more) having
// already read more; and where each dry run's read fits, the job's closes
// stop once they reach the expectation, the rest left.
func TestTheExpectationHoldsWithoutAPage(t *testing.T) {
	for _, tc := range []struct {
		name                string
		dryBudget, budget   int
		word                string
		closed              int
		refused, capReached bool
	}{
		{name: "an exact dry run past the expectation", word: OutcomeExpectExceeded, refused: true},
		{name: "a dry run with more already past it", dryBudget: 150, word: OutcomeExpectExceeded, refused: true},
		{name: "a dry run with more at it exactly", dryBudget: 120, word: OutcomeExpectExceeded, refused: true},
		{name: "closes up to it, then stop", dryBudget: 60, budget: 50, word: OutcomeJobIncomplete, closed: 100, capReached: true},
		// The close could close all 300; it is narrowed to the 60 ids its
		// dry run read.
		{name: "a close stays within its dry run's ids", dryBudget: 60, word: OutcomeJobIncomplete, closed: 60, capReached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 300; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(0, 100)...)
			h.link.dryBudget, h.link.closeBudget = tc.dryBudget, tc.budget
			closer := h.closer(t)
			word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3)
			stats := closer.Stats()
			closed := closedAlerts(t, h, 300)
			if word != tc.word || closed != tc.closed || (stats[OutcomeExpectExceeded] > 0) != tc.refused ||
				(stats[OutcomeCloseCapReached] > 0) != tc.capReached || closed > 120 {
				t.Fatalf("word %s closed %d, stats %v: want %s, %d closed, refused %v, cap %v", word, closed, nonZero(stats),
					tc.word, tc.closed, tc.refused, tc.capReached)
			}
		})
	}
}

// A request that carries a page or a cursor is refused whole by the link
// (it decodes strictly), and none does: every request a job sends is
// answered.
func TestNoRequestCarriesAPageOrACursor(t *testing.T) {
	h := slowLinkStrategy(t, 30)
	h.link.setCloseDelay(0)
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("word %s, stats %v", word, nonZero(closer.Stats()))
	}
	for _, request := range seen.seen {
		if request.Result != RequestOK {
			t.Fatalf("a request was told %s: %+v", request.Result, seen.seen)
		}
	}
	// The same link refuses a request that carries either, as the link does
	// (decodeAlertQuery, DisallowUnknownFields): the check above is real.
	for _, field := range []string{`"limit":100`, `"after":"x"`} {
		request, err := http.NewRequest(http.MethodPost, h.base+linkdmanage.RouteCloseFilter,
			strings.NewReader(`{"bk_tenant_id":"system","filter":{"match_all":{}},"dry_run":true,`+field+`}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Internal-Token", "Bearer fixture")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("a request carrying %s answered %d, want 400", field, response.StatusCode)
		}
	}
}

// A request that closed nothing while the link still answered has_more
// (every item busy) ends the job incomplete instead of sending the same
// request again at once: the next run or round goes on.
func TestARequestThatClosedNothingEndsTheJob(t *testing.T) {
	h := slowLinkStrategy(t, 50)
	h.link.setCloseDelay(0)
	h.link.closeBudget = 10
	h.link.itemFault = func(string, int) string { return "busy" }
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobIncomplete || realCloses(h) != 1 {
		t.Fatalf("word %s after %d real closes, stats %v: want one request, incomplete", word, realCloses(h), nonZero(closer.Stats()))
	}
}

// A round with less than one request's budget left starts no request: a
// close it cut off would lose its answer and keep every candidate to retry
// by id. The job ends incomplete, nothing sent and nothing kept; with the
// round's time, the same job closes everything.
func TestARoundNearlySpentStartsNoRequest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		round    time.Duration
		requests bool
	}{
		{"less than a request left", slowLinkRequest / 2, false},
		{"the round's time left", slowLinkRound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := slowLinkStrategy(t, 20)
			h.link.setCloseDelay(0)
			seen := &requests{}
			h.observe = seen.observe
			closer := h.closerWithBudget(t, slowLinkRequest)
			ctx, cancel := context.WithTimeout(context.Background(), tc.round) // fixed-wait: the round's time is the behaviour under test
			defer cancel()
			word := closer.Absent(ctx, testScope, DecisionAbsent, 3)
			sent := len(seen.seen) > 0
			if sent != tc.requests || closer.PendingSize(context.Background()) != 0 ||
				(!tc.requests && (word != OutcomeJobIncomplete || closer.Stats()[OutcomeRoundSpent] != 1)) ||
				(tc.requests && (word != OutcomeJobDone || closedAlerts(t, h, 20) != 20)) {
				t.Fatalf("word %s, requests %d, pending %d, stats %v", word, len(seen.seen), closer.PendingSize(context.Background()), nonZero(closer.Stats()))
			}
		})
	}
}

// A dry run that spends the round down to less than one request's budget
// is followed by no real close: the close would be cut off, lose its
// answer and keep every id it named. Only the dry run goes out, nothing
// is kept, and the job ends round_spent.
func TestADryRunThatSpentTheRoundIsFollowedByNoClose(t *testing.T) {
	const budget = time.Second
	h := slowLinkStrategy(t, 20)
	h.link.setCloseDelay(0)
	h.link.beforeDryRun = func(*fakeLink, *http.Request) {
		time.Sleep(budget * 7 / 10) // fixed-wait: the dry run's cost against the round is the behaviour under test
	}
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closerWithBudget(t, budget)
	ctx, cancel := context.WithTimeout(context.Background(), budget*3/2) // fixed-wait: the round's time is the behaviour under test
	defer cancel()
	word := closer.Absent(ctx, testScope, DecisionAbsent, 3)
	if dry, closes := seen.dryRuns(), seen.closes(); len(dry) != 1 || len(closes) != 0 || word != OutcomeJobIncomplete ||
		closer.Stats()[OutcomeRoundSpent] != 1 || closer.PendingSize(context.Background()) != 0 {
		t.Fatalf("word %s, dry runs %v, closes %v, pending %d, stats %v: want one dry run, no close, round_spent",
			word, dry, closes, closer.PendingSize(context.Background()), nonZero(closer.Stats()))
	}
}

// Items the link refused and left open - 400 invalid_command, 403
// tenant_mismatch, 409 source_unpublished, a 409 of a code alarmd does not
// know, any other status - leave the job unsettled
// (job_done_refused): the record keeps their entries, where a job that
// closed everything prunes them, so a later close still counts those
// alerts and they are not forgotten while still open.
func TestItemsTheLinkRefusedKeepTheRecord(t *testing.T) {
	for _, tc := range []struct{ fault, word string }{
		{"invalid", OutcomeJobDoneRefused},
		{"foreign", OutcomeJobDoneRefused},
		{"unpublished", OutcomeJobDoneRefused},
		{"conflict_other", OutcomeJobDoneRefused},
		{"unprocessable", OutcomeJobDoneRefused},
		{"", OutcomeJobDone},
	} {
		t.Run(tc.word+"/"+tc.fault, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 10; i++ {
				h.link.add(&fakeAlert{id: alertID(i), created: fakeOpenedAt, labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
			if tc.fault != "" {
				h.link.itemFault = func(string, int) string { return tc.fault }
			}
			closer := h.closer(t)
			if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != tc.word {
				t.Fatalf("word %s, want %s (stats %v)", word, tc.word, nonZero(closer.Stats()))
			}
			record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
			kept := len(record.Entries) == 10
			if err != nil || kept != (tc.fault != "") {
				t.Fatalf("record %d entries (%v), stats %v: want them kept only where items were refused", len(record.Entries), err, nonZero(closer.Stats()))
			}
		})
	}
}

// A dry run that read more ids than one request names
// (closeIDsPerRequest) has the rest closed by the job's next request, and
// the job is done only when every id it read was sent.
func TestADryRunPastOneRequestsIdsIsClosedOverSeveral(t *testing.T) {
	const n = closeIDsPerRequest + 4
	h := newHarness(t)
	for i := 0; i < n; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, n)...)
	seen := &requests{}
	h.observe = seen.observe
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if closes := seen.closes(); len(closes) != 2 {
		t.Fatalf("real closes %v, want %d ids then 4", closes, closeIDsPerRequest)
	}
	if closed := closedAlerts(t, h, n); closed != n {
		t.Fatalf("closed %d of %d", closed, n)
	}
}
