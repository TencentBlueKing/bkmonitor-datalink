// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// An activation whose state generation moves while its Plan stays active
// carries the history of every Level both versions have -- a new trigger, a
// new recovery window, a new formula, and a new threshold too, whose old
// results stand as decided, as Python's check results do --
// and then warms up for one full Slot rather than for its whole window. An
// edit to the query moves the Plan to another Query Group: its old results
// are about other data, and it carries nothing and warms up whole.
func TestAMovedGenerationCarriesTheLevelsBothVersionsHave(t *testing.T) {
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
		levels       []uint32
		outcome      string
		// revisions writes strategy_revision 7 before the edit and 8 with
		// it, and reads the carry back through PlanCarries.
		revisions bool
		// ringRatio publishes a simple ring ratio strategy instead, a
		// detection that reads its query's revision.
		ringRatio bool
		// twoLevels gives the strategy a second Level before anything is
		// published, so an edit can change one Level's detection and not
		// the other's.
		twoLevels bool
	}{
		{name: "a new trigger count", carried: true, outcome: "carried", levels: []uint32{1}, change: func(document map[string]any) {
			detect(document)["trigger_config"].(map[string]any)["count"] = 2
			detect(document)["trigger_config"].(map[string]any)["check_window"] = 3
		}},
		{name: "a new recovery window", carried: true, outcome: "carried", levels: []uint32{1}, change: func(document map[string]any) {
			detect(document)["recovery_config"].(map[string]any)["check_window"] = 4
		}},
		{name: "a new formula", carried: true, outcome: "carried", levels: []uint32{1}, changeFormat: true},
		{name: "one Level's threshold of two", carried: true, outcome: "carried_detect_changed", levels: []uint32{1, 2}, twoLevels: true, change: func(document map[string]any) {
			algorithms := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)
			algorithms[1].(map[string]any)["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": 95}}}
		}},
		{name: "a new threshold", carried: true, outcome: "carried_detect_changed", levels: []uint32{1}, revisions: true, change: func(document map[string]any) {
			document["strategy_revision"] = 8
			algorithm := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)
			algorithm["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": 90}}}
		}},
		// Every Level id changed: the previous version is read whole and
		// shares no Level with the new one.
		{name: "every Level renumbered", carried: false, outcome: "none_no_common_level", change: func(document map[string]any) {
			for _, algorithm := range document["items"].([]any)[0].(map[string]any)["algorithms"].([]any) {
				algorithm.(map[string]any)["level"] = 2
			}
			for _, detect := range document["detects"].([]any) {
				detect.(map[string]any)["level"] = 2
			}
		}},
		// A ring ratio's detection reads its query's revision, so a new metric
		// moves the Plan to another Query Group and changes its detection:
		// the old results are about the old metric (StateCarryGroupChanged).
		// A threshold's does not; a new metric there keeps the generation,
		// and its state with it, as Python keeps its results by item.
		{name: "a ring ratio's new metric", carried: false, outcome: "none_group_changed", ringRatio: true, change: func(document map[string]any) {
			query := document["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)
			query["metric_field"] = "idle"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			original := realThresholdDocuments(t)[0]
			if test.ringRatio {
				original = edit(t, original, func(document map[string]any) {
					algorithm := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)
					algorithm["type"], algorithm["config"] = strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}
				})
			}
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
			if test.revisions {
				original = edit(t, original, func(document map[string]any) { document["strategy_revision"] = 7 })
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
			var outcomes []string
			repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if facts := observation.StateCarry; facts != nil && facts.Scope == "plan" {
					outcomes = append(outcomes, facts.Result)
				}
			}))
			compiler, semantics := runtimePlanCompiler(t)
			publish := func(t *testing.T, reconciler *controlplane.SourceReconciler) controlplane.SnapshotPublicationRef {
				t.Helper()
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
			if len(outcomes) != 1 || outcomes[0] != test.outcome {
				t.Fatalf("carry outcomes %v, want one %s", outcomes, test.outcome)
			}
			before, after := initial.Plans[0].Fact.Selected, changed.Plans[0].Fact.Selected
			if before.StateGeneration == after.StateGeneration || !after.ForceWarming {
				t.Fatalf("the edit did not move the generation: before=%+v after=%+v", before, after)
			}
			if !test.carried {
				if after.Carry != nil || after.RequiredFullSlots != before.RequiredFullSlots {
					t.Fatalf("the edit carried: %+v, want the Plan to warm up whole on %d Slots", after, before.RequiredFullSlots)
				}
				return
			}
			want := execution.StateCarry{From: before.StateGeneration, Levels: test.levels}
			if after.Carry == nil || after.Carry.From != want.From || !slices.Equal(after.Carry.Levels, want.Levels) {
				t.Fatalf("carry = %+v, want %+v", after.Carry, want)
			}
			if after.RequiredFullSlots != 1 {
				t.Fatalf("RequiredFullSlots = %d, want one full Slot behind the guard", after.RequiredFullSlots)
			}
			if test.revisions {
				state, err := repository.LoadActivation(ctx)
				if err != nil {
					t.Fatal(err)
				}
				groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
				if err != nil || len(groups) != 1 {
					t.Fatalf("active groups = (%v, %v), want the one", groups, err)
				}
				reading, err := repository.PlanCarries(ctx, groups[0], controlplane.MaxPlanCarries)
				if err != nil || len(reading.Plans) != 1 {
					t.Fatalf("plan carries = (%+v, %v), want the one Plan", reading, err)
				}
				fact := reading.Plans[0]
				if fact.StrategyRevision != 8 || fact.StateGeneration != after.StateGeneration || fact.CarriedFrom == nil ||
					!fact.CarriedFrom.Found || fact.CarriedFrom.StrategyRevision != 7 || fact.CarriedFrom.StateGeneration != before.StateGeneration {
					t.Fatalf("plan carry %+v (from %+v), want revision 8 under %s, carried from revision 7 under %s",
						fact, fact.CarriedFrom, after.StateGeneration, before.StateGeneration)
				}
			}
			// The record reads back as written, carry included.
			reloaded, err := repository.LoadActivation(ctx)
			if err != nil || len(reloaded.Plans) != 1 || !reloaded.Plans[0].Fact.Selected.Equal(after) {
				t.Fatalf("reloaded = (%+v, %v), want %+v", reloaded.Plans, err, after)
			}
		})
	}
}

// A carried Plan keeps its activation and its carry on object.get when
// another Plan of its Query Group is edited afterwards: that edit opens a new
// Segment and records the carried Plan again, under the same generation and
// without a carry, but the Plan's history did not change.
func TestAPlanCarryOutlivesAnEditOfAnotherPlanOfItsGroup(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	documents := realThresholdDocuments(t)
	threshold := func(t *testing.T, document json.RawMessage, value int) json.RawMessage {
		t.Helper()
		var decoded map[string]any
		if err := json.Unmarshal(document, &decoded); err != nil {
			t.Fatal(err)
		}
		algorithm := decoded["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)
		algorithm["config"] = []any{[]any{map[string]any{"method": "gte", "threshold": value}}}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	set := func(t *testing.T, key string, value json.RawMessage) {
		t.Helper()
		if err := client.Set(ctx, key, string(value), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	set(t, "bkmonitor.cache.strategy_ids", json.RawMessage(`[1001,1002]`))
	set(t, "bkmonitor.cache.strategy_1001", documents[0])
	set(t, "bkmonitor.cache.strategy_1002", documents[1])
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:state-carry-group", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(t *testing.T) controlplane.SnapshotPublicationRef {
		t.Helper()
		result, err := reconciler.Refresh(ctx, source, planner)
		if err != nil || result.Status != controlplane.SourceRefreshPublished {
			t.Fatalf("publish = (%+v, %v)", result, err)
		}
		return result.Publication
	}
	initialActivator, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initialActivator.Ensure(ctx, publish(t)); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(120, 0)
	cutover, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	carries := func(t *testing.T) controlplane.PlanCarryFact {
		t.Helper()
		state, err := repository.LoadActivation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
		if err != nil || len(groups) != 1 {
			t.Fatalf("active groups = (%v, %v), want both strategies in one", groups, err)
		}
		reading, err := repository.PlanCarries(ctx, groups[0], controlplane.MaxPlanCarries)
		if err != nil || len(reading.Plans) != 2 {
			t.Fatalf("plan carries = (%+v, %v), want two Plans", reading, err)
		}
		for _, fact := range reading.Plans {
			if fact.Plan.StrategyID == "1001" {
				return fact
			}
		}
		t.Fatalf("no Plan of strategy 1001 in %+v", reading.Plans)
		return controlplane.PlanCarryFact{}
	}

	// Strategy 1001's threshold: its generation moves and carries.
	set(t, "bkmonitor.cache.strategy_1001", threshold(t, documents[0], 85))
	if _, err := cutover.Ensure(ctx, publish(t)); err != nil {
		t.Fatal(err)
	}
	carried := carries(t)
	if carried.CarriedFrom == nil || !carried.CarriedFrom.Found || carried.ActivatedAt != execution.EvaluationTime(120) || !carried.ActivationFound {
		t.Fatalf("after its own edit = %+v (from %+v), want a carry found, activated at 120s", carried, carried.CarriedFrom)
	}

	// Strategy 1002's threshold, a minute later: a new Segment for the group.
	now = time.Unix(180, 0)
	set(t, "bkmonitor.cache.strategy_1002", threshold(t, documents[1], 95))
	if _, err := cutover.Ensure(ctx, publish(t)); err != nil {
		t.Fatal(err)
	}
	after := carries(t)
	if after.StateGeneration != carried.StateGeneration || after.ActivatedAt != carried.ActivatedAt || !after.ActivationFound ||
		after.CarriedFrom == nil || after.CarriedFrom.StateGeneration != carried.CarriedFrom.StateGeneration ||
		!after.CarriedFrom.Found || !slices.Equal(after.CarriedFrom.Levels, carried.CarriedFrom.Levels) {
		t.Fatalf("after the other Plan's edit = %+v (from %+v), want it as it was: %+v (from %+v)",
			after, after.CarriedFrom, carried, carried.CarriedFrom)
	}
}

// A Plan that carried history, left its Query Group and came back reads as activated
// when it came back, with no carry: the return keeps the generation, warms
// up whole and carries nothing across the hole, so the carry recorded before
// the hold is no longer the Plan's history.
func TestAPlanBackFromAHoldReadsAsActivatedOnItsReturnWithoutACarry(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	original := realThresholdDocuments(t)[0]
	var decoded map[string]any
	if err := json.Unmarshal(original, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)["config"] =
		[]any{[]any{map[string]any{"method": "gte", "threshold": 85}}}
	edited, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["metric_field"] = "idle"
	moved, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	set := func(t *testing.T, key, value string) {
		t.Helper()
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	set(t, "bkmonitor.cache.strategy_ids", `[1001]`)
	set(t, "bkmonitor.cache.strategy_1001", string(original))
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:state-carry-hold", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(t *testing.T) controlplane.SnapshotPublicationRef {
		t.Helper()
		result, err := reconciler.Refresh(ctx, source, planner)
		if err != nil || result.Status != controlplane.SourceRefreshPublished {
			t.Fatalf("publish = (%+v, %v)", result, err)
		}
		return result.Publication
	}
	initialActivator, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initialActivator.Ensure(ctx, publish(t)); err != nil {
		t.Fatal(err)
	}
	state, err := repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil || len(groups) != 1 {
		t.Fatalf("active groups = (%v, %v), want the one", groups, err)
	}
	group := groups[0]
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		group: {Status: execution.ProgressMissing},
	}}
	now := time.Unix(120, 0)
	cutover, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	read := func(t *testing.T) controlplane.PlanCarryFact {
		t.Helper()
		reading, err := repository.PlanCarries(ctx, group, controlplane.MaxPlanCarries)
		if err != nil || len(reading.Plans) != 1 {
			t.Fatalf("plan carries = (%+v, %v), want the one Plan", reading, err)
		}
		return reading.Plans[0]
	}

	// A new threshold at 120s carries.
	set(t, "bkmonitor.cache.strategy_1001", string(edited))
	if _, err := cutover.Ensure(ctx, publish(t)); err != nil {
		t.Fatal(err)
	}
	if carried := read(t); carried.CarriedFrom == nil || carried.ActivatedAt != 120 {
		t.Fatalf("after the edit = %+v, want a carry activated at 120s", carried)
	}

	// The strategy moves to another metric at 180s, another Query Group, and
	// its Query Group drains.
	now = time.Unix(180, 0)
	set(t, "bkmonitor.cache.strategy_1001", string(moved))
	retired, err := cutover.Ensure(ctx, publish(t))
	if err != nil || len(retired.Draining) != 1 || retired.Draining[0].QueryGroup != group {
		t.Fatalf("retirement = (%+v, %v), want %s draining", retired.Draining, err, group)
	}
	progress.byGroup[group] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: group}, NextSlot: 180, LastFullSlot: 120,
		LastCompletionKind: execution.CompletionFull,
	}}

	// It comes back unchanged at 240s: same generation, warming up whole.
	now = time.Unix(240, 0)
	set(t, "bkmonitor.cache.strategy_1001", string(edited))
	reopened, err := cutover.Ensure(ctx, publish(t))
	if err != nil || len(reopened.Plans) != 1 || !reopened.Plans[0].Fact.Selected.ForceWarming || reopened.Plans[0].Fact.Selected.Carry != nil {
		t.Fatalf("return = (%+v, %v), want the Plan back, warming, carrying nothing", reopened.Plans, err)
	}
	for _, draining := range reopened.Draining {
		if draining.QueryGroup == group {
			t.Fatalf("%s still draining after the return: %+v", group, reopened.Draining)
		}
	}
	back := read(t)
	if back.ActivatedAt != 240 || !back.ActivationFound || back.CarriedFrom != nil {
		t.Fatalf("after the return = %+v (from %+v), want activated at 240s with no carry", back, back.CarriedFrom)
	}
}

// failCutoverOnce fails the next cutover commit, once, before it reaches the
// store: the attempt gets as far as it would on a lost compare-and-set.
type failCutoverOnce struct{ armed bool }

func (hook *failCutoverOnce) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if args := cmd.Args(); hook.armed && len(args) > 1 && args[0] == "eval" && args[1] == controlplane.CutoverScriptForTest() {
		hook.armed = false
		return ctx, errors.New("cutover commit failed by the test")
	}
	return ctx, nil
}
func (hook *failCutoverOnce) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (hook *failCutoverOnce) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (hook *failCutoverOnce) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// A Plan's carry is counted once per activation that commits, not once per
// attempt: an attempt whose cutover does not commit has activated nothing,
// and the retry that does is the one activation (counted per attempt, a single
// edit would read carried_detect_changed 2). So is every other outcome of that
// activation - one that carries nothing, and the no-data decision.
func TestAPlanCarryIsCountedOnceWhenTheCutoverIsRetried(t *testing.T) {
	ringRatio := func(document map[string]any) {
		algorithm := document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)
		algorithm["type"], algorithm["config"] = strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}
	}
	for _, test := range []struct {
		name     string
		original func(map[string]any)
		edit     func(map[string]any)
		outcome  string
	}{
		{name: "a new threshold", outcome: "carried_detect_changed", edit: func(document map[string]any) {
			document["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)["config"] =
				[]any{[]any{map[string]any{"method": "gte", "threshold": 85}}}
		}},
		{name: "a ring ratio's new metric", outcome: "none_group_changed", original: ringRatio, edit: func(document map[string]any) {
			document["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["metric_field"] = "idle"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			encode := func(change ...func(map[string]any)) string {
				var decoded map[string]any
				if err := json.Unmarshal(realThresholdDocuments(t)[0], &decoded); err != nil {
					t.Fatal(err)
				}
				// No-data detection on, so the activation also decides the
				// Plan's no-data state.
				decoded["items"].([]any)[0].(map[string]any)["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 5}
				for _, apply := range change {
					if apply != nil {
						apply(decoded)
					}
				}
				encoded, err := json.Marshal(decoded)
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			}
			for key, value := range map[string]string{"bkmonitor.cache.strategy_ids": `[1001]`, "bkmonitor.cache.strategy_1001": encode(test.original)} {
				if err := client.Set(ctx, key, value, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			source := newRedisStrategySource(t, client)
			planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
			if err != nil {
				t.Fatal(err)
			}
			repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:state-carry-retry", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			outcomes := map[string][]string{}
			repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if facts := observation.StateCarry; facts != nil && (facts.Scope == "plan" || facts.Scope == "no_data_plan") {
					outcomes[facts.Scope] = append(outcomes[facts.Scope], facts.Result)
				}
			}))
			compiler, semantics := runtimePlanCompiler(t)
			reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
			if err != nil {
				t.Fatal(err)
			}
			publish := func(t *testing.T) controlplane.SnapshotPublicationRef {
				t.Helper()
				result, err := reconciler.Refresh(ctx, source, planner)
				if err != nil || result.Status != controlplane.SourceRefreshPublished {
					t.Fatalf("publish = (%+v, %v)", result, err)
				}
				return result.Publication
			}
			initialActivator, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := initialActivator.Ensure(ctx, publish(t)); err != nil {
				t.Fatal(err)
			}
			cutover, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(120, 0) })
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", encode(test.original, test.edit), 0).Err(); err != nil {
				t.Fatal(err)
			}
			publication := publish(t)
			hook := &failCutoverOnce{armed: true}
			client.AddHook(hook)
			if _, err := cutover.Ensure(ctx, publication); err == nil || hook.armed {
				t.Fatalf("the first cutover = %v (armed %t), want it to fail on its commit", err, hook.armed)
			}
			if len(outcomes) != 0 {
				t.Fatalf("an attempt that did not commit counted %v", outcomes)
			}
			if _, err := cutover.Ensure(ctx, publication); err != nil {
				t.Fatalf("the retried cutover = %v", err)
			}
			if plan := outcomes["plan"]; len(plan) != 1 || plan[0] != test.outcome {
				t.Fatalf("plan outcomes %v, want %s once for the one activation", plan, test.outcome)
			}
			if noData := outcomes["no_data_plan"]; len(noData) != 1 {
				t.Fatalf("no-data outcomes %v, want one for the one activation", noData)
			}
		})
	}
}
