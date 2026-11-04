// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// levelCopy is an open-alert copy that knows each series' open Levels
// (contract.OpenLevelSet) and records what it is told was acknowledged.
type levelCopy struct {
	open         []uint32
	known        bool
	acknowledged []contract.TriggerEventV1
}

func (c *levelCopy) Contains(string, string, string) bool                              { return true }
func (c *levelCopy) TrackPlans(execution.QueryGroupIdentity, []execution.PlanIdentity) {}
func (c *levelCopy) Acknowledged(events []contract.TriggerEventV1) {
	c.acknowledged = append(c.acknowledged, events...)
}
func (c *levelCopy) OpenLevels(string, string, string) ([]uint32, bool) { return c.open, c.known }

// An absent series' no-data end carries only the Level the alert is at, as
// the open-alert gate does for an evaluated series: it goes
// when that Level is the one ending, and is held - nothing sent, the entry
// removed, counted level_not_open - when the alert is at a more severe Level.
// Where the copy cannot say which Levels are open, the end goes as before.
func TestAnAbsentSeriesEndGoesOnlyForTheLevelTheAlertIsAt(t *testing.T) {
	plan := internalCompiledPlanAtSnapshot(t, "301", 1, 60, json.RawMessage(`{"enabled":true,"consecutive_windows":2,"missing_data":"evaluate_as_zero"}`), 7)
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"}, CompiledPlan: plan}
	const now = int64(1_788_000_000)
	entry := noDataEndEntryFor("a", strings.Repeat("fa", 16), now-240)
	for _, test := range []struct {
		name  string
		open  []uint32
		known bool
		sent  bool
	}{
		{name: "the alert is at the ending Level", open: []uint32{5}, known: true, sent: true},
		{name: "the alert is at the ending Level, a less severe one open beside it", open: []uint32{9, 5}, known: true, sent: true},
		{name: "the alert is at a more severe Level", open: []uint32{1, 5}, known: true, sent: false},
		{name: "the open Levels are not known", known: false, sent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &reportingSink{}
			copy := &levelCopy{open: test.open, known: test.known}
			coordinator := &SlotExecutionCoordinator{ports: Ports{Events: sink, OpenAlerts: copy}}
			request := execution.SlotExecutionRequest{Operation: execution.OperationNormal}
			request.Contract.Slot.EvaluationTime = execution.EvaluationTime(now)
			sent, held, failed := coordinator.sendNoDataEnds(context.Background(), request, "execution-1", now-60, due,
				map[execution.SeriesIdentityDigest][]execution.NoDataEndEntry{entry.Series: {entry}})
			if failed != 0 {
				t.Fatalf("failed %d", failed)
			}
			if test.sent {
				if len(sent) != 1 || len(held) != 0 || sink.calls != 1 || len(copy.acknowledged) != 1 {
					t.Fatalf("sent %v held %v, sink calls %d, acknowledged %d: want the end sent", sent, held, sink.calls, len(copy.acknowledged))
				}
				if results := sink.received[0][0].LevelResults; len(results) != 1 || results[0].LevelID != 5 || results[0].Result != contract.LevelResultRecovery {
					t.Fatalf("end carries %+v, want Level 5's RECOVERY", results)
				}
				return
			}
			if len(sent) != 0 || len(held) != 1 || held[0] != entry.Key() || sink.calls != 0 || len(copy.acknowledged) != 0 {
				t.Fatalf("sent %v held %v, sink calls %d: want the end held and its entry returned for removal", sent, held, sink.calls)
			}
		})
	}
}
