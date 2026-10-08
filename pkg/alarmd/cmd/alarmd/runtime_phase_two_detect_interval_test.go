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
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// steppedStrategy rewrites strategy 1002 into strategy 1001's query detected
// every fifteen seconds: the two strategies of one publication differ only by
// the item's detect_interval.
func steppedStrategy(t *testing.T) func(context.Context, *redis.Client, config.Config) {
	return func(ctx context.Context, client *redis.Client, _ config.Config) {
		raw, err := client.Get(ctx, "alarm-config.strategy_1001").Bytes()
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		document["id"] = 1002
		item := document["items"].([]any)[0].(map[string]any)
		item["id"], item["query_md5"], item["detect_interval"] = 12, "cutover-stall-stepped", 15
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, "alarm-config.strategy_1002", encoded, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// runFullSlots runs a Query Group's runner on the fixture's clock until n of
// its Slots complete FULL, and returns the Slot each completed.
func runFullSlots(t *testing.T, fixture *cutoverStallFixture, runner phaseTwoQueryGroupRuntime, queryGroup execution.QueryGroupIdentity, n int) []execution.EvaluationTime {
	t.Helper()
	ctx := context.Background()
	var slots []execution.EvaluationTime
	for attempt := 0; attempt < 400 && len(slots) < n; attempt++ {
		if nextAt := runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		at := fixture.now()
		result, attempted, err := runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("RunOne: %v", err)
		}
		if !attempted {
			fixture.clock.Store(at.Add(time.Second).UnixMilli())
			continue
		}
		if result.Completed && result.CompletionKind == execution.CompletionFull {
			slots = append(slots, loadPhaseTwoProgress(t, ctx, fixture.production, queryGroup).LastFullSlot)
			continue
		}
		fixture.clock.Store(at.Add(time.Second).UnixMilli())
	}
	if len(slots) < n {
		t.Fatalf("%d FULL Slots of %s, want %d", len(slots), queryGroup, n)
	}
	return slots
}

// One publication, two strategies whose queries differ only by detect_interval,
// through the production wiring: the configured one moves to a Query Group of
// its own, scheduled every fifteen seconds and read unaligned with its
// buckets labelled where they start, while the other keeps its minute and
// its aligned query. Both execute and apply state; the configured one's
// FULL Slots are fifteen seconds apart.
func TestRuntimeAConfiguredAndAnUnconfiguredStrategyRunInOnePublication(t *testing.T) {
	ctx := context.Background()
	fixture := startCutoverFixtureWith(t, nil, observability.Discard(observability.ComponentRuntime), steppedStrategy(t))

	var stepped execution.QueryGroupIdentity
	var steppedSchedule execution.FrozenQueryGroupSchedule
	for _, candidate := range fixture.bundle.queryGroups {
		schedule, err := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if schedule.Plans[0].Identity.StrategyID == "1002" {
			stepped, steppedSchedule = candidate, schedule
		}
	}
	if stepped == "" || stepped == fixture.queryGroup {
		t.Fatalf("the configured strategy has Query Group %q beside %q, want one of its own", stepped, fixture.queryGroup)
	}
	if got := steppedSchedule.Plans[0].Spec.EvaluationIntervalSeconds; got != 15 {
		t.Fatalf("the configured strategy is scheduled every %ds, want 15", got)
	}
	base, err := fixture.repository.LoadQueryGroupObject(ctx, fixture.initialSchedule.Segment.ObjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := fixture.repository.LoadQueryGroupObject(ctx, steppedSchedule.Segment.ObjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	if base.QueryPlan.NotTimeAlign || !configured.QueryPlan.NotTimeAlign || configured.QueryPlan.StepMillis != base.QueryPlan.StepMillis ||
		configured.QueryPlan.QueryList[0].TableID != base.QueryPlan.QueryList[0].TableID ||
		configured.QueryPlan.QueryList[0].Offset != "59999ms" || configured.QueryPlan.QueryList[0].OffsetForward != "true" {
		t.Fatalf("queries %+v and %+v, want the same table read aligned and unaligned with a step less a millisecond forward",
			base.QueryPlan, configured.QueryPlan)
	}

	// The fixture runs one Query Group's runner at a time, so the state the
	// production observer saw applied between two reads is that runner's.
	applied := func() int {
		count := 0
		for _, observation := range fixture.observed() {
			if observation.Stage == observability.StageStateApplied && observation.Result == observability.ResultSuccess {
				count++
			}
		}
		return count
	}
	before := applied()
	slots := runFullSlots(t, fixture, settledRunner(fixture.bundle, stepped), stepped, 4)
	for index := 1; index < len(slots); index++ {
		if slots[index]-slots[index-1] != 15 {
			t.Fatalf("FULL Slots %v, want them fifteen seconds apart", slots)
		}
	}
	if fixture.unalignedCalls.Load() == 0 {
		t.Fatal("the configured strategy's Slots completed without an unaligned query")
	}
	afterStepped := applied()
	// The delay each group's advice is rounded to is the one its delay was:
	// fifteen seconds for the group read unaligned, the minute for the other.
	holds := fixture.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	steppedUnit, baseUnit := holds.groups[stepped].delayUnit, holds.groups[fixture.queryGroup].delayUnit
	holds.mu.Unlock()
	if steppedUnit != 15*time.Second || baseUnit != time.Minute {
		t.Fatalf("advice rounded to %v and %v, want 15s for the stepped group and a minute for the other", steppedUnit, baseUnit)
	}
	_ = runOneSlotFull(t, fixture)
	afterBase := applied()
	if afterStepped-before < len(slots) || afterBase == afterStepped {
		t.Fatalf("state applied %d times over the configured strategy's %d Slots and %d over the other's, want both to apply state",
			afterStepped-before, len(slots), afterBase-afterStepped)
	}
}
