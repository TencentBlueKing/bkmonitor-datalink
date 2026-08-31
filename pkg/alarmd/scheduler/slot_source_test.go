// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestProductionSlotSourceColdStartFreezesFirstDueSlot(t *testing.T) {
	schedule := testSchedule()
	source, catalog := newTestProductionSlotSource(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing})

	slot, due, err := source.Next(context.Background(), schedule.Lane.QueryGroup)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !due {
		t.Fatal("Next() due = false, want true")
	}
	if slot.Contract.Slot.EvaluationTime != 120 || slot.ExpectedNextSlot != 120 || slot.NextSlotAfterCompletion != 180 {
		t.Fatalf("slot times = %+v", slot)
	}
	if slot.Contract.SnapshotRevision != "snapshot-1" || slot.Contract.QueryRevision != "query-1" ||
		slot.Contract.ScheduleRevision != schedule.Lane.ScheduleRevision || slot.Contract.DuePlanSetDigest == "" {
		t.Fatalf("frozen contract = %+v", slot.Contract)
	}
	wantDue := []execution.FrozenPlanScheduleRef{
		{Identity: planIdentity("1"), ScheduleRevision: schedule.Plans[1].ScheduleRevision},
		{Identity: planIdentity("2"), ScheduleRevision: schedule.Plans[0].ScheduleRevision},
	}
	if !reflect.DeepEqual(catalog.requests[0].DuePlans, wantDue) {
		t.Fatalf("due plans = %+v, want %+v", catalog.requests[0].DuePlans, wantDue)
	}
	if slot.Dispatch.Operation != execution.OperationNormal || slot.Dispatch.AssignmentGeneration != 3 ||
		slot.Dispatch.OwnerFence.OwnerEpoch != 7 {
		t.Fatalf("dispatch context = %+v", slot.Dispatch)
	}
}

func TestProductionSlotSourceUsesContinuousProgress(t *testing.T) {
	schedule := testSchedule()
	namespace := schedule.Lane.ProgressNamespace()
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 180, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}
	source, catalog := newTestProductionSlotSource(t, schedule, load)

	slot, due, err := source.Next(context.Background(), schedule.Lane.QueryGroup)
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.ExpectedNextSlot != 180 || slot.NextSlotAfterCompletion != 240 {
		t.Fatalf("slot times = %+v", slot)
	}
	wantDue := []execution.FrozenPlanScheduleRef{{Identity: planIdentity("1"), ScheduleRevision: schedule.Plans[1].ScheduleRevision}}
	if !reflect.DeepEqual(catalog.requests[0].DuePlans, wantDue) {
		t.Fatalf("due plans = %+v, want %+v", catalog.requests[0].DuePlans, wantDue)
	}
}

func TestProductionSlotSourceFreezesContentRevisionByEvaluationTimeAcrossCutover(t *testing.T) {
	schedule := testSchedule()
	freeze := func(request execution.FreezeSlotContractRequest) execution.FrozenSlotContractFact {
		snapshotRevision := execution.SnapshotRevision("snapshot-before-cutover")
		queryRevision := execution.QueryRevision("query-before-cutover")
		if request.EvaluationTime >= 180 {
			snapshotRevision = "snapshot-after-cutover"
			queryRevision = "query-after-cutover"
		}
		fact := frozenSlotContractFact(t, request)
		fact.Contract = execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: request.Lane.QueryGroup, ScheduleRevision: request.Lane.ScheduleRevision,
				EvaluationTime: request.EvaluationTime},
			SnapshotRevision: snapshotRevision, QueryRevision: queryRevision,
			ScheduleRevision: request.Lane.ScheduleRevision, DuePlanSetDigest: fact.Contract.DuePlanSetDigest,
		}
		return fact
	}
	tests := []struct {
		name         string
		load         execution.ProgressLoadResult
		wantSnapshot execution.SnapshotRevision
		wantQuery    execution.QueryRevision
	}{
		{name: "before cutover", load: execution.ProgressLoadResult{Status: execution.ProgressMissing},
			wantSnapshot: "snapshot-before-cutover", wantQuery: "query-before-cutover"},
		{name: "after cutover", load: foundProgress(schedule, 180, 120),
			wantSnapshot: "snapshot-after-cutover", wantQuery: "query-after-cutover"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedule: schedule, freeze: freeze}
			source := mustProductionSlotSource(t, schedule.Lane,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: test.load}, time.Unix(200, 0))

			slot, due, err := source.Next(context.Background(), schedule.Lane.QueryGroup)
			if err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if slot.Contract.SnapshotRevision != test.wantSnapshot || slot.Contract.QueryRevision != test.wantQuery {
				t.Fatalf("contract content revisions = %+v", slot.Contract)
			}
		})
	}
}

func TestProductionSlotSourceReadsOnlyBoundScheduleLane(t *testing.T) {
	for _, schedule := range []execution.FrozenQueryGroupSchedule{testSchedule(), testScheduleWithFirstPlanInterval(180)} {
		revision := schedule.Lane.ScheduleRevision
		t.Run(string(revision), func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedule: schedule}
			source := mustProductionSlotSource(t, schedule.Lane,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, due, err := source.Next(context.Background(), schedule.Lane.QueryGroup); err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if catalog.readLane != schedule.Lane {
				t.Fatalf("ReadFrozenSchedule() lane = %+v, want %+v", catalog.readLane, schedule.Lane)
			}
			if catalog.requests[0].Lane.ScheduleRevision != revision {
				t.Fatalf("FreezeSlotContract() revision = %q, want %q", catalog.requests[0].Lane.ScheduleRevision, revision)
			}
		})
	}
}

func TestProductionSlotSourceRestartWithSameProgressFreezesSameContract(t *testing.T) {
	schedule := testSchedule()
	namespace := schedule.Lane.ProgressNamespace()
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 180, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}

	first, _ := newTestProductionSlotSource(t, schedule, load)
	second, _ := newTestProductionSlotSource(t, schedule, load)
	firstSlot, firstDue, firstErr := first.Next(context.Background(), schedule.Lane.QueryGroup)
	secondSlot, secondDue, secondErr := second.Next(context.Background(), schedule.Lane.QueryGroup)
	if firstErr != nil || secondErr != nil || !firstDue || !secondDue {
		t.Fatalf("restart results first=(%v,%v) second=(%v,%v)", firstDue, firstErr, secondDue, secondErr)
	}
	if !reflect.DeepEqual(firstSlot.Contract, secondSlot.Contract) || firstSlot.ExpectedNextSlot != secondSlot.ExpectedNextSlot {
		t.Fatalf("contracts drifted across restart: first=%+v second=%+v", firstSlot, secondSlot)
	}
}

func TestProductionSlotSourceReturnsNotDueBeforeEvaluationTime(t *testing.T) {
	schedule := testSchedule()
	source, catalog := newTestProductionSlotSourceAt(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing}, time.Unix(119, 0))

	_, due, err := source.Next(context.Background(), schedule.Lane.QueryGroup)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if due {
		t.Fatal("Next() due = true before EvaluationTime")
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("FreezeContract calls = %d, want 0", len(catalog.requests))
	}
}

func TestProductionSlotSourceRejectsProgressOutsideSchedule(t *testing.T) {
	schedule := testSchedule()
	namespace := schedule.Lane.ProgressNamespace()
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 181, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}
	source, _ := newTestProductionSlotSource(t, schedule, load)

	if _, _, err := source.Next(context.Background(), schedule.Lane.QueryGroup); !errors.Is(err, ErrProgressOffSchedule) {
		t.Fatalf("Next() error = %v, want ErrProgressOffSchedule", err)
	}
}

func TestProductionSlotSourceRejectsMissingDueFacts(t *testing.T) {
	schedule := testSchedule()
	schedule.Plans = nil
	source, _ := newTestProductionSlotSource(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing})

	if _, _, err := source.Next(context.Background(), schedule.Lane.QueryGroup); !errors.Is(err, ErrScheduleFactsInvalid) {
		t.Fatalf("Next() error = %v, want ErrScheduleFactsInvalid", err)
	}
}

func TestProductionSlotSourceRejectsNonCurrentAssignmentAndLostLease(t *testing.T) {
	tests := []struct {
		name       string
		assignment ownership.AssignmentRecord
		sessionErr error
		want       error
	}{
		{name: "not desired", assignment: testAssignment("worker-2", 3), want: ownership.ErrNotDesired},
		{name: "lost lease", assignment: testAssignment("worker-1", 3), sessionErr: ownership.ErrStaleFence, want: ownership.ErrStaleFence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule := testSchedule()
			assignments := &fakeAssignmentReader{records: []ownership.AssignmentRecord{test.assignment}}
			session := &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}, err: test.sessionErr}
			catalog := &fakeSlotCatalog{t: t, schedule: schedule}
			source := mustProductionSlotSource(t, schedule.Lane, assignments, session, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.Lane.QueryGroup); !errors.Is(err, test.want) {
				t.Fatalf("Next() error = %v, want %v", err, test.want)
			}
			if catalog.reads != 0 {
				t.Fatalf("catalog reads = %d, want 0", catalog.reads)
			}
		})
	}
}

func TestProductionSlotSourceRejectsOwnershipChangeWhileFreezing(t *testing.T) {
	tests := []struct {
		name        string
		assignments []ownership.AssignmentRecord
		fences      []execution.OwnerFence
	}{
		{name: "assignment generation", assignments: []ownership.AssignmentRecord{testAssignment("worker-1", 3), testAssignment("worker-1", 4)}, fences: []execution.OwnerFence{testFence(7), testFence(7)}},
		{name: "owner epoch", assignments: []ownership.AssignmentRecord{testAssignment("worker-1", 3), testAssignment("worker-1", 3)}, fences: []execution.OwnerFence{testFence(7), testFence(8)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule := testSchedule()
			source := mustProductionSlotSource(t, schedule.Lane, &fakeAssignmentReader{records: test.assignments},
				&sequenceOwnerSession{fences: test.fences}, &fakeSlotCatalog{t: t, schedule: schedule},
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.Lane.QueryGroup); !errors.Is(err, ErrSlotOwnershipChanged) {
				t.Fatalf("Next() error = %v, want ErrSlotOwnershipChanged", err)
			}
		})
	}
}

func TestProductionSlotSourceRejectsCatalogContractDrift(t *testing.T) {
	schedule := testSchedule()
	valid := frozenSlotContractFact(t, execution.FreezeSlotContractRequest{
		Lane: schedule.Lane, EvaluationTime: 120, DuePlans: schedule.DuePlanRefs(120),
	})
	valid.Contract = execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: schedule.Lane.QueryGroup, ScheduleRevision: schedule.Lane.ScheduleRevision, EvaluationTime: 120},
		SnapshotRevision: "snapshot-1",
		QueryRevision:    "query-1",
		ScheduleRevision: schedule.Lane.ScheduleRevision,
		DuePlanSetDigest: valid.Contract.DuePlanSetDigest,
	}
	tests := []struct {
		name   string
		mutate func(*execution.FrozenSlotContractFact)
	}{
		{name: "schedule revision", mutate: func(fact *execution.FrozenSlotContractFact) { fact.Contract.ScheduleRevision = "schedule-changed" }},
		{name: "missing due Plan digest", mutate: func(fact *execution.FrozenSlotContractFact) { fact.Contract.DuePlanSetDigest = "" }},
		{name: "returned exact due facts", mutate: func(fact *execution.FrozenSlotContractFact) {
			fact.DuePlans = append([]execution.DuePlan(nil), fact.DuePlans...)
			fact.DuePlans[0].ScheduleRevision = "plan-schedule-changed"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := valid
			test.mutate(&fact)
			catalog := &fakeSlotCatalog{t: t, schedule: schedule, contract: fact}
			source := mustProductionSlotSource(t, schedule.Lane,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.Lane.QueryGroup); !errors.Is(err, ErrSlotContractDrift) {
				t.Fatalf("Next() error = %v, want ErrSlotContractDrift", err)
			}
		})
	}
}

func testSchedule() execution.FrozenQueryGroupSchedule {
	plans := []execution.FrozenPlanSchedule{
		frozenPlanSchedule(planIdentity("2"), execution.ScheduleSpec{EvaluationIntervalSeconds: 120, Alignment: 0, Timezone: "UTC"}),
		frozenPlanSchedule(planIdentity("1"), execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}),
	}
	revision, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		panic(err)
	}
	return execution.FrozenQueryGroupSchedule{
		Lane:                execution.ScheduleLaneIdentity{QueryGroup: "query-group-1", ScheduleRevision: revision},
		FirstEvaluationTime: 120, Plans: plans,
	}
}

func testScheduleWithFirstPlanInterval(interval int64) execution.FrozenQueryGroupSchedule {
	schedule := testSchedule()
	schedule.Plans[0] = frozenPlanSchedule(schedule.Plans[0].Identity, execution.ScheduleSpec{
		EvaluationIntervalSeconds: interval, Alignment: 0, Timezone: "UTC",
	})
	revision, err := execution.DeriveQueryGroupScheduleRevision(schedule.Plans)
	if err != nil {
		panic(err)
	}
	schedule.Lane.ScheduleRevision = revision
	return schedule
}

func foundProgress(schedule execution.FrozenQueryGroupSchedule, nextSlot, lastFullSlot execution.EvaluationTime) execution.ProgressLoadResult {
	return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: schedule.Lane.ProgressNamespace(),
		NextSlot:  nextSlot, LastFullSlot: lastFullSlot, LastCompletionKind: execution.CompletionFull,
	}}
}

func planIdentity(strategyID string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID}
}

func frozenPlanSchedule(identity execution.PlanIdentity, spec execution.ScheduleSpec) execution.FrozenPlanSchedule {
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		panic(err)
	}
	return execution.FrozenPlanSchedule{Identity: identity, ScheduleRevision: revision, Spec: spec}
}

func testAssignment(workerID string, generation uint64) ownership.AssignmentRecord {
	return ownership.AssignmentRecord{QueryGroup: "query-group-1", DesiredWorkerID: workerID,
		AssignmentGeneration: generation, RecordRevision: generation, ControlEpoch: 2,
		PlacementReason: ownership.PlacementRendezvous, AssignedAt: time.Unix(50, 0)}
}

func testFence(epoch uint64) execution.OwnerFence {
	return execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: epoch, LeaseToken: "lease-token"}
}

func newTestProductionSlotSource(
	t *testing.T,
	schedule execution.FrozenQueryGroupSchedule,
	load execution.ProgressLoadResult,
) (*ProductionSlotSource, *fakeSlotCatalog) {
	return newTestProductionSlotSourceAt(t, schedule, load, time.Unix(200, 0))
}

func newTestProductionSlotSourceAt(
	t *testing.T,
	schedule execution.FrozenQueryGroupSchedule,
	load execution.ProgressLoadResult,
	at time.Time,
) (*ProductionSlotSource, *fakeSlotCatalog) {
	catalog := &fakeSlotCatalog{t: t, schedule: schedule}
	source := mustProductionSlotSource(t, schedule.Lane,
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
		&fakeProgressReader{result: load}, at)
	return source, catalog
}

func mustProductionSlotSource(
	t *testing.T,
	lane execution.ScheduleLaneIdentity,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	at time.Time,
) *ProductionSlotSource {
	t.Helper()
	source, err := NewProductionSlotSource(lane, "worker-1", assignments, session, catalog, progress, func() time.Time { return at })
	if err != nil {
		t.Fatalf("NewProductionSlotSource() error = %v", err)
	}
	return source
}

type fakeAssignmentReader struct {
	records []ownership.AssignmentRecord
	reads   int
}

func (reader *fakeAssignmentReader) ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	index := reader.reads
	if index >= len(reader.records) {
		index = len(reader.records) - 1
	}
	reader.reads++
	return reader.records[index], nil
}

type sequenceOwnerSession struct {
	fences []execution.OwnerFence
	reads  int
	err    error
}

func (session *sequenceOwnerSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	if session.err != nil {
		return execution.OwnerFence{}, session.err
	}
	index := session.reads
	if index >= len(session.fences) {
		index = len(session.fences) - 1
	}
	session.reads++
	return session.fences[index], nil
}

type fakeSlotCatalog struct {
	t        *testing.T
	schedule execution.FrozenQueryGroupSchedule
	contract execution.FrozenSlotContractFact
	freeze   func(execution.FreezeSlotContractRequest) execution.FrozenSlotContractFact
	requests []execution.FreezeSlotContractRequest
	reads    int
	readLane execution.ScheduleLaneIdentity
}

func (catalog *fakeSlotCatalog) ReadFrozenSchedule(
	_ context.Context,
	lane execution.ScheduleLaneIdentity,
) (execution.FrozenQueryGroupSchedule, error) {
	catalog.reads++
	catalog.readLane = lane
	return catalog.schedule, nil
}

func (catalog *fakeSlotCatalog) FreezeSlotContract(_ context.Context, request execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	catalog.requests = append(catalog.requests, request)
	if catalog.freeze != nil {
		return catalog.freeze(request), nil
	}
	if catalog.contract.Contract.Slot.QueryGroup != "" {
		return catalog.contract, nil
	}
	return frozenSlotContractFact(catalog.t, request), nil
}

func frozenSlotContractFact(t *testing.T, request execution.FreezeSlotContractRequest) execution.FrozenSlotContractFact {
	t.Helper()
	compiled := compiledPlanForSlotSource(t)
	plans := make([]execution.DuePlan, len(request.DuePlans))
	requirements := make([]execution.DataRequirement, len(request.DuePlans))
	for index, ref := range request.DuePlans {
		deadline := int64(request.EvaluationTime+60) * 1000
		plans[index] = execution.DuePlan{
			Identity: ref.Identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
			ScheduleRevision: ref.ScheduleRevision, CompletionDeadlineUnixMilli: deadline,
		}
		requirementID := execution.RequirementID("primary-" + ref.Identity.StrategyID)
		requirements[index] = execution.DataRequirement{
			RequirementID: requirementID, DatasetName: execution.DatasetName(requirementID), Role: execution.InputRolePrimary,
			LogicalQueryRef: execution.LogicalQueryRef("query-main"),
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
			StepMillis:      60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
			ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"},
			Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: ref.Identity}, ConsumerDeadlineUnixMilli: deadline,
				DownstreamExecutionReserveMilliSec: 5_000,
			}},
		}
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	return execution.FrozenSlotContractFact{
		Contract: execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: request.Lane.QueryGroup, ScheduleRevision: request.Lane.ScheduleRevision,
				EvaluationTime: request.EvaluationTime},
			SnapshotRevision: "snapshot-1", QueryRevision: "query-1",
			ScheduleRevision: request.Lane.ScheduleRevision, DuePlanSetDigest: digest,
		},
		DuePlans: plans, Requirements: requirements,
	}
}

func compiledPlanForSlotSource(t *testing.T) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 4, MaxAlgorithmsPerLevel: 4, MaxGroupsPerAlgorithm: 4,
		MaxConditionsPerAlgorithm: 8, MaxASTNodesPerLevel: 32, MaxTriggerWindowSize: 64,
		MaxRecoveryConsecutiveWindows: 64, MaxRequiredHistoryPoints: 64, MaxTriggerComputeCost: 1 << 16,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 4, MaxCacheBytes: 1 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "scheduler-test-v1",
	})
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "1", Revision: "r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: "1", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 60,
				AggregationInterval: 60, EvaluationInterval: 60,
			},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 1, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
					Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
				}}},
				TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
					Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1,
					Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "v1", CodecSemanticsVersion: "v1", IdentitySchemaDigest: strings.Repeat("c", 64),
			SourceTimeSemanticsVersion: "seconds-v1", HistoryCellSemanticsVersion: "v1",
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v", result.PlanTerminal())
	}
	return compiled
}

type fakeProgressReader struct {
	result execution.ProgressLoadResult
}

func (reader *fakeProgressReader) LoadProgress(context.Context, execution.ProgressNamespace) (execution.ProgressLoadResult, error) {
	return reader.result, nil
}
