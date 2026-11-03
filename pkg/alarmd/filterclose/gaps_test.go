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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// While the link is known not to serve the routes, the retry of kept
// items sends nothing: they stay kept for when it does.
func TestNoRetryIsSentWhileTheLinkIsUnsupported(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 3)...)
	h.link.loseAnswer = func(int) bool { return true }
	closer := h.closer(t)
	closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod)
	if closer.PendingSize(context.Background()) != 3 {
		t.Fatalf("pending %d (stats %v)", closer.PendingSize(context.Background()), nonZero(closer.Stats()))
	}
	h.link.unsupported = true
	closer.Absent(context.Background(), Scope{TenantID: "tenant-a", EventSourceID: "built_in_bk", StrategyID: "42"}, DecisionAbsent, 1)
	if !closer.Unsupported() {
		t.Fatal("the link was not found unsupported")
	}
	before := h.link.requests[linkdmanage.RouteSearch] + h.link.requests["close_one"]
	closer.RetryPending(context.Background(), 100)
	if after := h.link.requests[linkdmanage.RouteSearch] + h.link.requests["close_one"]; after != before ||
		closer.PendingSize(context.Background()) != 3 {
		t.Fatalf("requests %d, pending %d while unsupported", after-before, closer.PendingSize(context.Background()))
	}
}

// truncatedRecord is the store with every load past MaxLoad.
type truncatedRecord struct{ *openepisodes.Store }

func (record truncatedRecord) Load(ctx context.Context, key openepisodes.Key) (openepisodes.Record, error) {
	loaded, err := record.Store.Load(ctx, key)
	loaded.Truncated = true
	return loaded, err
}

// A record past MaxLoad is not an expectation: the close is decided alert
// by alert, by ids.
func TestATruncatedRecordIsDecidedAlertByAlert(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 5)...)
	closer := h.closer(t)
	closer.record = truncatedRecord{h.record(t)}
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if stats := closer.Stats(); stats[OutcomeRecordTruncated] != 1 || stats[OutcomeAlertDecidedClose] != 5 {
		t.Fatalf("stats %v", nonZero(stats))
	}
	for _, body := range h.link.closeBodies {
		if !strings.Contains(body, `"alert_id"`) {
			t.Fatalf("a close without alert ids: %s", body)
		}
	}
}

// An entry the record cannot read counts toward every decision's
// expectation: it may be any version's episode.
func TestAnUnreadableEntryCountsTowardTheExpectation(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 45; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 20)...)
	key := h.prefix + ":openepisodes:v1:tenant-a:41"
	for _, fingerprint := range fingerprints(100, 5) {
		h.redis.Client().HSet(context.Background(), key, fingerprint, "not json")
	}
	closer := h.closer(t)
	// 20 + 5 unreadable allow 45; the 20 alone would allow 40.
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
}

// An edit whose bucket walk did not reach its end (an alert whose version
// no bucket can exclude stopped it) prunes nothing: the older versions'
// entries may still be the link's.
func TestAnIncompleteBucketWalkPrunesNothing(t *testing.T) {
	h := newHarness(t)
	h.link.add(&fakeAlert{id: "a-bad", labels: map[string]string{"strategy_id": "41", "strategy_version": "v3"}})
	h.opened(t, "41", "3", nil, fingerprints(0, 4)...)
	closer := h.closer(t)
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
		t.Fatalf("word %q", word)
	}
	record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
	if err != nil || len(record.Entries) != 4 || closer.Stats()[OutcomeRecordPruned] != 0 || closer.Stats()[OutcomeSampleUnreadable] != 1 {
		t.Fatalf("record %+v, %v, stats %v", record, err, nonZero(closer.Stats()))
	}
}

// A record vouches two of its strategy's periods after its start, and
// never sooner than an hour: an entry written again within that time of the
// start (the first ABNORMAL since of an episode already open) does not
// count toward the dry run, one written after it does. A minute's strategy
// keeps the hour; an hour's waits two.
func TestTheRecordVouchesTwoOfItsStrategysPeriodsAndNeverUnderAnHour(t *testing.T) {
	start := fakeOpenedAt.Add(-3 * time.Hour)
	for _, c := range []struct {
		name    string
		period  time.Duration
		rewrite time.Duration
		want    string
	}{
		{"a minute's strategy, rewritten within the hour", time.Minute, 30 * time.Minute, OutcomeExpectExceeded},
		{"a minute's strategy, rewritten past the hour", time.Minute, 90 * time.Minute, OutcomeJobDone},
		{"an hour's strategy, rewritten within two hours", time.Hour, 90 * time.Minute, OutcomeExpectExceeded},
		{"an hour's strategy, rewritten past two hours", time.Hour, 150 * time.Minute, OutcomeJobDone},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			// 30 rewrites and 5 entries opened since: counted, 35 allow 55
			// alerts; the 5 alone allow 25. The link holds 45.
			writeEntries(t, h, start, start.Add(c.rewrite), 0, 30)
			writeEntries(t, h, start, fakeEntryOpened, 100, 5)
			for i := 0; i < 45; i++ {
				h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			}
			closer := h.closer(t)
			if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, c.period); word != c.want {
				t.Fatalf("word %s, want %s (stats %v)", word, c.want, nonZero(closer.Stats()))
			}
		})
	}
}

// writeEntries writes n entries of strategy 41 at version 3 opened at at,
// into a record that started at start.
func writeEntries(t *testing.T, h *harness, start, at time.Time, first, n int) {
	t.Helper()
	h.redis.Client().HSetNX(context.Background(), h.prefix+":openepisodes:v1:tenant-a:41", openepisodes.SinceField, strconv.FormatInt(start.Unix(), 10))
	var changes []openepisodes.Change
	for _, fingerprint := range fingerprints(first, n) {
		changes = append(changes, openepisodes.Change{Key: openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"}, Fingerprint: fingerprint,
			Open: true, Entry: openepisodes.Entry{Version: "3", OpenedAt: at.Unix()}})
	}
	if err := h.record(t).Apply(context.Background(), changes, nil); err != nil {
		t.Fatal(err)
	}
}

// Episodes still open when a record starts are written again by their
// first ABNORMAL since: entries opened within recordSettle of the start,
// their alerts older than it. They do not count toward the dry run of the
// alerts opened since, so a real over-match among those is still refused;
// entries opened later count as before.
func TestEntriesWrittenAgainAtTheRecordsStartDoNotWidenTheExpectation(t *testing.T) {
	h := newHarness(t)
	start := fakeOpenedAt.Add(-recordSettle - time.Hour)
	writeEntries(t, h, start, start.Add(time.Minute), 0, 30)
	writeEntries(t, h, start, fakeEntryOpened, 100, 5)
	for i := 0; i < 45; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeExpectExceeded {
		t.Fatalf("word %s, want expect_exceeded: 45 alerts against 5 vouched entries (stats %v)", word, nonZero(closer.Stats()))
	}
	if len(h.link.closeBodies) != 0 {
		t.Fatalf("closes sent: %d", len(h.link.closeBodies))
	}

	h2 := newHarness(t)
	writeEntries(t, h2, start, start.Add(time.Minute), 0, 30)
	writeEntries(t, h2, start, fakeEntryOpened, 100, 5)
	for i := 0; i < 5; i++ {
		h2.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	closer = h2.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
}

// A record with entries and no start (a hash written before starts were,
// or its start lost) cannot say from when it vouches: the close is decided
// alert by alert, counted record_missing, and its entries are no
// expectation.
func TestARecordWithEntriesAndNoStartIsDecidedAlertByAlert(t *testing.T) {
	h := newHarness(t)
	h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
	key := h.prefix + ":openepisodes:v1:tenant-a:41"
	if err := h.redis.Client().HDel(context.Background(), key, openepisodes.SinceField).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := h.redis.Client().HLen(context.Background(), key).Result(); err != nil || n != 10 {
		t.Fatalf("the record holds %d fields (%v), want its 10 entries and no start", n, err)
	}
	for i := 0; i < 10; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	stats := closer.Stats()
	if stats[OutcomeRecordMissing] != 1 || stats[OutcomeExpectOwnRecord] != 0 || stats[OutcomeAlertDecidedClose] != 10 {
		t.Fatalf("stats %v, want record_missing once and ten alerts decided one by one", nonZero(stats))
	}
	for _, body := range h.link.closeBodies {
		if !strings.Contains(body, `"alert_id"`) {
			t.Fatalf("a close without alert ids: %s", body)
		}
	}
}

// The record's entries are pruned only when neither part of the decision
// found anything: the alerts opened since the record vouches and the older
// ones decided one by one. Where the recent part matched nothing but older
// alerts were still open, the entries stay.
func TestNothingIsPrunedWhileOlderAlertsWereStillOpen(t *testing.T) {
	h := newHarness(t)
	h.opened(t, "41", "3", nil, fingerprints(0, 5)...)
	for i := 0; i < 3; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"},
			created: fakeRecordStart.Add(-time.Hour)})
	}
	closer := h.closer(t)
	if word := closer.Inactive(context.Background(), testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	for i := 0; i < 3; i++ {
		if h.link.alert(alertID(i)).closes != 1 {
			t.Fatalf("the older %s was not closed", alertID(i))
		}
	}
	record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
	if err != nil || len(record.Entries) != 5 || closer.Stats()[OutcomeRecordPruned] != 0 {
		t.Fatalf("record %d entries (%v), stats %v: want the five kept", len(record.Entries), err, nonZero(closer.Stats()))
	}
}
