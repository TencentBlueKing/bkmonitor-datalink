// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestRedisCatalogRuntimeFreezesG4HistoricalRequirements(t *testing.T) {
	tests := []struct {
		name       string
		strategyID int64
		kind       string
		metric     string
		table      string
		dimensions []string
		config     any
		datasets   []execution.DatasetName
	}{
		{
			name:       "SimpleRingRatio primary and previous",
			strategyID: 401,
			kind:       strategy.DetectorKindSimpleRingRatio,
			metric:     "usage",
			table:      "system.cpu",
			dimensions: []string{"host"},
			config:     map[string]any{"floor": 50, "ceil": nil},
			datasets:   []execution.DatasetName{"primary", "previous"},
		},
		{
			name:       "OsRestart primary and history",
			strategyID: 402,
			kind:       strategy.DetectorKindOsRestart,
			metric:     "uptime",
			table:      "system.env",
			dimensions: []string{"bk_target_cloud_id", "bk_target_ip"},
			config:     map[string]any{},
			datasets:   []execution.DatasetName{"primary", "uptime_history"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := freezeG4Slot(t, test.strategyID, test.kind, test.metric, test.table, test.dimensions, test.config)
			if len(fact.Requirements) != len(test.datasets) {
				t.Fatalf("frozen requirements = %+v, want datasets %v", fact.Requirements, test.datasets)
			}
			got := make(map[execution.DatasetName]execution.DataRequirement, len(fact.Requirements))
			for _, requirement := range fact.Requirements {
				got[requirement.DatasetName] = requirement
			}
			for _, dataset := range test.datasets {
				requirement, ok := got[dataset]
				if !ok {
					t.Fatalf("missing frozen dataset %q: %+v", dataset, fact.Requirements)
				}
				if len(requirement.Consumers) != 1 || !requirement.Consumers[0].Consumer.HasLevel ||
					requirement.Consumers[0].Consumer.LevelID != 1 {
					t.Fatalf("dataset %q consumers = %+v", dataset, requirement.Consumers)
				}
			}
		})
	}
}

func TestRedisCatalogRuntimeKeepsFrozenRequirementFacts(t *testing.T) {
	catalog := buildG4Catalog(t, g4SourceStrategy(
		t, 405, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil},
	))
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	templates := make(map[execution.RequirementID]execution.DataRequirementTemplate)
	for _, template := range catalog.QueryGroups[0].Plans[0].RequirementTemplates {
		templates[template.RequirementID] = template
	}
	for _, requirement := range fact.Requirements {
		template, ok := templates[requirement.RequirementID]
		if !ok {
			t.Fatalf("requirement %q has no frozen template", requirement.RequirementID)
		}
		if requirement.DatasetName != template.DatasetName || requirement.Role != template.Role ||
			requirement.LogicalQueryRef != template.LogicalQueryRef || requirement.RelativeWindow != template.RelativeWindow ||
			requirement.StepMillis != template.StepMillis || requirement.AlignmentMillis != template.AlignmentMillis ||
			requirement.ResultWindowPolicy != template.ResultWindowPolicy || requirement.ReadinessClass != template.ReadinessClass ||
			!reflect.DeepEqual(requirement.InputProjection, template.InputProjection) ||
			!reflect.DeepEqual(requirement.RequiredColumns, template.RequiredColumns) ||
			!reflect.DeepEqual(requirement.PointOffsetsSeconds, template.PointOffsetsSeconds) ||
			!reflect.DeepEqual(requirement.NamedPoints, template.NamedPoints) {
			t.Fatalf("materialized requirement = %+v, want frozen facts %+v", requirement, template)
		}
	}
}

func TestRedisCatalogRuntimeFreezesSharedG4RequirementsWithoutLosingConsumers(t *testing.T) {
	catalog := buildG4Catalog(t,
		g4SourceStrategy(t, 410, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}),
		g4SourceStrategy(t, 411, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}),
	)
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(fact.Requirements) != 2 {
		t.Fatalf("requirements = %+v, want shared primary and previous", fact.Requirements)
	}
	for _, requirement := range fact.Requirements {
		if len(requirement.Consumers) != 2 {
			t.Fatalf("requirement %q consumers = %+v", requirement.DatasetName, requirement.Consumers)
		}
		for index, consumer := range requirement.Consumers {
			if !consumer.Consumer.HasLevel || consumer.Consumer.LevelID != 1 ||
				consumer.Consumer.Plan.StrategyID != strconv.Itoa(410+index) {
				t.Fatalf("requirement %q consumers = %+v", requirement.DatasetName, requirement.Consumers)
			}
		}
	}
}

func TestRedisCatalogRuntimeFreezesOnlyCompiledLevels(t *testing.T) {
	document := withSecondG4Level(t, g4LegacyStrategyDocument(
		t, 420, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"floor": 50, "ceil": nil},
	))
	catalog := buildG4Catalog(t, controlplane.SourceStrategy{
		SourceID: "420", Document: document,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	})
	plan := &catalog.QueryGroups[0].Plans[0]
	extra := plan.RequirementTemplates[0]
	extra.RequirementID = ""
	extra.ConsumerLevelID = 99
	extra, err := execution.BuildDataRequirementTemplate(extra)
	if err != nil {
		t.Fatal(err)
	}
	plan.RequirementTemplates = append(plan.RequirementTemplates, extra)
	catalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", catalog.QueryGroups))

	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(fact.Requirements) != 4 {
		t.Fatalf("requirements = %+v, want two inputs for each compiled Level", fact.Requirements)
	}
	levels := make(map[uint32]int)
	for _, requirement := range fact.Requirements {
		if len(requirement.Consumers) != 1 || !requirement.Consumers[0].Consumer.HasLevel {
			t.Fatalf("requirement consumers = %+v", requirement.Consumers)
		}
		levels[requirement.Consumers[0].Consumer.LevelID]++
	}
	if !reflect.DeepEqual(levels, map[uint32]int{1: 2, 2: 2}) {
		t.Fatalf("materialized Levels = %+v", levels)
	}
}

func TestRedisCatalogRuntimeRejectsDanglingG4QueryReference(t *testing.T) {
	catalog := buildG4Catalog(t, g4SourceStrategy(
		t, 430, strategy.DetectorKindOsRestart, map[string]any{},
	))
	plan := &catalog.QueryGroups[0].Plans[0]
	delete(plan.QueryPlans, plan.RequirementTemplates[1].LogicalQueryRef)
	catalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", catalog.QueryGroups))

	_, err := freezeG4Catalog(t, catalog)
	if err == nil {
		t.Fatal("FreezeSlotContract() error = nil, want dangling query rejection")
	}
	if got := fmt.Sprintf("%T", err); got != "*controlplane.FreezeSlotContractError" {
		t.Fatalf("FreezeSlotContract() error type = %s, want classified input-closure failure", got)
	}
	var classified *controlplane.FreezeSlotContractError
	if !errors.As(err, &classified) || classified.Class != controlplane.FreezeSlotFailureInputClosure {
		t.Fatalf("FreezeSlotContract() class = %+v, want %q", classified, controlplane.FreezeSlotFailureInputClosure)
	}
	if strings.Contains(err.Error(), "QueryPlanFacts") || errors.Unwrap(err) == nil ||
		!strings.Contains(errors.Unwrap(err).Error(), "no QueryPlanFacts") {
		t.Fatalf("FreezeSlotContract() error = %v, unwrap=%v, want safe class with preserved cause", err, errors.Unwrap(err))
	}
}

func TestRedisCatalogRuntimeG4RequirementOrderIsStable(t *testing.T) {
	catalog := buildG4Catalog(t,
		g4SourceStrategy(t, 440, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}),
		g4SourceStrategy(t, 441, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}),
	)
	first, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Requirements, second.Requirements) {
		t.Fatalf("requirement order changed: first=%+v second=%+v", first.Requirements, second.Requirements)
	}
	if !sort.SliceIsSorted(first.Requirements, func(i, j int) bool {
		return first.Requirements[i].RequirementID < first.Requirements[j].RequirementID
	}) {
		t.Fatalf("requirements are not identity ordered: %+v", first.Requirements)
	}
}

func TestRedisCatalogRuntimeExpandsLegacyThresholdRequirementToEveryCompiledLevel(t *testing.T) {
	document := withSecondG4Level(t, g4LegacyStrategyDocument(
		t, 450, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"host"},
		[]any{map[string]any{"method": "gt", "threshold": 80}},
	))
	catalog := buildG4Catalog(t, controlplane.SourceStrategy{
		SourceID: "450", Document: document,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	})
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(fact.Requirements) != 1 || fact.Requirements[0].Role != execution.InputRolePrimary ||
		len(fact.Requirements[0].Consumers) != 2 {
		t.Fatalf("legacy Threshold requirement = %+v", fact.Requirements)
	}
	got := fact.Requirements[0].Consumers
	for index, levelID := range []uint32{1, 2} {
		if got[index].Consumer.Plan.StrategyID != "450" || !got[index].Consumer.HasLevel ||
			got[index].Consumer.LevelID != levelID {
			t.Fatalf("legacy Threshold consumers = %+v", got)
		}
	}
}

func TestRedisCatalogRuntimeAddsLegacyPrimaryOnlyToUncoveredLevels(t *testing.T) {
	document := mixedLegacyAndG4Levels(t, g4LegacyStrategyDocument(
		t, 451, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"floor": 50, "ceil": nil},
	))
	catalog := buildG4Catalog(t, controlplane.SourceStrategy{
		SourceID: "451", Document: document,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	})
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}

	primaryByLevel := map[uint32][]execution.DataRequirement{}
	for _, requirement := range fact.Requirements {
		if requirement.Role != execution.InputRolePrimary {
			continue
		}
		for _, consumer := range requirement.Consumers {
			primaryByLevel[consumer.Consumer.LevelID] = append(primaryByLevel[consumer.Consumer.LevelID], requirement)
		}
	}
	for _, levelID := range []uint32{1, 2} {
		if len(primaryByLevel[levelID]) != 1 {
			t.Fatalf("Level %d PRIMARY requirements = %+v, want exactly one", levelID, primaryByLevel[levelID])
		}
	}
	legacy := primaryByLevel[1][0]
	explicit := primaryByLevel[2][0]
	if legacy.DatasetName == "primary" || explicit.DatasetName != "primary" {
		t.Fatalf("legacy/explicit PRIMARY = %+v / %+v", legacy, explicit)
	}
	if legacy.LogicalQueryRef != explicit.LogicalQueryRef || legacy.RelativeWindow != explicit.RelativeWindow ||
		legacy.StepMillis != explicit.StepMillis || legacy.AlignmentMillis != explicit.AlignmentMillis {
		t.Fatalf("legacy/explicit PRIMARY query facts drifted: legacy=%+v explicit=%+v", legacy, explicit)
	}
	if len(fact.Requirements) != 3 {
		t.Fatalf("requirements = %+v, want legacy PRIMARY plus SRR PRIMARY/previous", fact.Requirements)
	}
}

func TestRedisCatalogRuntimeReplaysPreG4HistoricalSegmentAfterG4StateGenerationUpgrade(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g4-generation-upgrade", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	threshold := g4SourceStrategy(t, 460, strategy.DetectorKindThreshold,
		[]any{map[string]any{"method": "gt", "threshold": 80}},
	)
	previousCatalog := buildG4Catalog(t, threshold)
	previousSnapshot, _, err := repository.PublishCatalog(ctx, previousCatalog)
	if err != nil {
		t.Fatal(err)
	}
	previousSchedule := frozenSchedule(t, previousSnapshot.Publication, previousCatalog.QueryGroups[0], 60, nil)
	previousActivation := activationState(t, 1, previousSnapshot, previousSchedule, nil)
	legacyGeneration := legacyStateGeneration(t, previousCatalog.QueryGroups[0].Plans[0], stateSemantics)
	currentGeneration := compiledStateGeneration(t, compiler, previousCatalog.QueryGroups[0], previousCatalog.QueryGroups[0].Plans[0], stateSemantics)
	if legacyGeneration == currentGeneration {
		t.Fatal("pre-G4 and G4 state generations unexpectedly match")
	}
	if len(previousActivation.Plans) != 1 {
		t.Fatalf("previous activation Plans = %+v", previousActivation.Plans)
	}
	previousActivation.Plans[0].Fact.Selected.StateGeneration = legacyGeneration
	if err := repository.CompareAndSetInitialScheduleActivation(
		ctx, controlplane.ActivationExpectation{}, previousActivation,
		[]execution.InitialScheduleActivationFact{{Segment: previousSchedule.Segment}},
	); err != nil {
		t.Fatal(err)
	}

	currentCatalog := buildG4Catalog(t, threshold,
		g4SourceStrategy(t, 461, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}),
	)
	if currentCatalog.QueryGroups[0].Identity != previousCatalog.QueryGroups[0].Identity {
		t.Fatalf("G4 cutover changed Query Group identity: previous=%q current=%q",
			previousCatalog.QueryGroups[0].Identity, currentCatalog.QueryGroups[0].Identity)
	}
	currentSnapshot, _, err := repository.PublishCatalog(ctx, currentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewScheduleActivationReconciler(
		repository, compiler, stateSemantics, func() time.Time { return time.Unix(120, 0) },
	)
	if err != nil {
		t.Fatal(err)
	}
	currentActivation, err := reconciler.Ensure(ctx, currentSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if len(currentActivation.Plans) != 2 {
		t.Fatalf("current G4 activation Plans = %+v", currentActivation.Plans)
	}
	currentThreshold, ok := activationForStrategy(currentActivation, "460")
	if !ok || currentThreshold.Fact.Selected.StateGeneration != currentGeneration {
		t.Fatalf("current Threshold activation = %+v, want generation %q", currentThreshold, currentGeneration)
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := previousCatalog.QueryGroups[0].Identity
	currentSchedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, 120)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: currentSchedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: currentSchedule.Segment.Start, EvaluationTime: 120,
		DuePlans: currentSchedule.DuePlanRefs(120),
	}); err != nil {
		t.Fatalf("current G4 Segment did not freeze: %v", err)
	}

	historicalSchedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, 60)
	if err != nil {
		t.Fatal(err)
	}
	if historicalSchedule.Segment.End == nil || *historicalSchedule.Segment.End != 120 ||
		historicalSchedule.Segment.Publication.SnapshotRevision != previousSnapshot.Publication.SnapshotRevision ||
		len(historicalSchedule.DuePlanRefs(60)) != 1 {
		t.Fatalf("historical Segment facts = %+v", historicalSchedule)
	}
	_, err = runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: historicalSchedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: historicalSchedule.Segment.Start, EvaluationTime: 60,
		DuePlans: historicalSchedule.DuePlanRefs(60),
	})
	if err != nil {
		var classified *controlplane.FreezeSlotContractError
		if !errors.As(err, &classified) || classified.Class != controlplane.FreezeSlotFailurePlanMaterialize ||
			errors.Unwrap(err) == nil || !strings.Contains(errors.Unwrap(err).Error(), "state generation differs") {
			t.Fatalf("historical Segment failed outside state-generation compatibility: %v", err)
		}
		t.Fatalf("pre-G4 historical Segment was recompiled with the G4 state-generation formula: %v", err)
	}
}

func legacyStateGeneration(
	t *testing.T,
	plan controlplane.FrozenPlan,
	semantics strategy.StateSemantics,
) execution.StateGeneration {
	t.Helper()
	executionSemantics := plan.Plan.StrategyIR.ExecutionSemantics
	digest, err := contract.DeriveStateCompatibilityHashV1(contract.StateCompatibilityInputV1{
		StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
		IdentitySchemaDigest: semantics.IdentitySchemaDigest, EvaluationScope: executionSemantics.EvaluationScope,
		AggregationInterval: executionSemantics.AggregationInterval, EvaluationInterval: executionSemantics.EvaluationInterval,
		SourceTimeSemanticsVersion:  semantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return execution.StateGeneration(digest)
}

func compiledStateGeneration(
	t *testing.T,
	compiler *strategy.PlanCompiler,
	group controlplane.QueryGroup,
	plan controlplane.FrozenPlan,
	semantics strategy.StateSemantics,
) execution.StateGeneration {
	t.Helper()
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: semantics,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok || result.PlanTerminal() != nil || len(result.LevelTerminals()) != 0 {
		t.Fatalf("real G4 Plan did not compile: plan=%v terminal=%+v levels=%+v", ok, result.PlanTerminal(), result.LevelTerminals())
	}
	return execution.StateGeneration(compiled.StateCompatibilityHash())
}

func activationForStrategy(state controlplane.ActivationState, strategyID string) (controlplane.PlanActivationRecord, bool) {
	for _, record := range state.Plans {
		if record.Fact.Plan.StrategyID == strategyID {
			return record, true
		}
	}
	return controlplane.PlanActivationRecord{}, false
}

func freezeG4Slot(
	t *testing.T,
	strategyID int64,
	kind string,
	metric string,
	table string,
	dimensions []string,
	config any,
) execution.FrozenSlotContractFact {
	t.Helper()
	catalog := buildG4Catalog(t, controlplane.SourceStrategy{
		SourceID: strconv.FormatInt(strategyID, 10), Document: g4LegacyStrategyDocument(t, strategyID, kind, metric, table, dimensions, config),
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	})
	fact, err := freezeG4Catalog(t, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func g4SourceStrategy(t *testing.T, strategyID int64, kind string, config any) controlplane.SourceStrategy {
	t.Helper()
	metric := "usage"
	table := "system.cpu"
	dimensions := []string{"host"}
	if kind == strategy.DetectorKindOsRestart {
		metric = "uptime"
		table = "system.env"
		dimensions = []string{"bk_target_cloud_id", "bk_target_ip"}
	}
	return controlplane.SourceStrategy{
		SourceID: strconv.FormatInt(strategyID, 10), Document: g4LegacyStrategyDocument(t, strategyID, kind, metric, table, dimensions, config),
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}
}

func buildG4Catalog(t *testing.T, sources ...controlplane.SourceStrategy) controlplane.Catalog {
	t.Helper()
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: sources, Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != len(sources) {
		t.Fatalf("catalog = %+v", catalog)
	}
	return catalog
}

func freezeG4Catalog(t *testing.T, catalog controlplane.Catalog) (execution.FrozenSlotContractFact, error) {
	t.Helper()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g4-freeze", time.Hour)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	activation := activationState(t, 1, snapshot, schedule, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(
		context.Background(), controlplane.ActivationExpectation{}, activation,
		[]execution.InitialScheduleActivationFact{{Segment: schedule.Segment}},
	); err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
	}
	return runtime.FreezeSlotContract(context.Background(), request)
}

func withSecondG4Level(t *testing.T, document json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	algorithm := item["algorithms"].([]any)[0].(map[string]any)
	secondAlgorithm := make(map[string]any, len(algorithm))
	for key, field := range algorithm {
		secondAlgorithm[key] = field
	}
	secondAlgorithm["level"] = float64(2)
	item["algorithms"] = append(item["algorithms"].([]any), secondAlgorithm)
	value["detects"] = append(value["detects"].([]any), map[string]any{
		"level": float64(2), "priority": float64(2), "connector": "and",
		"trigger_config": map[string]any{"count": float64(1), "check_window": float64(1)},
	})
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mixedLegacyAndG4Levels(t *testing.T, document json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	g4 := item["algorithms"].([]any)[0].(map[string]any)
	g4["level"] = float64(2)
	item["algorithms"] = []any{
		map[string]any{
			"level": float64(1), "type": strategy.DetectorKindThreshold,
			"config": []any{map[string]any{"method": "gt", "threshold": float64(80)}},
		},
		g4,
	}
	value["detects"] = append(value["detects"].([]any), map[string]any{
		"level": float64(2), "priority": float64(2), "connector": "and",
		"trigger_config": map[string]any{"count": float64(1), "check_window": float64(1)},
	})
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
