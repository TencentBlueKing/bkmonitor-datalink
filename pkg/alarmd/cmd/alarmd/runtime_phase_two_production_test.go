// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestProductionFrozenExecutionResolvesExactPersistedContract(t *testing.T) {
	queryFacts := productionQueryFacts(t, "a")
	queryRef := execution.LogicalQueryRef(queryFacts.QueryRevision)
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	planRevision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	schedulePlan := execution.FrozenPlanSchedule{Identity: plan, ScheduleRevision: planRevision, Spec: spec}
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{schedulePlan})
	if err != nil {
		t.Fatal(err)
	}
	schedule := execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
		QueryGroup:  "query-group-1", QueryRevision: queryFacts.QueryRevision, ScheduleRevision: scheduleRevision, Start: 60,
	}, Plans: []execution.FrozenPlanSchedule{schedulePlan}}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: queryFacts.QueryRevision, ScheduleRevision: scheduleRevision,
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	catalog := &fakeFrozenCatalog{schedule: schedule, fact: execution.FrozenSlotContractFact{
		Contract: contractRef, DuePlans: []execution.DuePlan{{
			Identity: plan, ScheduleRevision: planRevision, ScheduleSpec: spec,
			CompletionDeadlineUnixMilli: 180_000,
		}},
		Requirements: []execution.DataRequirement{{LogicalQueryRef: queryRef, Consumers: []execution.DataRequirementConsumer{{
			Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 180_000,
			DownstreamExecutionReserveMilliSec: 5_000,
		}}}},
	}}
	repository := &fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1", QueryPlan: queryFacts}},
	}}
	resolver, err := newProductionFrozenExecution(catalog, repository, func() time.Time {
		return time.UnixMilli(174_999)
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := resolver.ResolveFrozenPlan(context.Background(), contractRef)
	if err != nil {
		t.Fatalf("ResolveFrozenPlan() error = %v", err)
	}
	wantRequest := execution.FreezeSlotContractRequest{
		QueryGroup: "query-group-1", ScheduleRevision: scheduleRevision, ScheduleSegmentStart: 60,
		EvaluationTime: 120, DuePlans: []execution.FrozenPlanScheduleRef{{Identity: plan, ScheduleRevision: planRevision}},
	}
	if !reflect.DeepEqual(catalog.request, wantRequest) || len(frozen.DuePlans) != 1 ||
		frozen.QueryFacts[queryRef].QueryRevision != queryFacts.QueryRevision {
		t.Fatalf("resolved request/facts = %+v / %+v", catalog.request, frozen)
	}
	request := productionRequest(catalog.fact, execution.OperationNormal)
	finalization, err := resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationQueryRequired || finalization.Contract != contractRef {
		t.Fatalf("ResolveFinalization() = %+v, %v", finalization, err)
	}
}

func TestProductionFrozenExecutionResolvesEveryFrozenRequirementQueryFact(t *testing.T) {
	primary := productionQueryFacts(t, "a")
	auxiliary, err := primary.WithMetricMerge("a + 1")
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: primary.QueryRevision,
		ScheduleRevision: "schedule-1", ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	primaryRef := execution.LogicalQueryRef(primary.QueryRevision)
	auxiliaryRef := execution.LogicalQueryRef(auxiliary.QueryRevision)
	fact := execution.FrozenSlotContractFact{
		Contract: contractRef,
		DuePlans: []execution.DuePlan{{Identity: plan}},
		Requirements: []execution.DataRequirement{
			{RequirementID: "primary", LogicalQueryRef: primaryRef},
			{RequirementID: "history", LogicalQueryRef: auxiliaryRef},
		},
	}
	resolver, err := newProductionFrozenExecution(
		&fakeFrozenCatalog{schedule: productionFrozenSchedule(contractRef, plan), fact: fact},
		&fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
			Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
			QueryGroups: []controlplane.QueryGroup{{
				Identity: "query-group-1", QueryPlan: primary,
				Plans: []controlplane.FrozenPlan{{
					Identity: plan,
					QueryPlans: map[execution.LogicalQueryRef]execution.QueryPlanFacts{
						primaryRef: primary, auxiliaryRef: auxiliary,
					},
				}},
			}},
		}},
		func() time.Time { return time.UnixMilli(100_000) },
	)
	if err != nil {
		t.Fatal(err)
	}

	frozen, err := resolver.ResolveFrozenPlan(context.Background(), contractRef)
	if err != nil {
		t.Fatalf("ResolveFrozenPlan() error=%v", err)
	}
	if len(frozen.QueryFacts) != 2 || frozen.QueryFacts[primaryRef].QueryRevision != primary.QueryRevision ||
		frozen.QueryFacts[auxiliaryRef].QueryRevision != auxiliary.QueryRevision {
		t.Fatalf("resolved query facts=%+v", frozen.QueryFacts)
	}

	delete(resolver.repository.(*fakeProductionCatalogRepository).snapshot.QueryGroups[0].Plans[0].QueryPlans, auxiliaryRef)
	if _, err := resolver.ResolveFrozenPlan(context.Background(), contractRef); !errors.Is(err, access.ErrFrozenQueryPlanUnavailable) {
		t.Fatalf("ResolveFrozenPlan(missing auxiliary facts) error=%v", err)
	}
}

func productionFrozenSchedule(
	contractRef execution.FrozenExecutionContractRef,
	plan execution.PlanIdentity,
) execution.FrozenQueryGroupSchedule {
	return execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: contractRef.SnapshotRevision, PublicationEpoch: 1},
			QueryGroup:  contractRef.Slot.QueryGroup, QueryRevision: contractRef.QueryRevision,
			ScheduleRevision: contractRef.ScheduleRevision, Start: contractRef.ScheduleSegmentStart,
		},
		Plans: []execution.FrozenPlanSchedule{{Identity: plan, ScheduleRevision: "plan-schedule-1",
			Spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}}},
	}
}

func productionQueryFacts(t *testing.T, metricMerge string) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.env", FieldName: "uptime",
			ReferenceName: "a", Driver: "influxdb", TimeField: "time"}},
		MetricMerge: metricMerge, StepMillis: 60_000, AlignmentMillis: 60_000, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
				IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestG4CatalogFrozenRequirementsReachProductionAccess(t *testing.T) {
	tests := []struct {
		name         string
		kind         string
		metric       string
		table        string
		dimensions   []string
		config       any
		wantDatasets []execution.DatasetName
		wantQueries  int
	}{
		{name: "SimpleRingRatio", kind: strategy.DetectorKindSimpleRingRatio, metric: "usage", table: "system.cpu",
			dimensions: []string{"host"}, config: map[string]any{"floor": 50, "ceil": nil},
			wantDatasets: []execution.DatasetName{"primary", "previous"}, wantQueries: 2},
		{name: "OsRestart", kind: strategy.DetectorKindOsRestart, metric: "uptime", table: "system.env",
			dimensions: []string{"bk_target_cloud_id", "bk_target_ip"}, config: map[string]any{},
			wantDatasets: []execution.DatasetName{"primary", "uptime_history"}, wantQueries: 2},
		{name: "Threshold", kind: strategy.DetectorKindThreshold, metric: "usage", table: "system.cpu",
			dimensions: []string{"host"}, config: [][]map[string]any{{{"method": "gte", "threshold": 80}}},
			wantDatasets: []execution.DatasetName{"primary"}, wantQueries: 1},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := productionG4Catalog(t, int64(500+index), test.kind, test.metric, test.table, test.dimensions, test.config)
			group := catalog.QueryGroups[0]
			fact, schedule := productionFrozenFactFromCatalog(t, catalog)
			resolver, err := newProductionFrozenExecution(
				&fakeFrozenCatalog{schedule: schedule, fact: fact},
				&fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
					Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: fact.Contract.SnapshotRevision, PublicationEpoch: 1},
					QueryGroups: catalog.QueryGroups,
				}},
				func() time.Time { return time.UnixMilli(int64(fact.Contract.Slot.EvaluationTime) * 1000) },
			)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := resolver.ResolveFrozenPlan(context.Background(), fact.Contract)
			if err != nil {
				t.Fatalf("ResolveFrozenPlan() error=%v", err)
			}
			prepared, err := access.Prepare(fact.Contract, frozen, time.Second)
			if err != nil {
				t.Fatalf("Prepare() error=%v", err)
			}
			preparedAgain, err := access.Prepare(fact.Contract, frozen, time.Second)
			if err != nil || !reflect.DeepEqual(prepared, preparedAgain) {
				t.Fatalf("Prepare() is not stable: first=%+v second=%+v error=%v", prepared, preparedAgain, err)
			}
			datasets := make([]execution.DatasetName, len(frozen.Requirements))
			for requirementIndex, requirement := range frozen.Requirements {
				datasets[requirementIndex] = requirement.DatasetName
				if strings.HasPrefix(string(datasets[requirementIndex]), "primary:") {
					datasets[requirementIndex] = "primary"
				}
				facts, exists := frozen.QueryFacts[requirement.LogicalQueryRef]
				if !exists || execution.LogicalQueryRef(facts.QueryRevision) != requirement.LogicalQueryRef {
					t.Fatalf("requirement has no exact frozen query facts: requirement=%+v facts=%+v", requirement, frozen.QueryFacts)
				}
			}
			if !reflect.DeepEqual(datasets, test.wantDatasets) || len(prepared.Queries) != test.wantQueries ||
				len(prepared.Header.RequiredPhysicalQueries) != test.wantQueries ||
				fact.Contract.QueryRevision != group.QueryPlan.QueryRevision {
				t.Fatalf("datasets/queries/refs=%v/%d/%+v, want %v/%d", datasets, len(prepared.Queries),
					prepared.Header.RequiredPhysicalQueries, test.wantDatasets, test.wantQueries)
			}
		})
	}
}

func productionG4Catalog(
	t *testing.T,
	id int64,
	kind string,
	metric string,
	table string,
	dimensions []string,
	algorithmConfig any,
) controlplane.Catalog {
	t.Helper()
	accessBKData := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{
		AccessBKData: &accessBKData, BKDataCMDBLevelTables: []string{},
		SystemDiskFilter:    controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660"}},
		SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	queryConfig := map[string]any{
		"data_source_label": "bk_monitor", "data_type_label": "time_series", "metric_field": metric,
		"alias": "a", "agg_dimension": dimensions, "agg_method": "MAX", "agg_interval": 60,
		"result_table_id": table,
	}
	item := map[string]any{
		"id": 1, "query_md5": "query-md5", "expression": "a", "unit": "",
		"query_configs": []any{queryConfig},
		"algorithms":    []any{map[string]any{"level": 1, "type": kind, "config": algorithmConfig}},
	}
	if kind == strategy.DetectorKindOsRestart {
		queryConfig["metric_id"] = "bk_monitor.os_restart"
		item["functions"] = []any{map[string]any{"id": "abs", "params": []any{}}}
	}
	document, err := json.Marshal(map[string]any{
		"id": id, "bk_biz_id": 2, "update_time": 1, "items": []any{item},
		"detects": []any{map[string]any{
			"level": 1, "priority": 1, "connector": "and",
			"trigger_config": map[string]any{"count": 1, "check_window": 1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{
			SourceID: strconv.FormatInt(id, 10), Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		}},
		Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
		t.Fatalf("catalog=%+v", catalog)
	}
	return catalog
}

func productionFrozenFactFromCatalog(
	t *testing.T,
	catalog controlplane.Catalog,
) (execution.FrozenSlotContractFact, execution.FrozenQueryGroupSchedule) {
	t.Helper()
	group := catalog.QueryGroups[0]
	plan := group.Plans[0]
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 8, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 8, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "g4-production-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	compiledResult, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract,
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "state-v1", CodecSemanticsVersion: "codec-v1",
			IdentitySchemaDigest: strings.Repeat("c", 64), SourceTimeSemanticsVersion: "seconds-v1",
			HistoryCellSemanticsVersion: "history-v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := compiledResult.Plan()
	if !ok {
		t.Fatalf("compiled plan terminal=%+v", compiledResult.PlanTerminal())
	}
	evaluationTime := execution.EvaluationTime(1_700_124_000)
	due := execution.DuePlan{
		Identity: plan.Identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: plan.ScheduleRevision, ScheduleSpec: plan.ScheduleSpec,
		CompletionDeadlineUnixMilli: (int64(evaluationTime) + plan.ScheduleSpec.EvaluationIntervalSeconds) * 1000,
	}
	requirements := make([]execution.DataRequirement, 0, len(plan.RequirementTemplates))
	for _, template := range plan.RequirementTemplates {
		requirements = append(requirements, template.Bind(execution.DataRequirementConsumer{
			Consumer:                  execution.ConsumerRef{Plan: plan.Identity, LevelID: template.ConsumerLevelID, HasLevel: true},
			ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000,
		}))
	}
	if len(requirements) == 0 {
		window := int64(compiled.EvaluationSemantics().QueryWindow)
		identity, err := contract.DeriveCanonicalDigestV2("alarmd-primary-requirement-v1", struct {
			QueryRevision execution.QueryRevision `json:"query_revision"`
			WindowSeconds int64                   `json:"window_seconds"`
		}{group.QueryPlan.QueryRevision, window})
		if err != nil {
			t.Fatal(err)
		}
		columns := append([]string{"value"}, group.QueryPlan.Normalization.DatasetContract.IdentityFields...)
		sort.Strings(columns)
		requirements = append(requirements, execution.DataRequirement{
			RequirementID: execution.RequirementID(identity), DatasetName: execution.DatasetName("primary:" + identity),
			Role: execution.InputRolePrimary, LogicalQueryRef: execution.LogicalQueryRef(group.QueryPlan.QueryRevision),
			RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -window, EndOffsetSeconds: 0, HalfOpen: true},
			StepMillis:     group.QueryPlan.StepMillis, AlignmentMillis: group.QueryPlan.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager,
			RequiredColumns: columns,
			Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: plan.Identity}, ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli,
				DownstreamExecutionReserveMilliSec: 5_000,
			}},
		})
	}
	dueDigest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: group.Identity, EvaluationTime: evaluationTime},
		SnapshotRevision: "snapshot-g4", QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, ScheduleSegmentStart: evaluationTime - 60,
		DuePlanSetDigest: dueDigest,
	}
	schedule := execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: contractRef.SnapshotRevision, PublicationEpoch: 1},
			QueryGroup:  group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
			ScheduleRevision: group.ScheduleRevision, Start: contractRef.ScheduleSegmentStart,
		},
		Plans: []execution.FrozenPlanSchedule{{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}},
	}
	fact := execution.FrozenSlotContractFact{Contract: contractRef, DuePlans: []execution.DuePlan{due}, Requirements: requirements}
	if err := fact.Validate(execution.FreezeSlotContractRequest{
		QueryGroup: group.Identity, ScheduleRevision: group.ScheduleRevision, ScheduleSegmentStart: contractRef.ScheduleSegmentStart,
		EvaluationTime: evaluationTime, DuePlans: schedule.DuePlanRefs(evaluationTime),
	}); err != nil {
		t.Fatalf("frozen fact invalid: %v", err)
	}
	return fact, schedule
}

func TestProductionFrozenExecutionSkipsExpiredNormalSlotWithoutRecoveryPermit(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	planRevision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	schedulePlan := execution.FrozenPlanSchedule{Identity: plan, ScheduleRevision: planRevision, Spec: spec}
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{schedulePlan})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision,
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	catalog := &fakeFrozenCatalog{
		schedule: execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
			QueryGroup:  "query-group-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision, Start: 60,
		}, Plans: []execution.FrozenPlanSchedule{schedulePlan}},
		fact: execution.FrozenSlotContractFact{
			Contract: contractRef,
			DuePlans: []execution.DuePlan{{
				Identity: plan, ScheduleRevision: planRevision, ScheduleSpec: spec,
				CompletionDeadlineUnixMilli: 180_000,
			}},
			Requirements: []execution.DataRequirement{
				{Consumers: []execution.DataRequirementConsumer{{
					Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 180_000,
					DownstreamExecutionReserveMilliSec: 2_000,
				}}},
				{Consumers: []execution.DataRequirementConsumer{{
					Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 180_000,
					DownstreamExecutionReserveMilliSec: 5_000,
				}}},
			},
		},
	}
	repository := &fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
		QueryGroups: []controlplane.QueryGroup{{
			Identity: "query-group-1", QueryPlan: execution.QueryPlanFacts{QueryRevision: "query-1"},
		}},
	}}
	now := time.UnixMilli(175_000)
	resolver, err := newProductionFrozenExecution(catalog, repository, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	request := productionRequest(catalog.fact, execution.OperationNormal)
	finalization, err := resolver.ResolveFinalization(context.Background(), request)
	if err != nil {
		t.Fatalf("ResolveFinalization() error = %v", err)
	}
	if finalization.Mode != execution.FinalizationQueryRequired {
		t.Fatalf("ResolveFinalization() = %+v", finalization)
	}
	if err := finalization.Validate(request); err != nil {
		t.Fatalf("ResolveFinalization() produced invalid finalization: %v", err)
	}
	now = time.UnixMilli(request.RecoveryUntilUnixMilli)
	finalization, err = resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotUnavailable ||
		finalization.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("ResolveFinalization(after recovery deadline) = %+v, %v", finalization, err)
	}

	// recovery_until closes Query for every operation. The operation label cannot
	// reopen an expired frozen Slot.
	for _, operation := range []execution.Operation{
		execution.OperationRetry,
		execution.OperationReplay,
		execution.OperationProbe,
	} {
		request.Operation = operation
		finalization, err = resolver.ResolveFinalization(context.Background(), request)
		if err != nil || finalization.Mode != execution.FinalizationSnapshotUnavailable ||
			finalization.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
			t.Fatalf("ResolveFinalization(%s) = %+v, %v", operation, finalization, err)
		}
	}

	request.Operation = execution.OperationNormal
	for _, test := range []struct {
		name         string
		requirements []execution.DataRequirement
	}{
		{name: "no consumer"},
		{name: "reserve consumes deadline", requirements: []execution.DataRequirement{{
			Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 5_000,
				DownstreamExecutionReserveMilliSec: 5_000,
			}},
		}}},
		{name: "non-positive reserve", requirements: []execution.DataRequirement{{
			Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 5_000,
			}},
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog.fact.Requirements = test.requirements
			_, err := resolver.ResolveFinalization(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "frozen contract query deadline") {
				t.Fatalf("ResolveFinalization(invalid frozen contract) error = %v", err)
			}
		})
	}
}

func TestProductionFrozenExecutionUsesRequestFactsWhenSnapshotDisappearsAfterFreeze(t *testing.T) {
	catalog, resolver, request := productionFinalizationFixture(t)
	catalog.readErr = controlplane.ErrSnapshotUnavailable

	finalization, err := resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotRetry ||
		finalization.ReasonCode != execution.ReasonCode(contract.ReasonProviderUnavailable) {
		t.Fatalf("ResolveFinalization(snapshot unavailable) = (%+v, %v)", finalization, err)
	}
	resolver.now = func() time.Time { return time.UnixMilli(request.RecoveryUntilUnixMilli) }
	finalization, err = resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotUnavailable ||
		!finalization.Targets.Equal(request.DuePlanTargets) {
		t.Fatalf("ResolveFinalization(expired snapshot unavailable) = (%+v, %v)", finalization, err)
	}
	finalization.Targets.Plans[0].StrategyID = "mutated"
	if request.DuePlanTargets.Plans[0].StrategyID == "mutated" {
		t.Fatal("query-free finalization aliases request frozen targets")
	}
}

func TestProductionFrozenExecutionSeparatesTransportRetryFromPersistedCorruption(t *testing.T) {
	catalog, resolver, request := productionFinalizationFixture(t)
	catalog.readErr = errors.New("redis timeout")
	finalization, err := resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotRetry ||
		finalization.ReasonCode != execution.ReasonCode(contract.ReasonProviderUnavailable) {
		t.Fatalf("ResolveFinalization(transport) = (%+v, %v)", finalization, err)
	}
	resolver.now = func() time.Time { return time.UnixMilli(request.RecoveryUntilUnixMilli) }
	finalization, err = resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotUnavailable ||
		finalization.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) ||
		!finalization.Targets.Equal(request.DuePlanTargets) {
		t.Fatalf("ResolveFinalization(expired transport) = (%+v, %v)", finalization, err)
	}
	catalog.readErr = &controlplane.PersistedSnapshotCorruptError{Err: errors.New("bad digest")}
	finalization, err = resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationSnapshotUnavailable ||
		!finalization.Targets.Equal(request.DuePlanTargets) {
		t.Fatalf("ResolveFinalization(corrupt) = (%+v, %v)", finalization, err)
	}
}

func TestPhaseTwoSnapshotMinimumRetentionUsesExistingRecoveryAndOwnershipParameters(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	offset := 55 * time.Second
	want := cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration() + 2*cfg.PhaseTwo.Control.RefreshInterval.Duration() +
		offset + cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration() + phaseTwoPostRecoveryTerminalDelay(cfg)
	if got := phaseTwoSnapshotMinimumRetention(cfg, offset); got != want {
		t.Fatalf("phaseTwoSnapshotMinimumRetention()=%s, want %s", got, want)
	}
}

func TestPhaseTwoCatalogRetentionValidatorIncludesCandidateScheduleOffset(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	catalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{{Plans: []controlplane.FrozenPlan{{
		ScheduleSpec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"},
	}}}}}
	validator := phaseTwoCatalogRetentionValidator(cfg)
	if err := validator(catalog); err != nil {
		t.Fatalf("validator(default) error=%v", err)
	}
	cfg.PhaseTwo.Control.CatalogTTL = config.Duration(phaseTwoSnapshotMinimumRetention(cfg, 55*time.Second) - time.Millisecond)
	if err := phaseTwoCatalogRetentionValidator(cfg)(catalog); !errors.Is(err, scheduler.ErrSnapshotRetentionInsufficient) {
		t.Fatalf("validator(insufficient) error=%v", err)
	}
}

func TestProductionFrozenExecutionRejectsChangedRefrozenExecutionFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*execution.FrozenSlotContractFact)
	}{
		{name: "exact target set", mutate: func(fact *execution.FrozenSlotContractFact) {
			fact.DuePlans[0].Identity.StrategyID = "changed"
			fact.Requirements[0].Consumers[0].Consumer.Plan = fact.DuePlans[0].Identity
		}},
		{name: "earliest query deadline", mutate: func(fact *execution.FrozenSlotContractFact) {
			fact.Requirements[0].Consumers[0].DownstreamExecutionReserveMilliSec++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, resolver, request := productionFinalizationFixture(t)
			test.mutate(&catalog.fact)
			if _, err := resolver.ResolveFinalization(context.Background(), request); err == nil ||
				!strings.Contains(err.Error(), "re-frozen execution facts differ") {
				t.Fatalf("ResolveFinalization(changed facts) error = %v", err)
			}
		})
	}
}

func productionFinalizationFixture(t *testing.T) (*fakeFrozenCatalog, *productionFrozenExecution, execution.SlotExecutionRequest) {
	t.Helper()
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	planRevision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	schedulePlan := execution.FrozenPlanSchedule{Identity: plan, ScheduleRevision: planRevision, Spec: spec}
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{schedulePlan})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision,
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	catalog := &fakeFrozenCatalog{
		schedule: execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
			QueryGroup:  "query-group-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision, Start: 60,
		}, Plans: []execution.FrozenPlanSchedule{schedulePlan}},
		fact: execution.FrozenSlotContractFact{
			Contract: contractRef,
			DuePlans: []execution.DuePlan{{Identity: plan, ScheduleRevision: planRevision, ScheduleSpec: spec, CompletionDeadlineUnixMilli: 180_000}},
			Requirements: []execution.DataRequirement{{Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 180_000,
				DownstreamExecutionReserveMilliSec: 5_000,
			}}}},
		},
	}
	resolver, err := newProductionFrozenExecution(catalog, &fakeProductionCatalogRepository{}, func() time.Time {
		return time.UnixMilli(174_999)
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog, resolver, productionRequest(catalog.fact, execution.OperationNormal)
}

type fakeFrozenCatalog struct {
	schedule  execution.FrozenQueryGroupSchedule
	fact      execution.FrozenSlotContractFact
	request   execution.FreezeSlotContractRequest
	readErr   error
	freezeErr error
}

func (catalog *fakeFrozenCatalog) ReadFrozenSchedule(
	context.Context,
	execution.QueryGroupIdentity,
	execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	if catalog.readErr != nil {
		return execution.FrozenQueryGroupSchedule{}, catalog.readErr
	}
	return catalog.schedule, nil
}

func (catalog *fakeFrozenCatalog) FreezeSlotContract(
	_ context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	catalog.request = request
	if catalog.freezeErr != nil {
		return execution.FrozenSlotContractFact{}, catalog.freezeErr
	}
	return catalog.fact, nil
}

var _ access.FrozenPlanSource = (*productionFrozenExecution)(nil)
var _ execution.QueryFreeFinalizationSource = (*productionFrozenExecution)(nil)

func TestProductionPhaseTwoControlConfirmsColdStartBeforeInitialActivation(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-1"},
		{Status: controlplane.SourceRefreshPublished, Observation: "observation-1", Publication: publication},
	}}
	repository := &fakeProductionCatalogRepository{activationErr: controlplane.ErrActivationUnavailable,
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}}}
	activator := &fakeInitialScheduleActivator{state: controlplane.ActivationState{
		RecordRevision: 1, Current: publication,
	}}
	waits := 0
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, RefreshInterval: time.Second,
		Wait: func(context.Context, time.Duration) error { waits++; return nil },
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoControl() error = %v", err)
	}
	result, err := control.InitialRefresh(context.Background())
	if err != nil || result.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("InitialRefresh() = %#v, %v", result, err)
	}
	if reconciler.calls != 2 || waits != 1 || activator.calls != 1 || activator.publication != publication {
		t.Fatalf("cold-start calls refresh/wait/activate=%d/%d/%d publication=%+v",
			reconciler.calls, waits, activator.calls, activator.publication)
	}
}

func TestProductionPhaseTwoControlLoadsAllActiveQueryGroups(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1}
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 1, Current: publication},
		snapshot: controlplane.PublishedSnapshot{Publication: publication, QueryGroups: []controlplane.QueryGroup{
			{Identity: "query-group-2"}, {Identity: "query-group-1"},
		}},
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
		Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, RefreshInterval: time.Second,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.LoadActive(context.Background())
	if err != nil || result.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}) {
		t.Fatalf("LoadActive() = %#v, %v", result, err)
	}
}

func TestProductionPhaseTwoControlDrainsRetiredQueryGroupBeforeRemovingIt(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-new", PublicationEpoch: 2}
	boundary := execution.EvaluationTime(90)
	retired := execution.QueryGroupIdentity("query-group-old")
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 2, Current: publication,
			Draining: []controlplane.DrainingQueryGroup{{QueryGroup: retired, RetiredBoundary: boundary}}},
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-new"}}},
	}
	schedules := &fakeScheduleProjection{
		initial: map[execution.QueryGroupIdentity]execution.FrozenQueryGroupSchedule{
			retired: schedulerScheduleForProductionControl(t, retired, 60, 60, &boundary),
		},
		retired: map[execution.QueryGroupIdentity]execution.EvaluationTime{retired: boundary},
	}
	progress := &fakeProductionProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		retired: {Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: retired}, NextSlot: 60,
		}},
	}}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
		Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: schedules, Progress: progress,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-new", retired}) {
		t.Fatalf("draining active projection=(%#v,%v)", result, err)
	}
	if len(observations) != 1 || observations[0].DrainingQG == nil || observations[0].DrainingQG.Total != 1 ||
		observations[0].DrainingQG.Undrained != 1 || observations[0].DrainingQG.Isolated != 0 ||
		len(observations[0].DrainingQG.Samples) != 1 ||
		observations[0].DrainingQG.Samples[0].QueryGroupKey != string(retired) ||
		observations[0].DrainingQG.Samples[0].RetiredBoundary != int64(boundary) ||
		observations[0].DrainingQG.Samples[0].NextSlot != 60 ||
		observations[0].DrainingQG.Samples[0].ProgressStatus != string(execution.ProgressFound) {
		t.Fatalf("undrained observations=%#v", observations)
	}
	progress.byGroup[retired] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: retired}, NextSlot: boundary, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	result, err = control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-new"}) {
		t.Fatalf("drained active projection=(%#v,%v)", result, err)
	}
	if len(observations) != 2 || observations[1].DrainingQG == nil || observations[1].DrainingQG.Total != 1 ||
		observations[1].DrainingQG.Undrained != 0 || len(observations[1].DrainingQG.Samples) != 0 {
		t.Fatalf("drained observations=%#v", observations)
	}
}

func TestProductionPhaseTwoControlRemovesRetiredZeroSlotQueryGroupWithoutProgress(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-new", PublicationEpoch: 2}
	boundary := execution.EvaluationTime(90)
	retired := execution.QueryGroupIdentity("query-group-old")
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 2, Current: publication,
			Draining: []controlplane.DrainingQueryGroup{{QueryGroup: retired, RetiredBoundary: boundary}}},
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-new"}}},
	}
	schedules := &fakeScheduleProjection{
		initial: map[execution.QueryGroupIdentity]execution.FrozenQueryGroupSchedule{
			retired: schedulerScheduleForProductionControl(t, retired, 60, 83, &boundary),
		},
		retired: map[execution.QueryGroupIdentity]execution.EvaluationTime{retired: boundary},
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
		Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: schedules,
		Progress: &fakeProductionProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
			retired: {Status: execution.ProgressMissing},
		}},
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-new"}) {
		t.Fatalf("zero-Slot retired active projection=(%#v,%v)", result, err)
	}
	if repository.renewCalls != 0 {
		t.Fatalf("Follower LoadActive renew calls=%d, want 0", repository.renewCalls)
	}
}

func TestProductionPhaseTwoControlIsolatesInvalidDrainingQueryGroup(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 3}
	boundary := execution.EvaluationTime(90)
	bad := execution.QueryGroupIdentity("query-group-bad-draining")
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 3, Current: publication,
			Draining: []controlplane.DrainingQueryGroup{{QueryGroup: bad, RetiredBoundary: boundary}}},
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
	}
	schedules := &fakeScheduleProjection{
		initial: map[execution.QueryGroupIdentity]execution.FrozenQueryGroupSchedule{
			bad: schedulerScheduleForProductionControl(t, bad, 60, 60, &boundary),
		},
		retired: map[execution.QueryGroupIdentity]execution.EvaluationTime{bad: boundary},
	}
	progress := &fakeProductionProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		bad: {Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "wrong-query-group"}, NextSlot: 60,
		}},
	}}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
		Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: schedules, Progress: progress,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
		t.Fatalf("isolated active projection=(%#v,%v)", result, err)
	}
	if len(observations) != 2 || observations[0].Result != observability.ResultDegraded ||
		observations[0].Trace.QueryGroupKey != string(bad) || observations[0].Err == nil {
		t.Fatalf("draining isolation observation=%#v", observations)
	}
	if observations[1].DrainingQG == nil || observations[1].DrainingQG.Total != 1 ||
		observations[1].DrainingQG.Undrained != 0 || observations[1].DrainingQG.Isolated != 1 {
		t.Fatalf("draining aggregate observation=%#v", observations)
	}
}

func TestProductionPhaseTwoControlKeepsCurrentActivationWhileCandidateIsPending(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-next"},
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-next-changed"},
	}}
	renewErr := errors.New("renew pending current objects")
	repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{
		RecordRevision: 1, Current: publication,
	}, snapshot: controlplane.PublishedSnapshot{Publication: publication,
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}}, renewErrs: []error{renewErr, nil}}
	activator := &fakeInitialScheduleActivator{}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, RefreshInterval: time.Second,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		Wait: func(context.Context, time.Duration) error { return errors.New("unexpected wait") },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Refresh(context.Background())
	if err != nil || result.Status != phaseTwoControlDegradedLastGood ||
		result.ReasonCode != observability.ReasonCode(contract.ReasonRedisUnavailable) ||
		!errors.Is(result.Cause, renewErr) ||
		!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("Refresh() = %#v, %v", result, err)
	}
	if activator.calls != 0 {
		t.Fatalf("pending candidate changed current activation, calls = %d", activator.calls)
	}
	recovered, err := control.Refresh(context.Background())
	if err != nil || recovered.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(recovered.QueryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("recovered Refresh() = %#v, %v", recovered, err)
	}
	if repository.renewCalls != 2 {
		t.Fatalf("pending refresh renew calls=%d, want 2", repository.renewCalls)
	}
	renewal := observationsWithoutDrainingFacts(observations)
	if len(renewal) != 2 || renewal[0].Stage != observability.StageActiveQGSet ||
		renewal[0].Result != observability.ResultDegraded || renewal[1].Result != observability.ResultRecovered {
		t.Fatalf("pending renewal observations=%#v", renewal)
	}
}

func TestProductionPhaseTwoControlKeepsHealthyQueryGroupsAcrossPublicationConflictAndRecovery(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPublicationConflict, Observation: "observation-stale", Publication: publication},
		{Status: controlplane.SourceRefreshUnchanged, Observation: "observation-current", Publication: publication},
	}}
	repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{
		RecordRevision: 2, Current: publication,
	}, snapshot: controlplane.PublishedSnapshot{Publication: publication,
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}}}
	activator := &fakeInitialScheduleActivator{state: repository.activation}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		result, err := control.Refresh(context.Background())
		if err != nil || result.Status != phaseTwoControlHealthy ||
			!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
			t.Fatalf("Refresh(%d)=(%#v,%v)", index, result, err)
		}
	}
	if activator.calls != 2 {
		t.Fatalf("activation calls=%d, want 2", activator.calls)
	}
	if repository.renewCalls != 2 {
		t.Fatalf("conflict/unchanged refresh renew calls=%d, want 2", repository.renewCalls)
	}
	conflict := observationsWithoutDrainingFacts(observations)
	if len(conflict) != 1 || conflict[0].Stage != observability.StageSnapshotRefreshed ||
		conflict[0].Result != observability.ResultDegraded ||
		conflict[0].ReasonCode != observability.ReasonContractRetryable {
		t.Fatalf("publication conflict observations=%#v", conflict)
	}
}

func TestProductionPhaseTwoControlKeepsLastGoodAcrossFailedRefreshAndRecovery(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	reconciler := &fakeSourceReconciler{
		results: []controlplane.SourceRefreshResult{
			{},
			{Status: controlplane.SourceRefreshUnchanged, Observation: "observation-current", Publication: publication},
		},
		errs: []error{controlplane.ErrSnapshotUnavailable, nil},
	}
	repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{
		RecordRevision: 2, Current: publication,
	}, snapshot: controlplane.PublishedSnapshot{Publication: publication,
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}}}
	activator := &fakeInitialScheduleActivator{state: repository.activation}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress:        &fakeProductionProgressReader{},
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := control.Refresh(context.Background())
	if err != nil || degraded.Status != phaseTwoControlDegradedLastGood ||
		degraded.SourceKind != observability.SourceKindCompiledSnapshot ||
		degraded.ReasonCode != observability.ReasonContractRetryable || degraded.Cause == nil ||
		!reflect.DeepEqual(degraded.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
		t.Fatalf("degraded Refresh()=(%#v,%v)", degraded, err)
	}
	healthy, err := control.Refresh(context.Background())
	if err != nil || healthy.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(healthy.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
		t.Fatalf("healthy Refresh()=(%#v,%v)", healthy, err)
	}
	if activator.calls != 1 {
		t.Fatalf("activation calls=%d, want 1 after recovery", activator.calls)
	}
	if repository.renewCalls != 2 {
		t.Fatalf("failed/successful refresh renew calls=%d, want 2", repository.renewCalls)
	}
}

func TestProductionPhaseTwoControlObservesRenewFailureAsOneRecoverableEpisode(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	sourceErr := controlplane.ErrSnapshotUnavailable
	renewErr := errors.New("renew current control objects")
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{}, {}, {Status: controlplane.SourceRefreshUnchanged, Publication: publication},
	}, errs: []error{sourceErr, sourceErr, nil}}
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 2, Current: publication},
		snapshot:   controlplane.PublishedSnapshot{Publication: publication, QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
		renewErrs:  []error{renewErr, renewErr, nil},
	}
	activator := &fakeInitialScheduleActivator{state: repository.activation}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		result, refreshErr := control.Refresh(context.Background())
		if refreshErr != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
			t.Fatalf("Refresh(%d)=(%#v,%v)", index, result, refreshErr)
		}
		if index < 2 && (result.Status != phaseTwoControlDegradedLastGood || !errors.Is(result.Cause, sourceErr) || result.ReasonCode != observability.ReasonContractRetryable) {
			t.Fatalf("source primary result was replaced: %#v", result)
		}
	}
	var renewal []observability.Observation
	for _, observation := range observations {
		if observation.Stage == observability.StageActiveQGSet && observation.DrainingQG == nil {
			renewal = append(renewal, observation)
		}
	}
	if len(renewal) != 2 || renewal[0].Result != observability.ResultDegraded || renewal[0].Err != renewErr ||
		renewal[1].Result != observability.Result(observability.ResultRecovered) {
		t.Fatalf("renewal episode observations=%#v", renewal)
	}
}

func observationsWithoutDrainingFacts(observations []observability.Observation) []observability.Observation {
	filtered := make([]observability.Observation, 0, len(observations))
	for _, observation := range observations {
		if observation.DrainingQG == nil {
			filtered = append(filtered, observation)
		}
	}
	return filtered
}

func TestProductionPhaseTwoControlPreservesPrimaryRefreshClassificationWithoutLastGoodPayload(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	tests := []struct {
		name        string
		cause       error
		snapshotErr error
		wantGroups  int
		sourceKind  observability.SourceKind
		reason      observability.ReasonCode
	}{
		{name: "legacy source", cause: controlplane.ErrLegacySourceIncomplete,
			snapshotErr: controlplane.ErrSnapshotUnavailable,
			sourceKind:  observability.SourceKindLegacyStrategy, reason: observability.ReasonContractRetryable},
		{name: "snapshot", cause: controlplane.ErrSnapshotUnavailable,
			snapshotErr: controlplane.ErrSnapshotUnavailable,
			sourceKind:  observability.SourceKindCompiledSnapshot, reason: observability.ReasonContractRetryable},
		{name: "occurrence collision without active payload", cause: controlplane.ErrPublicationOccurrenceCollision,
			snapshotErr: controlplane.ErrSnapshotUnavailable,
			sourceKind:  observability.SourceKindLegacyStrategy, reason: observability.ReasonContractDeterministic},
		{name: "occurrence collision with active payload", cause: controlplane.ErrPublicationOccurrenceCollision,
			wantGroups: 1,
			sourceKind: observability.SourceKindLegacyStrategy, reason: observability.ReasonContractDeterministic},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
				Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{},
				Reconciler: &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{{}}, errs: []error{test.cause}},
				Activator:  &fakeInitialScheduleActivator{}, Repository: &fakeProductionCatalogRepository{
					activation: controlplane.ActivationState{RecordRevision: 2, Current: publication},
					snapshot: controlplane.PublishedSnapshot{Publication: publication,
						QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
					snapshotErr: test.snapshotErr,
				},
				Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
				RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := control.Refresh(context.Background())
			if err != nil || result.Status != phaseTwoControlDegradedLastGood || result.SourceKind != test.sourceKind ||
				result.ReasonCode != test.reason || !errors.Is(result.Cause, test.cause) || len(result.QueryGroups) != test.wantGroups {
				t.Fatalf("Refresh()=(%#v,%v), want degraded source=%s reason=%s cause=%v",
					result, err, test.sourceKind, test.reason, test.cause)
			}
		})
	}
}

func TestProductionPhaseTwoControlKeepsLastGoodAcrossFailedCutoverAndRecovery(t *testing.T) {
	current := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	candidate := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-candidate", PublicationEpoch: 3}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPublished, Observation: "observation-candidate", Publication: candidate},
		{Status: controlplane.SourceRefreshUnchanged, Observation: "observation-candidate", Publication: candidate},
	}}
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 2, Current: current},
		snapshots: map[controlplane.SnapshotPublicationRef]controlplane.PublishedSnapshot{
			current:   {Publication: current, QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
			candidate: {Publication: candidate, QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
		},
	}
	activator := &fakeInitialScheduleActivator{
		state: controlplane.ActivationState{RecordRevision: 3, Current: candidate},
		errs:  []error{controlplane.ErrScheduleConflict, nil},
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress:        &fakeProductionProgressReader{},
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := control.Refresh(context.Background())
	if err != nil || degraded.Status != phaseTwoControlDegradedLastGood ||
		degraded.SourceKind != observability.SourceKindCompiledSnapshot ||
		degraded.ReasonCode != observability.ReasonContractRetryable ||
		!errors.Is(degraded.Cause, controlplane.ErrScheduleConflict) ||
		!reflect.DeepEqual(degraded.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
		t.Fatalf("degraded Refresh()=(%#v,%v)", degraded, err)
	}
	healthy, err := control.Refresh(context.Background())
	if err != nil || healthy.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(healthy.QueryGroups, []execution.QueryGroupIdentity{"query-group-healthy"}) {
		t.Fatalf("healthy Refresh()=(%#v,%v)", healthy, err)
	}
	if activator.calls != 2 {
		t.Fatalf("activation calls=%d, want 2", activator.calls)
	}
}

type fakeSourceReconciler struct {
	results []controlplane.SourceRefreshResult
	errs    []error
	calls   int
}

func (reconciler *fakeSourceReconciler) Refresh(
	context.Context,
	controlplane.StrategySource,
	controlplane.PrimaryQueryCompiler,
) (controlplane.SourceRefreshResult, error) {
	if reconciler.calls >= len(reconciler.results) {
		return controlplane.SourceRefreshResult{}, errors.New("unexpected source refresh")
	}
	result := reconciler.results[reconciler.calls]
	var err error
	if reconciler.calls < len(reconciler.errs) {
		err = reconciler.errs[reconciler.calls]
	}
	reconciler.calls++
	return result, err
}

type fakeProductionCatalogRepository struct {
	activation    controlplane.ActivationState
	activationErr error
	snapshot      controlplane.PublishedSnapshot
	snapshotErr   error
	snapshots     map[controlplane.SnapshotPublicationRef]controlplane.PublishedSnapshot
	activeGroups  []execution.QueryGroupIdentity
	activeSetErr  error
	renewErr      error
	renewErrs     []error
	renewCalls    int
}

func (repository *fakeProductionCatalogRepository) RenewCurrentActivationObjects(context.Context) error {
	repository.renewCalls++
	if repository.renewCalls <= len(repository.renewErrs) {
		return repository.renewErrs[repository.renewCalls-1]
	}
	return repository.renewErr
}

func (repository *fakeProductionCatalogRepository) LoadActiveQueryGroupSet(
	context.Context,
	controlplane.ActiveQueryGroupSetRef,
) ([]execution.QueryGroupIdentity, error) {
	return append([]execution.QueryGroupIdentity{}, repository.activeGroups...), repository.activeSetErr
}

func (repository *fakeProductionCatalogRepository) LoadPublishedSnapshot(
	_ context.Context,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.PublishedSnapshot, error) {
	if repository.snapshotErr != nil {
		return controlplane.PublishedSnapshot{}, repository.snapshotErr
	}
	if snapshot, ok := repository.snapshots[publication]; ok {
		return snapshot, nil
	}
	if repository.snapshot.Publication != publication {
		return controlplane.PublishedSnapshot{}, errors.New("unexpected Snapshot publication")
	}
	return repository.snapshot, nil
}

func (repository *fakeProductionCatalogRepository) LoadActivation(
	context.Context,
) (controlplane.ActivationState, error) {
	return repository.activation, repository.activationErr
}

func (repository *fakeProductionCatalogRepository) LoadSnapshot(
	_ context.Context,
	revision execution.SnapshotRevision,
) (controlplane.PublishedSnapshot, error) {
	if repository.snapshot.Publication.SnapshotRevision != revision {
		return controlplane.PublishedSnapshot{}, errors.New("unexpected Snapshot revision")
	}
	return repository.snapshot, nil
}

type fakeInitialScheduleActivator struct {
	state       controlplane.ActivationState
	publication controlplane.SnapshotPublicationRef
	errs        []error
	calls       int
}

type fakeScheduleProjection struct {
	initial map[execution.QueryGroupIdentity]execution.FrozenQueryGroupSchedule
	retired map[execution.QueryGroupIdentity]execution.EvaluationTime
}

func (projection *fakeScheduleProjection) ReadInitialFrozenSchedule(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.FrozenQueryGroupSchedule, error) {
	schedule, ok := projection.initial[queryGroup]
	if !ok {
		return execution.FrozenQueryGroupSchedule{}, errors.New("missing initial Schedule")
	}
	return schedule, nil
}

func (projection *fakeScheduleProjection) ReadFrozenSchedule(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	schedule, ok := projection.initial[queryGroup]
	if !ok || !schedule.Segment.Contains(evaluationTime) {
		return execution.FrozenQueryGroupSchedule{}, errors.New("missing frozen Schedule")
	}
	return schedule, nil
}

func (projection *fakeScheduleProjection) ReadSuccessorFrozenSchedule(
	context.Context,
	execution.QueryGroupIdentity,
	execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	return execution.FrozenQueryGroupSchedule{}, errors.New("missing successor Schedule")
}

func (projection *fakeScheduleProjection) ReadScheduleRetirement(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.EvaluationTime, bool, error) {
	boundary, ok := projection.retired[queryGroup]
	return boundary, ok, nil
}

type fakeProductionProgressReader struct {
	byGroup map[execution.QueryGroupIdentity]execution.ProgressLoadResult
}

func (reader *fakeProductionProgressReader) LoadProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	result, ok := reader.byGroup[identity.QueryGroup]
	if !ok {
		return execution.ProgressLoadResult{}, errors.New("missing Progress fixture")
	}
	return result, nil
}

func schedulerScheduleForProductionControl(
	t *testing.T,
	queryGroup execution.QueryGroupIdentity,
	interval int64,
	start execution.EvaluationTime,
	end *execution.EvaluationTime,
) execution.FrozenQueryGroupSchedule {
	t.Helper()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC"}
	planRevision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.FrozenPlanSchedule{
		Identity:         execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"},
		ScheduleRevision: planRevision, Spec: spec,
	}
	queryRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{plan})
	if err != nil {
		t.Fatal(err)
	}
	schedule := execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-old", PublicationEpoch: 1},
			QueryGroup:  queryGroup, QueryRevision: "query-old", ScheduleRevision: queryRevision,
			Start: start, End: end,
		},
		Plans: []execution.FrozenPlanSchedule{plan},
	}
	if err := schedule.Validate(); err != nil {
		t.Fatal(err)
	}
	return schedule
}

func (activator *fakeInitialScheduleActivator) Ensure(
	_ context.Context,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.ActivationState, error) {
	call := activator.calls
	activator.calls++
	activator.publication = publication
	if call < len(activator.errs) && activator.errs[call] != nil {
		return controlplane.ActivationState{}, activator.errs[call]
	}
	return activator.state, nil
}

type fakeStrategySource struct{}

func (fakeStrategySource) ActiveStrategyIDs(context.Context) ([]string, error) {
	return []string{}, nil
}

func (fakeStrategySource) Strategies(context.Context, []string) ([]controlplane.SourceStrategy, error) {
	return []controlplane.SourceStrategy{}, nil
}

type fakePrimaryQueryCompiler struct{}

func (fakePrimaryQueryCompiler) CompilePrimaryQuery(
	context.Context,
	controlplane.PrimaryQuerySource,
) (execution.QueryPlanFacts, error) {
	return execution.QueryPlanFacts{}, nil
}

func TestPhaseTwoLegacyQueryRuntimeFactsPreserveExplicitConfiguration(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	accessBKData := true
	cfg.PhaseTwo.Control.LegacyQueryRuntime.AccessBKData = &accessBKData
	cfg.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables = []string{"system.cpu_cmdb_level"}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter.Values = []string{"iso9660", "tmpfs"}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemNetworkFilter.Values = []string{}

	facts := phaseTwoLegacyQueryRuntimeFacts(cfg.PhaseTwo.Control.LegacyQueryRuntime)
	if facts.AccessBKData == nil || !*facts.AccessBKData ||
		!reflect.DeepEqual(facts.BKDataCMDBLevelTables, []string{"system.cpu_cmdb_level"}) ||
		facts.SystemDiskFilter.FieldName != "device_type" ||
		!reflect.DeepEqual(facts.SystemDiskFilter.Values, []string{"iso9660", "tmpfs"}) ||
		facts.SystemNetworkFilter.FieldName != "device_name" || facts.SystemNetworkFilter.Values == nil || len(facts.SystemNetworkFilter.Values) != 0 {
		t.Fatalf("legacy query runtime facts = %+v, want exact explicit configuration", facts)
	}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables[0] = "mutated"
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter.Values[0] = "mutated"
	if facts.BKDataCMDBLevelTables[0] != "system.cpu_cmdb_level" || facts.SystemDiskFilter.Values[0] != "iso9660" {
		t.Fatalf("legacy query runtime facts retained mutable config slices: %+v", facts)
	}
}

func TestProductionPhaseTwoActivationChecksExactPersistedStateEpoch(t *testing.T) {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: "query-1", ScheduleRevision: "schedule-1",
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}
	source := &fakePlanActivationSource{result: execution.PlanActivationResult{
		Contract: contractRef,
		Facts: []execution.PlanActivationFact{{Plan: plan, Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{Identity: plan, StateGeneration: "state-1", StateApplyEpoch: 7,
				ScheduleRevision: "plan-schedule-1", RequiredFullSlots: 2}}},
	}}
	activation := productionPhaseTwoActivation{source: source}
	active, err := activation.IsPlanActive(context.Background(), contractRef, plan, 7)
	if err != nil || !active {
		t.Fatalf("IsPlanActive(exact epoch) = %v, %v", active, err)
	}
	active, err = activation.IsPlanActive(context.Background(), contractRef, plan, 8)
	if err != nil || active {
		t.Fatalf("IsPlanActive(stale epoch) = %v, %v", active, err)
	}
}

func TestProductionPhaseTwoOwnershipUsesAssignmentAndLeaseBeforeRunner(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})}
	compatibility := ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"}
	eligibility, err := scheduler.NewStaticWorkerEligibility(compatibility)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	var observations []observability.Observation
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second,
		SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoOwnership() error = %v", err)
	}
	if production.flights != flights {
		t.Fatal("production ownership copied the process-wide FlightCoordinator")
	}
	if err := production.RegisterWorker(context.Background(), ownership.WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady,
		DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "shadow",
		CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute)
	if err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if err := production.PublishAssignments(
		context.Background(), []execution.QueryGroupIdentity{"query-group-1"}, now,
	); err != nil {
		t.Fatalf("PublishAssignments() error=%v", err)
	}
	assigned, err := production.AssignedQueryGroups(
		context.Background(), []execution.QueryGroupIdentity{"query-group-1"},
	)
	if err != nil || !reflect.DeepEqual(assigned, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("AssignedQueryGroups() assigned=%v error=%v", assigned, err)
	}
	runner, err := production.OpenQueryGroup(context.Background(), "query-group-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenQueryGroup() error = %v", err)
	}
	leaseContext, cancelLease := context.WithCancel(context.Background())
	leaseDone := make(chan error, 1)
	go func() { leaseDone <- runner.MaintainLease(leaseContext, time.Millisecond, time.Minute) }()
	waitSignal(t, store.renewed, "production lease renewal")
	cancelLease()
	if err := <-leaseDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("MaintainLease() error = %v, want context cancellation", err)
	}
	if !hasObservedStage(observations, observability.StageLeaseRenewed) {
		t.Fatalf("ownership observations = %+v, want lease_renewed", observations)
	}
	store.checkErr = ownership.ErrStaleFence
	if _, attempted, err := runner.RunOne(context.Background()); !errors.Is(err, ownership.ErrStaleFence) || attempted {
		t.Fatalf("RunOne(stale fence) attempted=%v error=%v", attempted, err)
	}
	if store.acquireLeaderCalls != 1 || store.publishAssignmentCalls != 1 || store.acquireLeaseCalls != 1 {
		t.Fatalf("control/assignment/lease calls = %d/%d/%d, want 1/1/1",
			store.acquireLeaderCalls, store.publishAssignmentCalls, store.acquireLeaseCalls)
	}
	if err := runner.Release(context.Background()); err != nil || store.releaseCalls != 1 {
		t.Fatalf("Release() calls=%d error=%v", store.releaseCalls, err)
	}
}

func TestProductionPhaseTwoOwnershipFollowerReadsAssignmentWithoutPublishing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := &fakePhaseTwoOwnershipStore{
		now: now, acquireLeaderErr: ownership.ErrLeaseBusy,
		assignment: ownership.AssignmentRecord{
			QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 1,
			RecordRevision: 1, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		},
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{
		DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities",
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Minute, Observer: observability.NopObserver{}, Reconcile: reconciler,
		Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second,
		SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute)
	if err != nil || leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v, want follower", leader, err)
	}
	assigned, err := production.AssignedQueryGroups(context.Background(), []execution.QueryGroupIdentity{"query-group-1"})
	if err != nil || !reflect.DeepEqual(assigned, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("AssignedQueryGroups() assigned=%v error=%v", assigned, err)
	}
	if store.publishAssignmentCalls != 0 {
		t.Fatalf("follower published %d Assignments", store.publishAssignmentCalls)
	}
}

func TestProductionSlotObservationsBracketRealExecutionWithFrozenProvenance(t *testing.T) {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:                 execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision:     "snapshot-1",
		QueryRevision:        "query-1",
		ScheduleRevision:     "schedule-1",
		ScheduleSegmentStart: 60,
		DuePlanSetDigest:     "due-plan-set-1",
	}
	fence := execution.OwnerFence{
		QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 3, LeaseToken: "lease-1",
	}
	var observations []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	source := observedProductionSlotSource{
		next: slotSourceFunc(func(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, error) {
			return scheduler.FrozenSlot{
				Contract: contractRef,
				DuePlanTargets: execution.FrozenDuePlanTargets{
					DuePlanSetDigest: contractRef.DuePlanSetDigest,
					Plans:            []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}},
				},
				EarliestQueryDeadlineUnixMilli: 121_000,
				RecoveryUntilUnixMilli:         721_000,
				KeepUntilUnixMilli:             797_000,
				Dispatch: scheduler.SlotDispatchContext{
					Operation: execution.OperationNormal, OwnerFence: fence, AssignmentGeneration: 1,
				},
				ExpectedNextSlot: contractRef.Slot.EvaluationTime,
			}, true, nil
		}),
		observer: observer,
	}
	slot, due, err := source.Next(context.Background(), contractRef.Slot.QueryGroup)
	if err != nil || !due {
		t.Fatalf("observed SlotSource.Next() due=%v error=%v", due, err)
	}
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			if got := observedStages(observations); !reflect.DeepEqual(got, []observability.Stage{
				observability.StageScheduleDue, observability.StageSlotStarted,
			}) {
				t.Fatalf("observations before real execution = %v", got)
			}
			return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}, nil
		}),
		observer: observer,
	}
	request := execution.SlotExecutionRequest{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli, Operation: slot.Dispatch.Operation,
		RecoveryUntilUnixMilli: slot.RecoveryUntilUnixMilli, KeepUntilUnixMilli: slot.KeepUntilUnixMilli,
		OwnerFence: slot.Dispatch.OwnerFence, ExpectedNextSlot: slot.ExpectedNextSlot, AttemptNo: 1,
	}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		t.Fatalf("observed Slot executor error = %v", err)
	}
	if got := observedStages(observations); !reflect.DeepEqual(got, []observability.Stage{
		observability.StageScheduleDue, observability.StageSlotStarted, observability.StageSlotCompleted,
	}) {
		t.Fatalf("Slot observations = %v", got)
	}
	for _, observation := range observations {
		trace := observation.Trace
		if trace.QueryGroupKey != "query-group-1" || trace.EvaluationTime != 120 ||
			trace.SnapshotRevision != "snapshot-1" || trace.QueryRevision != "query-1" ||
			trace.ScheduleRevision != "schedule-1" || trace.ScheduleSegmentStart != 60 ||
			trace.DuePlanSetDigest != "due-plan-set-1" || trace.OwnerID != "worker-1" || trace.OwnerEpoch != 3 {
			t.Fatalf("Slot observation lacks frozen provenance: %+v", observation)
		}
	}

	observations = nil
	notDue := observedProductionSlotSource{
		next: slotSourceFunc(func(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, error) {
			return scheduler.FrozenSlot{}, false, nil
		}),
		observer: observer,
	}
	if _, due, err := notDue.Next(context.Background(), "query-group-1"); err != nil || due {
		t.Fatalf("not-due SlotSource.Next() due=%v error=%v", due, err)
	}
	if len(observations) != 0 {
		t.Fatalf("not-due Slot emitted execution observations: %+v", observations)
	}
}

func TestObservedProductionSlotExecutorClassifiesMissingFrozenQueryFacts(t *testing.T) {
	var observations []observability.Observation
	wantErr := fmt.Errorf("resolve frozen input closure: %w", access.ErrFrozenQueryPlanUnavailable)
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{}, wantErr
		}),
		observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	}
	_, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{})
	if !errors.Is(err, access.ErrFrozenQueryPlanUnavailable) {
		t.Fatalf("Execute() error=%v", err)
	}
	if len(observations) != 2 || observations[1].Stage != observability.StageSlotCompleted ||
		observations[1].Result != observability.ResultFailed ||
		observations[1].ReasonCode != observability.ReasonContractDeterministic {
		t.Fatalf("observations=%+v", observations)
	}
}

func productionRequest(fact execution.FrozenSlotContractFact, operation execution.Operation) execution.SlotExecutionRequest {
	targets, deadline, err := frozenExecutionFacts(fact)
	if err != nil {
		panic(err)
	}
	return execution.SlotExecutionRequest{
		Contract: fact.Contract, DuePlanTargets: targets, EarliestQueryDeadlineUnixMilli: deadline,
		RecoveryUntilUnixMilli: deadline + int64((10*time.Minute)/time.Millisecond),
		KeepUntilUnixMilli:     deadline + int64((11*time.Minute)/time.Millisecond),
		OwnerFence:             execution.OwnerFence{QueryGroup: fact.Contract.Slot.QueryGroup, OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot:       fact.Contract.Slot.EvaluationTime, Operation: operation, AttemptNo: 1,
	}
}

type fakePhaseTwoOwnershipStore struct {
	mu                     sync.Mutex
	now                    time.Time
	worker                 ownership.WorkerRegistration
	assignment             ownership.AssignmentRecord
	checkErr               error
	acquireLeaderErr       error
	acquireLeaderCalls     int
	publishAssignmentCalls int
	acquireLeaseCalls      int
	releaseCalls           int
	renewed                chan struct{}
	renewOnce              sync.Once
}

func (store *fakePhaseTwoOwnershipStore) RegisterWorker(_ context.Context, worker ownership.WorkerRegistration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.worker = worker
	return nil
}

func (store *fakePhaseTwoOwnershipStore) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return []ownership.WorkerRegistration{store.worker}, nil
}

func (store *fakePhaseTwoOwnershipStore) ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.assignment.QueryGroup == "" {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
	}
	return store.assignment, nil
}

func (store *fakePhaseTwoOwnershipStore) PublishAssignment(
	_ context.Context,
	authority ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.publishAssignmentCalls++
	store.assignment = ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID,
		AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: authority.Fence.OwnerEpoch,
		PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}
	return store.assignment, nil
}

func (store *fakePhaseTwoOwnershipStore) AcquireControlLeader(
	_ context.Context,
	leaderID string,
	at time.Time,
	ttl time.Duration,
) (ownership.PublicationAuthority, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.acquireLeaderCalls++
	if store.acquireLeaderErr != nil {
		return ownership.PublicationAuthority{}, store.acquireLeaderErr
	}
	return ownership.PublicationAuthority{Fence: execution.OwnerFence{
		QueryGroup: ownership.ControlLeaderIdentity, OwnerID: leaderID, OwnerEpoch: 1, LeaseToken: "leader-token",
	}, Deadline: at.Add(ttl)}, nil
}

func (store *fakePhaseTwoOwnershipStore) RenewControlLeader(
	_ context.Context,
	authority ownership.PublicationAuthority,
	at time.Time,
	ttl time.Duration,
) (ownership.PublicationAuthority, error) {
	authority.Deadline = at.Add(ttl)
	return authority, nil
}

func (store *fakePhaseTwoOwnershipStore) Acquire(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	at time.Time,
	ttl time.Duration,
) (ownership.Lease, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.acquireLeaseCalls++
	return ownership.Lease{Fence: execution.OwnerFence{
		QueryGroup: queryGroup, OwnerID: workerID, OwnerEpoch: 1, LeaseToken: "lease-token",
	}, Deadline: at.Add(ttl)}, nil
}

func (store *fakePhaseTwoOwnershipStore) Renew(
	_ context.Context,
	fence execution.OwnerFence,
	at time.Time,
	ttl time.Duration,
) (ownership.Lease, error) {
	store.renewOnce.Do(func() { close(store.renewed) })
	return ownership.Lease{Fence: fence, Deadline: at.Add(ttl)}, nil
}

func (store *fakePhaseTwoOwnershipStore) CheckFence(context.Context, execution.OwnerFence, time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.checkErr
}

func (store *fakePhaseTwoOwnershipStore) Release(context.Context, execution.OwnerFence) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.releaseCalls++
	return nil
}

func (store *fakePhaseTwoOwnershipStore) Close() error { return nil }

type unavailableSlotCatalog struct{}

func (unavailableSlotCatalog) ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error) {
	return execution.FrozenQueryGroupSchedule{}, errors.New("unexpected schedule read")
}

func (unavailableSlotCatalog) ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	return execution.FrozenQueryGroupSchedule{}, errors.New("unexpected schedule read")
}

func (unavailableSlotCatalog) ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error) {
	return 0, false, errors.New("unexpected schedule read")
}

func (unavailableSlotCatalog) ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	return execution.FrozenQueryGroupSchedule{}, errors.New("unexpected schedule read")
}

func (unavailableSlotCatalog) NextSlotAfter(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.EvaluationTime, error) {
	return 0, errors.New("unexpected schedule read")
}

func (unavailableSlotCatalog) FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	return execution.FrozenSlotContractFact{}, errors.New("unexpected schedule freeze")
}

type unavailableScheduleProgress struct{}

func (unavailableScheduleProgress) LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	return execution.ProgressLoadResult{}, errors.New("unexpected Progress read")
}

type fakePlanActivationSource struct {
	result execution.PlanActivationResult
}

func (source *fakePlanActivationSource) LoadActivations(
	context.Context,
	execution.PlanActivationRequest,
) (execution.PlanActivationResult, error) {
	return source.result, nil
}

type rejectingSlotExecutor struct{}

func (rejectingSlotExecutor) Execute(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	return execution.SlotExecutionResult{}, errors.New("stale fence reached executor")
}

type slotSourceFunc func(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, error)

func (function slotSourceFunc) Next(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (scheduler.FrozenSlot, bool, error) {
	return function(ctx, queryGroup)
}

type slotExecutorFunc func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error)

func (function slotExecutorFunc) Execute(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	return function(ctx, request)
}

var _ scheduler.AssignmentStore = (*fakePhaseTwoOwnershipStore)(nil)
var _ ownership.LeaseStore = (*fakePhaseTwoOwnershipStore)(nil)

func hasObservedStage(observations []observability.Observation, stage observability.Stage) bool {
	for _, observation := range observations {
		if observation.Stage == stage {
			return true
		}
	}
	return false
}

func observedStages(observations []observability.Observation) []observability.Stage {
	stages := make([]observability.Stage, len(observations))
	for index, observation := range observations {
		stages[index] = observation.Stage
	}
	return stages
}
