// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

type productionFrozenCatalog interface {
	ReadFrozenSchedule(
		context.Context,
		execution.QueryGroupIdentity,
		execution.EvaluationTime,
	) (execution.FrozenQueryGroupSchedule, error)
	FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

type productionSnapshotReader interface {
	LoadSnapshot(context.Context, execution.SnapshotRevision) (controlplane.PublishedSnapshot, error)
}

type productionFrozenExecution struct {
	catalog    productionFrozenCatalog
	repository productionSnapshotReader
}

func newProductionFrozenExecution(
	catalog productionFrozenCatalog,
	repository productionSnapshotReader,
) (*productionFrozenExecution, error) {
	if catalog == nil || repository == nil {
		return nil, errors.New("phase-two frozen execution dependencies are incomplete")
	}
	return &productionFrozenExecution{catalog: catalog, repository: repository}, nil
}

func (source *productionFrozenExecution) ResolveFrozenPlan(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
) (access.FrozenPlan, error) {
	fact, err := source.resolveFrozenFact(ctx, contractRef)
	if err != nil {
		return access.FrozenPlan{}, err
	}
	snapshot, err := source.repository.LoadSnapshot(ctx, contractRef.SnapshotRevision)
	if err != nil {
		return access.FrozenPlan{}, err
	}
	for _, group := range snapshot.QueryGroups {
		if group.Identity != contractRef.Slot.QueryGroup {
			continue
		}
		if group.QueryPlan.QueryRevision != contractRef.QueryRevision {
			return access.FrozenPlan{}, errors.New("phase-two frozen Query Group changed query revision")
		}
		queryRef := execution.LogicalQueryRef(group.QueryPlan.QueryRevision)
		return access.FrozenPlan{
			DuePlans:     append([]execution.DuePlan(nil), fact.DuePlans...),
			Requirements: append([]execution.DataRequirement(nil), fact.Requirements...),
			QueryFacts:   map[execution.LogicalQueryRef]execution.QueryPlanFacts{queryRef: group.QueryPlan},
		}, nil
	}
	return access.FrozenPlan{}, errors.New("phase-two frozen Query Group is absent from Snapshot")
}

func (source *productionFrozenExecution) ResolveFinalization(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.QueryFreeFinalization, error) {
	if err := request.Validate(); err != nil {
		return execution.QueryFreeFinalization{}, err
	}
	if _, err := source.resolveFrozenFact(ctx, request.Contract); err != nil {
		return execution.QueryFreeFinalization{}, err
	}
	return execution.QueryFreeFinalization{
		Contract: request.Contract, Mode: execution.FinalizationQueryRequired,
	}, nil
}

func (source *productionFrozenExecution) VerifyFrozenDuePlanTargets(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
	targets execution.FrozenDuePlanTargets,
) error {
	fact, err := source.resolveFrozenFact(ctx, contractRef)
	if err != nil {
		return err
	}
	if targets.DuePlanSetDigest != contractRef.DuePlanSetDigest || len(targets.Plans) != len(fact.DuePlans) {
		return errors.New("phase-two frozen due Plan targets differ from exact contract")
	}
	want := append([]execution.PlanIdentity(nil), targets.Plans...)
	got := make([]execution.PlanIdentity, len(fact.DuePlans))
	for index := range fact.DuePlans {
		got[index] = fact.DuePlans[index].Identity
	}
	sort.Slice(want, func(i, j int) bool { return lessProductionPlanIdentity(want[i], want[j]) })
	sort.Slice(got, func(i, j int) bool { return lessProductionPlanIdentity(got[i], got[j]) })
	for index := range want {
		if want[index] != got[index] {
			return errors.New("phase-two frozen due Plan targets differ from exact contract")
		}
	}
	return nil
}

func (source *productionFrozenExecution) resolveFrozenFact(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
) (execution.FrozenSlotContractFact, error) {
	if source == nil || source.catalog == nil || source.repository == nil {
		return execution.FrozenSlotContractFact{}, errors.New("phase-two frozen execution is not initialized")
	}
	if err := contractRef.Validate(); err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	schedule, err := source.catalog.ReadFrozenSchedule(
		ctx, contractRef.Slot.QueryGroup, contractRef.Slot.EvaluationTime,
	)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	segment := schedule.Segment
	if segment.QueryGroup != contractRef.Slot.QueryGroup || segment.QueryRevision != contractRef.QueryRevision ||
		segment.ScheduleRevision != contractRef.ScheduleRevision || segment.Start != contractRef.ScheduleSegmentStart ||
		segment.Publication.SnapshotRevision != contractRef.SnapshotRevision || !segment.Contains(contractRef.Slot.EvaluationTime) {
		return execution.FrozenSlotContractFact{}, errors.New("phase-two persisted Schedule Segment differs from frozen contract")
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: segment.QueryGroup, ScheduleRevision: segment.ScheduleRevision,
		ScheduleSegmentStart: segment.Start, EvaluationTime: contractRef.Slot.EvaluationTime,
		DuePlans: schedule.DuePlanRefs(contractRef.Slot.EvaluationTime),
	}
	fact, err := source.catalog.FreezeSlotContract(ctx, request)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	if fact.Contract != contractRef {
		return execution.FrozenSlotContractFact{}, errors.New("phase-two re-frozen Slot differs from execution contract")
	}
	return fact, nil
}

func lessProductionPlanIdentity(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

var _ access.FrozenPlanSource = (*productionFrozenExecution)(nil)
var _ execution.QueryFreeFinalizationSource = (*productionFrozenExecution)(nil)

func phaseTwoLegacyQueryRuntimeFacts(
	runtime config.PhaseTwoLegacyQueryRuntimeConfig,
) controlplane.LegacyQueryRuntimeFacts {
	var accessBKData *bool
	if runtime.AccessBKData != nil {
		value := *runtime.AccessBKData
		accessBKData = &value
	}
	return controlplane.LegacyQueryRuntimeFacts{
		AccessBKData:          accessBKData,
		BKDataCMDBLevelTables: append([]string{}, runtime.BKDataCMDBLevelTables...),
		SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{
			FieldName: runtime.SystemDiskFilter.FieldName,
			Values:    append([]string{}, runtime.SystemDiskFilter.Values...),
		},
		SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{
			FieldName: runtime.SystemNetworkFilter.FieldName,
			Values:    append([]string{}, runtime.SystemNetworkFilter.Values...),
		},
	}
}

type productionSourceReconciler interface {
	Refresh(
		context.Context,
		controlplane.StrategySource,
		controlplane.PrimaryQueryCompiler,
	) (controlplane.SourceRefreshResult, error)
}

type productionInitialScheduleActivator interface {
	Ensure(
		context.Context,
		controlplane.SnapshotPublicationRef,
	) (controlplane.ActivationState, error)
}

type productionCatalogRepository interface {
	LoadActivation(context.Context) (controlplane.ActivationState, error)
	LoadSnapshot(context.Context, execution.SnapshotRevision) (controlplane.PublishedSnapshot, error)
}

type productionPhaseTwoControlDependencies struct {
	Source          controlplane.StrategySource
	Planner         controlplane.PrimaryQueryCompiler
	Reconciler      productionSourceReconciler
	Activator       productionInitialScheduleActivator
	Repository      productionCatalogRepository
	RefreshInterval time.Duration
	Wait            func(context.Context, time.Duration) error
	Close           func() error
}

type productionPhaseTwoControl struct {
	dependencies productionPhaseTwoControlDependencies
}

func newProductionPhaseTwoControl(
	dependencies productionPhaseTwoControlDependencies,
) (*productionPhaseTwoControl, error) {
	if dependencies.Source == nil || dependencies.Planner == nil || dependencies.Reconciler == nil ||
		dependencies.Activator == nil || dependencies.Repository == nil || dependencies.RefreshInterval <= 0 ||
		dependencies.Wait == nil {
		return nil, errors.New("phase-two production Control dependencies are incomplete")
	}
	return &productionPhaseTwoControl{dependencies: dependencies}, nil
}

func (runtime *productionPhaseTwoControl) InitialRefresh(
	ctx context.Context,
) ([]execution.QueryGroupIdentity, error) {
	if runtime == nil {
		return nil, errors.New("phase-two production Control is not initialized")
	}
	for {
		queryGroups, pending, err := runtime.refresh(ctx)
		if err != nil || !pending {
			return queryGroups, err
		}
		if err := runtime.dependencies.Wait(ctx, runtime.dependencies.RefreshInterval); err != nil {
			return nil, err
		}
	}
}

func (runtime *productionPhaseTwoControl) Refresh(
	ctx context.Context,
) ([]execution.QueryGroupIdentity, error) {
	if runtime == nil {
		return nil, errors.New("phase-two production Control is not initialized")
	}
	queryGroups, _, err := runtime.refresh(ctx)
	return queryGroups, err
}

func (runtime *productionPhaseTwoControl) Close() error {
	if runtime == nil || runtime.dependencies.Close == nil {
		return nil
	}
	return runtime.dependencies.Close()
}

func (runtime *productionPhaseTwoControl) refresh(
	ctx context.Context,
) ([]execution.QueryGroupIdentity, bool, error) {
	result, err := runtime.dependencies.Reconciler.Refresh(
		ctx, runtime.dependencies.Source, runtime.dependencies.Planner,
	)
	if err != nil {
		return nil, false, err
	}
	if result.Status == controlplane.SourceRefreshPendingConfirmation {
		state, err := runtime.dependencies.Repository.LoadActivation(ctx)
		if errors.Is(err, controlplane.ErrActivationUnavailable) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
		return queryGroups, false, err
	}
	if result.Status != controlplane.SourceRefreshPublished && result.Status != controlplane.SourceRefreshUnchanged {
		return nil, false, errors.New("phase-two source refresh returned an invalid status")
	}
	if result.Publication.SnapshotRevision == "" || result.Publication.PublicationEpoch == 0 {
		return nil, false, errors.New("phase-two source refresh returned an incomplete publication")
	}
	state, err := runtime.dependencies.Activator.Ensure(ctx, result.Publication)
	if err != nil {
		return nil, false, err
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	return queryGroups, false, err
}

func (runtime *productionPhaseTwoControl) loadActiveQueryGroups(
	ctx context.Context,
	state controlplane.ActivationState,
) ([]execution.QueryGroupIdentity, error) {
	if state.RecordRevision == 0 || state.Current.SnapshotRevision == "" || state.Current.PublicationEpoch == 0 {
		return nil, errors.New("phase-two activation state is incomplete")
	}
	snapshot, err := runtime.dependencies.Repository.LoadSnapshot(ctx, state.Current.SnapshotRevision)
	if err != nil {
		return nil, err
	}
	if snapshot.Publication != state.Current || len(snapshot.QueryGroups) != 1 || snapshot.QueryGroups[0].Identity == "" {
		return nil, errors.New("phase-two active Snapshot must contain exactly one Query Group")
	}
	return []execution.QueryGroupIdentity{snapshot.QueryGroups[0].Identity}, nil
}

var _ phaseTwoControlRuntime = (*productionPhaseTwoControl)(nil)

type productionPhaseTwoActivationSource interface {
	LoadActivations(context.Context, execution.PlanActivationRequest) (execution.PlanActivationResult, error)
}

type productionPhaseTwoActivation struct {
	source productionPhaseTwoActivationSource
}

func (activation productionPhaseTwoActivation) IsPlanActive(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
	plan execution.PlanIdentity,
	epoch execution.StateApplyEpoch,
) (bool, error) {
	if activation.source == nil || epoch == 0 {
		return false, errors.New("phase-two production Plan activation is invalid")
	}
	request := execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanIdentity{plan}}
	result, err := activation.source.LoadActivations(ctx, request)
	if err != nil {
		return false, err
	}
	if err := result.Validate(request); err != nil {
		return false, err
	}
	fact := result.Facts[0]
	return (fact.Selection == execution.ActivationCurrent || fact.Selection == execution.ActivationPending) &&
		fact.Selected.StateApplyEpoch == epoch, nil
}

type productionPhaseTwoOwnershipStore interface {
	scheduler.AssignmentStore
	ownership.LeaseStore
	RegisterWorker(context.Context, ownership.WorkerRegistration) error
	AcquireControlLeader(context.Context, string, time.Time, time.Duration) (ownership.PublicationAuthority, error)
	RenewControlLeader(context.Context, ownership.PublicationAuthority, time.Time, time.Duration) (ownership.PublicationAuthority, error)
	Close() error
}

type productionPhaseTwoSlotCatalog interface {
	ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error)
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

type productionPhaseTwoProgressReader interface {
	LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error)
}

type productionPhaseTwoOwnershipDependencies struct {
	Store            productionPhaseTwoOwnershipStore
	WorkerID         string
	Catalog          productionPhaseTwoSlotCatalog
	Progress         productionPhaseTwoProgressReader
	Executor         scheduler.Executor
	Now              func() time.Time
	Reconcile        *scheduler.Reconciler
	ControlLeaderTTL time.Duration
	Observer         observability.Observer
}

type productionPhaseTwoOwnership struct {
	dependencies productionPhaseTwoOwnershipDependencies
	reconciler   *scheduler.Reconciler
	flights      *scheduler.FlightCoordinator

	mu        sync.Mutex
	authority ownership.PublicationAuthority
}

func newProductionPhaseTwoOwnership(
	dependencies productionPhaseTwoOwnershipDependencies,
) (*productionPhaseTwoOwnership, error) {
	if dependencies.Store == nil || dependencies.WorkerID == "" || dependencies.Catalog == nil ||
		dependencies.Progress == nil || dependencies.Executor == nil || dependencies.Now == nil ||
		dependencies.ControlLeaderTTL <= 0 || dependencies.Observer == nil {
		return nil, errors.New("phase-two production ownership dependencies are incomplete")
	}
	reconciler := dependencies.Reconcile
	if reconciler == nil {
		var err error
		reconciler, err = scheduler.NewReconciler(scheduler.NewRouter(nil), dependencies.Store)
		if err != nil {
			return nil, err
		}
	}
	return &productionPhaseTwoOwnership{
		dependencies: dependencies, reconciler: reconciler, flights: scheduler.NewFlightCoordinator(),
	}, nil
}

func (runtime *productionPhaseTwoOwnership) RegisterWorker(
	ctx context.Context,
	registration ownership.WorkerRegistration,
) error {
	if runtime == nil {
		return errors.New("phase-two production ownership is not initialized")
	}
	if registration.WorkerID != runtime.dependencies.WorkerID {
		return errors.New("phase-two worker registration identity mismatch")
	}
	return runtime.dependencies.Store.RegisterWorker(ctx, registration)
}

func (runtime *productionPhaseTwoOwnership) AcquireControlLeader(
	ctx context.Context,
	at time.Time,
	ttl time.Duration,
) error {
	if runtime == nil || at.IsZero() || ttl != runtime.dependencies.ControlLeaderTTL {
		return errors.New("phase-two production Control Leader acquisition is invalid")
	}
	_, err := runtime.ensureControlAuthority(ctx, at)
	return err
}

func (runtime *productionPhaseTwoOwnership) Reconcile(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
	at time.Time,
) ([]execution.QueryGroupIdentity, error) {
	if runtime == nil || at.IsZero() {
		return nil, errors.New("phase-two production Assignment reconcile is invalid")
	}
	authority, err := runtime.ensureControlAuthority(ctx, at)
	if err != nil {
		return nil, err
	}
	ordered := append([]execution.QueryGroupIdentity(nil), queryGroups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	assigned := make([]execution.QueryGroupIdentity, 0, len(ordered))
	for index, queryGroup := range ordered {
		if queryGroup == "" || (index > 0 && ordered[index-1] == queryGroup) {
			return nil, errors.New("phase-two production reconcile contains an invalid Query Group set")
		}
		record, err := runtime.reconciler.Reconcile(ctx, authority, queryGroup, at)
		if err != nil {
			return nil, err
		}
		if record.DesiredWorkerID == runtime.dependencies.WorkerID {
			assigned = append(assigned, queryGroup)
		}
	}
	return assigned, nil
}

func (runtime *productionPhaseTwoOwnership) MaintainControlLeader(
	ctx context.Context,
	interval time.Duration,
	ttl time.Duration,
) error {
	if runtime == nil || interval <= 0 || ttl <= interval {
		return errors.New("phase-two production control leader cadence is invalid")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case at := <-ticker.C:
			runtime.mu.Lock()
			authority := runtime.authority
			runtime.mu.Unlock()
			if authority.Fence.QueryGroup == "" {
				return ownership.ErrStaleFence
			}
			renewed, err := runtime.dependencies.Store.RenewControlLeader(ctx, authority, at, ttl)
			if err != nil {
				return err
			}
			observeProductionOwnership(ctx, runtime.dependencies.Observer, observability.StageLeaseRenewed, nil)
			runtime.mu.Lock()
			if runtime.authority.Fence == authority.Fence {
				runtime.authority = renewed
			}
			runtime.mu.Unlock()
		}
	}
}

func (runtime *productionPhaseTwoOwnership) OpenQueryGroup(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	at time.Time,
	ttl time.Duration,
) (phaseTwoQueryGroupRuntime, error) {
	if runtime == nil {
		return nil, errors.New("phase-two production ownership is not initialized")
	}
	session, err := ownership.OpenSession(ctx, runtime.dependencies.Store, queryGroup, runtime.dependencies.WorkerID, at, ttl)
	if err != nil {
		return nil, err
	}
	source, err := scheduler.NewProductionSlotSource(
		queryGroup, runtime.dependencies.WorkerID, runtime.dependencies.Store, session,
		runtime.dependencies.Catalog, runtime.dependencies.Progress, runtime.dependencies.Now,
	)
	if err != nil {
		_ = session.Release(ctx)
		return nil, err
	}
	runner, err := scheduler.NewRunner(
		queryGroup, session, source, runtime.dependencies.Executor, runtime.flights, runtime.dependencies.Now,
	)
	if err != nil {
		_ = session.Release(ctx)
		return nil, err
	}
	return &productionPhaseTwoQueryGroup{session: session, runner: runner, observer: runtime.dependencies.Observer}, nil
}

func (runtime *productionPhaseTwoOwnership) Close() error {
	if runtime == nil || runtime.dependencies.Store == nil {
		return nil
	}
	return runtime.dependencies.Store.Close()
}

func (runtime *productionPhaseTwoOwnership) ensureControlAuthority(
	ctx context.Context,
	at time.Time,
) (ownership.PublicationAuthority, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.authority.Fence.QueryGroup != "" && runtime.authority.Deadline.After(at) {
		return runtime.authority, nil
	}
	authority, err := runtime.dependencies.Store.AcquireControlLeader(
		ctx, runtime.dependencies.WorkerID, at, runtime.dependencies.ControlLeaderTTL,
	)
	if err != nil {
		return ownership.PublicationAuthority{}, err
	}
	runtime.authority = authority
	return authority, nil
}

type productionPhaseTwoQueryGroup struct {
	session  *ownership.Session
	runner   *scheduler.Runner
	observer observability.Observer
}

func (runtime *productionPhaseTwoQueryGroup) RunOne(
	ctx context.Context,
) (execution.SlotExecutionResult, bool, error) {
	return runtime.runner.RunOne(ctx)
}

func (runtime *productionPhaseTwoQueryGroup) MaintainLease(
	ctx context.Context,
	interval time.Duration,
	ttl time.Duration,
) error {
	if runtime == nil || runtime.session == nil || interval <= 0 || ttl <= interval {
		return errors.New("phase-two production lease cadence is invalid")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case at := <-ticker.C:
			err := runtime.session.Renew(ctx, at, ttl)
			observeProductionOwnership(ctx, runtime.observer, observability.StageLeaseRenewed, err)
			if err != nil {
				return err
			}
		}
	}
}

func (runtime *productionPhaseTwoQueryGroup) Release(ctx context.Context) error {
	return runtime.session.Release(ctx)
}

var _ phaseTwoOwnershipRuntime = (*productionPhaseTwoOwnership)(nil)
var _ phaseTwoQueryGroupRuntime = (*productionPhaseTwoQueryGroup)(nil)

func observeProductionOwnership(
	ctx context.Context,
	observer observability.Observer,
	stage observability.Stage,
	err error,
) {
	result := observability.Result(observability.ResultSuccess)
	reason := observability.ReasonCode(observability.ReasonNone)
	if err != nil {
		result = observability.ResultFailed
		reason = observability.ReasonInternalUnknown
	}
	observeRuntime(ctx, observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: stage, Result: result,
		Direction: observability.DirectionInternal, ReasonCode: reason, Err: err,
	})
}
