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
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An absent close whose part since the record's start is refused past the
// record (expect_exceeded) while its older part closes its alerts hands on
// the refusal as the word to wait on, and is still a close sent: the link
// closed items, so close_sent counts it, execution.sent says so, and its
// job line is written (degraded, for the refused part). Read from the word
// alone it was neither, and expect_exceeded writes no job line.
func TestAnAbsentCloseThatClosedBesideARefusedPartIsSent(t *testing.T) {
	link := newFakeLinkd(t)
	r := newRecordedLinkd(t, link)
	r.acknowledge(t, recordedAbnormal("41", 3, 1))
	older := time.Now().Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		link.add(&fakeLinkdAlert{id: fmt.Sprintf("older-%d", i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}, created: older})
	}
	// 25 since the record's start against its one entry: past the 21 it
	// allows.
	for i := 0; i < 25; i++ {
		link.add(&fakeLinkdAlert{id: fmt.Sprintf("recent-%02d", i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	var mu sync.Mutex
	var lines []observability.Observation
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.StageAbsentStrategyClose {
				mu.Lock()
				lines = append(lines, observation)
				mu.Unlock()
			}
		})}}
	loop := newAbsentStrategyClose(bundle, nil, r.route)
	execution := loop.closeStrategy(context.Background(), absentalerts.Absent{Key: absentalerts.Key{TenantID: "system", StrategyID: "41"}}, time.Now())
	closed := link.closed()
	for id := range closed {
		if !strings.HasPrefix(id, "older-") {
			t.Fatalf("closed %v: want only the older part's alerts", closed)
		}
	}
	if len(closed) != 3 || execution.word != filterclose.OutcomeExpectExceeded {
		t.Fatalf("closed %v, word %s: want the older three closed and expect_exceeded handed on", closed, execution.word)
	}
	mu.Lock()
	defer mu.Unlock()
	if !execution.sent || loop.Stats()[absentalerts.OutcomeCloseSent] != 1 || len(lines) != 1 || lines[0].Result != observability.ResultDegraded {
		t.Fatalf("sent %v, close_sent %d, job lines %+v: want the close counted sent and its line written degraded",
			execution.sent, loop.Stats()[absentalerts.OutcomeCloseSent], lines)
	}
}

// A scope close is settled only when every part is: a part since the
// record's start whose items came back busy (job_done_rederived) beside an
// older part that closed leaves the digest open, and the next round closes
// what the first part left.
func TestAScopeCloseWithAPartLeftIsNotSettled(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0")
	fixture.opened(t, 1, "192.0.2.1")
	for i := 0; i < 3; i++ {
		fixture.alert(fmt.Sprintf("recent-%d", i), fmt.Sprintf("198.51.100.%d", i))
		fixture.link.add(&fakeLinkdAlert{id: fmt.Sprintf("older-%d", i), labels: map[string]string{"strategy_id": "51", "strategy_version": "7"},
			dimensions: map[string]string{"device": "sda", "bk_target_ip": fmt.Sprintf("203.0.113.%d", i), "bk_target_cloud_id": "0"},
			created:    time.Now().Add(-3 * time.Hour)})
	}
	var busy sync.Map
	busy.Store("on", true)
	fixture.link.itemBusy = func(id string) bool {
		_, on := busy.Load("on")
		return on && strings.HasPrefix(id, "recent-")
	}
	fixture.confirmedStep(t)
	closed := fixture.link.closed()
	if len(closed) != 3 || fixture.loop.Stats()[scopeCloseSettled] != 0 {
		t.Fatalf("closed %v, stats %v: want the older three closed and the digest not settled", closed, fixture.loop.Stats())
	}
	busy.Delete("on")
	fixture.loop.step(context.Background(), fixture.snapshot)
	if closed := fixture.link.closed(); len(closed) != 6 || fixture.loop.Stats()[scopeCloseSettled] != 1 {
		t.Fatalf("closed %v, stats %v: want the part left closed the next round, then settled", closed, fixture.loop.Stats())
	}
}

// The scope close judges each part: a part that ended on a filter it could
// not build (filter_refused) settles that part, not one beside it with
// more (job_done_rederived); and it waits scopeRefusedRounds on a part
// refused past its record only where no part has more. The fixtures
// cannot build a filter that fails, so the rule is held here on the parts
// a decision can end with.
func TestAScopeClosePartSettlesOnlyItself(t *testing.T) {
	for _, tc := range []struct {
		parts            []string
		settled, refused bool
	}{
		{[]string{filterclose.OutcomeJobDoneRederived, filterclose.OutcomeFilterRefused}, false, false},
		{[]string{filterclose.OutcomeJobDone, filterclose.OutcomeFilterRefused}, true, false},
		{[]string{filterclose.OutcomeTargetListTooLarge}, true, false},
		{[]string{filterclose.OutcomeExpectExceeded, filterclose.OutcomeJobDone}, false, true},
		{[]string{filterclose.OutcomeExpectExceeded, filterclose.OutcomeJobDoneRederived}, false, false},
	} {
		result := filterclose.Result{Parts: tc.parts}
		if got := settled(result); got != tc.settled {
			t.Errorf("%v settled %v, want %v", tc.parts, got, tc.settled)
		}
		if got := refusedPastRecord(result); got != tc.refused {
			t.Errorf("%v refused past the record %v, want %v", tc.parts, got, tc.refused)
		}
	}
}

// An absent close is run again within the round only while it closes
// something: a part whose items all came back busy (job_done_rederived,
// nothing closed) is run once, the round's time left notwithstanding, and
// waits for the next round. It was sent: the link answered its items.
func TestAnAbsentCloseThatClosedNothingIsNotRunAgainWithinTheRound(t *testing.T) {
	link := newFakeLinkd(t)
	r := newRecordedLinkd(t, link)
	r.acknowledge(t, recordedAbnormal("41", 3, 1))
	link.add(&fakeLinkdAlert{id: "recent-0", labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	link.itemBusy = func(string) bool { return true }
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: time.Now, Observer: observability.NopObserver{}}}
	loop := newAbsentStrategyClose(bundle, nil, r.route)
	execution := loop.closeStrategy(testwait.Context(t), absentalerts.Absent{Key: absentalerts.Key{TenantID: "system", StrategyID: "41"}}, time.Now())
	if closes := link.requests("/api/v1/alerts/close"); execution.word != filterclose.OutcomeJobDoneRederived || len(closes) != 2 ||
		!execution.sent || r.route.closer.Stats()[filterclose.OutcomeJobAgain] != 0 {
		t.Fatalf("word %s, %d close requests, sent %v, stats %v: want one dry run and one close, sent, not run again",
			execution.word, len(closes), execution.sent, r.route.closer.Stats())
	}
}

// A scope close refused past its record while another part has more is
// tried the next round, not held scopeRefusedRounds: the part since the
// record's start is past it (21 alerts against one entry) and the older
// part's items come back busy.
func TestAScopeCloseRefusedBesideAPartWithMoreIsTriedTheNextRound(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0")
	fixture.opened(t, 1, "192.0.2.1")
	for i := 0; i < 21; i++ {
		fixture.alert(fmt.Sprintf("removed-%02d", i), fmt.Sprintf("198.51.100.%d", i))
	}
	fixture.link.add(&fakeLinkdAlert{id: "older-0", labels: map[string]string{"strategy_id": "51", "strategy_version": "7"},
		dimensions: map[string]string{"device": "sda", "bk_target_ip": "203.0.113.1", "bk_target_cloud_id": "0"},
		created:    time.Now().Add(-3 * time.Hour)})
	fixture.link.itemBusy = func(id string) bool { return id == "older-0" }
	fixture.confirmedStep(t)
	if fixture.recorded.route.closer.Stats()[filterclose.OutcomeExpectExceeded] != 1 {
		t.Fatalf("stats %v: want the part since the record's start refused past it", fixture.recorded.route.closer.Stats())
	}
	sent := fixture.closes()
	fixture.loop.step(context.Background(), fixture.snapshot)
	if fixture.closes() == sent || fixture.loop.Stats()[scopeCloseBackoff] != 0 {
		t.Fatalf("requests %d the next round, stats %v: want it tried again, not held", fixture.closes()-sent, fixture.loop.Stats())
	}
}

// An absent close's job line reads a send as close_sent does: a close the
// link answered item by item is close_sent - a success when it is done or
// its items were refused, as the counter counts both - and a close the
// link refused whole (429) is send_failed, degraded, and not close_sent.
func TestAnAbsentCloseJobLineReadsASendAsTheCounterDoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     func(*fakeLinkd)
		result  observability.Result
		reason  observability.ReasonCode
		counted uint64
	}{
		{"closed", func(*fakeLinkd) {}, observability.ResultSuccess, absentalerts.OutcomeCloseSent, 1},
		{"items refused", func(link *fakeLinkd) { link.itemRefused = func(string) bool { return true } }, observability.ResultSuccess,
			absentalerts.OutcomeCloseSent, 1},
		{"refused whole", func(link *fakeLinkd) { link.closeBusy = true }, observability.ResultDegraded, "send_failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := newFakeLinkd(t)
			r := newRecordedLinkd(t, link)
			r.acknowledge(t, recordedAbnormal("41", 3, 1))
			link.add(&fakeLinkdAlert{id: "recent-0", labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
			tc.set(link)
			var mu sync.Mutex
			var lines []observability.Observation
			bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: time.Now,
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					if observation.Stage == observability.StageAbsentStrategyClose {
						mu.Lock()
						lines = append(lines, observation)
						mu.Unlock()
					}
				})}}
			loop := newAbsentStrategyClose(bundle, nil, r.route)
			loop.closeStrategy(context.Background(), absentalerts.Absent{Key: absentalerts.Key{TenantID: "system", StrategyID: "41"}}, time.Now())
			mu.Lock()
			defer mu.Unlock()
			if len(lines) != 1 || lines[0].Result != tc.result || lines[0].ReasonCode != tc.reason ||
				loop.Stats()[absentalerts.OutcomeCloseSent] != tc.counted {
				t.Fatalf("job lines %+v, close_sent %d: want one %s %s, close_sent %d", lines, loop.Stats()[absentalerts.OutcomeCloseSent],
					tc.result, tc.reason, tc.counted)
			}
		})
	}
}
