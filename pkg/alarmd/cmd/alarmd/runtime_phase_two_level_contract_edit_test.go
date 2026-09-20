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
		name    string
		install func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture)
	}{
		{name: "recovery window", install: func(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) {
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
			for _, record := range activationAfter.Plans {
				if record.Fact.Plan.StrategyID == "1001" {
					requiredFullSlots = record.Fact.Selected.RequiredFullSlots
				}
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
			if resumed := fixture.now().Unix() - base; resumed > int64(requiredFullSlots+2)*60+1 {
				t.Fatalf("resuming took %ds with %d required full Slots, which is the shape of a Slot aging out rather than warming", resumed, requiredFullSlots)
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
