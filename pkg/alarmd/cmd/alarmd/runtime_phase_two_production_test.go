// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func TestProductionFrozenExecutionResolvesExactPersistedContract(t *testing.T) {
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
		QueryGroup:  "query-group-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision, Start: 60,
	}, Plans: []execution.FrozenPlanSchedule{schedulePlan}}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 120},
		SnapshotRevision: "snapshot-1", QueryRevision: "query-1", ScheduleRevision: scheduleRevision,
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-1",
	}
	catalog := &fakeFrozenCatalog{schedule: schedule, fact: execution.FrozenSlotContractFact{
		Contract: contractRef, DuePlans: []execution.DuePlan{{
			Identity: plan, ScheduleRevision: planRevision, ScheduleSpec: spec,
			CompletionDeadlineUnixMilli: 180_000,
		}},
		Requirements: []execution.DataRequirement{{Consumers: []execution.DataRequirementConsumer{{
			Consumer: execution.ConsumerRef{Plan: plan}, ConsumerDeadlineUnixMilli: 180_000,
			DownstreamExecutionReserveMilliSec: 5_000,
		}}}},
	}}
	repository := &fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1", QueryPlan: execution.QueryPlanFacts{QueryRevision: "query-1"}}},
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
		frozen.QueryFacts[execution.LogicalQueryRef("query-1")].QueryRevision != "query-1" {
		t.Fatalf("resolved request/facts = %+v / %+v", catalog.request, frozen)
	}
	finalization, err := resolver.ResolveFinalization(context.Background(), execution.SlotExecutionRequest{
		Contract: contractRef, OwnerFence: execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: 120, Operation: execution.OperationNormal, AttemptNo: 1,
	})
	if err != nil || finalization.Mode != execution.FinalizationQueryRequired || finalization.Contract != contractRef {
		t.Fatalf("ResolveFinalization() = %+v, %v", finalization, err)
	}
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
	request := execution.SlotExecutionRequest{
		Contract: contractRef,
		OwnerFence: execution.OwnerFence{
			QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1",
		},
		ExpectedNextSlot: 120, Operation: execution.OperationNormal, AttemptNo: 1,
	}
	finalization, err := resolver.ResolveFinalization(context.Background(), request)
	if err != nil {
		t.Fatalf("ResolveFinalization() error = %v", err)
	}
	if finalization.Mode != execution.FinalizationGapSkipped ||
		finalization.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		!reflect.DeepEqual(finalization.Targets.Plans, []execution.PlanIdentity{plan}) {
		t.Fatalf("ResolveFinalization() = %+v", finalization)
	}
	if err := finalization.Validate(request); err != nil {
		t.Fatalf("ResolveFinalization() produced invalid finalization: %v", err)
	}
	now = time.UnixMilli(179_999)
	finalization, err = resolver.ResolveFinalization(context.Background(), request)
	if err != nil || finalization.Mode != execution.FinalizationGapSkipped {
		t.Fatalf("ResolveFinalization(after query deadline) = %+v, %v", finalization, err)
	}

	// Expiry is only the pre-G3 normal-path finalization rule. A future G3 recovery
	// scheduler must retain control of retry/replay/probe eligibility.
	for _, operation := range []execution.Operation{
		execution.OperationRetry,
		execution.OperationReplay,
		execution.OperationProbe,
	} {
		request.Operation = operation
		finalization, err = resolver.ResolveFinalization(context.Background(), request)
		if err != nil || finalization.Mode != execution.FinalizationQueryRequired {
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

type fakeFrozenCatalog struct {
	schedule execution.FrozenQueryGroupSchedule
	fact     execution.FrozenSlotContractFact
	request  execution.FreezeSlotContractRequest
}

func (catalog *fakeFrozenCatalog) ReadFrozenSchedule(
	context.Context,
	execution.QueryGroupIdentity,
	execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	return catalog.schedule, nil
}

func (catalog *fakeFrozenCatalog) FreezeSlotContract(
	_ context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	catalog.request = request
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
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
		Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: schedules, Progress: progress,
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-new", retired}) {
		t.Fatalf("draining active projection=(%#v,%v)", result, err)
	}
	progress.byGroup[retired] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: retired}, NextSlot: boundary, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	result, err = control.LoadActive(context.Background())
	if err != nil || !reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-new"}) {
		t.Fatalf("drained active projection=(%#v,%v)", result, err)
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
	if len(observations) != 1 || observations[0].Result != observability.ResultDegraded ||
		observations[0].Trace.QueryGroupKey != string(bad) || observations[0].Err == nil {
		t.Fatalf("draining isolation observation=%#v", observations)
	}
}

func TestProductionPhaseTwoControlKeepsCurrentActivationWhileCandidateIsPending(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{{
		Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-next",
	}}}
	repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{
		RecordRevision: 1, Current: publication,
	}, snapshot: controlplane.PublishedSnapshot{Publication: publication,
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}}}
	activator := &fakeInitialScheduleActivator{}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: activator, Repository: repository, Schedules: &fakeScheduleProjection{},
		Progress: &fakeProductionProgressReader{}, RefreshInterval: time.Second,
		Wait: func(context.Context, time.Duration) error { return errors.New("unexpected wait") },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Refresh(context.Background())
	if err != nil || result.Status != phaseTwoControlHealthy ||
		!reflect.DeepEqual(result.QueryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("Refresh() = %#v, %v", result, err)
	}
	if activator.calls != 0 {
		t.Fatalf("pending candidate changed current activation, calls = %d", activator.calls)
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
	if len(observations) != 1 || observations[0].Stage != observability.StageSnapshotRefreshed ||
		observations[0].Result != observability.ResultDegraded ||
		observations[0].ReasonCode != observability.ReasonContractRetryable {
		t.Fatalf("publication conflict observations=%#v", observations)
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
		degraded.SourceKind != observability.SourceKindLegacyStrategy ||
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
	snapshots     map[controlplane.SnapshotPublicationRef]controlplane.PublishedSnapshot
}

func (repository *fakeProductionCatalogRepository) LoadPublishedSnapshot(
	_ context.Context,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.PublishedSnapshot, error) {
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
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits,
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
		Flights: flights, RecoveryLimits: limits,
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
		Contract: slot.Contract, Operation: slot.Dispatch.Operation,
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
