// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The loop keeps the Query Groups it could not complete, by word, each once
// per strategy: first and last seen, how many times, and the latest error,
// cut on a rune to 256 bytes. One repeating on every tick moves its last time
// and count and pushes nobody out; past 32 of a word the one seen longest ago
// gives way. An acknowledged close is counted and not kept; every other word
// is listed, empty when none came, the most recently seen first.
func TestTheMaintenanceKeepsTheQueryGroupsItCouldNotComplete(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), nil)
	start := f.now()
	for index := 0; index < maintenanceRecentKept+8; index++ {
		f.advance(time.Second)
		f.m.observe(context.Background(), execution.QueryGroupIdentity(fmt.Sprintf("qg-%02d", index)), closeOutcomeUnavailable,
			fmt.Errorf("plans of the Query Group unreadable: %d", index), 0)
	}
	// One held on every tick for a hundred ticks: counted, not listed a
	// hundred times, and nobody else pushed out.
	held := observability.TraceFields{QueryGroupKey: "qg-held", StrategyID: "4101", BusinessID: "2"}
	heldFrom := f.now().Add(time.Second)
	for tick := 0; tick < 100; tick++ {
		f.advance(time.Second)
		f.m.observeTrace(context.Background(), held, closeOutcomeDeletionUnsettled,
			fmt.Errorf("calendar 7 has not read deleted in every snapshot that names it (tick %d)", tick), 0)
	}
	// The same Query Group held for another strategy is another entry.
	f.advance(time.Second)
	f.m.observeTrace(context.Background(), observability.TraceFields{QueryGroupKey: "qg-held", StrategyID: "4102", BusinessID: "2"},
		closeOutcomeDeletionUnsettled, errors.New("calendar 8"), 0)
	long := strings.Repeat("计划读不出", 40)
	f.m.observeTrace(context.Background(), observability.TraceFields{QueryGroupKey: "qg-send", StrategyID: "4101", BusinessID: "2"},
		closeOutcomeSendFailed, errors.New(long), 3)
	f.m.observe(context.Background(), "qg-acked", closeOutcomeAcked, nil, 5)
	// Served as every error is: a URL's credentials never reach a reader.
	f.advance(time.Second)
	f.m.observe(context.Background(), "qg-console", closeOutcomeSendFailed,
		errors.New("console refused: https://user:secret@example.test/api/close"), 1)

	reading := f.m.Reading()
	unavailable := reading.Recent[closeOutcomeUnavailable]
	if len(unavailable) != maintenanceRecentKept || unavailable[0].QueryGroup != "qg-39" ||
		unavailable[len(unavailable)-1].QueryGroup != "qg-08" || unavailable[0].Error != "plans of the Query Group unreadable: 39" ||
		unavailable[0].Count != 1 || !unavailable[0].FirstAt.Equal(start.Add(40*time.Second)) {
		t.Fatalf("unavailable kept %d, first %+v last %+v; want the %d seen most recently, newest first", len(unavailable), unavailable[0],
			unavailable[len(unavailable)-1], maintenanceRecentKept)
	}
	deletion := reading.Recent[closeOutcomeDeletionUnsettled]
	if len(deletion) != 2 || deletion[0].StrategyID != "4102" {
		t.Fatalf("held closes %+v, want the two strategies of one Query Group apart, the latest first", deletion)
	}
	deletion = deletion[1:]
	if deletion[0].Count != 100 || !deletion[0].FirstAt.Equal(heldFrom) || !deletion[0].LastAt.Equal(heldFrom.Add(99*time.Second)) ||
		deletion[0].StrategyID != "4101" || !strings.Contains(deletion[0].Error, "tick 99") {
		t.Fatalf("held close kept %+v, want it once, counted 100, since its first tick, with the latest error", deletion)
	}
	sent := reading.Recent[closeOutcomeSendFailed]
	if len(sent) != 2 || sent[0].QueryGroup != "qg-console" || strings.Contains(sent[0].Error, "secret") ||
		strings.Contains(sent[0].Error, "user:") || !strings.Contains(sent[0].Error, "console refused") {
		t.Fatalf("send_failed kept %+v, want the console's URL redacted and the rest of the error kept", sent)
	}
	sent = sent[1:]
	if sent[0].StrategyID != "4101" || sent[0].BusinessID != "2" || len(sent[0].Error) > maintenanceErrorBytes ||
		!utf8.ValidString(sent[0].Error) || !strings.HasPrefix(long, sent[0].Error) || len(sent[0].Error) < maintenanceErrorBytes-3 {
		t.Fatalf("send_failed kept %+v, want the strategy and the error cut on a rune to %d bytes", sent, maintenanceErrorBytes)
	}
	if _, kept := reading.Recent[closeOutcomeAcked]; kept || len(f.m.recent[closeOutcomeAcked]) != 0 || reading.Counts[closeOutcomeAcked] != 5 ||
		reading.Counts[closeOutcomeUnavailable] != uint64(maintenanceRecentKept+8) || reading.Counts[closeOutcomeSendFailed] != 4 ||
		reading.Counts[closeOutcomeDeletionUnsettled] != 101 {
		t.Fatalf("counts %v recent words %d; want close_acked counted and not kept", reading.Counts, len(reading.Recent))
	}
	for _, outcome := range observability.EffectiveCloseOutcomes {
		if list, listed := reading.Recent[string(outcome)]; string(outcome) != closeOutcomeAcked && (!listed || list == nil) {
			t.Errorf("%s not listed, want every word but close_acked present", outcome)
		}
	}
	// A copy: what the loop keeps next does not reach a reading given out.
	f.advance(time.Second)
	f.m.observe(context.Background(), "qg-39", closeOutcomeUnavailable, errors.New("later"), 0)
	if reading.Recent[closeOutcomeUnavailable][0].Count != 1 || reading.Recent[closeOutcomeUnavailable][0].Error == "later" {
		t.Fatal("a reading given out changed with the loop")
	}
}

// maintenance.get answers from the loop once it is bound: before, it says
// the loop is not running and is not complete; after, the counts and the
// latest outcomes, by the names the output says.
func TestMaintenanceGetReadsTheLoopOnceItIsBound(t *testing.T) {
	source := &maintenanceSource{}
	op := cliMaintenanceOperation(source)
	if op.ID != "maintenance.get" || !op.Targetable || op.EvidenceScope != "process" {
		t.Fatalf("operation %s targetable %v scope %s", op.ID, op.Targetable, op.EvidenceScope)
	}
	before := op.Run(context.Background(), obchannel.Params{})
	if value := before.Value.(cliMaintenanceReading); value.Running || before.Complete {
		t.Fatalf("unbound: %+v complete %v, want not running and not complete", value, before.Complete)
	}
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), nil)
	f.m.observe(context.Background(), "qg-a", closeOutcomeUnavailable, errors.New("owner is not accepting"), 0)
	source.bind(f.m)
	after := op.Run(context.Background(), obchannel.Params{})
	encoded, err := json.Marshal(after.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Complete || !strings.Contains(string(encoded), `"running":true`) ||
		!strings.Contains(string(encoded), `"unavailable":[{"query_group":"qg-a"`) || !strings.Contains(string(encoded), `"first_at":`) ||
		!strings.Contains(string(encoded), `"error":"owner is not accepting"`) || !strings.Contains(string(encoded), `"counts":{`) {
		t.Fatalf("bound: complete %v %s", after.Complete, encoded)
	}
}
