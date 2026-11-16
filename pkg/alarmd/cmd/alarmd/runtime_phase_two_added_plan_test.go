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
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// installStrategiesWithAdded stores the cutover fixture's two strategies as
// they were and, when added, a third, 1003: the first one's document on a
// table of its own - a Query Group of its own - at a threshold the
// backend's 5 crosses, so every Slot it evaluates writes an event.
func installStrategiesWithAdded(t *testing.T, ctx context.Context, fixture *cutoverStallFixture, added bool, updateTime int64) {
	t.Helper()
	installEditedStrategies(t, ctx, fixture.redisClient, updateTime, func(map[string]any) {})
	// The source reads the strategies again when this moves.
	if err := fixture.redisClient.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(updateTime, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if !added {
		return
	}
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var third map[string]any
	if err := json.Unmarshal(raw, &third); err != nil {
		t.Fatal(err)
	}
	third["id"], third["update_time"] = 1003, updateTime
	item := third["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 13, "cutover-stall-system.disk"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"], query["result_table_id"] = 60, "system.disk"
	thresholdTo(1)(third)
	encoded, err := json.Marshal(third)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{"alarm-config.strategy_ids": `[1001,1002,1003]`, "alarm-config.strategy_1003": encoded} {
		if err := fixture.redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// publishAt has the leader read and publish what the strategy source holds,
// at the clock's second.
func publishAt(t *testing.T, fixture *cutoverStallFixture, at int64) {
	t.Helper()
	fixture.clock.Store(at * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(context.Background(), true); err != nil {
			t.Fatalf("leader round %d: %v", round, err)
		}
	}
}

// addedGroup is the Query Group strategy 1003 runs in, and its runner.
func addedGroup(t *testing.T, fixture *cutoverStallFixture) (execution.QueryGroupIdentity, phaseTwoQueryGroupRuntime) {
	t.Helper()
	for _, candidate := range fixture.bundle.queryGroups {
		schedule, err := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(context.Background(), candidate)
		if err == nil && len(schedule.Plans) == 1 && schedule.Plans[0].Identity.StrategyID == "1003" {
			return candidate, settledRunner(fixture.bundle, candidate)
		}
	}
	t.Fatal("strategy 1003 has no Query Group")
	return "", nil
}

// endingsOf is how each of a Query Group's Slots ended.
func endingsOf(fixture *cutoverStallFixture, queryGroup execution.QueryGroupIdentity) map[execution.EvaluationTime]string {
	endings := map[execution.EvaluationTime]string{}
	for _, observation := range fixture.observed() {
		if observation.Trace.QueryGroupKey == string(queryGroup) && observation.Stage == "slot_completed" && observation.Result != "retrying" {
			endings[execution.EvaluationTime(observation.Trace.EvaluationTime)] = observation.SlotCompletionKind + "/" + string(observation.ReasonCode)
		}
	}
	return endings
}

// eventsOf is the events a strategy wrote for a Slot.
func eventsOf(fixture *cutoverStallFixture, strategy string, slot execution.EvaluationTime) []contract.TriggerEventV1 {
	var events []contract.TriggerEventV1
	for _, event := range fixture.events.snapshot() {
		if event.EvaluationTime == int64(slot) && event.PlanRef.StrategyID == strategy {
			events = append(events, event)
		}
	}
	return events
}

// runSlot runs a Query Group's rounds from the Slot's first until it ends.
func runSlot(t *testing.T, fixture *cutoverStallFixture, queryGroup execution.QueryGroupIdentity, runner phaseTwoQueryGroupRuntime,
	slot execution.EvaluationTime) string {
	t.Helper()
	fixture.clock.Store(max(fixture.clock.Load(), firstRoundAtMillis(fixture.cfg, queryGroup, int64(slot), 60)))
	for attempt := 0; attempt < 10; attempt++ {
		if ended, done := endingsOf(fixture, queryGroup)[slot]; done {
			return ended
		}
		if nextAt := runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil {
			t.Fatalf("Slot %d RunOne: %v", slot, err)
		} else if !attempted {
			fixture.clock.Store(fixture.now().Add(time.Second).UnixMilli())
		}
	}
	t.Fatalf("Slot %d of %s did not end; endings %v", slot, queryGroup, endingsOf(fixture, queryGroup))
	return ""
}

// A strategy added to a running deployment detects from its first Slot
// (design 013 section 4.6): nothing of it was ever stored, so the Worker
// judges it new, evaluates the Slot - its series warm up on their own
// windows as any new series does - and writes its events, instead of
// spending the Slot on a warm-up marker (CONFIG_DRIFT). Its second Slot,
// which finds the first one's state, is not taken for one that ran before
// under another configuration either.
func TestAnAddedStrategyDetectsFromItsFirstSlot(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	installStrategiesWithAdded(t, ctx, fixture, true, 1725000600)
	publishAt(t, fixture, fixture.base+59)
	queryGroup, runner := addedGroup(t, fixture)
	first := execution.EvaluationTime(fixture.base + 60)
	for _, slot := range []execution.EvaluationTime{first, first + 60} {
		if ended := runSlot(t, fixture, queryGroup, runner, slot); ended != fullEnding {
			t.Fatalf("the added strategy's Slot %d ended %q, want FULL: it was spent warming up", slot, ended)
		}
		if events := eventsOf(fixture, "1003", slot); len(events) == 0 {
			t.Fatalf("the added strategy's Slot %d wrote no event", slot)
		}
	}
}

// An added strategy whose first Slot reads its input only in part has that
// Slot's own guard written for it, and nothing beside: judged new, its
// satisfied marker would be a second write of the same key at the same
// version. The Slot completes without a conflict, and the next one, with
// its input whole, detects.
func TestAnAddedStrategyWhoseFirstInputIsPartialCompletesWithoutConflict(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	installStrategiesWithAdded(t, ctx, fixture, true, 1725000600)
	publishAt(t, fixture, fixture.base+59)
	queryGroup, runner := addedGroup(t, fixture)
	first := execution.EvaluationTime(fixture.base + 60)
	fixture.uqPartial = func(end int64) bool { return end == int64(first) }
	ended := runSlot(t, fixture, queryGroup, runner, first)
	if ended == fullEnding || ended == "COMPLETED_WITH_PARTIAL_GAP/CONFIG_DRIFT" {
		t.Fatalf("the added strategy's partial first Slot ended %q, want it completed on its partial input", ended)
	}
	for _, observation := range fixture.observed() {
		if observation.Trace.QueryGroupKey == string(queryGroup) && observation.Err != nil &&
			observation.ReasonCode == "GAP_GUARD_CONFLICT" {
			t.Fatalf("the partial first Slot met a guard conflict: %v", observation.Err)
		}
	}
	if ended := runSlot(t, fixture, queryGroup, runner, first+60); ended == "COMPLETED_WITH_PARTIAL_GAP/CONFIG_DRIFT" {
		t.Fatalf("the Slot after the partial one ended %q, want it evaluated", ended)
	}
	if events := eventsOf(fixture, "1003", first+60); len(events) == 0 {
		t.Fatal("the Slot after the partial one wrote no event")
	}
}
