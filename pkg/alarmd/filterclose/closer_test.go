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
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// decisionSuperseded is the per-series decision the superseded-identity
// close sends (scopeclose.DecisionSuperseded), spelled here: scopeclose
// imports this package.
const decisionSuperseded = "superseded_identity"

// Every builder puts the tenant, the event source and the strategy in the
// filter, and a filter missing any of the three is refused before a
// request is made.
func TestEveryFilterCarriesTenantSourceAndStrategy(t *testing.T) {
	builders := map[string]func() (Filter, error){
		"strategy": func() (Filter, error) { return StrategyFilter(testScope) },
		"version":  func() (Filter, error) { return VersionFilter(testScope, "4") },
		"bucket": func() (Filter, error) {
			return BucketFilter(testScope, Bucket{Version: "4", Digest: strings.Repeat("a", 32)})
		},
		"no digest": func() (Filter, error) {
			return BucketFilter(testScope, Bucket{Version: "4", NoData: true})
		},
		"level":       func() (Filter, error) { return LevelFilter(testScope, []string{"3", "4"}, []string{"info"}) },
		"enumeration": func() (Filter, error) { return enumerationFilter(testScope, "5", []Bucket{{Version: "4"}}) },
		"fingerprint": func() (Filter, error) { return FingerprintFilter(testScope, []string{"f"}, fakeDecidedAt) },
	}
	for name, build := range builders {
		filter, err := build()
		if err != nil || requireScope(filter) != nil {
			t.Fatalf("%s: %v / %v", name, err, requireScope(filter))
		}
	}
	for _, scope := range []Scope{{EventSourceID: "built_in_bk", StrategyID: "41"}, {TenantID: "tenant-a", StrategyID: "41"},
		{TenantID: "tenant-a", EventSourceID: "built_in_bk"}, {TenantID: "tenant-a", EventSourceID: "built_in_bk", StrategyID: "041"}} {
		if _, err := StrategyFilter(scope); err == nil {
			t.Fatalf("scope %+v built", scope)
		}
	}
	h := newHarness(t)
	closer := h.closer(t)
	missing := map[string]string{
		"tenant":   `{"bool":{"filter":[{"term":{"event_source_id":"built_in_bk"}},{"term":{"labels.strategy_id":"41"}}]}}`,
		"source":   `{"bool":{"filter":[{"term":{"bk_tenant_id":"tenant-a"}},{"term":{"labels.strategy_id":"41"}}]}}`,
		"strategy": `{"bool":{"filter":[{"term":{"bk_tenant_id":"tenant-a"}},{"term":{"event_source_id":"built_in_bk"}}]}}`,
		"another":  `{"bool":{"filter":[{"term":{"bk_tenant_id":"tenant-a"}},{"term":{"event_source_id":"built_in_bk"}},{"term":{"labels.strategy_id":"42"}}]}}`,
		"nested":   `{"bool":{"should":[{"bool":{"filter":[{"term":{"bk_tenant_id":"tenant-a"}},{"term":{"event_source_id":"built_in_bk"}},{"term":{"labels.strategy_id":"41"}}]}}]}}`,
	}
	for name, body := range missing {
		word := closer.Run(context.Background(), Job{Filter: Filter{scope: testScope, body: json.RawMessage(body)}, Decision: DecisionAbsent,
			Expect: Expectation{Count: 10, Source: ExpectDecidedIDs}, Reason: "r"})
		if word != OutcomeFilterRefused {
			t.Fatalf("%s: %s", name, word)
		}
	}
	if len(h.link.requests) != 0 {
		t.Fatalf("requests %v, want none for an unscoped filter", h.link.requests)
	}
}

// A dry run that matches more than alarmd's own record of the strategy's
// episodes allows closes nothing; within it, the strategy's alerts close
// and another strategy's, another source's and another tenant's do not. A
// close that then finds nothing prunes the record, and the strategy has
// none left.
func TestADryRunPastTheRecordClosesNothing(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 50; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.link.add(&fakeAlert{id: "other-strategy", labels: map[string]string{"strategy_id": "42"}})
	h.link.add(&fakeAlert{id: "other-source", source: "another", labels: map[string]string{"strategy_id": "41"}})
	h.link.add(&fakeAlert{id: "other-tenant", tenant: "tenant-b", labels: map[string]string{"strategy_id": "41"}})
	// 29 episodes allow 29 + 20 = 49: one short of the link's 50.
	h.opened(t, "41", "3", nil, fingerprints(0, 29)...)
	h.opened(t, "42", "3", nil, fingerprints(100, 40)...)
	closer := h.closer(t)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeExpectExceeded {
		t.Fatalf("word %s, want expect_exceeded", word)
	}
	if h.link.requests[linkdmanage.RouteCloseFilter] == 0 || h.link.alert(alertID(0)).closes != 0 {
		t.Fatalf("closed past the expectation: %+v", h.link.alert(alertID(0)))
	}
	if stats := closer.Stats(); stats[OutcomeExpectOwnRecord] != 1 || stats[OutcomeExpectExceeded] != 1 {
		t.Fatalf("stats %v", nonZero(stats))
	}
	// 30 allow exactly 50.
	h.opened(t, "41", "3", nil, fingerprints(29, 1)...)
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("word %s, want job_done (stats %v)", word, nonZero(closer.Stats()))
	}
	for i := 0; i < 50; i++ {
		if h.link.alert(alertID(i)).closes != 1 {
			t.Fatalf("%s closes %d", alertID(i), h.link.alert(alertID(i)).closes)
		}
	}
	for _, id := range []string{"other-strategy", "other-source", "other-tenant"} {
		if h.link.alert(id).closes != 0 {
			t.Fatalf("%s closed", id)
		}
	}
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeNothingMatched {
		t.Fatalf("word %s, want nothing_matched", word)
	}
	if closer.Stats()[OutcomeRecordPruned] != 30 {
		t.Fatalf("stats %v, want the 30 entries pruned", nonZero(closer.Stats()))
	}
	for strategy, want := range map[string]int{"41": 0, "42": 40} {
		record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: strategy})
		// The pruned record keeps its start: no entry left.
		if err != nil || len(record.Entries) != want || !record.Exists || record.Since.IsZero() {
			t.Fatalf("strategy %s record %+v, %v; want %d entries", strategy, record, err, want)
		}
	}
}

// The margin is a tenth of the count, at least twenty.
func TestTheMarginIsATenthAtLeastTwenty(t *testing.T) {
	for count, want := range map[int]int{0: 20, 30: 50, 200: 220, 36000: 39600} {
		if got := (Expectation{Count: count, Source: ExpectOwnRecord}).allowed(); got != want {
			t.Fatalf("count %d allows %d, want %d", count, got, want)
		}
	}
}

// An inactive close of one version counts only that version's entries:
// the record holds 25 at version 3 and 100 at version 4, the link 50 at
// version 3. 25 allow 45, so the close is refused; the entries of version
// 4 do not lend it their count.
func TestAnInactiveCloseCountsOnlyItsVersionsEntries(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 50; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 25)...)
	h.opened(t, "41", "4", nil, fingerprints(100, 100)...)
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeExpectExceeded {
		t.Fatalf("word %s, want expect_exceeded", word)
	}
	h.opened(t, "41", "3", nil, fingerprints(25, 5)...)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s, want job_done", word)
	}
	// Nothing left at version 3: its entries are pruned, version 4's kept.
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeNothingMatched {
		t.Fatalf("word %s", word)
	}
	record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
	if err != nil || len(record.Entries) != 100 {
		t.Fatalf("record %d entries, %v; want version 4's 100", len(record.Entries), err)
	}
	for _, entry := range record.Entries {
		if entry.Version != "4" {
			t.Fatalf("entry %+v left", entry)
		}
	}
}

// A strategy with no record - expired, lost - is never closed by its
// filter: each alert the search pages is decided from its own labels and
// closed by its id. The inactive close of version 3 closes the alerts
// whose strategy_version is 3 and not version 4's; the requests closing
// carry alert ids.
func TestWithoutARecordEachAlertIsDecidedAndClosedByItsID(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 30; i++ {
		version := "3"
		if i%3 == 0 {
			version = "4"
		}
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": version}})
	}
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s, want job_done (stats %v)", word, nonZero(closer.Stats()))
	}
	for i := 0; i < 30; i++ {
		want := 1
		if i%3 == 0 {
			want = 0
		}
		if got := h.link.alert(alertID(i)).closes; got != want {
			t.Fatalf("%s closed %d, want %d", alertID(i), got, want)
		}
	}
	stats := closer.Stats()
	if stats[OutcomeRecordMissing] != 1 || stats[OutcomeAlertDecidedClose] != 20 || stats[OutcomeExpectDecidedIDs] != 1 ||
		stats[OutcomeExpectOwnRecord] != 0 {
		t.Fatalf("stats %v", nonZero(stats))
	}
	for _, body := range h.link.closeBodies {
		if !strings.Contains(body, `"alert_id"`) {
			t.Fatalf("a close without alert ids: %s", body)
		}
	}
	// The absent close of the strategy without a record decides by the
	// strategy label: the ten left close.
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeJobDone {
		t.Fatalf("absent word %s", word)
	}
	for i := 0; i < 30; i += 3 {
		if h.link.alert(alertID(i)).closes != 1 {
			t.Fatalf("%s not closed by the absent close", alertID(i))
		}
	}
}

// A record that cannot be read decides nothing: no dry run, no close.
func TestAnUnreadableRecordDecidesNothing(t *testing.T) {
	h := newHarness(t)
	h.link.add(&fakeAlert{id: alertID(0), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	closer := h.closer(t)
	closer.record = failingRecord{}
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeRecordUnavailable {
		t.Fatalf("word %s", word)
	}
	if h.link.requests[linkdmanage.RouteCloseFilter] != 0 || h.link.requests[linkdmanage.RouteSearch] != 0 {
		t.Fatalf("requests %v", h.link.requests)
	}
}

type failingRecord struct{}

func (failingRecord) Load(context.Context, openepisodes.Key) (openepisodes.Record, error) {
	return openepisodes.Record{}, errors.New("redis down")
}

func (failingRecord) Since(context.Context, openepisodes.Key) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("redis down")
}

func (failingRecord) Prune(context.Context, openepisodes.Key, []string) error {
	return errors.New("redis down")
}

// A link that cannot search answers 501: filtered close is unsupported,
// counted and warned, every job until the recheck answers the same word
// without a request, and nothing is closed another way. After the recheck
// it is asked again.
func TestALinkThatCannotSearchIsFilteredCloseUnsupported(t *testing.T) {
	h := newHarness(t)
	h.link.unsupported = true
	closer := h.closer(t)
	warned := 0
	closer.warn = func(error) { warned++ }
	at := time.Unix(1_800_000_000, 0)
	closer.now = func() time.Time { return at }
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeUnsupported {
		t.Fatalf("word %s", word)
	}
	sent := h.link.requests[linkdmanage.RouteCloseFilter] + h.link.requests[linkdmanage.RouteSearch]
	if !closer.Unsupported() || warned != 1 {
		t.Fatalf("unsupported %v, warned %d", closer.Unsupported(), warned)
	}
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeUnsupported {
		t.Fatalf("word %s", word)
	}
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != OutcomeUnsupported {
		t.Fatalf("edit word %s", word)
	}
	if got := h.link.requests[linkdmanage.RouteCloseFilter] + h.link.requests[linkdmanage.RouteSearch]; got != sent {
		t.Fatalf("%d requests inside the recheck", got-sent)
	}
	if n := closer.Stats()[OutcomeUnsupported]; n != 3 {
		t.Fatalf("%s = %d", OutcomeUnsupported, n)
	}
	at = at.Add(unsupportedRecheck)
	if closer.Unsupported() {
		t.Fatal("not asked again after the recheck")
	}
	if word := closer.Absent(context.Background(), testScope, DecisionAbsent, 3); word != OutcomeUnsupported || warned != 2 {
		t.Fatalf("after the recheck word %s, warned %d", word, warned)
	}
}

// One decision has one command: two processes resolving it - a restart, a
// new leader - get the same bytes, and the link sees one operation id with
// one content. Past the command's life the same decision mints a new one.
func TestADecisionsCommandIsTheSameBytesAcrossProcesses(t *testing.T) {
	h := newHarness(t)
	first, second := h.closer(t), h.closer(t)
	at := time.Unix(1_800_000_000, 0)
	key := decisionKey(testScope, "4", strings.Repeat("a", 32), "metric_changed", "", 5)
	a, err := first.commands.Resolve(context.Background(), key, "tenant-a", "reason", at)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.commands.Resolve(context.Background(), key, "tenant-a", "another reason", at.Add(time.Minute))
	if err != nil || a.ID != b.ID || string(a.Bytes) != string(b.Bytes) {
		t.Fatalf("%s %s / %s %s (%v)", a.ID, a.Bytes, b.ID, b.Bytes, err)
	}
	var fields commandFields
	if json.Unmarshal(a.Bytes, &fields) != nil || fields.OperationID != a.ID || fields.OperationSource != "strategy_change" ||
		fields.OperatorKind != "system" || fields.EffectiveAt != "2027-01-15T08:00:00Z" {
		t.Fatalf("command %s", a.Bytes)
	}
	h.redis.Client().Del(context.Background(), first.commands.key(key))
	c, err := first.commands.Resolve(context.Background(), key, "tenant-a", "reason", at.Add(commandLife))
	if err != nil || c.ID == a.ID {
		t.Fatalf("a new life kept the operation id %s (%v)", c.ID, err)
	}
	if ttl := h.redis.Client().TTL(context.Background(), first.commands.key(key)).Val(); ttl <= 0 || ttl > commandLife {
		t.Fatalf("command ttl %v", ttl)
	}
}

// The pending store keeps the first command for an alert, holds to its
// bound, and drops an item past its attempts.
func TestThePendingStoreIsBounded(t *testing.T) {
	h := newHarness(t)
	pending, err := NewPending(h.redis.Client(), h.prefix, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	item := func(id, op string) pendingItem {
		return pendingItem{TenantID: "tenant-a", AlertID: id, OperationID: op, Command: []byte(`{"operation_id":"` + op + `"}`), FirstAt: 1}
	}
	if pending.Keep(ctx, item("a", "first")) != nil || pending.Keep(ctx, item("a", "second")) != nil || pending.Keep(ctx, item("b", "x")) != nil {
		t.Fatal("keep failed")
	}
	if err := pending.Keep(ctx, item("c", "x")); err != errPendingFull {
		t.Fatalf("past the bound: %v", err)
	}
	items, _, err := pending.Next(ctx, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("items %+v (%v)", items, err)
	}
	for _, kept := range items {
		if kept.AlertID == "a" && kept.OperationID != "first" {
			t.Fatalf("the first command was replaced: %+v", kept)
		}
	}
	if !(pendingItem{Attempts: pendingAttempts, FirstAt: time.Now().Unix()}).expired(time.Now()) ||
		!(pendingItem{FirstAt: time.Now().Add(-pendingAge).Unix()}).expired(time.Now()) ||
		(pendingItem{FirstAt: time.Now().Unix()}).expired(time.Now()) {
		t.Fatal("expiry")
	}
}

// Each item of a filtered close is counted by its status first and its
// error code second: done, ended another way, found again by the filter,
// or kept to retry by id - only the ones whose close may have been saved.
func TestEachItemIsCountedByItsKind(t *testing.T) {
	h := newHarness(t)
	closer := h.closer(t)
	filter, _ := StrategyFilter(testScope)
	items := []linkdmanage.Item{
		{AlertID: "a", Status: 200}, {AlertID: "b", Status: 200, AlreadyClosed: true},
		{AlertID: "c", Status: 409, ErrorCode: "invalid_transition"}, {AlertID: "d", Status: 409, ErrorCode: "version_conflict"},
		{AlertID: "e", Status: 409, ErrorCode: "source_unpublished"}, {AlertID: "f", Status: 409, ErrorCode: "another"},
		{AlertID: "g", Status: 429, ErrorCode: "busy"}, {AlertID: "h", Status: 504, ErrorCode: "not_attempted"},
		{AlertID: "i", Status: 502, ErrorCode: "result_uncertain"}, {AlertID: "j", Status: 503, ErrorCode: "unavailable"},
		{AlertID: "k", Status: 504, ErrorCode: "timeout"}, {AlertID: "l", Status: 400, ErrorCode: "invalid_command"},
		{AlertID: "m", Status: 403, ErrorCode: "tenant_mismatch"}, {AlertID: "n", Status: 404, ErrorCode: "not_found"},
		{AlertID: "o", Status: 422, ErrorCode: "another"},
	}
	closer.items(context.Background(), Job{Filter: filter}, Command{ID: "op", Bytes: []byte(`{}`)}, items, nil)
	want := map[string]uint64{OutcomeItemClosed: 1, OutcomeItemAlreadyClosed: 1, OutcomeItemEndedOtherwise: 1, OutcomeItemRederived: 3,
		OutcomeItemUnavailable: 1, OutcomeItemSourceUnpublished: 1, OutcomeItemConflictUnknown: 1, OutcomeItemPending: 2,
		OutcomeItemInvalidCommand: 1, OutcomeItemTenantMismatch: 1, OutcomeItemNotFound: 1, OutcomeItemRefused: 1}
	if got := nonZero(closer.Stats()); len(got) != len(want) {
		t.Fatalf("stats %v, want %v", got, want)
	}
	for word, n := range want {
		if closer.Stats()[word] != n {
			t.Fatalf("%s = %d, want %d (%v)", word, closer.Stats()[word], n, nonZero(closer.Stats()))
		}
	}
	kept, _, _ := closer.pending.Next(context.Background(), 100)
	ids := map[string]bool{}
	for _, item := range kept {
		ids[item.AlertID] = true
	}
	if len(ids) != 2 || !ids["i"] || !ids["k"] {
		t.Fatalf("kept %v, want the two 5xx other than not_attempted and 503", ids)
	}
}

// A link that answers every item 503 - its close resources not configured -
// closed nothing and saved nothing: no item is kept to retry by id however
// many rounds it lasts, the job is not settled (so the record keeps its
// entries), it is not run again within the round, and once the link is
// fixed the next round's filter finds the alerts again and closes them -
// run again within that round while a run closes some and leaves more.
// It shows the run-again wrapper (UntilProgressStops) over the decision's
// own Result, its Closed from the jobs' tallies; it is not the inactive
// close's pace, which does not go through the wrapper in production.
func TestItemsTheLinkRefusedUnavailableAreFoundAgainNotKept(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
	unavailable := true
	h.link.itemFault = func(id string, n int) string {
		if unavailable {
			return "unavailable"
		}
		// Fixed: the first close after the fix leaves the odd alerts,
		// attempted three times before it, answered 503 once more.
		if n == 3 && id[len(id)-1]%2 == 1 {
			return "unavailable"
		}
		return ""
	}
	closer := h.closer(t)
	run := func() Result {
		return closer.UntilProgressStops(context.Background(), func() Result {
			return closer.inactiveResult(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod)
		})
	}
	for round := 0; round < 3; round++ {
		if word := run().Word; word != OutcomeJobDoneRederived {
			t.Fatalf("round %d word %s, want job_done_rederived (stats %v)", round, word, nonZero(closer.Stats()))
		}
	}
	stats := closer.Stats()
	if stats[OutcomeItemUnavailable] != 30 || stats[OutcomeItemPending] != 0 || stats[OutcomeJobAgain] != 0 {
		t.Fatalf("stats %v, want 30 item_unavailable over three rounds, nothing kept, no run again within a round", nonZero(stats))
	}
	if kept, _, _ := closer.pending.Next(context.Background(), 100); len(kept) != 0 {
		t.Fatalf("kept %d items to retry by id", len(kept))
	}
	record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
	if err != nil || len(record.Entries) != 10 {
		t.Fatalf("record %d entries (%v): pruned while the alerts are still active", len(record.Entries), err)
	}
	unavailable = false
	if result := run(); result.Word != OutcomeJobDone || result.Closed != 10 || closer.Stats()[OutcomeJobAgain] != 1 {
		t.Fatalf("result %+v once the link is fixed, want job_done with 10 closed, run once again within the round (stats %v)",
			result, nonZero(closer.Stats()))
	}
	for i := 0; i < 10; i++ {
		if h.link.alert(alertID(i)).closes != 1 {
			t.Fatalf("%s closed %d times", alertID(i), h.link.alert(alertID(i)).closes)
		}
	}
}

// A close decided per series closes the strategy's active alerts of those
// fingerprints and no other, under a command of its own decision.
func TestAPerSeriesCloseClosesOnlyItsFingerprints(t *testing.T) {
	h := newHarness(t)
	for i, fingerprint := range []string{"f1", "f2", "f3"} {
		h.link.add(&fakeAlert{id: alertID(i), fingerprint: fingerprint, labels: map[string]string{"strategy_id": "41"}})
	}
	h.link.add(&fakeAlert{id: "other", fingerprint: "f1", labels: map[string]string{"strategy_id": "42"}})
	closer := h.closer(t)
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f1", "f2"}, "target_out_of_scope", 7, fakeDecidedAt); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	for id, want := range map[string]int{alertID(0): 1, alertID(1): 1, alertID(2): 0, "other": 0} {
		if h.link.alert(id).closes != want {
			t.Fatalf("%s closed %d, want %d", id, h.link.alert(id).closes, want)
		}
	}
}

// A job whose filter runs out with items the link certainly did not close
// (429, 504 not_attempted, 409 version_conflict) is not settled: it answers
// job_done_rederived, and the next run closes them and answers job_done.
// The close of a strategy without a record keeps that word across its id
// batches: a later batch that settles does not hide an earlier one that
// did not.
func TestAJobWithItemsLeftToFindAgainIsNotSettled(t *testing.T) {
	for _, fault := range []string{"busy", "conflict"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < 10; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
			h.link.itemFault = func(id string, n int) string {
				if id == alertID(4) && n == 0 {
					return fault
				}
				return ""
			}
			closer := h.closer(t)
			if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDoneRederived {
				t.Fatalf("word %s, want job_done_rederived (stats %v)", word, nonZero(closer.Stats()))
			}
			if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
				t.Fatalf("second word %s, want job_done (stats %v)", word, nonZero(closer.Stats()))
			}
			if h.link.alert(alertID(4)).closes != 1 {
				t.Fatal("the rederived alert was not closed by the second run")
			}
		})
	}
	h := newHarness(t)
	for i := 0; i < linkdmanage.MaxLimit+50; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.link.itemFault = func(id string, n int) string {
		if id == alertID(5) && n == 0 {
			return "busy"
		}
		return ""
	}
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDoneRederived {
		t.Fatalf("per-alert word %s, want job_done_rederived (stats %v)", word, nonZero(closer.Stats()))
	}
}

// A close decided per series or per target is about the alerts opened by
// the moment its decision was read: an alert the series opened again after
// it (the target back in scope) is not selected, and one opened before is.
// A close that names no such moment sends nothing.
func TestASeriesCloseLeavesAnAlertOpenedAfterItsDecision(t *testing.T) {
	h := newHarness(t)
	h.link.add(&fakeAlert{id: "a-before", fingerprint: "f1", labels: map[string]string{"strategy_id": "41"},
		dimensions: map[string]string{"bk_target_ip": "192.0.2.1"}})
	h.link.add(&fakeAlert{id: "b-after", fingerprint: "f2", labels: map[string]string{"strategy_id": "41"},
		dimensions: map[string]string{"bk_target_ip": "192.0.2.2"}, created: fakeDecidedAt.Add(time.Second)})
	h.link.add(&fakeAlert{id: "c-at", fingerprint: "f3", labels: map[string]string{"strategy_id": "41"},
		dimensions: map[string]string{"bk_target_ip": "192.0.2.3"}, created: fakeDecidedAt})
	closer := h.closer(t)
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f1", "f2", "f3"}, "target_out_of_scope", 7, time.Time{}); word != OutcomeFilterRefused {
		t.Fatalf("word %s without a decision time, want filter_refused", word)
	}
	if len(h.link.requests) != 0 {
		t.Fatalf("requests %v", h.link.requests)
	}
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f1", "f2", "f3"}, "target_out_of_scope", 7, fakeDecidedAt); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	for id, want := range map[string]int{"a-before": 1, "b-after": 0, "c-at": 1} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d, want %d", id, got, want)
		}
	}
}

// Another batch of one per-series decision, and the next inactive period
// of a version, is its own decision: its own command, effective at its own
// moment, not at the first batch's or period's - an alert opened after
// that moment would end before it began. The same batch again keeps its
// command.
func TestEachBatchAndPeriodHasItsOwnCommand(t *testing.T) {
	h := newHarness(t)
	// In the past: the link refuses an effective_at ahead of its clock.
	now := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
	commands, _ := NewCommands(h.redis.Client(), h.prefix)
	pending, _ := NewPending(h.redis.Client(), h.prefix, MaxPending)
	closer, err := New(Options{Link: h.client, Commands: commands, Pending: pending, Record: h.record(t), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	command := func(id string) fakeCommand {
		var fields fakeCommand
		if err := json.Unmarshal([]byte(h.link.alert(id).endOperation), &fields); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return fields
	}
	for i, fingerprint := range []string{"f1", "f2", "f3"} {
		h.link.add(&fakeAlert{id: alertID(i), fingerprint: fingerprint, labels: map[string]string{"strategy_id": "41"}})
	}
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f1"}, decisionSuperseded, 7, fakeDecidedAt); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	now = now.Add(time.Hour)
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f2"}, decisionSuperseded, 7, fakeDecidedAt); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	first, second := command(alertID(0)), command(alertID(1))
	if first.OperationID == second.OperationID || second.EffectiveAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("second batch %+v, first %+v", second, first)
	}
	// The first batch again, an hour on: its command, its effective_at.
	h.link.add(&fakeAlert{id: alertID(9), fingerprint: "f1", labels: map[string]string{"strategy_id": "41"}})
	if word := closer.Fingerprints(context.Background(), testScope, []string{"f1"}, decisionSuperseded, 7, fakeDecidedAt); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	if again := command(alertID(9)); again != first {
		t.Fatalf("the same batch again %+v, first %+v", again, first)
	}

	for i := 20; i < 22; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 2)...)
	period := now
	if word := closer.Inactive(context.Background(), testScope, "3", 3, period, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	h.link.add(&fakeAlert{id: alertID(30), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	now = now.Add(2 * time.Hour)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, now, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s", word)
	}
	if a, b := command(alertID(20)), command(alertID(30)); a.OperationID == b.OperationID || b.EffectiveAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("next period %+v, first period %+v", b, a)
	}
}

// A real close page whose answer is lost leaves its items unknown; an
// item whose close was saved with its outputs failed no longer matches
// active, so no filter finds it again. The candidates the dry run just
// before the page read are kept and retried by id: the item's outputs are
// completed, and every other candidate answers already closed.
func TestALostPageKeepsItsCandidatesForTheRetry(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
	h.link.itemFault = func(id string, n int) string {
		if id == alertID(3) && n == 0 {
			return "uncertain_saved"
		}
		return ""
	}
	h.link.loseAnswer = func(n int) bool { return n == 1 }
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeLinkFailed {
		t.Fatalf("word %s, want link_failed (stats %v)", word, nonZero(closer.Stats()))
	}
	if stats := closer.Stats(); stats[OutcomePageLost] != 1 || stats[OutcomeItemPending] != 10 {
		t.Fatalf("stats %v, want the page's ten candidates kept", nonZero(stats))
	}
	if h.link.alert(alertID(3)).completed {
		t.Fatal("the fault did not leave the outputs unfinished")
	}
	closer.RetryPending(context.Background(), 100)
	for i := 0; i < 10; i++ {
		if alert := h.link.alert(alertID(i)); alert.closes != 1 || !alert.completed {
			t.Fatalf("%s closes %d completed %v (stats %v)", alert.id, alert.closes, alert.completed, nonZero(closer.Stats()))
		}
	}
	if stats := closer.Stats(); stats[OutcomePendingAlreadyClosed] != 10 || closer.PendingSize(context.Background()) != 0 {
		t.Fatalf("stats %v", nonZero(stats))
	}
}

// A decision run again within the round answers what every run sent and
// closed, not only its last: a first run that sent a close and closed two
// with more left, then a run that sent nothing and settled, is a decision
// sent that closed two - the absent close's close_sent and execution.sent
// read Sent, and a strategy whose closes all went out in the first run is
// still a send.
func TestARunAgainAnswersWhatEveryRunSentAndClosed(t *testing.T) {
	closer := newHarness(t).closer(t)
	runs := []Result{
		{Word: OutcomeJobDoneRederived, Parts: []string{OutcomeJobDoneRederived}, Sent: true, Closed: 2},
		{Word: OutcomeNothingMatched, Parts: []string{OutcomeNothingMatched}},
	}
	calls := 0
	result := closer.UntilProgressStops(context.Background(), func() Result {
		calls++
		return runs[calls-1]
	})
	if calls != 2 || result.Word != OutcomeNothingMatched || !result.Sent || result.Closed != 2 {
		t.Fatalf("%d runs answered %+v, want two runs, the last's word, sent, 2 closed", calls, result)
	}
}
