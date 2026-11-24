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
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// currentFacts is the strategy's current Plans' facts; metric names the
// metrics' digest so a test can make an older version's differ.
func factsOf(metric string) *contract.StrategyFactsV2 {
	return &contract.StrategyFactsV2{Schema: contract.StrategyFactsSchemaV2, MetricDigest: metric, ConditionMD5: "c",
		ConditionBasis: contract.ConditionBasisQuery, DimensionDigest: "d", NoDataDimensionDigest: "nd"}
}

func currentPlan() editclose.Plan {
	return editclose.Plan{Revision: 5, DimensionFields: []string{"bk_target_ip"}, LevelsWhole: true,
		Levels: []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}}, NoData: true, NoDataDimensions: []string{"bk_target_ip"},
		Facts: factsOf("m5")}
}

// opened is an alert as the link keeps it after alarmd's opening event:
// the strategy, the version, the facts' digest as labels, the facts and the
// family in extra_data, the no-data tag on a no-data alert. facts nil is an
// alert opened before events carried facts.
func opened(id, version string, facts *contract.StrategyFactsV2, noData bool, severity string, dimensions map[string]string) *fakeAlert {
	labels := map[string]string{"strategy_id": "41", "strategy_version": version, "bk_biz_id": "2"}
	extra := map[string]any{"evaluation_family": "metric_algorithm"}
	if dimensions == nil {
		dimensions = map[string]string{"bk_target_ip": "192.0.2.1"}
	}
	if noData {
		extra["evaluation_family"] = "no_data"
		dimensions[contract.NoDataDimensionTag] = "true"
	}
	if facts != nil {
		event := facts.ForEvent(noData)
		labels["strategy_facts_digest"] = event.Digest()
		extra["strategy_facts"] = event
	}
	encoded, _ := json.Marshal(extra)
	return &fakeAlert{id: id, labels: labels, dimensions: dimensions, severity: severity, extra: encoded}
}

// roster records the strategy's episodes as its workers would have: 20
// open at each of the versions 3 and 4.
func roster(t *testing.T, h *harness) {
	t.Helper()
	h.opened(t, "41", "4", nil, fingerprints(0, 20)...)
	h.opened(t, "41", "3", nil, fingerprints(20, 20)...)
}

// An edit closes, by bucket, the older version whose metric changed (both
// families), the threshold alerts of older versions at a level no longer
// detected, and the alerts opened without a digest that their own record
// decides; it keeps a bucket whose facts survived, its no-data alert at the
// removed level, an undecided alert without a digest, the current
// version's alerts and a version ahead.
func TestAnEditClosesTheOrphanedBucketsAndKeepsTheRest(t *testing.T) {
	h := newHarness(t)
	old, current := factsOf("m4"), factsOf("m5")
	for _, alert := range []*fakeAlert{
		opened("x1", "4", old, false, "critical", nil), opened("x2", "4", old, false, "warning", nil),
		opened("xn", "4", old, true, "warning", nil),
		opened("k1", "4", current, false, "critical", nil), opened("k2", "4", current, false, "info", nil),
		opened("kn", "4", current, true, "info", nil),
		opened("n1", "3", nil, false, "critical", map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"}),
		opened("n2", "3", nil, false, "critical", nil),
		opened("c1", "5", current, false, "info", nil),
		opened("a1", "6", old, false, "info", nil),
		// Raised under the publisher's earlier scheme, a microsecond time
		// (synthetic): not ahead of revision 5, judged from its record.
		opened("u1", microsecondVersion, nil, false, "critical", map[string]string{"bk_target_ip": "192.0.2.1", "removed_dim": "x"}),
		opened("u2", microsecondVersion, nil, false, "critical", nil),
	} {
		h.link.add(alert)
	}
	roster(t, h)
	closer := h.closer(t)
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
		t.Fatalf("word %s", word)
	}
	for id, want := range map[string]int{"x1": 1, "x2": 1, "xn": 1, "k2": 1, "n1": 1, "u1": 1, "k1": 0, "kn": 0, "n2": 0, "u2": 0, "c1": 0, "a1": 0} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d times, want %d (stats %v)", id, got, want, nonZero(closer.Stats()))
		}
	}
	// Every close alarmd sends is a strategy_change by the system, the
	// no-data alert's too: the link's no-data linkage skips that source
	// (b7e5beddf docs/reference/contracts/alert-close.md, "alarmd 无数据关闭联动"),
	// so it never calls alarmd back for alarmd's own close.
	if !strings.Contains(h.link.alert("xn").endOperation, `"operation_source":"strategy_change"`) {
		t.Fatalf("the no-data close was sent as %s", h.link.alert("xn").endOperation)
	}
	for id, command := range h.link.operations {
		var fields fakeCommand
		if json.Unmarshal([]byte(command), &fields) != nil || fields.OperationSource != "strategy_change" || fields.OperatorKind != "system" {
			t.Fatalf("operation %s: %s", id, command)
		}
	}
	stats := closer.Stats()
	if stats["edit_"+editclose.OutcomeMetricChanged] != 2 || stats["edit_"+editclose.OutcomeRevisionAhead] != 1 ||
		stats["edit_"+editclose.OutcomeDimensionsChanged] != 2 || stats["edit_"+editclose.OutcomeRevisionIncomparable] != 2 {
		t.Fatalf("stats %v", nonZero(stats))
	}
}

// microsecondVersion is a strategy_version of the publisher's earlier
// revision scheme: a Unix microsecond time (2023-11-14, synthetic).
const microsecondVersion = "1700000000000000"

// Without a record the edit closes the same alerts, each decided from its
// own record and closed by its id: no request closes by a bucket's or the
// level's filter alone.
func TestAnEditWithoutARecordClosesTheSameAlertsByID(t *testing.T) {
	h := newHarness(t)
	old, current := factsOf("m4"), factsOf("m5")
	for _, alert := range []*fakeAlert{
		opened("x1", "4", old, false, "critical", nil), opened("x2", "4", old, false, "warning", nil),
		opened("xn", "4", old, true, "warning", nil),
		opened("k1", "4", current, false, "critical", nil), opened("k2", "4", current, false, "info", nil),
		opened("kn", "4", current, true, "info", nil),
		opened("c1", "5", current, false, "info", nil),
	} {
		h.link.add(alert)
	}
	closer := h.closer(t)
	if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" {
		t.Fatalf("word %s", word)
	}
	for id, want := range map[string]int{"x1": 1, "x2": 1, "xn": 1, "k2": 1, "k1": 0, "kn": 0, "c1": 0} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d times, want %d (stats %v)", id, got, want, nonZero(closer.Stats()))
		}
	}
	for _, body := range h.link.closeBodies {
		if !strings.Contains(body, `"alert_id"`) {
			t.Fatalf("a close without alert ids: %s", body)
		}
	}
	if closer.Stats()[OutcomeRecordMissing] != 1 || closer.Stats()[OutcomeExpectOwnRecord] != 0 {
		t.Fatalf("stats %v", nonZero(closer.Stats()))
	}
}

// The design relies on the link keeping an alert's labels as its opening
// event wrote them (b7e5beddf internal/lifecycle/plan.go:417), which is
// the link's implementation and not a stated contract. If a label moved
// after opening - to the current version and digest - an edit decision
// about the old version must not close the alert: not by the bucket's
// filter, and not by a retry of a close kept from before.
func TestAnAlertWhoseLabelsMovedIsNotClosedByTheOldVersionsDecision(t *testing.T) {
	h := newHarness(t)
	old, current := factsOf("m4"), factsOf("m5")
	h.link.add(opened("x1", "4", old, false, "critical", nil))
	h.link.add(opened("x2", "4", old, false, "critical", nil))
	h.link.add(opened("y1", "4", old, false, "critical", nil))
	moved := current.ForEvent(false).Digest()
	h.link.beforeClose = func(link *fakeLink, _ *http.Request) {
		link.alerts["x1"].labels["strategy_version"] = "5"
		link.alerts["x1"].labels["strategy_facts_digest"] = moved
		link.beforeClose = nil
	}
	h.link.itemFault = func(id string, n int) string {
		if id == "y1" && n == 0 {
			return "uncertain_unsaved"
		}
		return ""
	}
	roster(t, h)
	closer := h.closer(t)
	closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()})
	if h.link.alert("x1").closes != 0 || h.link.alert("x2").closes != 1 || h.link.alert("y1").closes != 0 {
		t.Fatalf("x1 %d x2 %d y1 %d", h.link.alert("x1").closes, h.link.alert("x2").closes, h.link.alert("y1").closes)
	}
	if closer.Stats()[OutcomeItemPending] != 1 {
		t.Fatalf("stats %v, want y1 kept", nonZero(closer.Stats()))
	}
	h.link.mu.Lock()
	h.link.alerts["y1"].labels["strategy_version"] = "5"
	h.link.mu.Unlock()
	closer.RetryPending(context.Background(), 10)
	if h.link.alert("y1").closes != 0 || closer.Stats()[OutcomePendingLeftDecision] != 1 {
		t.Fatalf("y1 closed %d, stats %v", h.link.alert("y1").closes, nonZero(closer.Stats()))
	}
}

func nonZero(stats map[string]uint64) map[string]uint64 {
	out := map[string]uint64{}
	for word, n := range stats {
		if n > 0 {
			out[word] = n
		}
	}
	return out
}

func spread(id string, mod uint32) bool {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()%mod == 0
}

// faulty makes the link answer as it does under load: every 17th
// request of a route 429, a filtered close's budget spent after 1000
// attempts with 100 more enumerated and answered 504 not_attempted, a dry
// run's after 1000 matches (has_more), and on its first attempt an alert
// now and then busy, in a version conflict, closed with an uncertain
// answer, or not closed with an uncertain answer.
func faulty(link *fakeLink) {
	link.requestBusy = func(_ string, n int) bool { return n%17 == 0 }
	link.closeBudget, link.notAttempted, link.dryBudget = 1000, 100, 1000
	link.itemFault = func(id string, n int) string {
		if n > 0 {
			return ""
		}
		switch {
		case spread(id, 97):
			return "busy"
		case spread(id, 89):
			return "conflict"
		case spread(id, 83):
			return "uncertain_saved"
		case spread(id, 79):
			return "uncertain_unsaved"
		}
		return ""
	}
}

// bigBucket is 36,000 alerts in an orphaned bucket and 1,000 in a kept one.
func bigBucket(h *harness) { orphanedBucket(h, 36000, 1000) }

// orphanedBucket is orphaned alerts in an orphaned bucket and kept ones in a kept
// bucket after them.
func orphanedBucket(h *harness, orphaned, kept int) {
	old, current := factsOf("m4"), factsOf("m5")
	for i := 0; i < orphaned; i++ {
		h.link.add(opened(alertID(i), "4", old, false, "critical", map[string]string{"bk_target_ip": fmt.Sprintf("192.0.2.%d", i%250)}))
	}
	for i := orphaned; i < orphaned+kept; i++ {
		h.link.add(opened(alertID(i), "4", current, false, "critical", nil))
	}
}

// settled says every orphaned alert was closed exactly once with its
// outputs answered, no kept alert was closed, and nothing is pending.
func settled(t *testing.T, h *harness, closer *Closer) bool {
	t.Helper()
	return settledOf(t, h, closer, 36000, 1000)
}

func settledOf(t *testing.T, h *harness, closer *Closer, orphaned, kept int) bool {
	t.Helper()
	h.link.mu.Lock()
	defer h.link.mu.Unlock()
	done := true
	for i := 0; i < orphaned+kept; i++ {
		alert := h.link.alerts[alertID(i)]
		switch {
		case i >= orphaned && alert.closes != 0:
			t.Fatalf("%s outside the decision closed", alert.id)
		case alert.closes > 1:
			t.Fatalf("%s closed %d times", alert.id, alert.closes)
		case i < orphaned && (alert.closes != 1 || !alert.completed):
			done = false
		}
	}
	return done && closer.PendingSize(context.Background()) == 0
}

// A bucket of 36,000 alerts at a link that answers 429,
// 504 not_attempted, version conflicts and uncertain results eventually
// closes whole: every alert once, its outputs answered, nothing outside the
// filter. Busy and unattempted items are found again by the filter and
// uncertain ones retried by id; nothing is lost.
func TestABigBucketUnderLoadClosesWholeAndOnce(t *testing.T) {
	h := newHarness(t)
	bigBucket(h)
	h.opened(t, "41", "4", nil, fingerprints(0, 37000)...)
	faulty(h.link)
	closer := h.closer(t)
	input := EditInput{Scope: testScope, Plan: currentPlan()}
	for round := 0; ; round++ {
		if round == 400 {
			t.Fatalf("not settled after %d rounds: %v", round, nonZero(closer.Stats()))
		}
		closer.Edit(context.Background(), input)
		closer.RetryPending(context.Background(), 5000)
		if settled(t, h, closer) {
			break
		}
	}
	stats := closer.Stats()
	if stats[OutcomeItemRederived] == 0 || stats[OutcomeItemPending] == 0 || stats[OutcomeLinkBusy] == 0 || stats[OutcomePendingAlreadyClosed] == 0 {
		t.Fatalf("the faults did not all happen: %v", nonZero(stats))
	}
}

// A leader that stops partway - requests done, items pending - is followed
// by another with nothing in memory: it runs the same filter again (the link
// keeps no cursor), retries the pending items from the runtime Redis with
// their stored commands, and the bucket closes whole and once. The link never sees one
// operation id with two contents.
func TestANewLeaderCarriesOnWhereTheLastStopped(t *testing.T) {
	const orphaned, kept = 3600, 100
	h := newHarness(t)
	orphanedBucket(h, orphaned, kept)
	h.opened(t, "41", "4", nil, fingerprints(0, orphaned+kept)...)
	faulty(h.link)
	// The bucket a tenth of TestABigBucketUnderLoadClosesWholeAndOnce's, the
	// link's budgets with it: a dry run reads at least what a close may
	// reach, as the link's own dry run, closing nothing, does.
	h.link.closeBudget, h.link.notAttempted, h.link.dryBudget = 100, 10, 110
	input := EditInput{Scope: testScope, Plan: currentPlan()}
	first := h.closer(t)
	// The first leader runs rounds until it has closed some and kept some,
	// and is stopped in the middle of its next filtered close.
	for round := 0; first.PendingSize(context.Background()) == 0; round++ {
		if round == 50 {
			t.Fatalf("the first leader kept nothing: %v", nonZero(first.Stats()))
		}
		first.Edit(context.Background(), input)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.link.beforeClose = func(*fakeLink, *http.Request) { cancel() }
	first.Edit(ctx, input)
	if settledOf(t, h, first, orphaned, kept) {
		t.Fatal("the first leader finished")
	}
	h.link.beforeClose = nil
	operations := len(h.link.operations)
	second := h.closer(t)
	for round := 0; ; round++ {
		if round == 400 {
			t.Fatalf("not settled after %d rounds: %v", round, nonZero(second.Stats()))
		}
		second.RetryPending(context.Background(), 5000)
		second.Edit(context.Background(), input)
		if settledOf(t, h, second, orphaned, kept) {
			break
		}
	}
	if len(h.link.operations) != operations || second.Stats()[OutcomePendingAlreadyClosed]+second.Stats()[OutcomePendingClosed] == 0 {
		t.Fatalf("operations %d -> %d, stats %v", operations, len(h.link.operations), nonZero(second.Stats()))
	}
}

// A walk that finds no alert of the strategy at an older version prunes
// the record's older entries and keeps the current version's.
func TestAnEditThatFindsNoOlderBucketPrunesTheOlderEntries(t *testing.T) {
	h := newHarness(t)
	h.link.add(opened("c1", "5", factsOf("m5"), false, "info", nil))
	h.opened(t, "41", "3", nil, fingerprints(0, 4)...)
	h.opened(t, "41", "4", nil, fingerprints(10, 4)...)
	h.opened(t, "41", "5", nil, fingerprints(20, 4)...)
	closer := h.closer(t)
	if word, buckets := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()}); word != "" || buckets != 0 {
		t.Fatalf("word %q buckets %d", word, buckets)
	}
	record, err := h.record(t).Load(context.Background(), openepisodes.Key{TenantID: "tenant-a", StrategyID: "41"})
	if err != nil || len(record.Entries) != 4 || closer.Stats()[OutcomeRecordPruned] != 8 {
		t.Fatalf("record %+v, %v, stats %v", record, err, nonZero(closer.Stats()))
	}
	for _, entry := range record.Entries {
		if entry.Version != "5" {
			t.Fatalf("entry %+v left", entry)
		}
	}
}

// A bucket an edit walks alert by alert is not read again once a whole pass
// of it ended under the same Plans' key with nothing left it could not
// close: every alert still in it decides as it did. One strategy's old
// revisions, kept open by the stale-alerts ruling, were read and counted
// again each round, about 2,000 reads and a facts-missing count that only
// grew. A changed key walks the bucket again from its first alert.
func TestAWalkedBucketIsNotReadAgainWhileThePlansKeyHolds(t *testing.T) {
	h := newHarness(t)
	h.link.add(opened("k1", "4", factsOf("m5"), false, "critical", nil))
	h.link.add(opened("f1", "4", nil, false, "critical", nil))
	closer := h.closer(t)
	plan := currentPlan()
	edit := func() map[string]uint64 {
		t.Helper()
		if word, _ := closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: plan}); word != "" {
			t.Fatalf("word %s", word)
		}
		return closer.Stats()
	}
	first := edit()
	// k1 carries facts and is decided with its bucket; f1 carries none and
	// is the one alert the edit reads alert by alert.
	if first[OutcomeAlertsRead] != 1 || first["edit_"+editclose.OutcomeFactsMissing] != 1 || first[OutcomeWalkRemembered] != 0 {
		t.Fatalf("the first pass: %v", nonZero(first))
	}
	second := edit()
	if second[OutcomeAlertsRead] != 1 || second["edit_"+editclose.OutcomeFactsMissing] != 1 || second[OutcomeWalkRemembered] != 1 {
		t.Fatalf("the same key again: %v, want nothing read or counted again and the bucket remembered", nonZero(second))
	}
	plan.NoDataSuspended = true
	third := edit()
	if third[OutcomeAlertsRead] != 2 || third["edit_"+editclose.OutcomeFactsMissing] != 2 {
		t.Fatalf("a changed key: %v, want the bucket walked again", nonZero(third))
	}
	if h.link.alert("k1").closes != 0 || h.link.alert("f1").closes != 0 {
		t.Fatalf("kept alerts were closed")
	}
}

// A pass whose close did not settle is not remembered: the next round walks
// the bucket again rather than leave an alert it decided to close open.
func TestAPassWithAnUnsettledCloseIsWalkedAgain(t *testing.T) {
	h := newHarness(t)
	// No facts, and grouped by a dimension the Plans no longer group by: read
	// alert by alert and decided closed. The link answers every close busy.
	h.link.add(opened("d1", "4", nil, false, "critical", map[string]string{"old_dim": "x"}))
	h.link.requestBusy = func(route string, _ int) bool { return route != linkdmanage.RouteSearch }
	closer := h.closer(t)
	for round := 1; round <= 2; round++ {
		closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()})
		if stats := closer.Stats(); stats[OutcomeAlertsRead] != uint64(round) || stats[OutcomeAlertDecidedClose] != uint64(round) ||
			stats[OutcomeWalkRemembered] != 0 {
			t.Fatalf("round %d: %v, want the bucket walked each round while its close fails", round, nonZero(stats))
		}
	}
}

// A pass cut at maxAlertsPerTurn goes on from where it stopped next round,
// but not across a change of the Plans' key: the alerts read before the
// change were decided under the old key, so the walk starts again from the
// bucket's first alert. Carried on instead, the pass would end and be
// remembered with those alerts never decided under the key it is
// remembered by.
func TestAPassCutAndThenUnderANewKeyStartsAgain(t *testing.T) {
	h := newHarness(t)
	for i := 0; i <= maxAlertsPerTurn; i++ {
		h.link.add(opened(fmt.Sprintf("f%04d", i), "4", nil, false, "critical", nil))
	}
	closer := h.closer(t)
	plan := currentPlan()
	closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: plan})
	if stats := closer.Stats(); stats[OutcomeAlertsRead] != maxAlertsPerTurn || stats[OutcomeAlertsTruncated] != 1 {
		t.Fatalf("the first round: %v, want a pass cut at %d", nonZero(stats), maxAlertsPerTurn)
	}
	plan.NoDataSuspended = true
	closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: plan})
	if stats := closer.Stats(); stats[OutcomeAlertsRead] != 2*maxAlertsPerTurn {
		t.Fatalf("the key changed mid-pass: %v, want the walk started again from the first alert", nonZero(stats))
	}
}

// An alert can still join a remembered bucket: a Slot frozen with the
// older Plans, replayed or taken over after the edit, opens at the older
// version. A remembered bucket is read again when an alert opened in it
// after the newest one its pass read, and that alert is decided - here,
// closed for a dimension the Plans no longer group by. With nothing newer
// the bucket stays remembered and is not read (R63).
func TestAnAlertJoiningARememberedBucketIsDecided(t *testing.T) {
	h := newHarness(t)
	h.link.add(opened("f1", "4", nil, false, "critical", nil))
	closer := h.closer(t)
	edit := func() map[string]uint64 {
		t.Helper()
		closer.Edit(context.Background(), EditInput{Scope: testScope, Plan: currentPlan()})
		return closer.Stats()
	}
	edit()
	if stats := edit(); stats[OutcomeWalkRemembered] != 1 || stats[OutcomeWalkReopened] != 0 || stats[OutcomeAlertsRead] != 1 {
		t.Fatalf("nothing newer: %v, want the bucket remembered and not read", nonZero(stats))
	}
	late := opened("d1", "4", nil, false, "critical", map[string]string{"old_dim": "x"})
	late.created = fakeOpenedAt.Add(time.Hour)
	h.link.add(late)
	stats := edit()
	if stats[OutcomeWalkReopened] != 1 || stats[OutcomeAlertsRead] != 3 || h.link.alert("d1").closes != 1 || h.link.alert("f1").closes != 0 {
		t.Fatalf("an alert joined: %v, d1 closed %d times; want the bucket walked again and d1 closed", nonZero(stats), h.link.alert("d1").closes)
	}
	if stats := edit(); stats[OutcomeWalkRemembered] != 2 || stats[OutcomeWalkReopened] != 1 {
		t.Fatalf("after the late alert was decided: %v, want the bucket remembered again", nonZero(stats))
	}
}
