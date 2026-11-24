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
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// restartAddedStrategyID is a strategy added beside the lifecycle fixture's,
// on the same query, so the one Query Group holds both.
const restartAddedStrategyID = "1009"

// lifecycleEndings is how each Slot of the fixture's Query Group ended, by
// evaluation time, as the scheduler reported its completions.
func (fixture *lifecycleFixture) lifecycleEndings() map[int64]string {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	endings := map[int64]string{}
	for _, observation := range fixture.observations {
		if observation.Trace.QueryGroupKey == string(fixture.queryGroup) && observation.Stage == "slot_completed" &&
			observation.Result != "retrying" {
			endings[observation.Trace.EvaluationTime] = observation.SlotCompletionKind + "/" + string(observation.ReasonCode)
		}
	}
	return endings
}

// activationOf is the strategy's record in the current activation, as the
// store keeps it.
func (fixture *lifecycleFixture) activationOf(strategyID string) (execution.ActivatedPlan, bool) {
	fixture.t.Helper()
	repository := fixture.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Repository.(*controlplane.RedisCatalogRepository)
	activation, err := repository.LoadActivation(context.Background())
	if err != nil {
		fixture.t.Fatal(err)
	}
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID == strategyID {
			return record.Fact.Selected, true
		}
	}
	return execution.ActivatedPlan{}, false
}

// eventsOfStrategy is every written threshold event of the strategy for the
// round's Slot.
func (fixture *lifecycleFixture) eventsOfStrategy(strategyID string, round int64) []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	var matched []contract.TriggerEventV1
	for _, event := range fixture.sink.written {
		if !noDataTagged(event) && event.PlanRef.StrategyID == strategyID && event.EvaluationTime == fixture.evaluationAt(round) {
			matched = append(matched, event)
		}
	}
	return matched
}

// A process restart with the Plan's State stored and no gap marker - the
// Plan ran from the first activation, which forces no warm-up - goes on
// where it was: the restarted Worker's first Slot is evaluated and writes
// its events, and no Slot is spent on a warm-up marker. The restart moves no
// generation and adds no Plan, so the record is neither forced nor new.
func TestARestartedWorkerDoesNotWarmUpAPlanThatHasRun(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true}, nil)
	// Above the strategy's threshold of 80, so every Slot evaluated alerts.
	fixture.serve(false, 95, lifecycleHostA)
	fixture.mustAttempt(1)
	fixture.mustAttempt(2)
	if events := fixture.eventsOfStrategy(lifecycleStrategyID, 2); len(events) == 0 {
		t.Fatal("fixture: the strategy wrote no event before the restart")
	}

	fixture.replace("alarmd-worker-0")
	fixture.mustAttempt(3)
	endings := fixture.lifecycleEndings()
	if ended := endings[fixture.evaluationAt(3)]; ended != fullEnding {
		t.Fatalf("the restarted Worker's first Slot ended %q, want FULL; endings %v", ended, endings)
	}
	if events := fixture.eventsOfStrategy(lifecycleStrategyID, 3); len(events) == 0 {
		t.Fatal("the restarted Worker's first Slot wrote no event: it was spent warming up")
	}
	for slot, ended := range endings {
		if ended == "COMPLETED_WITH_PARTIAL_GAP/CONFIG_DRIFT" {
			t.Fatalf("Slot %d ended on a warm-up marker; endings %v", slot, endings)
		}
	}
	if record, found := fixture.activationOf(lifecycleStrategyID); !found || record.ForceWarming || record.Arrived {
		t.Fatalf("the restarted Plan's activation = %+v (found %t), want it neither forced nor new", record, found)
	}
}

// A strategy added beside the fixture's, and the process restarted before
// the added one's first Slot: that Slot is still the one that judges it, by
// what it finds of it - nothing - and it is not warmed up. The record the
// cutover wrote it under, new to that activation and forced, is what the
// restarted Worker reads.
func TestAStrategyAddedAndRestartedBeforeItsFirstSlotIsNotWarmedUp(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true}, nil)
	// Above the strategy's threshold of 80, so every Slot evaluated alerts.
	fixture.serve(false, 95, lifecycleHostA)
	fixture.mustAttempt(1)
	first := fixture.addBeside(restartAddedStrategyID, 1)
	if record, found := fixture.activationOf(restartAddedStrategyID); !found || !record.ForceWarming || !record.Arrived {
		t.Fatalf("the added Plan's activation = %+v (found %t), want it forced and new to the activation", record, found)
	}
	for round := int64(2); round < first; round++ {
		fixture.mustAttempt(round)
	}

	fixture.replace("alarmd-worker-0")
	if record, found := fixture.activationOf(restartAddedStrategyID); !found || !record.ForceWarming || !record.Arrived {
		t.Fatalf("after the restart the added Plan's activation = %+v (found %t), want the record it was added under", record, found)
	}
	fixture.mustAttempt(first)
	endings := fixture.lifecycleEndings()
	if ended := endings[fixture.evaluationAt(first)]; ended != fullEnding {
		t.Fatalf("the added strategy's first Slot ended %q, want FULL: it was warmed up; endings %v", ended, endings)
	}
	for _, strategyID := range []string{lifecycleStrategyID, restartAddedStrategyID} {
		if events := fixture.eventsOfStrategy(strategyID, first); len(events) == 0 {
			t.Fatalf("strategy %s wrote no event for the added strategy's first Slot", strategyID)
		}
	}
}

// addBeside adds a strategy on the fixture strategy's query, so it joins its
// Query Group, through the leader's publication, and returns the round of
// the first Slot of the Segment that holds it.
func (fixture *lifecycleFixture) addBeside(strategyID string, after int64) int64 {
	t := fixture.t
	t.Helper()
	ctx := context.Background()
	raw, err := fixture.redis.Get(ctx, "alarm-config.strategy_"+lifecycleStrategyID).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	id, err := strconv.Atoi(strategyID)
	if err != nil {
		t.Fatal(err)
	}
	document["id"] = id
	document["name"] = "added beside"
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	lastUpdated, err := fixture.redis.Get(ctx, "alarm-config.last_updated").Int64()
	if err != nil {
		t.Fatal(err)
	}
	catalog := fixture.bundle.dependencies.Ownership.(*productionPhaseTwoOwnership).dependencies.Catalog
	before, err := catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(after)))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":           `[` + lifecycleStrategyID + `,` + strategyID + `]`,
		"alarm-config.strategy_" + strategyID: encoded,
		"alarm-config.last_updated":           strconv.FormatInt(lastUpdated+1, 10),
	} {
		if err := fixture.redis.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	var first int64
	testwait.Until(t, "a Segment holds the added strategy", func() bool {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after the add: %v", err)
		}
		for round := after + 1; round <= after+30; round++ {
			schedule, err := catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
			if err == nil && schedule.Segment.ObjectDigest != before.Segment.ObjectDigest && len(schedule.Plans) == 2 {
				first = round
				return true
			}
		}
		return false
	})
	return first
}
