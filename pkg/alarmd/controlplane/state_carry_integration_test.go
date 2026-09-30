// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An activation whose state generation moves while its Plan stays active
// carries its history when every Level's detection is unchanged -- a new
// trigger, a new recovery window, a new formula -- and then warms up for one
// full Slot rather than for its whole window. A Level whose detection changed
// carries nothing, and the Plan warms up whole as before.
func TestAMovedGenerationCarriesOnlyWhatDetectionLeftUnchanged(t *testing.T) {
	edit := func(t *testing.T, document json.RawMessage, change func(map[string]any)) json.RawMessage {
		t.Helper()
		var decoded map[string]any
		if err := json.Unmarshal(document, &decoded); err != nil {
			t.Fatal(err)
		}
		change(decoded)
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	detect := func(document map[string]any) map[string]any {
		return document["detects"].([]any)[0].(map[string]any)
	}
	for _, test := range []struct {
		name         string
		change       func(map[string]any)
		changeFormat bool
		carried      bool
		// twoLevels gives the strategy a second Level before anything is
		// published, so an edit can change one Level's detection and not
		// the other's.
		twoLevels bool
	}{
		{name: "a new trigger count", carried: true, change: func(document map[string]any) {
			detect(document)["trigger_config"].(map[string]any)["count"] = 2
			detect(document)["trigger_config"].(map[string]any)["check_window"] = 3
		}},
		{name: "a new recovery window", carried: true, change: func(document map[string]any) {
			detect(document)["recovery_config"].(map[string]any)["check_window"] = 4
		}},
		{name: "a new formula", carried: true, changeFormat: true},
		{name: "one Level's threshold of two", carried: false, twoLevels: true, change: func(document map[string]any) {
			algorithms := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)
			algorithms[1].(map[string]any)["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": 95}}}
		}},
		{name: "a new threshold", carried: false, change: func(document map[string]any) {
			algorithm := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)
			algorithm["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": 90}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			original := realThresholdDocuments(t)[0]
			if test.twoLevels {
				original = edit(t, original, func(document map[string]any) {
					item := document["items"].([]any)[0].(map[string]any)
					item["algorithms"] = append(item["algorithms"].([]any), map[string]any{
						"level": 2, "type": "Threshold", "unit_prefix": "", "config": []any{[]any{map[string]any{"method": "gte", "threshold": 90}}}})
					document["detects"] = append(document["detects"].([]any), map[string]any{
						"level": 2, "connector": "and", "trigger_config": map[string]any{"count": 1, "check_window": 1},
						"recovery_config": map[string]any{"check_window": 1}})
				})
			}
			if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
				t.Fatal(err)
			}
			if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(original), 0).Err(); err != nil {
				t.Fatal(err)
			}
			source := newRedisStrategySource(t, client)
			planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
			if err != nil {
				t.Fatal(err)
			}
			repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:state-carry", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			compiler, semantics := runtimePlanCompiler(t)
			publish := func(t *testing.T, reconciler *controlplane.SourceReconciler) controlplane.SnapshotPublicationRef {
				t.Helper()
				if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
					t.Fatalf("pending = (%+v, %v)", result, err)
				}
				result, err := reconciler.Refresh(ctx, source, planner)
				if err != nil || result.Status != controlplane.SourceRefreshPublished {
					t.Fatalf("publish = (%+v, %v)", result, err)
				}
				return result.Publication
			}
			initialReconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
			if err != nil {
				t.Fatal(err)
			}
			initialActivator, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
			if err != nil {
				t.Fatal(err)
			}
			initial, err := initialActivator.Ensure(ctx, publish(t, initialReconciler))
			if err != nil || len(initial.Plans) != 1 {
				t.Fatalf("initial activation = (%+v, %v)", initial.Plans, err)
			}

			changedSemantics := semantics
			if test.changeFormat {
				changedSemantics.IdentitySchemaDigest = strings.Repeat("4", 64)
			} else if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(edit(t, original, test.change)), 0).Err(); err != nil {
				t.Fatal(err)
			}
			changedReconciler, err := controlplane.NewSourceReconciler(repository, compiler, changedSemantics)
			if err != nil {
				t.Fatal(err)
			}
			cutover, err := controlplane.NewScheduleActivationReconciler(repository, compiler, changedSemantics, func() time.Time { return time.Unix(120, 0) })
			if err != nil {
				t.Fatal(err)
			}
			changed, err := cutover.Ensure(ctx, publish(t, changedReconciler))
			if err != nil || len(changed.Plans) != 1 {
				t.Fatalf("changed activation = (%+v, %v)", changed.Plans, err)
			}
			before, after := initial.Plans[0].Fact.Selected, changed.Plans[0].Fact.Selected
			if before.StateGeneration == after.StateGeneration || !after.ForceWarming {
				t.Fatalf("the edit did not move the generation: before=%+v after=%+v", before, after)
			}
			if !test.carried {
				if after.Carry != nil || after.RequiredFullSlots != before.RequiredFullSlots {
					t.Fatalf("a changed detection carried: %+v, want the Plan to warm up whole on %d Slots", after, before.RequiredFullSlots)
				}
				return
			}
			want := execution.StateCarry{From: before.StateGeneration, Levels: []uint32{1}}
			if after.Carry == nil || after.Carry.From != want.From || len(after.Carry.Levels) != 1 || after.Carry.Levels[0] != 1 {
				t.Fatalf("carry = %+v, want %+v", after.Carry, want)
			}
			if after.RequiredFullSlots != 1 {
				t.Fatalf("RequiredFullSlots = %d, want one full Slot behind the guard", after.RequiredFullSlots)
			}
			// The record reads back as written, carry included.
			reloaded, err := repository.LoadActivation(ctx)
			if err != nil || len(reloaded.Plans) != 1 || !reloaded.Plans[0].Fact.Selected.Equal(after) {
				t.Fatalf("reloaded = (%+v, %v), want %+v", reloaded.Plans, err, after)
			}
		})
	}
}
