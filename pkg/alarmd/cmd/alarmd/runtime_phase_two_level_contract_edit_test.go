// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestProductionPhaseTwoStateGenerationEditRewarmsAndResumes: an edit that
// moves a Plan's state generation - a threshold, or a recovery window now
// that the Level contract inputs are part of the generation - must let the
// Query Group resume: the first Slot on the new Segment completes query-free
// under CONFIG_DRIFT while the forced WARMING marker is written, and the
// Slots after it warm and reach a FULL completion. Two ways this used to go
// wrong, both silent on every completion counter: a recovery window edit
// failed the loaded state's Level contract on every attempt, and any
// generation move made the retried Slot collide with its own WARMING marker
// on every attempt, until the Slot aged out ten minutes later.
func TestProductionPhaseTwoStateGenerationEditRewarmsAndResumes(t *testing.T) {
	edits := []struct {
		name string
		// carries is whether the edit leaves every Level's detection as it
		// was, so the activation carries the history across; requiredPoints
		// is then how many positions the edited window needs, which is what
		// a series short of history still warms up on.
		carries        bool
		requiredPoints uint32
		install        func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture)
	}{
		{name: "recovery window", carries: true, requiredPoints: uint32(cutoverStallTriggerWindow + 3 - 1), install: func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) {
			installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
				for _, detect := range first["detects"].([]any) {
					detect.(map[string]any)["recovery_config"].(map[string]any)["check_window"] = 3
				}
			})
		}},
		{name: "threshold", install: func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) {
			installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
				item := first["items"].([]any)[0].(map[string]any)
				for _, algorithm := range item["algorithms"].([]any) {
					for _, group := range algorithm.(map[string]any)["config"].([]any) {
						for _, condition := range group.([]any) {
							condition.(map[string]any)["threshold"] = 81
						}
					}
				}
			})
		}},
	}
	for _, edit := range edits {
		t.Run(edit.name, func(t *testing.T) {
			fixture := startCutoverFixture(t, nil)
			ctx := context.Background()
			base := fixture.base

			activationBefore, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			generationBefore := activationGenerationOf(t, activationBefore.Plans, "1001")

			edit.install(t, ctx, fixture)
			boundary := execution.EvaluationTime(base + 59)
			fixture.clock.Store(int64(boundary) * 1000)
			for attempt := 0; attempt < 2; attempt++ {
				if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
					t.Fatalf("publication refresh %d error = %v", attempt+1, err)
				}
			}
			activationAfter, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if activationGenerationOf(t, activationAfter.Plans, "1001") == generationBefore {
				t.Fatalf("a %s edit must move the state generation: %s", edit.name, generationBefore)
			}
			var requiredFullSlots uint32
			var carry *execution.StateCarry
			for _, record := range activationAfter.Plans {
				if record.Fact.Plan.StrategyID == "1001" {
					requiredFullSlots = record.Fact.Selected.RequiredFullSlots
					carry = record.Fact.Selected.Carry
				}
			}
			if (carry != nil) != edit.carries {
				t.Fatalf("a %s edit carried %+v, want carried=%t", edit.name, carry, edit.carries)
			}
			// A carried activation warms up behind its guard for one full Slot;
			// a series whose carried history is shorter than the edited window
			// still warms up on its window, one position per Slot. This fixture
			// runs one Slot before the edit, so that is what bounds it.
			warmSlots := requiredFullSlots
			if carry != nil {
				warmSlots = edit.requiredPoints
			}

			var full, drifted bool
			idle := false
			for tick := 1; tick <= 16 && !full; tick++ {
				nextAt := fixture.runner.NextReadyAt()
				at := fixture.now().Add(time.Second)
				if nextAt.After(at) {
					at = nextAt.Add(time.Millisecond)
				}
				if idle {
					// Nothing was due: move to the next grid point, the way the
					// dispatcher would only wake this Runner when its next Slot is.
					next := at.Unix() + 60 - at.Unix()%60
					at = time.Unix(next, 0).Add(time.Second)
				}
				fixture.clock.Store(at.UnixMilli())
				result, attempted, err := fixture.runner.RunOne(ctx)
				idle = !attempted
				if err != nil {
					t.Fatalf("tick %d RunOne error = %v: the edit must re-warm the Plan, not fail its Slots", tick, err)
				}
				t.Logf("tick %d at +%ds: attempted=%t completed=%t result=%s reason=%s kind=%s", tick, at.Unix()-base, attempted, result.Completed, result.Result, result.ReasonCode, result.CompletionKind)
				if result.Completed && result.ReasonCode == execution.ReasonCode(contract.ReasonConfigDrift) {
					drifted = true
				}
				if result.Completed && result.CompletionKind == execution.CompletionFull {
					full = true
				}
			}
			if !drifted || !full {
				t.Fatalf("after a %s edit the Query Group must complete the drift Slot query-free and then reach FULL: drifted=%t full=%t", edit.name, drifted, full)
			}
			// The drift Slot, then one warming Slot per required full Slot, then
			// the first FULL one; anything slower is a Slot aging out, not warming.
			if resumed := fixture.now().Unix() - base; resumed > int64(warmSlots+2)*60+1 {
				t.Fatalf("resuming took %ds with %d warming Slots, which is the shape of a Slot aging out rather than warming", resumed, warmSlots)
			}
			for _, observation := range fixture.observed() {
				if failure := observation.QueryFailure; failure != nil && failure.Code == execution.QueryFailureCodeStateContractMismatch {
					t.Fatalf("the loaded state contract mismatch was reported after the edit: %+v", failure)
				}
				if observation.Stage == observability.StageQueryCompleted && observation.Result == observability.ResultFailed {
					t.Fatalf("a query failure was reported after the edit: %+v", observation.QueryFailure)
				}
			}
		})
	}
}

// TestProductionPhaseTwoACarriedEditResumesWithinOneWarmingSlot: an edit that
// leaves detection alone - here the trigger's required count - carries the
// history the window already holds, so after the drift Slot the Plan warms
// up for one full Slot and is back, instead of warming up its whole window
// again. The fixture runs a full window before the edit, which is what makes
// the difference visible: with no history the Plan warms on its window
// whether it carries or not.
func TestProductionPhaseTwoACarriedEditResumesWithinOneWarmingSlot(t *testing.T) {
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 3
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	idle := false
	// drive runs Slots the way the dispatcher would until done says so, and
	// reports whether it got there within the ticks it was given.
	drive := func(ticks int, done func(execution.SlotExecutionResult) bool) bool {
		for tick := 1; tick <= ticks; tick++ {
			nextAt := fixture.runner.NextReadyAt()
			at := fixture.now().Add(time.Second)
			if nextAt.After(at) {
				at = nextAt.Add(time.Millisecond)
			}
			if idle {
				next := at.Unix() + 60 - at.Unix()%60
				at = time.Unix(next, 0).Add(time.Second)
			}
			fixture.clock.Store(at.UnixMilli())
			result, attempted, err := fixture.runner.RunOne(ctx)
			idle = !attempted
			if err != nil {
				t.Fatalf("tick %d RunOne error = %v", tick, err)
			}
			t.Logf("at +%ds: attempted=%t completed=%t reason=%s kind=%s", at.Unix()-fixture.base, attempted, result.Completed, result.ReasonCode, result.CompletionKind)
			if result.Completed && done(result) {
				return true
			}
		}
		return false
	}
	full := func(result execution.SlotExecutionResult) bool {
		return result.CompletionKind == execution.CompletionFull
	}
	// A full window of history, then one more FULL Slot on it.
	fulls := 0
	if !drive(40, func(result execution.SlotExecutionResult) bool {
		if full(result) {
			fulls++
		}
		return fulls >= cutoverStallTriggerWindow
	}) {
		t.Fatal("the Plan did not reach a full window before the edit")
	}

	before := len(fixture.observed())
	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
		for _, detect := range first["detects"].([]any) {
			detect.(map[string]any)["trigger_config"].(map[string]any)["count"] = 2
		}
	})
	boundary := fixture.now().Unix()/60*60 + 59
	fixture.clock.Store(boundary * 1000)
	for attempt := 0; attempt < 2; attempt++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("publication refresh %d error = %v", attempt+1, err)
		}
	}
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID == "1001" && (record.Fact.Selected.Carry == nil || record.Fact.Selected.RequiredFullSlots != 1) {
			t.Fatalf("a trigger count edit did not carry: %+v", record.Fact.Selected)
		}
	}
	idle = false
	if !drive(16, full) {
		t.Fatal("the Plan did not come back to FULL after the edit")
	}
	// The drift Slot, one warming Slot, then FULL on the carried window.
	if resumed := fixture.now().Unix() - boundary; resumed > int64(1+2)*60+1 {
		t.Fatalf("resuming took %ds after the edit, want the drift Slot, one warming Slot and a FULL one", resumed)
	}
	// The one warming Slot is a guard's, and a Slot completes FULL whatever
	// its series' windows hold; what says the history was carried is the
	// windows themselves. The first window read after the edit holds the
	// carried points and this Slot's own - short only by the position of the
	// drift Slot, which completed without a query and wrote none - where a
	// series that had started over would hold this Slot's one point alone.
	carried, summarised := 0, 0
	for _, observation := range fixture.observed()[before:] {
		if facts := observation.StateCarry; facts != nil && facts.Scope == "series" && facts.Result == "carried" {
			carried += facts.Count
		}
		if coverage := observation.HistoryCoverage; coverage != nil && coverage.Levels > 0 {
			summarised++
			if coverage.WorstValid <= 1 {
				t.Fatalf("a window after the edit held %d of %d positions: the series started over instead of carrying", coverage.WorstValid, coverage.WorstRequired)
			}
		}
	}
	if carried == 0 || summarised == 0 {
		t.Fatalf("carried=%d series, summarised=%d windows after the edit: want the series to carry and their windows to be read", carried, summarised)
	}
}

func activationGenerationOf(t *testing.T, records []controlplane.PlanActivationRecord, strategyID string) execution.StateGeneration {
	t.Helper()
	for _, record := range records {
		if record.Fact.Plan.StrategyID == strategyID {
			return record.Fact.Selected.StateGeneration
		}
	}
	t.Fatalf("strategy %s has no activation record", strategyID)
	return ""
}

// installEditedStrategies stores the two cutover fixture strategies with an
// edit applied to the first one, everything else as
// installCutoverStallStrategies stores it for the initial publication.
func installEditedStrategies(t *testing.T, ctx context.Context, redisClient *redis.Client, updateTime int64, edit func(first map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	first["update_time"] = updateTime
	firstItem := first["items"].([]any)[0].(map[string]any)
	firstItem["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	for _, detect := range first["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = cutoverStallTriggerWindow
	}
	// The second strategy is derived before the edit so that only the first
	// one changes.
	unedited, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	edit(first)
	firstEncoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(unedited, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = 1002
	item := second["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 12, "cutover-stall-system.mem"
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.mem"
	secondEncoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001,1002]`,
		"alarm-config.strategy_1001": firstEncoded,
		"alarm-config.strategy_1002": secondEncoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}
