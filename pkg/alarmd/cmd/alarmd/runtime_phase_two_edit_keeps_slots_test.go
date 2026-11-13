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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// installEditedCutoverStrategy rewrites strategy 1001 at another threshold,
// as an operator's edit does: the query is the one it had, so the Query Group
// stays, and the detection moved, so the state generation does.
func installEditedCutoverStrategy(t *testing.T, ctx context.Context, redisClient *redis.Client, threshold int, updateTime int64) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var strategy map[string]any
	if err := json.Unmarshal(raw, &strategy); err != nil {
		t.Fatal(err)
	}
	strategy["update_time"] = updateTime
	item := strategy["items"].([]any)[0].(map[string]any)
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	item["algorithms"].([]any)[0].(map[string]any)["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": threshold}}}
	for _, detect := range strategy["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = cutoverStallTriggerWindow
	}
	encoded, err := json.Marshal(strategy)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", encoded, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

// slotEndings is how each of the Query Group's Slots ended, by evaluation
// time, as the scheduler reported their completions.
func (fixture *cutoverStallFixture) slotEndings() map[execution.EvaluationTime]string {
	endings := map[execution.EvaluationTime]string{}
	for _, observation := range fixture.observed() {
		if observation.Trace.QueryGroupKey == string(fixture.queryGroup) && observation.Stage == "slot_completed" &&
			observation.Result != "retrying" {
			endings[execution.EvaluationTime(observation.Trace.EvaluationTime)] = observation.SlotCompletionKind + "/" + string(observation.ReasonCode)
		}
	}
	return endings
}

// eventsAt is the events strategy 1001 wrote for the Slot.
func (fixture *cutoverStallFixture) eventsAt(slot execution.EvaluationTime) []contract.TriggerEventV1 {
	var events []contract.TriggerEventV1
	for _, event := range fixture.events.snapshot() {
		if event.EvaluationTime == int64(slot) && event.PlanRef.StrategyID == "1001" {
			events = append(events, event)
		}
	}
	return events
}

// An edit lands while a Slot of the edited strategy is in flight. That Slot,
// dated before the cutover's boundary, completes under the configuration it
// was frozen with, instead of PLAN_NOT_ACTIVE as if the strategy had left.
// The new Segment's first Slot - the threshold moved the state generation and
// the state carried - evaluates on the carried state and writes its event,
// instead of being spent writing a warm-up marker (CONFIG_DRIFT). The edit
// costs no Slot.
func TestAnEditWhileASlotIsInFlightLosesNoSlot(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	base := fixture.base
	inFlight := execution.EvaluationTime(base + 60)

	// The next Slot is begun under the initial Segment and stopped in its
	// read: its unfinished projection stays persisted with that Segment's
	// contract.
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.queryGroup, int64(inFlight), 60))
	roundCtx, stopRound := context.WithCancel(ctx)
	fixture.stopInRead.Store(&stopRound)
	result, attempted, err := fixture.runner.RunOne(roundCtx)
	stopRound()
	if !attempted || result.Completed || !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped Slot RunOne = (%+v, %t, %v), want an attempted, uncompleted Slot stopped in its read", result, attempted, err)
	}

	// The edit, cut over a second after the in-flight Slot's time: the Slot
	// falls in the Segment the cutover closes. The edited threshold is one
	// the backend's 5 crosses, so the edited version fires.
	installEditedCutoverStrategy(t, ctx, fixture.redisClient, 1, 1725000600)
	boundary := inFlight + 1
	fixture.clock.Store(int64(boundary) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("leader round %d: %v", round, err)
		}
	}
	closed, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, inFlight)
	if err != nil || closed.Segment.End == nil || *closed.Segment.End != boundary || closed.Segment.Start != fixture.initialSchedule.Segment.Start {
		t.Fatalf("the in-flight Slot's Segment after the edit = (%+v, %v), want the initial Segment closed at %d", closed.Segment, err, boundary)
	}

	// The in-flight Slot runs to its end.
	fixture.clock.Store((int64(inFlight) + 2) * 1000)
	if result, attempted, err := fixture.runner.RunOne(ctx); err != nil || !attempted || !result.Completed {
		t.Fatalf("the in-flight Slot RunOne = (%+v, %t, %v), want it completed", result, attempted, err)
	}
	// Progress moves past a partial gap as past a full Slot; how the Slot
	// ended is what tells them apart.
	if ended := fixture.slotEndings()[inFlight]; ended != "FULL_COMPLETED/none" {
		t.Fatalf("the in-flight Slot ended %q, want FULL_COMPLETED: it was thrown away", ended)
	}

	// The new Segment's first Slot.
	first := execution.EvaluationTime(base + 120)
	fixture.clock.Store((int64(first) + 1) * 1000)
	if result, attempted, err := fixture.runner.RunOne(ctx); err != nil || !attempted || !result.Completed {
		t.Fatalf("the new Segment's first Slot RunOne = (%+v, %t, %v), want it completed", result, attempted, err)
	}
	if ended := fixture.slotEndings()[first]; ended != "FULL_COMPLETED/none" {
		t.Fatalf("the new Segment's first Slot ended %q, want FULL_COMPLETED: it was spent warming up", ended)
	}
	if events := fixture.eventsAt(first); len(events) == 0 {
		t.Fatalf("the new Segment's first Slot wrote no event for the edited strategy; endings %v", fixture.slotEndings())
	}
}

// An edit that carries nothing - every Level renumbered, so no Level is
// common to both versions (none_no_common_level) - still warms the Plan up:
// the new Segment's first Slot is spent on the warm-up marker
// (CONFIG_DRIFT). The new generation holds no state and no marker, as a new
// strategy's does, but the Plan stayed active and ran under the generation
// before; only a Plan new to its activation is judged by what its first Slot
// finds.
func TestAnEditThatCarriesNothingStillWarmsUpItsFirstSlot(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
		for _, algorithm := range first["items"].([]any)[0].(map[string]any)["algorithms"].([]any) {
			algorithm.(map[string]any)["level"] = 2
		}
		for _, detect := range first["detects"].([]any) {
			detect.(map[string]any)["level"] = 2
		}
	})
	boundary := fixture.base + 59
	fixture.clock.Store(boundary * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("leader round %d: %v", round, err)
		}
	}
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID == "1001" && (!record.Fact.Selected.ForceWarming || record.Fact.Selected.Carry != nil || record.Fact.Selected.Arrived) {
			t.Fatalf("the renumbered Plan's activation = %+v, want it forced to warm up, carrying nothing, not new", record.Fact.Selected)
		}
	}
	first := execution.EvaluationTime(boundary + 1)
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.queryGroup, int64(first), 60))
	for attempt := 0; attempt < 20; attempt++ {
		if _, ended := fixture.slotEndings()[first]; ended {
			break
		}
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if _, attempted, err := fixture.runner.RunOne(ctx); err != nil {
			t.Fatal(err)
		} else if !attempted {
			fixture.clock.Store(fixture.now().Add(time.Second).UnixMilli())
		}
	}
	if ended := fixture.slotEndings()[first]; ended != "COMPLETED_WITH_PARTIAL_GAP/CONFIG_DRIFT" {
		t.Fatalf("the new Segment's first Slot ended %q, want it spent on the warm-up marker; endings %v", ended, fixture.slotEndings())
	}
	if events := fixture.eventsAt(first); len(events) != 0 {
		t.Fatalf("the warm-up Slot wrote events: %+v", events)
	}
}
