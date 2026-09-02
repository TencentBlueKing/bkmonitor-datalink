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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
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
	now        func() time.Time
}

func newProductionFrozenExecution(
	catalog productionFrozenCatalog,
	repository productionSnapshotReader,
	now func() time.Time,
) (*productionFrozenExecution, error) {
	if catalog == nil || repository == nil || now == nil {
		return nil, errors.New("phase-two frozen execution dependencies are incomplete")
	}
	return &productionFrozenExecution{catalog: catalog, repository: repository, now: now}, nil
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
	fact, err := source.resolveFrozenFact(ctx, request.Contract)
	if err != nil {
		if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			if source.now().UnixMilli() < request.RecoveryUntilUnixMilli {
				return execution.QueryFreeFinalization{
					Contract: request.Contract, Mode: execution.FinalizationSnapshotRetry,
					ReasonCode: execution.ReasonCode(contract.ReasonProviderUnavailable),
				}, nil
			}
			return execution.QueryFreeFinalization{
				Contract: request.Contract, Mode: execution.FinalizationSnapshotUnavailable,
				ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
				Targets:    request.DuePlanTargets.Clone(),
			}, nil
		}
		var corrupt *controlplane.PersistedSnapshotCorruptError
		if errors.As(err, &corrupt) {
			return execution.QueryFreeFinalization{
				Contract: request.Contract, Mode: execution.FinalizationSnapshotUnavailable,
				ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
				Targets:    request.DuePlanTargets.Clone(),
			}, nil
		}
		if source.now().UnixMilli() >= request.RecoveryUntilUnixMilli {
			return execution.QueryFreeFinalization{
				Contract: request.Contract, Mode: execution.FinalizationSnapshotUnavailable,
				ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
				Targets:    request.DuePlanTargets.Clone(),
			}, nil
		}
		return execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: execution.FinalizationSnapshotRetry,
			ReasonCode: execution.ReasonCode(contract.ReasonProviderUnavailable),
		}, nil
	}
	targets, deadline, err := frozenExecutionFacts(fact)
	if err != nil {
		return execution.QueryFreeFinalization{}, err
	}
	if !targets.Equal(request.DuePlanTargets) || deadline != request.EarliestQueryDeadlineUnixMilli {
		return execution.QueryFreeFinalization{}, errors.New("phase-two re-frozen execution facts differ from the request")
	}
	if source.now().UnixMilli() >= request.RecoveryUntilUnixMilli {
		return execution.QueryFreeFinalization{
			Contract:   request.Contract,
			Mode:       execution.FinalizationSnapshotUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
			Targets:    request.DuePlanTargets.Clone(),
		}, nil
	}
	if request.Operation == execution.OperationNormal {
		return execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: execution.FinalizationQueryRequired,
		}, nil
	}
	return execution.QueryFreeFinalization{
		Contract: request.Contract, Mode: execution.FinalizationQueryRequired,
	}, nil
}

func frozenExecutionFacts(
	fact execution.FrozenSlotContractFact,
) (execution.FrozenDuePlanTargets, int64, error) {
	deadline := int64(0)
	for _, requirement := range fact.Requirements {
		for _, consumer := range requirement.Consumers {
			if consumer.ConsumerDeadlineUnixMilli <= 0 ||
				consumer.DownstreamExecutionReserveMilliSec <= 0 ||
				consumer.DownstreamExecutionReserveMilliSec >= consumer.ConsumerDeadlineUnixMilli {
				return execution.FrozenDuePlanTargets{}, 0, errors.New("phase-two frozen contract query deadline or reserve is invalid")
			}
			candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
			if deadline == 0 || candidate < deadline {
				deadline = candidate
			}
		}
	}
	if deadline == 0 {
		return execution.FrozenDuePlanTargets{}, 0, errors.New("phase-two frozen contract query deadline has no consumer")
	}
	targets := execution.FrozenDuePlanTargets{
		DuePlanSetDigest: fact.Contract.DuePlanSetDigest,
		Plans:            make([]execution.PlanIdentity, len(fact.DuePlans)),
	}
	for index := range fact.DuePlans {
		targets.Plans[index] = fact.DuePlans[index].Identity
	}
	if err := targets.Validate(fact.Contract); err != nil {
		return execution.FrozenDuePlanTargets{}, 0, err
	}
	return targets, deadline, nil
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

var _ access.FrozenPlanSource = (*productionFrozenExecution)(nil)
var _ execution.QueryFreeFinalizationSource = (*productionFrozenExecution)(nil)

type productionQueryPermitAcquirer struct {
	flights *scheduler.FlightCoordinator
}

func (acquirer productionQueryPermitAcquirer) AcquireQueryPermit(
	ctx context.Context,
	slot execution.SlotIdentity,
	operation execution.Operation,
	deadline time.Time,
) (access.QueryPermit, error) {
	if acquirer.flights == nil {
		return nil, errors.New("phase-two production query permits are not initialized")
	}
	return acquirer.flights.AcquireQueryPermit(ctx, slot, operation, deadline)
}

var _ access.QueryPermitAcquirer = productionQueryPermitAcquirer{}

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
	LoadPublishedSnapshot(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedSnapshot, error)
}

type productionScheduleProjection interface {
	ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error)
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error)
}

type productionPhaseTwoControlDependencies struct {
	Source          controlplane.StrategySource
	Planner         controlplane.PrimaryQueryCompiler
	Reconciler      productionSourceReconciler
	Activator       productionInitialScheduleActivator
	Repository      productionCatalogRepository
	Schedules       productionScheduleProjection
	Progress        productionPhaseTwoProgressReader
	Observer        observability.Observer
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
		dependencies.Activator == nil || dependencies.Repository == nil || dependencies.Schedules == nil ||
		dependencies.Progress == nil || dependencies.RefreshInterval <= 0 ||
		dependencies.Wait == nil {
		return nil, errors.New("phase-two production Control dependencies are incomplete")
	}
	if dependencies.Observer == nil {
		dependencies.Observer = observability.NopObserver{}
	}
	return &productionPhaseTwoControl{dependencies: dependencies}, nil
}

func (runtime *productionPhaseTwoControl) InitialRefresh(
	ctx context.Context,
) (phaseTwoControlRefreshResult, error) {
	if runtime == nil {
		return phaseTwoControlRefreshResult{}, errors.New("phase-two production Control is not initialized")
	}
	for {
		result, pending, err := runtime.refresh(ctx)
		if err != nil || !pending {
			return result, err
		}
		if err := runtime.dependencies.Wait(ctx, runtime.dependencies.RefreshInterval); err != nil {
			return phaseTwoControlRefreshResult{}, err
		}
	}
}

func (runtime *productionPhaseTwoControl) Refresh(
	ctx context.Context,
) (phaseTwoControlRefreshResult, error) {
	if runtime == nil {
		return phaseTwoControlRefreshResult{}, errors.New("phase-two production Control is not initialized")
	}
	result, _, err := runtime.refresh(ctx)
	return result, err
}

func (runtime *productionPhaseTwoControl) LoadActive(
	ctx context.Context,
) (phaseTwoControlRefreshResult, error) {
	if runtime == nil {
		return phaseTwoControlRefreshResult{}, errors.New("phase-two production Control is not initialized")
	}
	state, err := runtime.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		return phaseTwoControlRefreshResult{}, err
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, err
}

func (runtime *productionPhaseTwoControl) Close() error {
	if runtime == nil || runtime.dependencies.Close == nil {
		return nil
	}
	return runtime.dependencies.Close()
}

func (runtime *productionPhaseTwoControl) refresh(
	ctx context.Context,
) (phaseTwoControlRefreshResult, bool, error) {
	result, err := runtime.dependencies.Reconciler.Refresh(
		ctx, runtime.dependencies.Source, runtime.dependencies.Planner,
	)
	if err != nil {
		sourceKind := observability.SourceKindLegacyStrategy
		if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			sourceKind = observability.SourceKindCompiledSnapshot
		}
		result, fallbackErr := runtime.keepLastGood(ctx, sourceKind, err)
		return result, false, fallbackErr
	}
	if result.Status == controlplane.SourceRefreshPendingConfirmation {
		state, err := runtime.dependencies.Repository.LoadActivation(ctx)
		if errors.Is(err, controlplane.ErrActivationUnavailable) {
			return phaseTwoControlRefreshResult{}, true, nil
		}
		if err != nil {
			return phaseTwoControlRefreshResult{}, false, err
		}
		queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
		return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, false, err
	}
	if result.Status != controlplane.SourceRefreshPublished && result.Status != controlplane.SourceRefreshUnchanged &&
		result.Status != controlplane.SourceRefreshPublicationConflict {
		return phaseTwoControlRefreshResult{}, false, errors.New("phase-two source refresh returned an invalid status")
	}
	if result.Publication.SnapshotRevision == "" || result.Publication.PublicationEpoch == 0 {
		return phaseTwoControlRefreshResult{}, false, errors.New("phase-two source refresh returned an incomplete publication")
	}
	if result.Status == controlplane.SourceRefreshPublicationConflict {
		runtime.dependencies.Observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultDegraded, ReasonCode: observability.ReasonContractRetryable,
		})
	}
	state, err := runtime.dependencies.Activator.Ensure(ctx, result.Publication)
	if err != nil {
		fallback, fallbackErr := runtime.keepLastGood(ctx, observability.SourceKindCompiledSnapshot, err)
		return fallback, false, fallbackErr
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, false, err
}

func (runtime *productionPhaseTwoControl) keepLastGood(
	ctx context.Context,
	sourceKind observability.SourceKind,
	cause error,
) (phaseTwoControlRefreshResult, error) {
	state, err := runtime.dependencies.Repository.LoadActivation(ctx)
	if errors.Is(err, controlplane.ErrActivationUnavailable) {
		return phaseTwoControlRefreshResult{}, cause
	}
	if err != nil {
		return phaseTwoControlRefreshResult{}, errors.Join(cause, err)
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	if err != nil {
		if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			reason := observability.ReasonContractRetryable
			if errors.Is(cause, controlplane.ErrPublicationOccurrenceCollision) {
				reason = observability.ReasonContractDeterministic
			}
			return phaseTwoControlRefreshResult{
				Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
				ReasonCode: reason, Cause: cause,
			}, nil
		}
		return phaseTwoControlRefreshResult{}, errors.Join(cause, err)
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
		ReasonCode: observability.ReasonContractRetryable, Cause: cause,
	}, nil
}

func (runtime *productionPhaseTwoControl) loadActiveQueryGroups(
	ctx context.Context,
	state controlplane.ActivationState,
) ([]execution.QueryGroupIdentity, error) {
	if state.RecordRevision == 0 || state.Current.SnapshotRevision == "" || state.Current.PublicationEpoch == 0 {
		return nil, errors.New("phase-two activation state is incomplete")
	}
	snapshot, err := runtime.dependencies.Repository.LoadPublishedSnapshot(ctx, state.Current)
	if err != nil {
		return nil, err
	}
	queryGroups := make([]execution.QueryGroupIdentity, len(snapshot.QueryGroups))
	for index, queryGroup := range snapshot.QueryGroups {
		if queryGroup.Identity == "" {
			return nil, errors.New("phase-two active Snapshot contains an empty Query Group")
		}
		queryGroups[index] = queryGroup.Identity
	}
	sort.Slice(queryGroups, func(left, right int) bool { return queryGroups[left] < queryGroups[right] })
	for index := 1; index < len(queryGroups); index++ {
		if queryGroups[index-1] == queryGroups[index] {
			return nil, errors.New("phase-two active Snapshot contains duplicate Query Groups")
		}
	}
	active := make(map[execution.QueryGroupIdentity]struct{}, len(queryGroups)+len(state.Draining))
	for _, queryGroup := range queryGroups {
		active[queryGroup] = struct{}{}
	}
	for _, draining := range state.Draining {
		if _, current := active[draining.QueryGroup]; current {
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
				errors.New("phase-two Query Group cannot be current and draining"))
			continue
		}
		retiredAt, retired, err := runtime.dependencies.Schedules.ReadScheduleRetirement(ctx, draining.QueryGroup)
		if err != nil {
			if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
				continue
			}
			return nil, err
		}
		if !retired || retiredAt != draining.RetiredBoundary {
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
				errors.New("phase-two draining projection differs from persisted Schedule retirement"))
			continue
		}
		identity := execution.ProgressIdentity{QueryGroup: draining.QueryGroup}
		load, err := runtime.dependencies.Progress.LoadProgress(ctx, identity)
		if err != nil {
			if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
				continue
			}
			return nil, err
		}
		if err := load.Validate(identity); err != nil {
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup, err)
			continue
		}
		drained := load.Status == execution.ProgressFound && load.Progress.NextSlot >= draining.RetiredBoundary
		if load.Status == execution.ProgressMissing {
			initial, err := runtime.dependencies.Schedules.ReadInitialFrozenSchedule(ctx, draining.QueryGroup)
			if err != nil {
				if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
					continue
				}
				return nil, err
			}
			isolated := false
			for {
				if err := initial.Validate(); err != nil || initial.Segment.QueryGroup != draining.QueryGroup {
					runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
						errors.New("phase-two draining Schedule is invalid"))
					isolated = true
					break
				}
				if _, hasSlot := initial.FirstSlot(); hasSlot {
					break
				}
				if initial.Segment.End == nil || *initial.Segment.End > draining.RetiredBoundary {
					runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
						errors.New("phase-two draining Schedule does not reach its retirement boundary"))
					isolated = true
					break
				}
				if *initial.Segment.End == draining.RetiredBoundary {
					drained = true
					break
				}
				next, err := runtime.dependencies.Schedules.ReadSuccessorFrozenSchedule(
					ctx, draining.QueryGroup, *initial.Segment.End,
				)
				if err != nil {
					if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
						isolated = true
						break
					}
					return nil, err
				}
				if next.Segment.Start < *initial.Segment.End {
					runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
						errors.New("phase-two draining Schedule Segments overlap"))
					isolated = true
					break
				}
				initial = next
			}
			if isolated {
				continue
			}
		}
		if !drained {
			active[draining.QueryGroup] = struct{}{}
		}
	}
	queryGroups = queryGroups[:0]
	for queryGroup := range active {
		queryGroups = append(queryGroups, queryGroup)
	}
	sort.Slice(queryGroups, func(left, right int) bool { return queryGroups[left] < queryGroups[right] })
	return queryGroups, nil
}

func (runtime *productionPhaseTwoControl) isLocalDrainingError(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	err error,
) bool {
	var invalidProgress *progress.DeterministicInvalidError
	var invalidSchedule *controlplane.DeterministicScheduleError
	if !errors.Is(err, controlplane.ErrScheduleUnavailable) && !errors.As(err, &invalidProgress) &&
		!errors.As(err, &invalidSchedule) {
		return false
	}
	runtime.isolateDrainingQueryGroup(ctx, queryGroup, err)
	return true
}

func (runtime *productionPhaseTwoControl) isolateDrainingQueryGroup(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	err error,
) {
	runtime.dependencies.Observer.Observe(ctx, observability.Observation{
		Component:  observability.ComponentControlPlane,
		Stage:      observability.StageSnapshotUnavailable,
		Result:     observability.ResultDegraded,
		Operation:  observability.OperationLoad,
		ReasonCode: observability.ReasonContractDeterministic,
		Trace:      observability.TraceFields{QueryGroupKey: string(queryGroup)},
		Err:        err,
	})
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
	ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error)
	NextSlotAfter(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.EvaluationTime, error)
	FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

type productionPhaseTwoProgressReader interface {
	LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error)
}

type productionPhaseTwoOwnershipDependencies struct {
	Store                     productionPhaseTwoOwnershipStore
	WorkerID                  string
	Catalog                   productionPhaseTwoSlotCatalog
	Progress                  productionPhaseTwoProgressReader
	Executor                  scheduler.Executor
	Now                       func() time.Time
	Reconcile                 *scheduler.Reconciler
	ControlLeaderTTL          time.Duration
	Observer                  observability.Observer
	Flights                   *scheduler.FlightCoordinator
	RecoveryLimits            scheduler.RecoveryLimits
	PostRecoveryTerminalDelay time.Duration
	QueryDeadlineReserve      time.Duration
	SnapshotRetention         time.Duration
	PublicationDelayAllowance time.Duration
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
		dependencies.ControlLeaderTTL <= 0 || dependencies.Observer == nil || dependencies.Reconcile == nil ||
		dependencies.Flights == nil || dependencies.RecoveryLimits.Validate() != nil {
		return nil, errors.New("phase-two production ownership dependencies are incomplete")
	}
	if dependencies.PostRecoveryTerminalDelay <= 0 || dependencies.QueryDeadlineReserve <= 0 ||
		dependencies.SnapshotRetention <= 0 || dependencies.PublicationDelayAllowance <= 0 {
		return nil, errors.New("phase-two post-recovery terminal delay is required")
	}
	return &productionPhaseTwoOwnership{
		dependencies: dependencies, reconciler: dependencies.Reconcile, flights: dependencies.Flights,
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

func (runtime *productionPhaseTwoOwnership) TryAcquireControlLeader(
	ctx context.Context,
	at time.Time,
	ttl time.Duration,
) (bool, error) {
	if runtime == nil || at.IsZero() || ttl != runtime.dependencies.ControlLeaderTTL {
		return false, errors.New("phase-two production Control Leader acquisition is invalid")
	}
	_, err := runtime.ensureControlAuthority(ctx, at)
	if errors.Is(err, ownership.ErrLeaseBusy) {
		return false, nil
	}
	return err == nil, err
}

func (runtime *productionPhaseTwoOwnership) PublishAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
	at time.Time,
) error {
	if runtime == nil || at.IsZero() {
		return errors.New("phase-two production Assignment reconcile is invalid")
	}
	authority, err := runtime.ensureControlAuthority(ctx, at)
	if err != nil {
		return err
	}
	ordered := append([]execution.QueryGroupIdentity(nil), queryGroups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	for index, queryGroup := range ordered {
		if queryGroup == "" || (index > 0 && ordered[index-1] == queryGroup) {
			return errors.New("phase-two production reconcile contains an invalid Query Group set")
		}
		if _, err := runtime.reconciler.Reconcile(ctx, authority, queryGroup, at); err != nil {
			if errors.Is(err, ownership.ErrStaleFence) {
				runtime.clearControlAuthority(authority)
			}
			return err
		}
	}
	return nil
}

func (runtime *productionPhaseTwoOwnership) AssignedQueryGroups(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	if runtime == nil {
		return nil, errors.New("phase-two production Assignment reader is not initialized")
	}
	ordered := append([]execution.QueryGroupIdentity(nil), queryGroups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	assigned := make([]execution.QueryGroupIdentity, 0, len(ordered))
	for index, queryGroup := range ordered {
		if queryGroup == "" || (index > 0 && ordered[index-1] == queryGroup) {
			return nil, errors.New("phase-two production Assignment read contains an invalid Query Group set")
		}
		record, err := runtime.dependencies.Store.ReadAssignment(ctx, queryGroup)
		if errors.Is(err, ownership.ErrAssignmentAbsent) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.QueryGroup != queryGroup {
			return nil, errors.New("phase-two production Assignment identity mismatch")
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
				runtime.clearControlAuthority(authority)
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

func (runtime *productionPhaseTwoOwnership) clearControlAuthority(authority ownership.PublicationAuthority) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.authority.Fence == authority.Fence {
		runtime.authority = ownership.PublicationAuthority{}
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
		scheduler.WithRecoveryLimits(runtime.dependencies.RecoveryLimits),
		scheduler.WithPostRecoveryTerminalDelay(runtime.dependencies.PostRecoveryTerminalDelay),
		scheduler.WithQueryDeadlineReserve(runtime.dependencies.QueryDeadlineReserve),
		scheduler.WithSnapshotRetention(runtime.dependencies.SnapshotRetention, runtime.dependencies.PublicationDelayAllowance),
	)
	if err != nil {
		_ = session.Release(ctx)
		return nil, err
	}
	observedSource := &observedProductionSlotSource{next: source, observer: runtime.dependencies.Observer}
	observedExecutor := &observedProductionSlotExecutor{next: runtime.dependencies.Executor, observer: runtime.dependencies.Observer}
	runner, err := scheduler.NewRunner(
		queryGroup, session, observedSource, observedExecutor, runtime.flights, runtime.dependencies.Now,
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

type observedProductionSlotSource struct {
	next     scheduler.SlotSource
	observer observability.Observer
}

func (source observedProductionSlotSource) Next(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (scheduler.FrozenSlot, bool, error) {
	slot, due, err := source.next.Next(ctx, queryGroup)
	if err == nil && due {
		observeRuntime(ctx, source.observer, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
			Result: observability.ResultSuccess, Direction: observability.DirectionInternal,
			Trace: frozenSlotTrace(slot.Contract, slot.Dispatch.OwnerFence),
		})
	}
	return slot, due, err
}

type observedProductionSlotExecutor struct {
	next     scheduler.Executor
	observer observability.Observer
}

func (executor observedProductionSlotExecutor) Execute(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	trace := frozenSlotTrace(request.Contract, request.OwnerFence)
	observeRuntime(ctx, executor.observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotStarted,
		Result: observability.ResultStarted, Direction: observability.DirectionInternal, Trace: trace,
	})
	started := time.Now()
	result, err := executor.next.Execute(ctx, request)
	observedResult := result.Result
	reason := result.ReasonCode
	if err != nil {
		observedResult = observability.ResultFailed
		reason = observability.ReasonInternalUnknown
	} else if observedResult == "" {
		observedResult = observability.ResultSuccess
	}
	observeRuntime(ctx, executor.observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Result: observedResult, ReasonCode: reason, Direction: observability.DirectionInternal,
		Duration: time.Since(started), Trace: trace, Err: err,
	})
	return result, err
}

func frozenSlotTrace(
	contractRef execution.FrozenExecutionContractRef,
	fence execution.OwnerFence,
) observability.TraceFields {
	return observability.TraceFields{
		QueryGroupKey: string(contractRef.Slot.QueryGroup), EvaluationTime: int64(contractRef.Slot.EvaluationTime),
		SnapshotRevision: string(contractRef.SnapshotRevision), QueryRevision: string(contractRef.QueryRevision),
		ScheduleRevision: string(contractRef.ScheduleRevision), ScheduleSegmentStart: int64(contractRef.ScheduleSegmentStart),
		DuePlanSetDigest: string(contractRef.DuePlanSetDigest), OwnerID: fence.OwnerID, OwnerEpoch: fence.OwnerEpoch,
	}
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
