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
		Contract: contractRef, DuePlans: []execution.DuePlan{{Identity: plan}},
	}}
	repository := &fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1},
		QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1", QueryPlan: execution.QueryPlanFacts{QueryRevision: "query-1"}}},
	}}
	resolver, err := newProductionFrozenExecution(catalog, repository)
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
		ExpectedNextSlot: 120, Operation: execution.OperationNormal,
	})
	if err != nil || finalization.Mode != execution.FinalizationQueryRequired || finalization.Contract != contractRef {
		t.Fatalf("ResolveFinalization() = %+v, %v", finalization, err)
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
		Activator: activator, Repository: repository, RefreshInterval: time.Second,
		Wait: func(context.Context, time.Duration) error { waits++; return nil },
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoControl() error = %v", err)
	}
	queryGroups, err := control.InitialRefresh(context.Background())
	if err != nil || !reflect.DeepEqual(queryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("InitialRefresh() = %v, %v", queryGroups, err)
	}
	if reconciler.calls != 2 || waits != 1 || activator.calls != 1 || activator.publication != publication {
		t.Fatalf("cold-start calls refresh/wait/activate=%d/%d/%d publication=%+v",
			reconciler.calls, waits, activator.calls, activator.publication)
	}
}

func TestProductionPhaseTwoControlRejectsG1SnapshotWithoutExactlyOneQueryGroup(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1}
	for name, groups := range map[string][]controlplane.QueryGroup{
		"empty":    {},
		"multiple": {{Identity: "query-group-1"}, {Identity: "query-group-2"}},
	} {
		t.Run(name, func(t *testing.T) {
			reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{{
				Status: controlplane.SourceRefreshPublished, Observation: "observation-1", Publication: publication,
			}}}
			repository := &fakeProductionCatalogRepository{snapshot: controlplane.PublishedSnapshot{
				Publication: publication, QueryGroups: groups,
			}}
			activator := &fakeInitialScheduleActivator{state: controlplane.ActivationState{
				RecordRevision: 1, Current: publication,
			}}
			control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
				Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
				Activator: activator, Repository: repository, RefreshInterval: time.Second,
				Wait: func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.InitialRefresh(context.Background()); err == nil ||
				!strings.Contains(err.Error(), "exactly one Query Group") {
				t.Fatalf("InitialRefresh() error = %v, want exact-one G1 rejection", err)
			}
		})
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
		Activator: activator, Repository: repository, RefreshInterval: time.Second,
		Wait: func(context.Context, time.Duration) error { return errors.New("unexpected wait") },
	})
	if err != nil {
		t.Fatal(err)
	}
	queryGroups, err := control.Refresh(context.Background())
	if err != nil || !reflect.DeepEqual(queryGroups, []execution.QueryGroupIdentity{"query-group-1"}) {
		t.Fatalf("Refresh() = %v, %v", queryGroups, err)
	}
	if activator.calls != 0 {
		t.Fatalf("pending candidate changed current activation, calls = %d", activator.calls)
	}
}

type fakeSourceReconciler struct {
	results []controlplane.SourceRefreshResult
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
	reconciler.calls++
	return result, nil
}

type fakeProductionCatalogRepository struct {
	activation    controlplane.ActivationState
	activationErr error
	snapshot      controlplane.PublishedSnapshot
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
	calls       int
}

func (activator *fakeInitialScheduleActivator) Ensure(
	_ context.Context,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.ActivationState, error) {
	activator.calls++
	activator.publication = publication
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
	store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})}
	var observations []observability.Observation
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoOwnership() error = %v", err)
	}
	if err := production.RegisterWorker(context.Background(), ownership.WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady,
		DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "shadow",
		CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	assigned, err := production.Reconcile(
		context.Background(), []execution.QueryGroupIdentity{"query-group-1"}, now,
	)
	if err != nil || len(assigned) != 1 || assigned[0] != "query-group-1" {
		t.Fatalf("Reconcile() assigned=%v error=%v", assigned, err)
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
		OwnerFence: slot.Dispatch.OwnerFence, ExpectedNextSlot: slot.ExpectedNextSlot,
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
