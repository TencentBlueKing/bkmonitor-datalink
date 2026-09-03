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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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
	if err == nil || !strings.Contains(err.Error(), "no QueryPlanFacts") {
		t.Fatalf("FreezeSlotContract() error = %v, want dangling query rejection", err)
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
