// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
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
	LoadQueryGroup(context.Context, execution.SnapshotRevision, execution.QueryGroupIdentity) (controlplane.QueryGroup, error)
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
	group, err := source.repository.LoadQueryGroup(ctx, contractRef.SnapshotRevision, contractRef.Slot.QueryGroup)
	if err != nil {
		if errors.Is(err, controlplane.ErrCatalogObjectUnavailable) {
			return access.FrozenPlan{}, errors.New("phase-two frozen Query Group is absent from Snapshot")
		}
		return access.FrozenPlan{}, err
	}
	if group.QueryPlan.QueryRevision != contractRef.QueryRevision {
		return access.FrozenPlan{}, errors.New("phase-two frozen Query Group changed query revision")
	}
	queryFacts := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	queryRef := execution.LogicalQueryRef(group.QueryPlan.QueryRevision)
	queryFacts[queryRef] = group.QueryPlan
	duePlans := make(map[execution.PlanIdentity]struct{}, len(fact.DuePlans))
	for _, due := range fact.DuePlans {
		duePlans[due.Identity] = struct{}{}
	}
	for _, plan := range group.Plans {
		if _, due := duePlans[plan.Identity]; !due {
			continue
		}
		for ref, facts := range plan.QueryPlans {
			queryFacts[ref] = facts
		}
	}
	resolvedFacts := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	for _, requirement := range fact.Requirements {
		facts, exists := queryFacts[requirement.LogicalQueryRef]
		if !exists || facts.Validate() != nil ||
			execution.LogicalQueryRef(facts.QueryRevision) != requirement.LogicalQueryRef {
			return access.FrozenPlan{}, fmt.Errorf("%w: logical query %s", access.ErrFrozenQueryPlanUnavailable, requirement.LogicalQueryRef)
		}
		resolvedFacts[requirement.LogicalQueryRef] = facts
	}
	return access.FrozenPlan{
		DuePlans:     append([]execution.DuePlan(nil), fact.DuePlans...),
		Requirements: append([]execution.DataRequirement(nil), fact.Requirements...),
		QueryFacts:   resolvedFacts,
	}, nil
}

func (source *productionFrozenExecution) ResolveFinalization(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.QueryFreeFinalization, error) {
	if err := request.Validate(); err != nil {
		return execution.QueryFreeFinalization{}, err
	}
	if request.ReplayExpired && source.now().UnixMilli() < request.RecoveryUntilUnixMilli {
		return execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: execution.FinalizationGapSkipped,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			Targets:    request.DuePlanTargets.Clone(),
		}, nil
	}
	// The Coordinator has already persisted this validated request as the unfinished
	// projection. After recovery_until, that exact set no longer depends on Snapshot availability.
	if source.now().UnixMilli() >= request.RecoveryUntilUnixMilli {
		return execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: execution.FinalizationSnapshotUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
			Targets:    request.DuePlanTargets.Clone(),
		}, nil
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
	LoadActiveQueryGroupSet(context.Context, controlplane.ActiveQueryGroupSetRef) ([]execution.QueryGroupIdentity, error)
	RenewCurrentActivationObjects(context.Context) error
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
	dependencies  productionPhaseTwoControlDependencies
	renewMu       sync.Mutex
	renewDegraded bool
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
) (refreshResult phaseTwoControlRefreshResult, pending bool, refreshErr error) {
	renewed := false
	defer func() {
		if renewed {
			return
		}
		// Renewal is guarded by the Activation CAS and must not replace the
		// Source/activation result or stop pending confirmation from converging.
		runtime.observeCurrentObjectRenewal(ctx, runtime.dependencies.Repository.RenewCurrentActivationObjects(ctx))
	}()
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
	if !knownSourceRefreshStatus(result.Status) {
		return phaseTwoControlRefreshResult{}, false, errors.New("phase-two source refresh returned an invalid status")
	}
	sourceRefresh := sourceRefreshIdentity(result, result.Publication)
	defer func() {
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultSuccess, SourceRefresh: sourceRefresh,
		})
	}()
	if result.Status == controlplane.SourceRefreshPendingConfirmation {
		state, err := runtime.dependencies.Repository.LoadActivation(ctx)
		if errors.Is(err, controlplane.ErrActivationUnavailable) {
			return phaseTwoControlRefreshResult{}, true, nil
		}
		if err != nil {
			return phaseTwoControlRefreshResult{}, false, err
		}
		queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
		if err != nil {
			return phaseTwoControlRefreshResult{}, false, err
		}
		sourceRefresh.SnapshotRevision = string(state.Current.SnapshotRevision)
		sourceRefresh.PublicationEpoch = state.Current.PublicationEpoch
		currentCount := sourceRefreshCurrentCount(state, queryGroups)
		runtime.enrichSourceRefreshCounts(ctx, sourceRefresh, state, nil, state, currentCount)
		renewErr := runtime.dependencies.Repository.RenewCurrentActivationObjects(ctx)
		renewed = true
		runtime.observeCurrentObjectRenewal(ctx, renewErr)
		if renewErr != nil {
			return phaseTwoControlRefreshResult{
				QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood,
				SourceKind: observability.SourceKindCompiledSnapshot,
				ReasonCode: observability.ReasonCode(contract.ReasonRedisUnavailable), Cause: renewErr,
			}, false, nil
		}
		return phaseTwoControlRefreshResult{
			QueryGroups: queryGroups, Status: phaseTwoControlHealthy, SourceRefreshObserved: true,
		}, false, nil
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
	previous := controlplane.ActivationState{}
	previousErr := controlplane.ErrActivationUnavailable
	if result.Status != controlplane.SourceRefreshUnchanged {
		previous, previousErr = runtime.dependencies.Repository.LoadActivation(ctx)
	}
	state, err := runtime.dependencies.Activator.Ensure(ctx, result.Publication)
	if err != nil {
		if failure, ok := controlplane.ActivationFailureFromError(err); ok {
			var samples []string
			samplesTruncated := false
			if failure.Class == controlplane.ActivationFailureClassNotDrained {
				samples = make([]string, len(failure.ReappearedQueryGroupSamples))
				for index, identity := range failure.ReappearedQueryGroupSamples {
					samples[index] = string(identity)
				}
				samplesTruncated = failure.ReappearedQueryGroupSamplesTruncated
			}
			observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
				Component:  observability.ComponentControlPlane,
				Stage:      observability.StageActivationFailed,
				Result:     observability.ResultDegraded,
				Operation:  observability.OperationTransition,
				Direction:  observability.DirectionInternal,
				ReasonCode: observability.ReasonContractRetryable,
				ActivationFailure: &observability.ActivationFailureFacts{
					Stage:                                observability.ActivationFailureStage(failure.Stage),
					Class:                                observability.ActivationFailureClass(failure.Class),
					DrainingQueryGroups:                  failure.DrainingQueryGroups,
					CandidateQueryGroups:                 failure.CandidateQueryGroups,
					ReappearedQueryGroups:                failure.ReappearedQueryGroups,
					ReappearedQueryGroupSamples:          samples,
					ReappearedQueryGroupSamplesTruncated: samplesTruncated,
				},
				Err: err,
			})
		}
		fallback, fallbackErr := runtime.keepLastGood(ctx, observability.SourceKindCompiledSnapshot, err)
		return fallback, false, fallbackErr
	}
	if result.Status == controlplane.SourceRefreshUnchanged {
		previous, previousErr = state, nil
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	if err != nil {
		return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, false, err
	}
	currentCount := sourceRefreshCurrentCount(state, queryGroups)
	runtime.enrichSourceRefreshCounts(ctx, sourceRefresh, previous, previousErr, state, currentCount)
	return phaseTwoControlRefreshResult{
		QueryGroups: queryGroups, Status: phaseTwoControlHealthy, SourceRefreshObserved: true,
	}, false, nil
}

func (runtime *productionPhaseTwoControl) enrichSourceRefreshCounts(
	ctx context.Context,
	facts *observability.SourceRefreshFacts,
	previous controlplane.ActivationState,
	previousErr error,
	current controlplane.ActivationState,
	currentCount *int,
) {
	sameSet := previousErr == nil && previous.Current == current.Current &&
		previous.ActiveQGSetRef == current.ActiveQGSetRef
	if sameSet {
		if currentCount != nil {
			facts.CountsKnown = true
			facts.OldQueryGroups = *currentCount
			facts.NewQueryGroups = *currentCount
		}
		return
	}
	previousMissing := errors.Is(previousErr, controlplane.ErrActivationUnavailable)
	if previousMissing {
		if currentCount != nil {
			facts.CountsKnown = true
			facts.NewQueryGroups = *currentCount
			facts.AddedQueryGroups = *currentCount
		}
		return
	}
	if previousErr != nil && !previousMissing {
		return
	}

	// Exact set reads are diagnostic only and are limited to legacy references
	// or a real publication transition where added/retired overlap is unknown.
	currentGroups, err := runtime.loadCurrentActiveQueryGroups(ctx, current)
	if err != nil {
		return
	}
	previousGroups := []execution.QueryGroupIdentity{}
	if sameSet {
		previousGroups = append(previousGroups, currentGroups...)
	} else if !previousMissing {
		previousGroups, err = runtime.loadCurrentActiveQueryGroups(ctx, previous)
		if err != nil {
			return
		}
	}
	facts.CountsKnown = true
	facts.OldQueryGroups = len(previousGroups)
	facts.NewQueryGroups = len(currentGroups)
	previousSet := make(map[execution.QueryGroupIdentity]struct{}, len(previousGroups))
	for _, queryGroup := range previousGroups {
		previousSet[queryGroup] = struct{}{}
	}
	currentSet := make(map[execution.QueryGroupIdentity]struct{}, len(currentGroups))
	for _, queryGroup := range currentGroups {
		currentSet[queryGroup] = struct{}{}
		if _, existed := previousSet[queryGroup]; !existed {
			facts.AddedQueryGroups++
		}
	}
	for _, queryGroup := range previousGroups {
		if _, exists := currentSet[queryGroup]; !exists {
			facts.RetiredQueryGroups++
		}
	}
}

func knownSourceRefreshStatus(status controlplane.SourceRefreshStatus) bool {
	switch status {
	case controlplane.SourceRefreshPendingConfirmation, controlplane.SourceRefreshPublished,
		controlplane.SourceRefreshUnchanged, controlplane.SourceRefreshPublicationConflict:
		return true
	default:
		return false
	}
}

func sourceRefreshIdentity(
	result controlplane.SourceRefreshResult,
	publication controlplane.SnapshotPublicationRef,
) *observability.SourceRefreshFacts {
	return &observability.SourceRefreshFacts{
		Status: observability.SourceRefreshStatus(result.Status), ObservationID: result.Observation,
		SnapshotRevision: string(publication.SnapshotRevision), PublicationEpoch: publication.PublicationEpoch,
	}
}

func sourceRefreshCurrentCount(
	state controlplane.ActivationState,
	queryGroups []execution.QueryGroupIdentity,
) *int {
	reference := state.ActiveQGSetRef
	if reference != (controlplane.ActiveQueryGroupSetRef{}) {
		if reference.SchemaVersion != "alarmd-active-qg-set-v1" || len(reference.Digest) != 64 {
			return nil
		}
		if _, err := hex.DecodeString(reference.Digest); err != nil {
			return nil
		}
		count := int(reference.QGCount)
		if count < 0 || uint64(count) != reference.QGCount {
			return nil
		}
		return &count
	}
	if len(state.Draining) == 0 && queryGroups != nil {
		count := len(queryGroups)
		return &count
	}
	return nil
}

func (runtime *productionPhaseTwoControl) observeCurrentObjectRenewal(ctx context.Context, err error) {
	if errors.Is(err, controlplane.ErrActivationConflict) || errors.Is(err, controlplane.ErrActivationUnavailable) {
		return
	}
	runtime.renewMu.Lock()
	degraded := runtime.renewDegraded
	if err != nil {
		runtime.renewDegraded = true
	} else {
		runtime.renewDegraded = false
	}
	runtime.renewMu.Unlock()
	if err != nil && !degraded {
		runtime.dependencies.Observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
			Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(contract.ReasonRedisUnavailable),
			SourceKind: observability.SourceKindCompiledSnapshot, Err: err,
		})
	} else if err == nil && degraded {
		runtime.dependencies.Observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
			Result: observability.Result(observability.ResultRecovered), ReasonCode: observability.ReasonCode(contract.ReasonRedisUnavailable),
			SourceKind: observability.SourceKindCompiledSnapshot,
		})
	}
}

func (runtime *productionPhaseTwoControl) keepLastGood(
	ctx context.Context,
	sourceKind observability.SourceKind,
	cause error,
) (phaseTwoControlRefreshResult, error) {
	reason := observability.ReasonContractRetryable
	if errors.Is(cause, controlplane.ErrPublicationOccurrenceCollision) {
		reason = observability.ReasonContractDeterministic
	}
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
			return phaseTwoControlRefreshResult{
				Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
				ReasonCode: reason, Cause: cause,
			}, nil
		}
		return phaseTwoControlRefreshResult{}, errors.Join(cause, err)
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
		ReasonCode: reason, Cause: cause,
	}, nil
}

func (runtime *productionPhaseTwoControl) loadActiveQueryGroups(
	ctx context.Context,
	state controlplane.ActivationState,
) ([]execution.QueryGroupIdentity, error) {
	if state.RecordRevision == 0 || state.Current.SnapshotRevision == "" || state.Current.PublicationEpoch == 0 {
		return nil, errors.New("phase-two activation state is incomplete")
	}
	queryGroups, err := runtime.loadCurrentActiveQueryGroups(ctx, state)
	if err != nil {
		return nil, err
	}
	active := make(map[execution.QueryGroupIdentity]struct{}, len(queryGroups)+len(state.Draining))
	for _, queryGroup := range queryGroups {
		active[queryGroup] = struct{}{}
	}
	drainingFacts := &observability.DrainingQGFacts{Total: len(state.Draining)}
	for _, draining := range state.Draining {
		if _, current := active[draining.QueryGroup]; current {
			drainingFacts.Isolated++
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
				errors.New("phase-two Query Group cannot be current and draining"))
			continue
		}
		retiredAt, retired, err := runtime.dependencies.Schedules.ReadScheduleRetirement(ctx, draining.QueryGroup)
		if err != nil {
			if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
				drainingFacts.Isolated++
				continue
			}
			return nil, err
		}
		if !retired || retiredAt != draining.RetiredBoundary {
			drainingFacts.Isolated++
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
				errors.New("phase-two draining projection differs from persisted Schedule retirement"))
			continue
		}
		identity := execution.ProgressIdentity{QueryGroup: draining.QueryGroup}
		load, err := runtime.dependencies.Progress.LoadProgress(ctx, identity)
		if err != nil {
			if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
				drainingFacts.Isolated++
				continue
			}
			return nil, err
		}
		if err := load.Validate(identity); err != nil {
			drainingFacts.Isolated++
			runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup, err)
			continue
		}
		drained := load.Status == execution.ProgressFound && load.Progress.NextSlot >= draining.RetiredBoundary
		if load.Status == execution.ProgressMissing {
			initial, err := runtime.dependencies.Schedules.ReadInitialFrozenSchedule(ctx, draining.QueryGroup)
			if err != nil {
				if runtime.isLocalDrainingError(ctx, draining.QueryGroup, err) {
					drainingFacts.Isolated++
					continue
				}
				return nil, err
			}
			isolated := false
			for {
				if err := initial.Validate(); err != nil || initial.Segment.QueryGroup != draining.QueryGroup {
					drainingFacts.Isolated++
					runtime.isolateDrainingQueryGroup(ctx, draining.QueryGroup,
						errors.New("phase-two draining Schedule is invalid"))
					isolated = true
					break
				}
				if _, hasSlot := initial.FirstSlot(); hasSlot {
					break
				}
				if initial.Segment.End == nil || *initial.Segment.End > draining.RetiredBoundary {
					drainingFacts.Isolated++
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
						drainingFacts.Isolated++
						isolated = true
						break
					}
					return nil, err
				}
				if next.Segment.Start < *initial.Segment.End {
					drainingFacts.Isolated++
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
			drainingFacts.Undrained++
			if len(drainingFacts.Samples) < observability.MaxDrainingQGLogSamples {
				nextSlot := int64(0)
				if load.Progress != nil {
					nextSlot = int64(load.Progress.NextSlot)
				}
				drainingFacts.Samples = append(drainingFacts.Samples, observability.DrainingQGSample{
					QueryGroupKey: string(draining.QueryGroup), RetiredBoundary: int64(draining.RetiredBoundary),
					NextSlot: nextSlot, ProgressStatus: string(load.Status),
				})
			} else {
				drainingFacts.Truncated = true
			}
		}
	}
	runtime.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageDrainingQGReconciled,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad,
		DrainingQG: drainingFacts,
	})
	queryGroups = queryGroups[:0]
	for queryGroup := range active {
		queryGroups = append(queryGroups, queryGroup)
	}
	sort.Slice(queryGroups, func(left, right int) bool { return queryGroups[left] < queryGroups[right] })
	return queryGroups, nil
}

func (runtime *productionPhaseTwoControl) loadCurrentActiveQueryGroups(
	ctx context.Context,
	state controlplane.ActivationState,
) ([]execution.QueryGroupIdentity, error) {
	var queryGroups []execution.QueryGroupIdentity
	var err error
	if state.ActiveQGSetRef.Digest != "" {
		queryGroups, err = runtime.dependencies.Repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	} else {
		// Followers never migrate legacy Activation state. They may read its
		// current immutable Snapshot while the Control Leader performs the
		// one-time v1-to-v2 upgrade.
		var snapshot controlplane.PublishedSnapshot
		snapshot, err = runtime.dependencies.Repository.LoadPublishedSnapshot(ctx, state.Current)
		if err == nil {
			queryGroups = make([]execution.QueryGroupIdentity, len(snapshot.QueryGroups))
			for index, queryGroup := range snapshot.QueryGroups {
				if queryGroup.Identity == "" {
					return nil, errors.New("phase-two active Snapshot contains an empty Query Group")
				}
				queryGroups[index] = queryGroup.Identity
			}
		}
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(queryGroups, func(left, right int) bool { return queryGroups[left] < queryGroups[right] })
	for index := 1; index < len(queryGroups); index++ {
		if queryGroups[index-1] == queryGroups[index] {
			return nil, errors.New("phase-two active Snapshot contains duplicate Query Groups")
		}
	}
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
	defer startSlotTiming(ctx, source.observer, observability.StageSlotSourceCompleted, time.Now)()
	slot, due, err := source.next.Next(ctx, queryGroup)
	var retry *scheduler.SourceRetryError
	var blocked *scheduler.SourceBlockedError
	if errors.As(err, &retry) || errors.As(err, &blocked) {
		reason := observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable)
		var cause error
		if retry != nil {
			reason = observability.ReasonCode(contract.ReasonProviderUnavailable)
			cause = retry.Err
		} else {
			cause = blocked.Err
		}
		observeRuntime(ctx, source.observer, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
			Result: observability.Result(observability.ResultRetrying), ReasonCode: reason, Direction: observability.DirectionInternal,
			Trace: observability.TraceFields{QueryGroupKey: string(queryGroup)}, Err: cause,
		})
	}
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
	var shortCompletion *observability.ShortPeriodCompletionFacts
	if err == nil && result.Completed && result.CompletionKind != "" && (request.ShortPeriodCohort == "10s" || request.ShortPeriodCohort == "15s") {
		shortCompletion = &observability.ShortPeriodCompletionFacts{Cohort: request.ShortPeriodCohort, CompletionKind: string(result.CompletionKind), LagSeconds: time.Since(time.Unix(int64(request.Contract.Slot.EvaluationTime), 0)).Seconds()}
	}
	observedResult := result.Result
	reason := result.ReasonCode
	observedErr := err
	if err != nil {
		if _, deferred := access.ReadinessDeferredAt(err); deferred {
			observedResult = observability.ResultRetrying
			reason = observability.ReasonNone
			observedErr = nil
		} else {
			observedResult = observability.ResultFailed
			reason = observability.ReasonInternalUnknown
			if errors.Is(err, access.ErrFrozenQueryPlanUnavailable) {
				reason = observability.ReasonContractDeterministic
			}
		}
	} else if observedResult == "" {
		observedResult = observability.ResultSuccess
	}
	observeRuntime(ctx, executor.observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Operation: observability.Operation(request.Operation), ShortPeriodCompletion: shortCompletion,
		ExecuteOutcome: executeReturnOutcome(result, err),
		Result:         observedResult, ReasonCode: reason, Direction: observability.DirectionInternal,
		Duration: time.Since(started), Trace: trace, Err: observedErr,
	})
	observability.EmitTargetFlow(ctx, "execution_outcome", trace, observability.TargetFlowFacts{ExecutionOutcomeKnown: true, Attempted: true, Completed: result.Completed, Completion: string(result.CompletionKind)})
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
	ctx, releaseSnapshot := controlplane.WithSnapshotReadScope(ctx)
	defer releaseSnapshot()
	return runtime.runner.RunOne(ctx)
}

func (runtime *productionPhaseTwoQueryGroup) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	ctx, releaseSnapshot := controlplane.WithSnapshotReadScope(ctx)
	defer releaseSnapshot()
	return runtime.runner.RunOneAdmitted(ctx, admission)
}

func (runtime *productionPhaseTwoQueryGroup) NextReadyAt() time.Time {
	if runtime == nil || runtime.runner == nil {
		return time.Time{}
	}
	return runtime.runner.NextReadyAt()
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

func executeReturnOutcome(result execution.SlotExecutionResult, err error) string {
	if err != nil {
		if _, ok := access.ReadinessDeferredAt(err); ok {
			return "readiness_deferred"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "cancelled"
		}
		return "error"
	}
	if result.Completed {
		return "completed"
	}
	if result.Result == observability.ResultRetrying {
		return "retrying"
	}
	return "incomplete"
}
