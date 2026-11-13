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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A backlog across an edit whose oldest Slots are past the replay's reach:
// those are skipped without their query (GAP_SKIPPED), which guards the
// generation they ran under, and the Slots after them, still dated before
// the boundary, are replayed under it - run, not thrown away as if the Plan
// had left - and read the guard: their Level is held for the skipped
// positions, under the guard's word (GAP_SKIPPED). The new generation's
// first Slot does not carry a series a guard held: the series starts over on
// its own window. The skipped positions come before the replayed Slots'
// first point, so the guard held the Level WARMING (skipped_guard_warming);
// one after it would hold it GAPPED. Before the closed Segment answered its
// own Plans, the replayed Slots ended PLAN_NOT_ACTIVE and wrote no state, and
// the series was carried with the holes.
func TestABacklogAcrossAnEditThatStartsQueryFreeGuardsWhatFollows(t *testing.T) {
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 3
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	// Five Slots of the old generation behind at the edit.
	boundary := fixture.base + 5*60 + 59
	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, thresholdTo(81))
	fixture.clock.Store(boundary * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	observed := len(fixture.observed())
	first := execution.EvaluationTime(boundary + 1)
	fixture.clock.Store((boundary + 2) * 1000)
	for attempt := 0; attempt < 60; attempt++ {
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
	endings := fixture.slotEndings()
	skipped, replayed := 0, 0
	for slot := execution.EvaluationTime(fixture.base + 60); slot < execution.EvaluationTime(boundary); slot += 60 {
		switch ended := endings[slot]; ended {
		case "GAP_SKIPPED/GAP_SKIPPED":
			if replayed > 0 {
				t.Fatalf("Slot %d was skipped after a replayed one; endings %v", slot, endings)
			}
			skipped++
		case "COMPLETED_WITH_UNAVAILABLE/GAP_SKIPPED":
			replayed++
		case "":
		default:
			t.Fatalf("the backlog's Slot %d ended %q, want it skipped, or replayed under the guard; endings %v", slot, ended, endings)
		}
	}
	if skipped == 0 || replayed == 0 {
		t.Fatalf("%d Slots skipped and %d replayed under the guard, want both; endings %v", skipped, replayed, endings)
	}
	series := map[string]int{}
	shortWithHistory, startedOver := 0, 0
	for _, observation := range fixture.observed()[observed:] {
		if facts := observation.StateCarry; facts != nil && facts.Scope == "series" {
			series[facts.Result] += facts.Count
		}
	}
	// The last window read is the new Segment's first Slot's.
	for _, observation := range fixture.observed()[observed:] {
		if coverage := observation.HistoryCoverage; coverage != nil && coverage.Levels > 0 && coverage.Guarded == 0 && coverage.Short > 0 {
			if coverage.WorstValid > 1 {
				shortWithHistory++
			} else {
				startedOver++
			}
		}
	}
	if series["skipped_guard_warming"] == 0 || series["carried"] != 0 || series["carried_across_detect"] != 0 {
		t.Fatalf("series carried %v, want the series a guard held started over (skipped_guard_warming)", series)
	}
	if ended := endings[first]; ended != "COMPLETED_WITH_UNAVAILABLE/HISTORY_WARMING" || startedOver == 0 || shortWithHistory != 0 {
		t.Fatalf("the new Segment's first Slot ended %q, windows started over %d, short with history %d; want it warming on its own window, started over",
			ended, startedOver, shortWithHistory)
	}
}
