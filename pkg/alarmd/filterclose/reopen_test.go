// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

type reopenClock struct{ at time.Time }

func (c *reopenClock) now() time.Time { return c.at }

type reopenNoSearch struct{}

func (reopenNoSearch) Search(context.Context, string, json.RawMessage, string, int) (linkdmanage.SearchPage, error) {
	return linkdmanage.SearchPage{}, nil
}

// A strategy-wide close after a close and a reopen expects what the link
// holds. The worker's copy still holds its own sends of the closed episodes
// when the series open again -- the close ran on the leader, the copy is the
// worker's -- so nothing marks the reopen as new; the entries the first close
// pruned are written back by the writer's next pass over the series, and the
// second close counts all of them (a close that counted only the written
// entries counted one of 48 and was refused).
func TestACloseAfterACloseAndAReopenExpectsWhatTheLinkHolds(t *testing.T) {
	const series = 30 // past expectSlack: a record that forgot them is refused
	h := newHarness(t)
	clk := &reopenClock{at: fakeOpenedAt}
	copy, err := activecopy.New(activecopy.Options{Link: reopenNoSearch{}, EventSourceID: "built_in_bk", Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := openepisodes.NewWriter(h.record(t), clk.now)
	if err != nil {
		t.Fatal(err)
	}
	acknowledge := func() {
		t.Helper()
		var events []contract.TriggerEventV1
		for _, fingerprint := range fingerprints(0, series) {
			events = append(events, contract.TriggerEventV1{EventKind: contract.TriggerEventAbnormal, TenantID: testScope.TenantID,
				PlanRef: contract.RuntimePlanRefV1{StrategyID: testScope.StrategyID}, StrategyRef: &contract.StrategySnapshotRef{Revision: 3},
				DedupeMD5: fingerprint, RecordRef: contract.TriggerRecordRefV1{Dimensions: map[string]json.RawMessage{}}})
		}
		writer.Acknowledged(events, copy.Acknowledged(events))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { writer.Run(ctx); close(done) }()
		testwait.Until(t, "the episode writer drains", func() bool { return writer.Pending() == 0 })
		cancel()
		<-done
	}
	link := func(round int, created time.Time) {
		for i, fingerprint := range fingerprints(0, series) {
			h.link.add(&fakeAlert{id: fmt.Sprintf("alert-%d-%02d", round, i), fingerprint: fingerprint, created: created,
				labels: map[string]string{"strategy_id": testScope.StrategyID, "strategy_version": "3"}})
		}
	}
	// The record started long enough before for it to vouch for what the
	// worker writes now.
	h.redis.Client().HSetNX(context.Background(), h.prefix+":openepisodes:v1:"+testScope.TenantID+":"+testScope.StrategyID,
		openepisodes.SinceField, strconv.FormatInt(fakeRecordStart.Unix(), 10))
	acknowledge()
	link(1, fakeOpenedAt)
	if word := h.closer(t).Absent(context.Background(), testScope, "absent", 3); word != OutcomeJobDone {
		t.Fatalf("the first close: %s", word)
	}
	// The series open again two hours on: new alerts at the link, the same
	// fingerprints, and the worker's copy still holding its own sends.
	clk.at = fakeOpenedAt.Add(2 * time.Hour)
	link(2, clk.at)
	acknowledge()
	closer := h.closer(t)
	word := closer.Absent(context.Background(), testScope, "absent", 3)
	stats := closer.Stats()
	if word != OutcomeJobDone || stats[OutcomeExpectExceeded] != 0 {
		t.Fatalf("the close after the reopen: %s (stats %v), want the %d reopened closed against a record that holds them",
			word, nonZero(stats), series)
	}
	for i := 0; i < series; i++ {
		if h.link.alert(fmt.Sprintf("alert-2-%02d", i)).closes != 1 {
			t.Fatalf("reopened alert %d was not closed", i)
		}
	}
}

// A close refused past its expectation is told to Report with the strategy,
// the decision and the numbers; a close within it is not told.
func TestARefusalPastTheExpectationIsReportedWithItsNumbers(t *testing.T) {
	for _, test := range []struct {
		name     string
		alerts   int
		reported bool
	}{
		{name: "48 at the link against one recorded", alerts: 48, reported: true},
		{name: "5 at the link against one recorded", alerts: 5, reported: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.opened(t, testScope.StrategyID, "3", nil, fingerprints(0, 1)...)
			for i := 0; i < test.alerts; i++ {
				h.link.add(&fakeAlert{id: fmt.Sprintf("alert-%02d", i), fingerprint: fingerprints(i, 1)[0],
					labels: map[string]string{"strategy_id": testScope.StrategyID, "strategy_version": "3"}})
			}
			var notices []Notice
			base := h.closer(t)
			closer, err := New(Options{Link: base.link, Commands: base.commands, Pending: base.pending, Record: base.record,
				Report: func(notice Notice) { notices = append(notices, notice) }})
			if err != nil {
				t.Fatal(err)
			}
			word := closer.Absent(context.Background(), testScope, "absent", 3)
			if !test.reported {
				if len(notices) != 0 || word != OutcomeJobDone {
					t.Fatalf("a close within its expectation: %s, notices %+v", word, notices)
				}
				return
			}
			if word != OutcomeExpectExceeded || len(notices) != 1 {
				t.Fatalf("word %s, notices %+v", word, notices)
			}
			got := notices[0]
			if got.Outcome != OutcomeExpectExceeded || got.Scope.StrategyID != testScope.StrategyID || got.Decision != "absent" ||
				got.Expected != 1 || got.Source != ExpectOwnRecord || got.Allowed != 21 || got.Matched <= got.Allowed {
				t.Fatalf("the notice does not place the refusal: %+v", got)
			}
		})
	}
}
