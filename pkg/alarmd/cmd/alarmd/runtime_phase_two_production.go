// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
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
	LoadSegmentQueryGroup(context.Context, execution.ScheduleSegmentFact, execution.EvaluationTime, func(context.Context) (controlplane.QueryGroup, error)) (controlplane.QueryGroup, error)
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
	fact, segment, err := source.resolveFrozenFact(ctx, contractRef)
	if err != nil {
		return access.FrozenPlan{}, err
	}
	group, err := source.repository.LoadSegmentQueryGroup(ctx, segment, contractRef.Slot.EvaluationTime, func(ctx context.Context) (controlplane.QueryGroup, error) {
		return source.repository.LoadQueryGroup(ctx, contractRef.SnapshotRevision, contractRef.Slot.QueryGroup)
	})
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
	fact, _, err := source.resolveFrozenFact(ctx, request.Contract)
	if err != nil {
		if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			if source.now().UnixMilli() < request.RecoveryUntilUnixMilli {
				return execution.QueryFreeFinalization{
					Contract: request.Contract, Mode: execution.FinalizationSnapshotRetry,
					ReasonCode: execution.ReasonCode(contract.ReasonSnapshotRetryPending),
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
			ReasonCode: execution.ReasonCode(contract.ReasonSnapshotRetryPending),
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
		Plans:            make([]execution.PlanKey, len(fact.DuePlans)),
	}
	for index := range fact.DuePlans {
		targets.Plans[index] = fact.DuePlans[index].Key()
	}
	if err := targets.Validate(fact.Contract); err != nil {
		return execution.FrozenDuePlanTargets{}, 0, err
	}
	return targets, deadline, nil
}

// resolveFrozenFact reads the persisted Segment a frozen contract was taken
// from and returns the contract fact with the Segment itself, which names
// the content the Query Group is then read by.
func (source *productionFrozenExecution) resolveFrozenFact(
	ctx context.Context,
	contractRef execution.FrozenExecutionContractRef,
) (execution.FrozenSlotContractFact, execution.ScheduleSegmentFact, error) {
	if source == nil || source.catalog == nil || source.repository == nil {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, errors.New("phase-two frozen execution is not initialized")
	}
	if err := contractRef.Validate(); err != nil {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, err
	}
	schedule, err := source.catalog.ReadFrozenSchedule(
		ctx, contractRef.Slot.QueryGroup, contractRef.Slot.EvaluationTime,
	)
	if err != nil {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, err
	}
	segment := schedule.Segment
	if segment.QueryGroup != contractRef.Slot.QueryGroup || segment.QueryRevision != contractRef.QueryRevision ||
		segment.ScheduleRevision != contractRef.ScheduleRevision || segment.Start != contractRef.ScheduleSegmentStart ||
		segment.Publication.SnapshotRevision != contractRef.SnapshotRevision || !segment.Contains(contractRef.Slot.EvaluationTime) {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, errors.New("phase-two persisted Schedule Segment differs from frozen contract")
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: segment.QueryGroup, ScheduleRevision: segment.ScheduleRevision,
		ScheduleSegmentStart: segment.Start, EvaluationTime: contractRef.Slot.EvaluationTime,
		DuePlans: schedule.DuePlanRefs(contractRef.Slot.EvaluationTime),
	}
	fact, err := source.catalog.FreezeSlotContract(ctx, request)
	if err != nil {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, err
	}
	if fact.Contract != contractRef {
		return execution.FrozenSlotContractFact{}, execution.ScheduleSegmentFact{}, errors.New("phase-two re-frozen Slot differs from execution contract")
	}
	return fact, segment, nil
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

type productionSourceReconciler interface {
	Refresh(
		context.Context,
		controlplane.StrategySource,
		controlplane.PrimaryQueryCompiler,
	) (controlplane.SourceRefreshResult, error)
	// StepDown forgets the catalog memory a Leader answers strategy
	// lookups from. Called on every follower tick, so a process that lost
	// the lease stops answering from its old term.
	StepDown()
}

type productionInitialScheduleActivator interface {
	Ensure(
		context.Context,
		controlplane.SnapshotPublicationRef,
	) (controlplane.ActivationState, error)
}

type productionCatalogRepository interface {
	LoadActivationHead(context.Context) (controlplane.ActivationState, error)
	ControlVersionTag(context.Context) (string, bool, error)
	LoadActiveQueryGroupSet(context.Context, controlplane.ActiveQueryGroupSetRef) ([]execution.QueryGroupIdentity, error)
	RenewCurrentActivationObjects(context.Context) error
	LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error)
	// MarkSourceRefreshSuccess and LoadSourceRefreshSuccess keep the time of
	// the last refresh round that succeeded as a persisted fact, so that its
	// age survives the process that wrote it.
	MarkSourceRefreshSuccess(context.Context, time.Time) error
	LoadSourceRefreshSuccess(context.Context) (time.Time, bool, error)
}

type productionScheduleProjection interface {
	ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error)
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error)
}

type productionPhaseTwoControlDependencies struct {
	Source     controlplane.StrategySource
	Planner    controlplane.PrimaryQueryCompiler
	Reconciler productionSourceReconciler
	Activator  productionInitialScheduleActivator
	Repository productionCatalogRepository
	Schedules  productionScheduleProjection
	Progress   productionPhaseTwoProgressReader
	Observer   observability.Observer
	// Recorder counts the control-plane reads this runtime makes. It is
	// counted here, where the read returns, rather than from the result the
	// caller applies: a round that finds the activation missing and then
	// rebuilds it returns a healthy result, so a count taken from the result
	// would be absent in exactly the case worth seeing. Nil records nothing.
	Recorder        *metric.Recorder
	RefreshInterval time.Duration
	Wait            func(context.Context, time.Duration) error
	Close           func() error
	// Now and MaxReplayAge bound how long an undrained draining Query Group
	// stays in the active set. Past the termination window derived from
	// MaxReplayAge nothing can execute its retired Slots, so it is retired
	// from the active set instead of being source-blocked on every tick.
	// Nil Now means the wall clock; zero MaxReplayAge never retires by age.
	Now          func() time.Time
	MaxReplayAge time.Duration
}

type productionPhaseTwoControl struct {
	dependencies  productionPhaseTwoControlDependencies
	renewMu       sync.Mutex
	renewDegraded bool
}

func (runtime *productionPhaseTwoControl) recordControlFactRead(fact string) {
	runtime.dependencies.Recorder.RecordControlFactRead(fact)
}

func (runtime *productionPhaseTwoControl) recordControlFactUnavailable(fact, reason string) {
	runtime.dependencies.Recorder.RecordControlFactUnavailable(fact, reason)
}

func newProductionPhaseTwoControl(
	dependencies productionPhaseTwoControlDependencies,
) (*productionPhaseTwoControl, error) {
	if dependencies.Source == nil || dependencies.Planner == nil || dependencies.Reconciler == nil ||
		dependencies.Activator == nil || dependencies.Repository == nil || dependencies.Schedules == nil ||
		dependencies.Progress == nil || dependencies.RefreshInterval <= 0 ||
		dependencies.Wait == nil || dependencies.MaxReplayAge < 0 {
		return nil, errors.New("phase-two production Control dependencies are incomplete")
	}
	if dependencies.Observer == nil {
		dependencies.Observer = observability.NopObserver{}
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
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
			return completeControlResult(result, err)
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
	return completeControlResult(result, err)
}

// completeControlResult refuses the one shape this runtime must never hand
// back: a zero-valued result with no error.
//
// The caller decides what to do from the health fact in the result, and it
// only looks at the result when the error is nil. A round that returns
// neither is telling it nothing while claiming to have succeeded, which it
// reads as a fact it cannot act on -- and answering that by ending the
// process was how one unreadable activation took a Control Leader down four
// times on 2026-09-16. Every path through refresh now names its outcome; this
// is the guard that keeps the next one from forgetting to, and it turns the
// omission into a retried round rather than an exit.
func completeControlResult(
	result phaseTwoControlRefreshResult,
	err error,
) (phaseTwoControlRefreshResult, error) {
	if err != nil || result.Status != "" {
		return result, err
	}
	return phaseTwoControlRefreshResult{}, errIncompleteControlResult
}

var errIncompleteControlResult = errors.New(
	"phase-two Control round returned no health fact and no error")

func (runtime *productionPhaseTwoControl) LoadActive(
	ctx context.Context,
) (phaseTwoControlRefreshResult, error) {
	if runtime == nil {
		return phaseTwoControlRefreshResult{}, errors.New("phase-two production Control is not initialized")
	}
	// This tick runs as a follower: whatever this process published in an
	// earlier term is not its to answer from any more.
	runtime.dependencies.Reconciler.StepDown()
	state, err := runtime.dependencies.Repository.LoadActivationHead(ctx)
	if err != nil {
		reason := "read_failed"
		if errors.Is(err, controlplane.ErrActivationUnavailable) {
			reason = "missing"
		}
		runtime.recordControlFactUnavailable("activation", reason)
		return phaseTwoControlRefreshResult{}, err
	}
	runtime.recordControlFactRead("activation")
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, err
}

// SourceRefreshSuccessAt reads the persisted time of the last successful
// refresh round under this store, by any process. The second result is
// false when no round is known to have succeeded.
func (runtime *productionPhaseTwoControl) SourceRefreshSuccessAt(ctx context.Context) (time.Time, bool, error) {
	if runtime == nil || runtime.dependencies.Repository == nil {
		return time.Time{}, false, errors.New("phase-two production Control repository is not initialized")
	}
	return runtime.dependencies.Repository.LoadSourceRefreshSuccess(ctx)
}

// ControlVersion serves the activation header for the due index. It reads live
// every time: a caller polling for change has to see the current value, and a
// cached one would answer "nothing has changed" for as long as the cache lasts.
func (runtime *productionPhaseTwoControl) ControlVersion(ctx context.Context) (string, bool, error) {
	if runtime == nil || runtime.dependencies.Repository == nil {
		return "", false, errors.New("phase-two production Control repository is not initialized")
	}
	return runtime.dependencies.Repository.ControlVersionTag(ctx)
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
		// Every failed round is reported, with where it stopped and what it
		// said. The transition into the degraded state is reported once by
		// the bundle, which is right for a transition and blind to an
		// episode: a source failing every round for hours was one line and
		// one increment, and the line had scrolled out of reach. The log
		// limiter bounds these lines per window; the metric counts them all.
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultFailed, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonContractRetryable, Err: err, SourceKind: sourceKind,
			ControlSourceRound: &observability.ControlSourceRoundFacts{
				Outcome: observability.ControlSourceRoundFailed, Exit: string(controlplane.SourceRefreshExitOf(err)),
			},
		})
		result, _, fallbackErr := runtime.keepLastGood(ctx, sourceKind, err)
		return result, false, fallbackErr
	}
	if !knownSourceRefreshStatus(result.Status) {
		return phaseTwoControlRefreshResult{}, false, errors.New("phase-two source refresh returned an invalid status")
	}
	// A round that returned, under any status, is a success of the source
	// refresh, and its time is persisted: the age of the last success is
	// then a difference between that fact and the clock, which survives this
	// process. A write that fails leaves the previous mark standing, so the
	// age reads too old rather than too young, and the failure is reported.
	if markErr := runtime.dependencies.Repository.MarkSourceRefreshSuccess(ctx, runtime.dependencies.Now()); markErr != nil {
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultFailed, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonContractRetryable, Err: fmt.Errorf("phase-two mark source refresh success: %w", markErr),
		})
	}
	// What this round's Catalog is made of rides out on every return that
	// reports a healthy round. Stamping it once here rather than at each
	// return is what keeps the two from drifting: refresh has seven healthy
	// returns, and a reader asking which data sources are running must not
	// get an answer that depends on which one the round took.
	composition := result.Composition
	changeSignalPresent, changeSignalAge := result.ChangeSignalPresent, result.ChangeSignalAgeSeconds
	defer func() {
		// Delivered whenever this round composed a Catalog, not only when the
		// round ended healthy.
		//
		// The gauges say what the Catalog the leader last built is made of.
		// The leader builds one on every round that reads its source; whether
		// the fleet was then activated onto it is a different fact, and
		// activation_failed and the activation standing already report that.
		// Gating the composition on a healthy round meant a process whose
		// activation never succeeded published no composition at all, ever --
		// so the one state where somebody most needs to see the partition, a
		// cutover refusing every round, is precisely the state in which it
		// disappears. It did, for five leader generations.
		//
		// A composed Catalog is told from an absent one by its maps: ComposeCatalog
		// pre-creates every partition it publishes, so a round that did not get
		// that far leaves them nil rather than empty, and nothing is delivered.
		refreshResult.Composition = publishedComposition(refreshErr, composition, refreshResult.Status)
		refreshResult.ChangeSignalPresent, refreshResult.ChangeSignalAgeSeconds = changeSignalPresent, changeSignalAge
		refreshResult.SourceRefreshStatus = result.Status
	}()
	// Which strategies are behind the counts, once per change. Written here
	// rather than at each return for the same reason the composition is: the
	// lines and the counts come from one pass over one list, and a reader who
	// sees a count move must be able to find the line that moved it whichever
	// return the round took.
	observeWithheldObjects(ctx, runtime.dependencies.Observer, result.Withheld)
	observeSuspendedNoDataObjects(ctx, runtime.dependencies.Observer, result.Suspended)
	sourceRefresh := sourceRefreshIdentity(result, result.Publication)
	defer func() {
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultSuccess, SourceRefresh: sourceRefresh,
			ControlSourceRound: &observability.ControlSourceRoundFacts{
				Outcome: observability.ControlSourceRoundSucceeded, Exit: string(controlplane.SourceRefreshExitNone),
			},
		})
	}()
	if result.Status == controlplane.SourceRefreshPendingConfirmation {
		state, err := runtime.dependencies.Repository.LoadActivationHead(ctx)
		activationMissing := false
		if errors.Is(err, controlplane.ErrActivationUnavailable) {
			// The activation record is gone. The store answered; there is
			// nothing there -- a Redis reload that came back without the key,
			// which is what happened on 2026-09-16.
			//
			// Falling through with the zero state is what repairs it: the
			// branch below finds the publication this round named and no
			// activation on it, and activating is idempotent -- it is the same
			// call that establishes the first activation of a fresh
			// deployment. The Control Leader is the only process that can do
			// this, so returning early here left the one replica able to write
			// the record deciding not to, once a round, for as long as it went
			// on.
			//
			// Returning early also returned the zero result with a nil error,
			// which the caller read as a health fact it could not act on and
			// answered by ending the process. Whatever this round can say, it
			// says as a complete fact below.
			runtime.recordControlFactUnavailable("activation", "missing")
			activationMissing = true
			if result.Latest == (controlplane.SnapshotPublicationRef{}) {
				// Nothing has been published yet, so there is nothing to
				// activate. This is a fresh deployment whose candidate still
				// needs its second observation, and it is the case pending
				// exists for: InitialRefresh waits, and a running process
				// keeps the facts it already has.
				return phaseTwoControlRefreshResult{
					Status: phaseTwoControlDegradedLastGood, QueryGroupsRetained: true,
					SourceKind: observability.SourceKindCompiledSnapshot,
					ReasonCode: observability.ReasonCode(contract.ReasonActivationMissing), Cause: err,
				}, true, nil
			}
		} else if err != nil {
			runtime.recordControlFactUnavailable("activation", "read_failed")
			return phaseTwoControlRefreshResult{}, false, err
		} else {
			runtime.recordControlFactRead("activation")
		}
		// A publication an earlier round published and never activated (the
		// process stopped between the two) is still the one the fleet should
		// execute, and this round's candidate does not change that: the
		// candidate needs its own confirmation, and a source that changes on
		// every round never gives one. Returning here left the activation on
		// the previous publication for as long as that went on, past the point
		// where its payload expired, with nothing that could move it - only
		// InitialRefresh loops on pending. The activation is caught up now,
		// with the same failure handling as a round that published.
		if result.Latest != (controlplane.SnapshotPublicationRef{}) && result.Latest != state.Current {
			activated, activationResult, ok := runtime.activate(ctx, result.Latest)
			if !ok {
				return activationResult.result, false, activationResult.err
			}
			queryGroups, err := runtime.loadActiveQueryGroups(ctx, activated)
			if err != nil {
				return phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}, false, err
			}
			sourceRefresh.ActivatedRevision = string(activated.Current.SnapshotRevision)
			sourceRefresh.ActivatedEpoch = activated.Current.PublicationEpoch
			sourceRefresh.ActivationCaughtUp = true
			var previousErr error
			if activationMissing {
				// There was no previous activation to difference against: the
				// counts say every Query Group was added, and the round is
				// named as a rebuild in the log and in its own counter, so a
				// store that lost the record is told from a lagging one.
				previousErr = controlplane.ErrActivationUnavailable
				sourceRefresh.ActivationRebuilt = true
				runtime.dependencies.Recorder.RecordControlFactRebuilt("activation")
			}
			runtime.enrichSourceRefreshCounts(ctx, sourceRefresh, state, previousErr, activated, sourceRefreshCurrentCount(activated, queryGroups))
			return phaseTwoControlRefreshResult{
				QueryGroups: queryGroups, Status: phaseTwoControlHealthy, SourceRefreshObserved: true,
				Activation: &phaseTwoActivationOutcome{Published: result.Latest, Applied: activated.Current},
			}, false, nil
		}
		queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
		if err != nil {
			return phaseTwoControlRefreshResult{}, false, err
		}
		// This round published nothing, so it has no publication to name. What it
		// does know is which publication the fleet is executing, and that goes
		// under its own name: filling snapshot_revision here made a lagging
		// activation indistinguishable from a stalled publication in the log.
		sourceRefresh.ActivatedRevision = string(state.Current.SnapshotRevision)
		sourceRefresh.ActivatedEpoch = state.Current.PublicationEpoch
		// The size is reported; the change is not. Differencing the activation
		// state against itself -- which is all this round has -- can only yield
		// added=0, retired=0 and old==new, which reads as a measured finding and
		// is arithmetic.
		if currentCount := sourceRefreshCurrentCount(state, queryGroups); currentCount != nil {
			sourceRefresh.ActiveQueryGroups = *currentCount
			sourceRefresh.ActiveQueryGroupsKnown = true
		}
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
		previous, previousErr = runtime.dependencies.Repository.LoadActivationHead(ctx)
	}
	state, activationResult, ok := runtime.activate(ctx, result.Publication)
	if !ok {
		return activationResult.result, false, activationResult.err
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
		// A round that found the source unchanged still brought the activation
		// to it (Ensure is idempotent), so it is a success of the same kind.
		Activation: &phaseTwoActivationOutcome{Published: result.Publication, Applied: state.Current},
	}, false, nil
}

type phaseTwoActivationFallback struct {
	result phaseTwoControlRefreshResult
	err    error
}

// activate brings the activation to publication. A failure is reported on
// the activation_failed stage with its bounded classification and answered
// with the last good activation, whichever round asked.
func (runtime *productionPhaseTwoControl) activate(
	ctx context.Context,
	publication controlplane.SnapshotPublicationRef,
) (controlplane.ActivationState, phaseTwoActivationFallback, bool) {
	state, err := runtime.dependencies.Activator.Ensure(ctx, publication)
	if err == nil {
		return state, phaseTwoActivationFallback{}, true
	}
	outcome := &phaseTwoActivationOutcome{Published: publication, Cause: err}
	if failure, ok := controlplane.ActivationFailureFromError(err); ok {
		copied := failure
		outcome.Failure = &copied
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
			Component: observability.ComponentControlPlane,
			Stage:     observability.StageActivationFailed,
			Result:    observability.ResultDegraded,
			Operation: observability.OperationTransition,
			Direction: observability.DirectionInternal,
			// The classification, as the reason: the same word the fleet
			// page groups the failure on. contract_retryable here was folded
			// to _other by the normaliser, and the field people grep said
			// nothing while the two beside it said schedule_conflict.
			ReasonCode: observability.ActivationFailureReason(
				observability.ActivationFailureStage(failure.Stage), observability.ActivationFailureClass(failure.Class)),
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
	fallback, lastGood, fallbackErr := runtime.keepLastGood(ctx, observability.SourceKindCompiledSnapshot, err)
	// The last good activation keepLastGood answered with is what the fleet
	// keeps executing; the outcome names it beside the publication it could
	// not reach, so the standing can say both. Zero when even that could not
	// be read, which the standing keeps apart from "on the last good one".
	outcome.Applied = lastGood.Current
	fallback.Activation = outcome
	return controlplane.ActivationState{}, phaseTwoActivationFallback{result: fallback, err: fallbackErr}, false
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
		CompiledStrategies: result.CompiledStrategies, ReusedStrategies: result.ReusedStrategies,
		ReadMode:            observability.SourceReadMode(result.ReadMode),
		ReadReason:          observability.SourceReadReason(result.ReadReason),
		StrategiesRead:      result.StrategiesRead,
		ChangeSignalPresent: result.ChangeSignalPresent, ChangeSignalAgeSeconds: result.ChangeSignalAgeSeconds,
		RetainedStaleRevisions: result.RetainedStaleRevisions,
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

// The activation it answered with travels back as the second result, so a
// caller that failed to move the activation can say which publication the
// fleet is therefore still executing.
func (runtime *productionPhaseTwoControl) keepLastGood(
	ctx context.Context,
	sourceKind observability.SourceKind,
	cause error,
) (phaseTwoControlRefreshResult, controlplane.ActivationState, error) {
	reason := observability.ReasonContractRetryable
	if errors.Is(cause, controlplane.ErrPublicationOccurrenceCollision) {
		reason = observability.ReasonContractDeterministic
	}
	state, err := runtime.dependencies.Repository.LoadActivationHead(ctx)
	if errors.Is(err, controlplane.ErrActivationUnavailable) {
		return phaseTwoControlRefreshResult{}, controlplane.ActivationState{}, cause
	}
	if err != nil {
		return phaseTwoControlRefreshResult{}, controlplane.ActivationState{}, errors.Join(cause, err)
	}
	queryGroups, err := runtime.loadActiveQueryGroups(ctx, state)
	if err != nil {
		if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			return phaseTwoControlRefreshResult{
				Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
				ReasonCode: reason, Cause: cause,
			}, state, nil
		}
		return phaseTwoControlRefreshResult{}, controlplane.ActivationState{}, errors.Join(cause, err)
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood, SourceKind: sourceKind,
		ReasonCode: reason, Cause: cause,
	}, state, nil
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
	now := execution.EvaluationTime(runtime.dependencies.Now().Unix())
	terminationWindow := controlplane.DrainingTerminationWindow(runtime.dependencies.MaxReplayAge)
	addSample := func(draining controlplane.DrainingQueryGroup, load execution.ProgressLoadResult, disposition string, retention prunedCursor) {
		if retention.pruned {
			drainingFacts.CursorPruned++
		}
		if len(drainingFacts.Samples) >= observability.MaxDrainingQGLogSamples {
			drainingFacts.Truncated = true
			return
		}
		nextSlot, inFlight := int64(0), ""
		if load.Progress != nil {
			nextSlot = int64(load.Progress.NextSlot)
			switch {
			case load.Progress.UnfinishedRange != nil:
				inFlight = observability.DrainingInFlightRange
			case load.Progress.UnfinishedSlot != nil:
				inFlight = observability.DrainingInFlightSlot
			}
		}
		drainingFacts.Samples = append(drainingFacts.Samples, observability.DrainingQGSample{
			QueryGroupKey: string(draining.QueryGroup), RetiredBoundary: int64(draining.RetiredBoundary),
			NextSlot: nextSlot, ProgressStatus: string(load.Status), Disposition: disposition,
			EarliestRetainedSlot: retention.earliest, CursorPruned: retention.pruned, InFlight: inFlight,
		})
	}
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
		drained := load.Status == execution.ProgressFound && load.Progress.UnfinishedRange == nil && load.Progress.NextSlot >= draining.RetiredBoundary
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
		if drained {
			continue
		}
		// An undrained Query Group stays active only while it can still
		// execute. Past the termination window every retired Slot is older
		// than the replay age, so keeping it active would only source-block
		// the Query Group on every tick without ever advancing Progress.
		retention := runtime.prunedCursor(ctx, draining.QueryGroup, load)
		if controlplane.DrainingQueryGroupTerminated(draining, now, terminationWindow) {
			drainingFacts.Retired++
			addSample(draining, load, observability.DrainingQGSampleRetired, retention)
			continue
		}
		active[draining.QueryGroup] = struct{}{}
		drainingFacts.Undrained++
		addSample(draining, load, "", retention)
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
		var content controlplane.PublishedContent
		content, err = runtime.dependencies.Repository.LoadPublishedContent(ctx, state.Current)
		if err == nil {
			queryGroups = make([]execution.QueryGroupIdentity, 0, len(content.Groups))
			for identity := range content.Groups {
				if identity == "" {
					return nil, errors.New("phase-two active Snapshot contains an empty Query Group")
				}
				queryGroups = append(queryGroups, identity)
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

// prunedCursor is what a draining Query Group's timeline says about the
// Progress cursor: the earliest Slot the timeline still holds, and whether
// the cursor lies before it. A cursor in that position asks for a Slot no
// read can find, so the Query Group blocks on every attempt and never
// drains; a retirement that keeps returning it then holds it for as long as
// the projection lives. The claim is made only from a timeline that was
// read: a read that fails, or a Progress that carries no cursor, says
// nothing, because "could not read" must not be mistaken for "pruned".
// Nothing acts on the answer yet; it is reported so the move it would
// justify can be read against real numbers first.
type prunedCursor struct {
	earliest int64
	pruned   bool
}

func (runtime *productionPhaseTwoControl) prunedCursor(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	load execution.ProgressLoadResult,
) prunedCursor {
	if load.Status != execution.ProgressFound || load.Progress == nil {
		return prunedCursor{}
	}
	initial, err := runtime.dependencies.Schedules.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil || initial.Validate() != nil || initial.Segment.QueryGroup != queryGroup {
		return prunedCursor{}
	}
	earliest := initial.Segment.Start
	if first, ok := initial.FirstSlot(); ok {
		earliest = first
	}
	return prunedCursor{earliest: int64(earliest), pruned: load.Progress.NextSlot < earliest}
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
	plan execution.PlanKey,
	epoch execution.StateApplyEpoch,
) (bool, error) {
	if activation.source == nil || epoch == 0 {
		return false, errors.New("phase-two production Plan activation is invalid")
	}
	request := execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanKey{plan}}
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
	PublishAssignmentIndex(context.Context, ownership.PublicationAuthority, time.Time, []ownership.AssignedSetWrite) (ownership.AssignmentIndexPublication, error)
	ReadAssignmentIndex(context.Context) (ownership.AssignmentIndex, error)
	ReadAssignedSet(context.Context, string) (ownership.AssignedSet, error)
	AcquireControlLeader(context.Context, string, time.Time, time.Duration) (ownership.PublicationAuthority, error)
	RenewControlLeader(context.Context, ownership.PublicationAuthority, time.Time, time.Duration) (ownership.PublicationAuthority, error)
	SweepAssignments(context.Context, ownership.PublicationAuthority, map[execution.QueryGroupIdentity]struct{}) (ownership.AssignmentSweep, error)
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
	ExpiredRangeEnabled bool
	// QueryCooldowns keeps each owned Query Group's place in the query
	// cooldown pool across restarts and owners. Nil keeps it in the Runner
	// alone, which is what every runtime did before.
	QueryCooldowns   scheduler.QueryCooldownStore
	Store            productionPhaseTwoOwnershipStore
	WorkerID         string
	Catalog          productionPhaseTwoSlotCatalog
	Progress         productionPhaseTwoProgressReader
	Executor         scheduler.Executor
	Now              func() time.Time
	Reconcile        *scheduler.Reconciler
	ControlLeaderTTL time.Duration
	Observer         observability.Observer
	// SteppedDownAsLeader is told when this process stops being the Control
	// Leader, so the readings that belong to the role can be taken off the
	// scrape. Optional; a runtime without it keeps its last readings, which
	// is what every runtime did before.
	SteppedDownAsLeader       func()
	Flights                   *scheduler.FlightCoordinator
	RecoveryLimits            scheduler.RecoveryLimits
	PostRecoveryTerminalDelay time.Duration
	QueryDeadlineReserve      time.Duration
	SnapshotRetention         time.Duration
	PublicationDelayAllowance time.Duration
	// SettlingWait is access's MinReadyDelay, given to the slot source so it
	// can tell a replay it could dispatch from one it could only dispatch and
	// then abandon. The two were derived in two packages that did not read each
	// other, and a wait longer than the replay window was the result.
	SettlingWait time.Duration
	// LeaseTTL and ReconcileInterval set how long the rebalance writer waits
	// after the ready set changed before it moves anything: a lease TTL for
	// the leases a departed worker still holds to lapse, plus two rounds for
	// the reconcile to have re-placed what it released. Moving during a
	// rolling update would hand Query Groups to a replica about to be
	// terminated; moving after it settles is one convergence.
	LeaseTTL          time.Duration
	ReconcileInterval time.Duration
	// ContentScopes reads the content each Query Group is currently
	// published with -- the ObjectDigest the current activation's manifest
	// names for it -- for the reconcile round to write into Assignment
	// records (decision-016). Required: a leader that cannot read them
	// cannot start the contract, and one that reads them from a fallback
	// would name content the fleet is not executing.
	ContentScopes func(context.Context) (map[execution.QueryGroupIdentity]string, error)
	// ViewStream and ViewSource are decision-016's view stream: the round
	// hands the stream the desired set it arrived at, built from what the
	// source says the fleet executes. Both nil is a runtime without the
	// stream, which every round tolerates.
	ViewStream *viewstream.Server
	ViewSource viewSource
	// Costs is the Leader's ledger of what each Worker's heartbeat reported
	// its Query Groups cost, judged for the byte constraint each round
	// (decision-020 section 5.7). Nil is a runtime that judges nothing and
	// reports every ready Worker as not judged.
	Costs *scheduler.CostLedger
	// SplitCensus reads what the split dry run needs about an object over its
	// share: the Plans it carries and the census each has (decision-020
	// section 4.7.4). Nil is a runtime that works out no splits, which is
	// every round of a deployment that has not turned the reading on.
	SplitCensus splitCensusSource
}

type productionPhaseTwoOwnership struct {
	dependencies productionPhaseTwoOwnershipDependencies
	reconciler   *scheduler.Reconciler
	flights      *scheduler.FlightCoordinator
	// viewGate decides, per Slot read, whether a Query Group is executed
	// from the installed view (decision-016 batch 4); nil is the shadow
	// step, every read the control plane's way.
	viewGate *viewExecutionGate

	mu        sync.Mutex
	authority ownership.PublicationAuthority

	// indexDigests remembers, per ready worker, the content digest of the
	// assigned set this Leader last wrote, so a round only rewrites sets
	// that changed; it is forgotten when the fence epoch changes.
	indexEpoch   uint64
	indexDigests map[string][sha256.Size]byte
	indexReader  assignmentIndexReader

	// lastRebalance is the plan the latest round computed, kept for the
	// fleet snapshot this replica publishes. Nil until this process has
	// planned a round, which only a Leader does.
	lastRebalance *fleet.RebalanceFacts
	// lastAssignmentScope is the same round's census of the content scope
	// on the records it settled, for the fleet snapshot. Nil until a round.
	lastAssignmentScope *fleet.AssignmentScopeFacts
	// lastAssignmentSweep is the latest sweep of retired records, success or
	// failure, for the fleet snapshot. Nil until a sweep has run.
	lastAssignmentSweep *fleet.AssignmentSweepFacts
	// lastLeaderRound is the latest reconcile round stage by stage, and
	// leaderRounds and leaderRoundSeconds the rounds this process has led,
	// by result, and their seconds by stage, the whole round under "total".
	// Only a round that held the authority is counted: one that could not
	// get it was not a leader's round.
	lastLeaderRound    *fleet.LeaderRoundFacts
	leaderRounds       map[string]uint64
	leaderRoundSeconds map[string]float64

	// readySet is the ready set the last round reconciled against and when
	// it last changed, remembered under the fence epoch it was observed in;
	// a new Leader starts a fresh memory and so waits out one window before
	// it moves anything.
	readyEpoch     uint64
	readySet       map[string]struct{}
	readyChangedAt time.Time

	// lastSet is the Query Group set the last round ran, under the fence
	// epoch it ran in, and sweepOwed whether the last sweep left retired
	// records behind because a lease still held them. A round sweeps when
	// it is the first of a term, when a Query Group of the last round's set
	// is gone, or when a sweep is owed: those are the moments a record can
	// be left behind or still be waiting.
	//
	// The last round's set, not the last swept set: a Query Group that
	// arrived after a sweep and left before the next one was in no swept
	// set, so measured against that it never left, and its record stayed
	// for good. And owed, because a record a lease still held is reclaimed
	// only by a later sweep, which nothing else asked for once the set was
	// remembered as swept.
	sweptEpoch uint64
	lastSet    map[execution.QueryGroupIdentity]struct{}
	sweepOwed  bool
}

// rebalanceStabilisation is how long the ready set must have been unchanged
// before a round publishes the moves it planned.
func (runtime *productionPhaseTwoOwnership) rebalanceStabilisation() time.Duration {
	return runtime.dependencies.LeaseTTL + 2*runtime.dependencies.ReconcileInterval
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
		dependencies.SnapshotRetention <= 0 || dependencies.PublicationDelayAllowance <= 0 ||
		dependencies.SettlingWait <= 0 {
		return nil, errors.New("phase-two post-recovery terminal delay is required")
	}
	if dependencies.LeaseTTL <= 0 || dependencies.ReconcileInterval <= 0 {
		return nil, errors.New("phase-two rebalance stabilisation inputs are required")
	}
	if dependencies.ContentScopes == nil {
		return nil, errors.New("phase-two content scope reader is required")
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

// contentScopesFor is the round's content policy. Declaring needs the
// current content in hand; a failed read is reported and the round leaves
// scopes as they are, because a round that withdrew on a read failure would
// turn a Redis blip into a fleet-wide rollback of the contract.
func (runtime *productionPhaseTwoOwnership) contentScopesFor(
	ctx context.Context,
	workers []ownership.WorkerRegistration,
) (scheduler.ContentScopes, error) {
	if !ownership.AllDeclare(workers, ownership.CapabilityContentScope) {
		return scheduler.ContentScopes{Policy: scheduler.ContentScopesWithdrawn}, nil
	}
	digests, err := runtime.dependencies.ContentScopes(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return scheduler.ContentScopes{}, err
		}
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageAssignmentAcquired,
			Result: observability.ResultDegraded, Operation: observability.OperationTransition,
			Direction: observability.DirectionInternal, ReasonCode: observability.ReasonInternalUnknown, Err: err,
		})
		return scheduler.ContentScopes{}, nil
	}
	return scheduler.ContentScopes{Policy: scheduler.ContentScopesDeclared, Digests: digests}, nil
}

func (runtime *productionPhaseTwoOwnership) PublishAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
	at time.Time,
) (err error) {
	if runtime == nil || at.IsZero() {
		return newPhaseTwoInvariantError("phase-two production Assignment reconcile is invalid")
	}
	round := newLeaderRoundTimer(at)
	authority, err := runtime.ensureControlAuthority(ctx, at)
	if err != nil {
		return err
	}
	round.done(fleet.LeaderRoundStageAuthority)
	defer func() { runtime.recordLeaderRound(round.finish(err)) }()
	ordered := append([]execution.QueryGroupIdentity(nil), queryGroups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	for index, queryGroup := range ordered {
		if queryGroup == "" || (index > 0 && ordered[index-1] == queryGroup) {
			return newPhaseTwoInvariantError("phase-two production reconcile contains an invalid Query Group set")
		}
	}
	// One ready set per round: every Query Group below is settled against
	// the same workers, and a listing that fails fails the round before any
	// Query Group is touched, exactly as a failed listing did before.
	workers, registryReads, err := runtime.reconciler.ListReadyWorkers(ctx, at)
	if err != nil {
		return err
	}
	round.done(fleet.LeaderRoundStageReadyWorkers)
	// The content contract's gate, decided once per round on the same ready
	// set the placements use: every ready worker declares it, and the round
	// brings each record to the content its Query Group is published with;
	// one does not, and the round withdraws every scope (decision-016
	// section 7.1.1). A leader that cannot read the current content this
	// round declares nothing and withdraws nothing -- unknown is not a
	// withdrawal -- and says so once per round.
	scopes, err := runtime.contentScopesFor(ctx, workers)
	if err != nil {
		return err
	}
	round.done(fleet.LeaderRoundStageContentScopes)
	// Every Query Group's record in one bounded batch, then the placement
	// decisions over it. Reading them one at a time cost the round a Redis
	// round trip per Query Group, all of it waiting, all of it before any
	// decision could be taken.
	records, assignmentReads, err := runtime.reconciler.ReconcileRoundWithScopes(ctx, authority, ordered, workers, at, scopes)
	runtime.observeControlReads(ctx, assignmentReads, registryReads, len(ordered))
	if err != nil {
		if errors.Is(err, ownership.ErrStaleFence) {
			runtime.clearControlAuthority(authority)
		}
		return err
	}
	round.done(fleet.LeaderRoundStageReconcileRecords)
	// The round's census of the content scope on the records it settled,
	// for the page: the same read, counted once, so how far the contract
	// has reached the records is not a question for a script in a Pod.
	runtime.recordAssignmentScope(at, scopes, records)
	owners := make(map[execution.QueryGroupIdentity]string, len(ordered))
	for queryGroup, record := range records {
		owners[queryGroup] = record.DesiredWorkerID
	}
	// Placement first, correction second, both under this round's ready
	// set: rendezvous only decides where a Query Group with no eligible
	// holder goes, and the holder it picks is sticky, so a replica that
	// comes back after a crash or a rollout owns nothing until this moves
	// its share to it. The index is written from the owners after the moves,
	// so a worker reads the round's final answer.
	// Feasibility before balance (decision-020 section 5.7): a Worker whose
	// Query Groups' retained-byte peaks sum past its pool's share gives its
	// largest one to the Worker with the most headroom, whatever the
	// counts say; the count correction then plans over the owners after
	// those moves and under the same readings, so it neither undoes them
	// nor fills a destination past its share.
	stable, remaining := runtime.observeReadySet(authority, workers, at)
	// The split contract's gate (decision-020 section 4.7.7), decided on
	// the same ready set: no split is published this round unless every
	// ready worker declares it, and the replicas that do not are named on
	// the round's facts. Nothing asks for a split yet; the gate and its
	// reading exist so a roll can be watched going 0 -> n -> 0 before one
	// does.
	shardGate := ownership.ShardSplitAdmission(workers)
	readings := runtime.dependencies.Costs.Readings(owners, workers)
	bytePlan := runtime.reconciler.PlanByteMoves(owners, workers, readings, at)
	byteMoves := make([]scheduler.RebalanceMove, 0, len(bytePlan.Moves))
	for _, move := range bytePlan.Moves {
		byteMoves = append(byteMoves, scheduler.RebalanceMove{QueryGroup: move.QueryGroup, From: move.From, To: move.To})
	}
	byteOutcome, err := runtime.publishMoves(ctx, authority, byteMoves, records, stable, remaining, at)
	for _, move := range byteOutcome.applied {
		owners[move.QueryGroup] = move.To
	}
	if err != nil {
		runtime.observeRebalance(ctx, scheduler.RebalancePlan{Owned: map[string]int{}}, rebalanceOutcome{}, bytePlan, byteOutcome, shardGate, at)
		return err
	}
	round.done(fleet.LeaderRoundStageByteMoves)
	plan := runtime.reconciler.PlanRebalanceWithBytes(owners, workers, readings, at)
	outcome, err := runtime.publishMoves(ctx, authority, plan.Moves, records, stable, remaining, at)
	for _, move := range outcome.applied {
		owners[move.QueryGroup] = move.To
	}
	runtime.observeRebalance(ctx, plan, outcome, bytePlan, byteOutcome, shardGate, at)
	if err != nil {
		return err
	}
	round.done(fleet.LeaderRoundStageRebalanceMoves)
	// What a split would be for whatever is still over its share once this
	// round's moves are in. Reported and not acted on; it reads Plans and
	// censuses for the few objects the trigger names and writes nothing.
	runtime.dryRunSplits(ctx, owners, workers, readings, at)
	round.done(fleet.LeaderRoundStageSplitDryRun)
	// The ledger keeps what the round's final owners agree with; a Query
	// Group that moved has no reading until its new holder reports it.
	runtime.dependencies.Costs.Retain(owners)
	runtime.publishAssignmentIndex(ctx, authority, owners, workers, at)
	round.done(fleet.LeaderRoundStageAssignmentIndex)
	runtime.sweepRetiredAssignments(ctx, authority, ordered)
	round.done(fleet.LeaderRoundStageAssignmentSweep)
	runtime.publishView(ctx, authority, records, owners)
	round.done(fleet.LeaderRoundStageViewPublish)
	return nil
}

// leaderRoundTimer times one leader round stage by stage, on the monotonic
// clock: at is the round's own time, for the facts, and never a duration.
type leaderRoundTimer struct {
	at      time.Time
	started time.Time
	last    time.Time
	stages  []fleet.LeaderRoundStage
}

func newLeaderRoundTimer(at time.Time) *leaderRoundTimer {
	now := time.Now()
	return &leaderRoundTimer{at: at, started: now, last: now, stages: make([]fleet.LeaderRoundStage, 0, len(fleet.LeaderRoundStages))}
}

// done closes the stage that has just finished.
func (timer *leaderRoundTimer) done(stage string) {
	now := time.Now()
	timer.stages = append(timer.stages, fleet.LeaderRoundStage{Stage: stage, Seconds: now.Sub(timer.last).Seconds()})
	timer.last = now
}

// finish is the round as it ended: a failed round names the stage after
// the last one it finished, the one it failed in.
func (timer *leaderRoundTimer) finish(err error) fleet.LeaderRoundFacts {
	facts := fleet.LeaderRoundFacts{At: timer.at, Result: fleet.LeaderRoundCompleted,
		TotalSeconds: time.Since(timer.started).Seconds(), Stages: timer.stages}
	if err != nil {
		facts.Result = fleet.LeaderRoundFailed
		if len(timer.stages) < len(fleet.LeaderRoundStages) {
			facts.FailedStage = fleet.LeaderRoundStages[len(timer.stages)]
		}
	}
	return facts
}

// recordLeaderRound keeps the round for the fleet snapshot and adds it to
// the process's totals.
func (runtime *productionPhaseTwoOwnership) recordLeaderRound(facts fleet.LeaderRoundFacts) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.leaderRounds == nil {
		runtime.leaderRounds = map[string]uint64{}
		runtime.leaderRoundSeconds = map[string]float64{}
	}
	runtime.lastLeaderRound = &facts
	runtime.leaderRounds[facts.Result]++
	for _, stage := range facts.Stages {
		runtime.leaderRoundSeconds[stage.Stage] += stage.Seconds
	}
	runtime.leaderRoundSeconds[metric.LeaderRoundStageTotal] += facts.TotalSeconds
}

// LastLeaderRound is the latest reconcile round this process led, stage by
// stage, for the fleet snapshot; nil until one.
func (runtime *productionPhaseTwoOwnership) LastLeaderRound() *fleet.LeaderRoundFacts {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.lastLeaderRound == nil {
		return nil
	}
	facts := *runtime.lastLeaderRound
	facts.Stages = append([]fleet.LeaderRoundStage(nil), facts.Stages...)
	return &facts
}

// LeaderRoundStats is the rounds this process has led, for the collector.
func (runtime *productionPhaseTwoOwnership) LeaderRoundStats() metric.LeaderRoundStats {
	if runtime == nil {
		return metric.LeaderRoundStats{}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.leaderRounds == nil {
		return metric.LeaderRoundStats{}
	}
	stats := metric.LeaderRoundStats{Leading: true, Rounds: map[string]uint64{}, Seconds: map[string]float64{}}
	for result, count := range runtime.leaderRounds {
		stats.Rounds[result] = count
	}
	for stage, seconds := range runtime.leaderRoundSeconds {
		stats.Seconds[stage] = seconds
	}
	return stats
}

// sweepRetiredAssignments reclaims the Assignment records of Query Groups
// this round no longer runs, when something could have been left behind or
// still be waiting: the first round of a term, a Query Group of the last
// round's set missing from this one, or a sweep owed because the last one
// found records a lease still held. Records carry no expiry, so without
// this a retired Query Group's record and ownership hash stayed for good.
// Advisory to the round: a failed sweep is reported and the round stands.
func (runtime *productionPhaseTwoOwnership) sweepRetiredAssignments(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroups []execution.QueryGroupIdentity,
) {
	keep := make(map[execution.QueryGroupIdentity]struct{}, len(queryGroups))
	for _, queryGroup := range queryGroups {
		keep[queryGroup] = struct{}{}
	}
	runtime.mu.Lock()
	due := runtime.sweptEpoch != authority.Fence.OwnerEpoch || runtime.lastSet == nil || runtime.sweepOwed
	if !due {
		for queryGroup := range runtime.lastSet {
			if _, still := keep[queryGroup]; !still {
				due = true
				break
			}
		}
	}
	// Remembered every round, swept or not, so the next round measures
	// what left against what this round ran.
	runtime.sweptEpoch, runtime.lastSet = authority.Fence.OwnerEpoch, keep
	runtime.mu.Unlock()
	if !due {
		return
	}
	sweep, err := runtime.dependencies.Store.SweepAssignments(ctx, authority, keep)
	facts := &observability.AssignmentSweepFacts{Scanned: sweep.Scanned, Retired: sweep.Retired,
		Reclaimed: sweep.Reclaimed, HeldByLease: sweep.HeldByLease, Changed: sweep.Changed}
	// The same numbers for the fleet snapshot, success or failure: a sweep
	// that ran and reclaimed six records on a live deployment was known
	// only to the Pod's own memory.
	sweptAt := time.Now()
	if runtime.dependencies.Now != nil {
		sweptAt = runtime.dependencies.Now()
	}
	published := &fleet.AssignmentSweepFacts{At: sweptAt, Result: string(observability.ResultSuccess),
		Scanned: sweep.Scanned, Retired: sweep.Retired, Reclaimed: sweep.Reclaimed, HeldByLease: sweep.HeldByLease,
		Changed: sweep.Changed, DurationSeconds: sweep.Duration.Seconds()}
	if err != nil {
		if errors.Is(err, ownership.ErrStaleFence) {
			runtime.clearControlAuthority(authority)
		}
		reason := ownershipObservationReason(err)
		published.Result, published.Reason = string(observability.ResultFailed), string(reason)
		runtime.mu.Lock()
		// A sweep that failed reclaimed nothing; the next round owes it.
		runtime.sweepOwed = true
		runtime.lastAssignmentSweep = published
		runtime.mu.Unlock()
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageAssignmentSwept,
			Result: observability.ResultFailed, Operation: observability.OperationWrite, Duration: sweep.Duration,
			ReasonCode: reason, Err: err, AssignmentSweep: facts,
		})
		return
	}
	runtime.mu.Lock()
	// Records a lease still held are not reclaimed until it lapses, and no
	// set change will ask for the sweep that does it: the next round is
	// asked here.
	runtime.sweepOwed = sweep.HeldByLease > 0
	runtime.lastAssignmentSweep = published
	runtime.mu.Unlock()
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentSwept,
		Result: observability.ResultSuccess, Operation: observability.OperationWrite, Duration: sweep.Duration,
		AssignmentSweep: facts,
	})
}

// rebalanceOutcome is what one round did with the plan it computed.
type rebalanceOutcome struct {
	applied   []scheduler.RebalanceMove
	conflicts int
	paused    bool
	pausedFor time.Duration
}

// publishMoves publishes one round's moves - the byte-constraint moves and
// the count rebalance's alike - as Assignment decisions under this round's
// authority, unless the ready set changed within the stabilisation window,
// in which case the round only reports them.
//
// Each move names the record revision the reconcile just read, so a record
// another writer moved in between is refused by the store and skipped, not
// overwritten; the next round plans over what is actually there. A stale
// fence ends the round, as it ends the reconcile. The old holder is not
// asked: its next renewal is refused with NOT_DESIRED and its in-flight
// commit by the fence, and the new holder resumes the Query Group from its
// Progress, which is the same handover a rendezvous re-placement makes.
// Every move is written as REBALANCE: the byte-constraint word is accepted
// by readers first (ownership.PlacementByteConstraint) and written once
// every reader accepts it.
func (runtime *productionPhaseTwoOwnership) publishMoves(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	moves []scheduler.RebalanceMove,
	records map[execution.QueryGroupIdentity]ownership.AssignmentRecord,
	stable bool,
	remaining time.Duration,
	at time.Time,
) (rebalanceOutcome, error) {
	outcome := rebalanceOutcome{}
	if len(moves) == 0 {
		return outcome, nil
	}
	if !stable {
		outcome.paused, outcome.pausedFor = true, remaining
		return outcome, nil
	}
	for _, move := range moves {
		record, known := records[move.QueryGroup]
		if !known || record.DesiredWorkerID != move.From {
			// The plan was computed from these records; a move over a Query
			// Group they do not hold as the planner saw it is a defect in the
			// planner, and skipping it is the safe reading.
			outcome.conflicts++
			continue
		}
		_, err := runtime.dependencies.Store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
			QueryGroup: move.QueryGroup, DesiredWorkerID: move.To, ExpectedRecordRevision: record.RecordRevision,
			PlacementReason: ownership.PlacementRebalance, DecidedAt: at,
		})
		switch {
		case errors.Is(err, ownership.ErrAssignmentConflict):
			outcome.conflicts++
			continue
		case errors.Is(err, ownership.ErrStaleFence):
			runtime.clearControlAuthority(authority)
			return outcome, err
		case err != nil:
			return outcome, err
		}
		outcome.applied = append(outcome.applied, move)
	}
	return outcome, nil
}

// observeReadySet remembers the ready set this round reconciled against and
// reports whether it has been unchanged for the stabilisation window, and
// if not, for how much longer the writer waits. The memory belongs to the
// fence epoch: a new Leader starts from this round.
func (runtime *productionPhaseTwoOwnership) observeReadySet(
	authority ownership.PublicationAuthority,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (bool, time.Duration) {
	ready := make(map[string]struct{}, len(workers))
	for _, worker := range workers {
		if worker.Validate() != nil || worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			continue
		}
		ready[worker.WorkerID] = struct{}{}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	changed := runtime.readySet == nil || runtime.readyEpoch != authority.Fence.OwnerEpoch || len(ready) != len(runtime.readySet)
	if !changed {
		for workerID := range ready {
			if _, known := runtime.readySet[workerID]; !known {
				changed = true
				break
			}
		}
	}
	if changed {
		runtime.readyEpoch, runtime.readySet, runtime.readyChangedAt = authority.Fence.OwnerEpoch, ready, at
	}
	elapsed := at.Sub(runtime.readyChangedAt)
	if window := runtime.rebalanceStabilisation(); elapsed < window {
		return false, window - elapsed
	}
	return true, 0
}

// observeRebalance reports one rebalance round: the plan, moves named one by
// one so the plan can be checked against the Assignments by hand, and what
// the round did with it -- published, paused for the ready set to settle,
// or refused by the store for some of them.
// observeControlReads reports what this round spent on the two control-plane
// reads, whether or not the round went on to succeed.
//
// It is called before the error is handled on purpose. A round that failed
// still spent its round trips, and the reading that matters most -- a round
// that took far longer than the others -- is most likely to be one that then
// failed. Reporting only on success would leave those out of the very
// distribution somebody is looking at them in.
func (runtime *productionPhaseTwoOwnership) observeControlReads(
	ctx context.Context,
	assignments ownership.ControlReadStats,
	registry ownership.ControlReadStats,
	queryGroups int,
) {
	facts := &observability.ControlReadFacts{
		QueryGroups:            queryGroups,
		AssignmentKeys:         assignments.Keys,
		AssignmentRoundTrips:   assignments.RoundTrips,
		AssignmentMilliseconds: float64(assignments.Duration.Nanoseconds()) / float64(time.Millisecond),
		RegistryKeys:           registry.Keys,
		RegistryRoundTrips:     registry.RoundTrips,
		RegistryMilliseconds:   float64(registry.Duration.Nanoseconds()) / float64(time.Millisecond),
	}
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageControlReadsSpent,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad, ControlReads: facts,
	})
}

func (runtime *productionPhaseTwoOwnership) observeRebalance(
	ctx context.Context,
	plan scheduler.RebalancePlan,
	outcome rebalanceOutcome,
	bytePlan scheduler.BytePlan,
	byteOutcome rebalanceOutcome,
	shardGate ownership.ShardSplitGate,
	at time.Time,
) {
	facts := &observability.RebalanceFacts{
		ReadyWorkers: plan.ReadyWorkers, Assigned: plan.Assigned, Target: plan.Target,
		MostOwned: plan.MostOwned, LeastOwned: plan.LeastOwned, Batch: plan.Batch, PlannedMoves: len(plan.Moves),
		PublishedMoves: len(outcome.applied), Conflicts: outcome.conflicts,
		Paused: outcome.paused, PausedForSeconds: outcome.pausedFor.Seconds(),
		Bytes:      byteConstraintFacts(bytePlan, byteOutcome),
		ShardAware: &observability.ShardAwareFacts{Ready: shardGate.Ready, Unaware: shardGate.Unaware},
	}
	workerIDs := make([]string, 0, len(plan.Owned))
	for workerID := range plan.Owned {
		workerIDs = append(workerIDs, workerID)
	}
	sort.Strings(workerIDs)
	for _, workerID := range workerIDs {
		facts.Owned = append(facts.Owned, observability.RebalanceOwnedSample{WorkerID: workerID, Owned: plan.Owned[workerID]})
	}
	for _, move := range plan.Moves {
		facts.Moves = append(facts.Moves, observability.RebalanceMoveSample{QueryGroup: string(move.QueryGroup), From: move.From, To: move.To})
	}
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageRebalancePlanned,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad, Rebalance: facts,
	})
	// The same round for the fleet snapshot. Shadow is false here because
	// this is the round that publishes the moves; what it did with them is
	// beside the plan, so the page can say "moving" or "waiting for the
	// ready set to settle" rather than "would move".
	published := &fleet.RebalanceFacts{
		PlannedAt: at, ReadyWorkers: plan.ReadyWorkers, Assigned: plan.Assigned, Target: plan.Target,
		MostOwned: plan.MostOwned, LeastOwned: plan.LeastOwned, Batch: plan.Batch, PlannedMoves: len(plan.Moves),
		StopSpreadPercent: scheduler.RebalanceStopSpreadPercent, Shadow: false,
		PublishedMoves: len(outcome.applied), Conflicts: outcome.conflicts,
		Paused: outcome.paused, PausedForSeconds: outcome.pausedFor.Seconds(),
	}
	if len(plan.Moves) > 0 {
		// The pair the round chose, from the round's own first move rather
		// than a second walk over the counts with its own tie rule.
		published.MostOwnedBy, published.LeastOwnedBy = plan.Moves[0].From, plan.Moves[0].To
	}
	published.Bytes = fleetByteConstraintFacts(bytePlan, byteOutcome)
	published.ShardAware = &fleet.ShardAwareFacts{Ready: shardGate.Ready, Unaware: shardGate.Unaware}
	runtime.mu.Lock()
	runtime.lastRebalance = published
	runtime.mu.Unlock()
}

// byteConstraintFacts is one round's byte-constraint planning for the log
// line: what was judged, what was not, who was over, what moved.
func byteConstraintFacts(plan scheduler.BytePlan, outcome rebalanceOutcome) *observability.ByteConstraintFacts {
	facts := &observability.ByteConstraintFacts{
		SharePercent: scheduler.ByteConstraintPercent, Judged: plan.Judged, PoolUnknown: plan.PoolUnknown, Unread: plan.Unread, Unsettled: plan.Unsettled,
		Overloaded: plan.Overloaded, Unplaceable: plan.Unplaceable, PlannedMoves: len(plan.Moves),
		PublishedMoves: len(outcome.applied), Conflicts: outcome.conflicts, Paused: outcome.paused,
	}
	for _, move := range plan.Moves {
		facts.Moves = append(facts.Moves, observability.ByteMoveSample{QueryGroup: string(move.QueryGroup), From: move.From, To: move.To, Bytes: move.Bytes})
	}
	return facts
}

// fleetByteConstraintFacts is the same round for the fleet snapshot.
func fleetByteConstraintFacts(plan scheduler.BytePlan, outcome rebalanceOutcome) *fleet.ByteConstraintFacts {
	facts := &fleet.ByteConstraintFacts{
		SharePercent: scheduler.ByteConstraintPercent, Judged: plan.Judged, PoolUnknown: plan.PoolUnknown, Unread: plan.Unread, Unsettled: plan.Unsettled,
		Overloaded: plan.Overloaded, Unplaceable: plan.Unplaceable, PlannedMoves: len(plan.Moves),
		PublishedMoves: len(outcome.applied), Conflicts: outcome.conflicts, Paused: outcome.paused,
	}
	workerIDs := make([]string, 0, len(plan.Sum))
	for workerID := range plan.Sum {
		workerIDs = append(workerIDs, workerID)
	}
	sort.Strings(workerIDs)
	for _, workerID := range workerIDs {
		facts.Sums = append(facts.Sums, fleet.ByteSumSample{WorkerID: workerID, PeakSumBytes: plan.Sum[workerID]})
	}
	for _, move := range plan.Moves {
		facts.Moves = append(facts.Moves, fleet.ByteMoveSample{QueryGroup: string(move.QueryGroup), From: move.From, To: move.To, Bytes: move.Bytes})
	}
	return facts
}

// LastRebalance is the plan the latest round on this process computed, for
// the fleet snapshot; nil on a process that has never been the Leader.
func (runtime *productionPhaseTwoOwnership) LastRebalance() *fleet.RebalanceFacts {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.lastRebalance == nil {
		return nil
	}
	facts := *runtime.lastRebalance
	return &facts
}

// recordAssignmentScope keeps the round's content-scope census for the
// fleet snapshot, with the policy spelled in the fleet's words.
func (runtime *productionPhaseTwoOwnership) recordAssignmentScope(
	at time.Time,
	scopes scheduler.ContentScopes,
	records map[execution.QueryGroupIdentity]ownership.AssignmentRecord,
) {
	facts := fleet.AssignmentScopeOf(at, assignmentScopePolicyWord(scopes.Policy), scopes.Digests, records)
	runtime.mu.Lock()
	runtime.lastAssignmentScope = facts
	runtime.mu.Unlock()
}

// assignmentScopePolicyWord is the fleet's word for the round's policy.
// Every policy has one; a new policy without a word here is the untouched
// word, which is the one that reads as "the round changed nothing".
func assignmentScopePolicyWord(policy scheduler.ContentScopePolicy) string {
	switch policy {
	case scheduler.ContentScopesDeclared:
		return fleet.AssignmentScopePolicyDeclared
	case scheduler.ContentScopesWithdrawn:
		return fleet.AssignmentScopePolicyWithdrawn
	default:
		return fleet.AssignmentScopePolicyUntouched
	}
}

// LastAssignmentScope is the latest round's content-scope census on this
// process, for the fleet snapshot; nil on a process that has never been the
// Leader.
func (runtime *productionPhaseTwoOwnership) LastAssignmentScope() *fleet.AssignmentScopeFacts {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.lastAssignmentScope == nil {
		return nil
	}
	facts := *runtime.lastAssignmentScope
	return &facts
}

// LastAssignmentSweep is the latest sweep of retired Assignment records on
// this process, success or failure, for the fleet snapshot; nil until one
// has run.
func (runtime *productionPhaseTwoOwnership) LastAssignmentSweep() *fleet.AssignmentSweepFacts {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.lastAssignmentSweep == nil {
		return nil
	}
	facts := *runtime.lastAssignmentSweep
	return &facts
}

// publishAssignmentIndex writes the per-worker Assignment index for the
// round just reconciled: every ready worker gets the Query Groups whose
// desired owner it is, rewritten only when the set's digest changed since
// this Leader last wrote it. Digests are forgotten when the fence epoch
// changes, so a new Leader rewrites everything once. Sets the store reports
// missing are rewritten in one follow-up round. A failed write is reported
// and does not fail the reconcile: the index is an accelerator, readers
// fall back to the records.
func (runtime *productionPhaseTwoOwnership) publishAssignmentIndex(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	at time.Time,
) {
	byWorker := make(map[string][]execution.QueryGroupIdentity, len(workers))
	for _, worker := range workers {
		if _, seen := byWorker[worker.WorkerID]; !seen {
			byWorker[worker.WorkerID] = nil
		}
	}
	for queryGroup, owner := range owners {
		if _, ready := byWorker[owner]; ready {
			byWorker[owner] = append(byWorker[owner], queryGroup)
		}
	}
	workerIDs := make([]string, 0, len(byWorker))
	for workerID := range byWorker {
		workerIDs = append(workerIDs, workerID)
	}
	sort.Strings(workerIDs)
	digests := make(map[string][sha256.Size]byte, len(workerIDs))
	writes := make([]ownership.AssignedSetWrite, 0, len(workerIDs))
	runtime.mu.Lock()
	if runtime.indexDigests == nil || runtime.indexEpoch != authority.Fence.OwnerEpoch {
		runtime.indexEpoch = authority.Fence.OwnerEpoch
		runtime.indexDigests = map[string][sha256.Size]byte{}
	}
	for _, workerID := range workerIDs {
		// Ordered before it is written: the digest sorts for itself, but the
		// set is persisted as given, and a set gathered from a map is in a
		// different order on every round.
		set := byWorker[workerID]
		sort.Slice(set, func(left, right int) bool { return set[left] < set[right] })
		digest := ownership.AssignedSetDigest(set)
		digests[workerID] = digest
		previous, known := runtime.indexDigests[workerID]
		writes = append(writes, ownership.AssignedSetWrite{
			WorkerID: workerID, QueryGroups: byWorker[workerID], Rewrite: !known || previous != digest,
		})
	}
	runtime.mu.Unlock()
	facts := &observability.AssignmentIndexFacts{ControlEpoch: authority.Fence.OwnerEpoch, Workers: len(writes)}
	publication, err := runtime.dependencies.Store.PublishAssignmentIndex(ctx, authority, at, writes)
	if err == nil && len(publication.Missing) > 0 {
		missing := make(map[string]struct{}, len(publication.Missing))
		for _, workerID := range publication.Missing {
			missing[workerID] = struct{}{}
		}
		for index := range writes {
			if _, absent := missing[writes[index].WorkerID]; absent {
				writes[index].Rewrite = true
			}
		}
		facts.Missing = len(publication.Missing)
		publication, err = runtime.dependencies.Store.PublishAssignmentIndex(ctx, authority, at, writes)
	}
	for _, write := range writes {
		if write.Rewrite {
			facts.Rewritten++
		}
	}
	if err != nil {
		if errors.Is(err, ownership.ErrStaleFence) {
			runtime.clearControlAuthority(authority)
		}
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexWritten,
			Result: observability.ResultFailed, Operation: observability.OperationWrite, Err: err, AssignmentIndex: facts,
			ReasonCode: ownershipObservationReason(err),
		})
		return
	}
	runtime.mu.Lock()
	if runtime.indexEpoch == authority.Fence.OwnerEpoch {
		for workerID := range runtime.indexDigests {
			if _, present := digests[workerID]; !present {
				delete(runtime.indexDigests, workerID)
			}
		}
		for workerID, digest := range digests {
			runtime.indexDigests[workerID] = digest
		}
	}
	runtime.mu.Unlock()
	facts.Round = publication.Round
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexWritten,
		Result: observability.ResultSuccess, Operation: observability.OperationWrite, AssignmentIndex: facts,
	})
}

// assignmentIndexReader is a worker's memory of the Assignment index: the
// last round it saw and for how many consecutive rounds that number stayed
// put, its own candidate set with the round it was read at, and the set of
// Query Groups it holds as confirmed against the records. It exists so an
// unchanged round costs one small read and none of the population.
type assignmentIndexReader struct {
	mu          sync.Mutex
	lastRound   uint64
	staleRounds int
	haveSet     bool
	setRound    uint64
	candidates  map[execution.QueryGroupIdentity]struct{}
	owned       map[execution.QueryGroupIdentity]struct{}
}

// readAssignmentIndex reads the index once and this worker's set only when
// the index says the set changed, reporting the read in facts. It returns
// whether the candidate set is usable this round; when it is not, the
// result in facts says why, and the error, if any, is the store failure
// behind an invalid read.
func (runtime *productionPhaseTwoOwnership) readAssignmentIndex(
	ctx context.Context,
	reader *assignmentIndexReader,
	facts *observability.AssignmentIndexFacts,
) (bool, error) {
	index, err := runtime.dependencies.Store.ReadAssignmentIndex(ctx)
	switch {
	case errors.Is(err, ownership.ErrAssignmentIndexAbsent):
		facts.Result, facts.StaleRounds = observability.AssignmentIndexMissing, reader.staleRounds
		return false, nil
	case err != nil:
		facts.Result, facts.StaleRounds = observability.AssignmentIndexInvalid, reader.staleRounds
		return false, err
	}
	facts.Round, facts.ControlEpoch = index.Round, index.ControlEpoch
	if index.Round == reader.lastRound {
		reader.staleRounds++
		facts.Result = observability.AssignmentIndexStale
	} else {
		reader.lastRound, reader.staleRounds = index.Round, 0
		facts.Result = observability.AssignmentIndexFresh
	}
	facts.StaleRounds = reader.staleRounds
	setRound, named := index.SetRounds[runtime.dependencies.WorkerID]
	switch {
	case !named:
		reader.candidates, reader.setRound, reader.haveSet = map[execution.QueryGroupIdentity]struct{}{}, 0, true
	case !reader.haveSet || setRound != reader.setRound:
		facts.SetRead = true
		set, setErr := runtime.dependencies.Store.ReadAssignedSet(ctx, runtime.dependencies.WorkerID)
		switch {
		case errors.Is(setErr, ownership.ErrAssignedSetAbsent):
			reader.haveSet, facts.Result = false, observability.AssignmentIndexMissing
			return false, nil
		case setErr != nil:
			reader.haveSet, facts.Result = false, observability.AssignmentIndexInvalid
			return false, setErr
		case set.Round < setRound:
			reader.haveSet, facts.Result = false, observability.AssignmentIndexInvalid
			return false, nil
		default:
			candidates := make(map[execution.QueryGroupIdentity]struct{}, len(set.QueryGroups))
			for _, queryGroup := range set.QueryGroups {
				candidates[queryGroup] = struct{}{}
			}
			reader.candidates, reader.setRound, reader.haveSet = candidates, set.Round, true
		}
	}
	return true, nil
}

// readAllAssignments is the path from before the index existed: one record
// per Query Group of the population. It stays the fallback for a round
// without a usable index.
func (runtime *productionPhaseTwoOwnership) readAllAssignments(
	ctx context.Context,
	ordered []execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	assigned := make([]execution.QueryGroupIdentity, 0, len(ordered))
	for _, queryGroup := range ordered {
		record, err := runtime.dependencies.Store.ReadAssignment(ctx, queryGroup)
		if errors.Is(err, ownership.ErrAssignmentAbsent) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.QueryGroup != queryGroup {
			return nil, newPhaseTwoInvariantError("phase-two production Assignment identity mismatch")
		}
		if record.DesiredWorkerID == runtime.dependencies.WorkerID {
			assigned = append(assigned, queryGroup)
		}
	}
	return assigned, nil
}

// AssignedQueryGroups answers which of the given Query Groups this worker
// is the desired owner of. It reads the Assignment index once per round and
// its own set only when the index says the set changed; the index only
// names candidates. A candidate this worker does not yet hold is confirmed
// against its Assignment record before it is returned, and a held Query
// Group the index no longer names is confirmed released against its record
// before it is dropped; a record that disagrees with the index wins. So the
// index decides how many records are read, never what is returned, and an
// unchanged round reads none. Without a usable index every record is read,
// as before the index existed, and the held set is rebuilt from that read.
func (runtime *productionPhaseTwoOwnership) AssignedQueryGroups(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	if runtime == nil {
		return nil, errors.New("phase-two production Assignment reader is not initialized")
	}
	ordered := append([]execution.QueryGroupIdentity(nil), queryGroups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	for index, queryGroup := range ordered {
		if queryGroup == "" || (index > 0 && ordered[index-1] == queryGroup) {
			return nil, newPhaseTwoInvariantError("phase-two production Assignment read contains an invalid Query Group set")
		}
	}
	reader := &runtime.indexReader
	reader.mu.Lock()
	defer reader.mu.Unlock()
	facts := &observability.AssignmentIndexFacts{}
	usable, readErr := runtime.readAssignmentIndex(ctx, reader, facts)
	var result observability.Result = observability.ResultSuccess
	if readErr != nil {
		result = observability.ResultFailed
	}
	if !usable {
		assigned, err := runtime.readAllAssignments(ctx, ordered)
		if err != nil {
			return nil, err
		}
		owned := make(map[execution.QueryGroupIdentity]struct{}, len(assigned))
		for _, queryGroup := range assigned {
			owned[queryGroup] = struct{}{}
		}
		reader.owned = owned
		facts.Assigned, facts.Reads, facts.FullRead = len(assigned), len(ordered), true
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexRead,
			Result: result, Operation: observability.OperationLoad, Err: readErr, AssignmentIndex: facts,
		})
		return assigned, nil
	}
	population := make(map[execution.QueryGroupIdentity]struct{}, len(ordered))
	for _, queryGroup := range ordered {
		population[queryGroup] = struct{}{}
	}
	next := make(map[execution.QueryGroupIdentity]struct{}, len(reader.candidates))
	confirm := func(queryGroup execution.QueryGroupIdentity) (bool, error) {
		facts.Reads++
		record, err := runtime.dependencies.Store.ReadAssignment(ctx, queryGroup)
		switch {
		case errors.Is(err, ownership.ErrAssignmentAbsent):
			return false, nil
		case err != nil:
			return false, err
		case record.QueryGroup != queryGroup:
			return false, newPhaseTwoInvariantError("phase-two production Assignment identity mismatch")
		}
		return record.DesiredWorkerID == runtime.dependencies.WorkerID, nil
	}
	for queryGroup := range reader.candidates {
		if _, inPopulation := population[queryGroup]; !inPopulation {
			continue
		}
		facts.Candidates++
		if _, held := reader.owned[queryGroup]; held {
			next[queryGroup] = struct{}{}
			continue
		}
		mine, err := confirm(queryGroup)
		if err != nil {
			return nil, err
		}
		if mine {
			next[queryGroup] = struct{}{}
			facts.Opened++
		} else {
			facts.Rejected++
		}
	}
	for queryGroup := range reader.owned {
		if _, kept := next[queryGroup]; kept {
			continue
		}
		if _, inPopulation := population[queryGroup]; !inPopulation {
			facts.Released++
			continue
		}
		mine, err := confirm(queryGroup)
		if err != nil {
			return nil, err
		}
		if mine {
			next[queryGroup] = struct{}{}
			facts.Retained++
		} else {
			facts.Released++
		}
	}
	reader.owned = next
	assigned := make([]execution.QueryGroupIdentity, 0, len(next))
	for queryGroup := range next {
		assigned = append(assigned, queryGroup)
	}
	sort.Slice(assigned, func(left, right int) bool { return assigned[left] < assigned[right] })
	facts.Assigned = len(assigned)
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexRead,
		Result: result, Operation: observability.OperationLoad, AssignmentIndex: facts,
	})
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
		case <-ticker.C:
		}
		runtime.mu.Lock()
		authority := runtime.authority
		runtime.mu.Unlock()
		if authority.Fence.QueryGroup == "" {
			return ownership.ErrStaleFence
		}
		var renewed ownership.PublicationAuthority
		err := renewPhaseTwoWithinInterval(ctx, interval, func(attemptCtx context.Context) error {
			var renewErr error
			renewed, renewErr = runtime.dependencies.Store.RenewControlLeader(
				attemptCtx, authority, runtime.dependencies.Now(), ttl,
			)
			return renewErr
		}, func(err error) {
			observeProductionRenewalFailure(ctx, runtime.dependencies.Observer, observability.StageLeaseRenewed, err)
		}, func() bool {
			return authority.Deadline.After(runtime.dependencies.Now())
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isPhaseTwoInvariantError(err) || ownership.IsLeaseDecision(err) ||
				!authority.Deadline.After(runtime.dependencies.Now()) {
				runtime.clearControlAuthority(authority)
				return err
			}
			continue
		}
		observeProductionOwnership(ctx, runtime.dependencies.Observer, observability.StageLeaseRenewed, nil)
		runtime.mu.Lock()
		if runtime.authority.Fence == authority.Fence {
			runtime.authority = renewed
		}
		runtime.mu.Unlock()
	}
}

func (runtime *productionPhaseTwoOwnership) clearControlAuthority(authority ownership.PublicationAuthority) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.authority.Fence == authority.Fence {
		runtime.authority = ownership.PublicationAuthority{}
		runtime.viewStepDown()
		// The leader-round readings go with the role. They are aggregated
		// across replicas with max, so a replica that stopped leading and
		// kept its last reading outranks the Leader that has one.
		if steppedDown := runtime.dependencies.SteppedDownAsLeader; steppedDown != nil {
			steppedDown()
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
	var catalog productionPhaseTwoSlotCatalog = runtime.dependencies.Catalog
	var executor scheduler.Executor = runtime.dependencies.Executor
	release := func() {}
	if runtime.viewGate != nil {
		// The early renewal the gate makes when the view is ahead of the
		// lease: the same renewal the session's maintenance makes on its
		// interval, at this moment instead.
		renew := func(ctx context.Context) error { return session.Renew(ctx, runtime.dependencies.Now(), ttl) }
		catalog = &viewGatedCatalog{next: catalog, gate: runtime.viewGate, queryGroup: queryGroup, session: session, renew: renew}
		// Inside the observed executor, so a refusal at execution is a
		// slot_completed line with the gate's word like any other outcome.
		executor = &viewGatedExecutor{next: executor, gate: runtime.viewGate, queryGroup: queryGroup, session: session, renew: renew}
		release = func() { runtime.viewGate.forget(queryGroup) }
	}
	executor = &observedProductionSlotExecutor{next: executor, observer: runtime.dependencies.Observer}
	source, err := scheduler.NewProductionSlotSource(
		queryGroup, runtime.dependencies.WorkerID, session,
		catalog, runtime.dependencies.Progress, runtime.dependencies.Now,
		scheduler.WithRecoveryLimits(runtime.dependencies.RecoveryLimits),
		scheduler.WithPostRecoveryTerminalDelay(runtime.dependencies.PostRecoveryTerminalDelay),
		scheduler.WithQueryDeadlineReserve(runtime.dependencies.QueryDeadlineReserve),
		scheduler.WithSettlingWait(runtime.dependencies.SettlingWait),
		scheduler.WithSnapshotRetention(runtime.dependencies.SnapshotRetention, runtime.dependencies.PublicationDelayAllowance),
		scheduler.WithExpiredRangeCreation(runtime.dependencies.ExpiredRangeEnabled),
		scheduler.WithObserver(runtime.dependencies.Observer),
	)
	if err != nil {
		_ = session.Release(ctx)
		return nil, err
	}
	observedSource := &observedProductionSlotSource{next: source, observer: runtime.dependencies.Observer}
	runner, err := scheduler.NewRunner(
		queryGroup, session, observedSource, executor, runtime.flights, runtime.dependencies.Now,
	)
	if err != nil {
		_ = session.Release(ctx)
		return nil, err
	}
	if runtime.dependencies.QueryCooldowns != nil {
		runner.WithQueryCooldownStore(runtime.dependencies.QueryCooldowns)
	}
	return &productionPhaseTwoQueryGroup{
		session: session, runner: runner, observer: runtime.dependencies.Observer, now: runtime.dependencies.Now,
		release: release, flights: runtime.flights, queryGroup: queryGroup, viewGate: runtime.viewGate,
	}, nil
}

// WithViewExecutionGate makes every Query Group opened from now on read the
// control plane through the gate.
func (runtime *productionPhaseTwoOwnership) WithViewExecutionGate(gate *viewExecutionGate) {
	if runtime != nil {
		runtime.viewGate = gate
	}
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
	runtime.viewLead(authority.Fence.OwnerEpoch)
	return authority, nil
}

type productionPhaseTwoQueryGroup struct {
	flights    *scheduler.FlightCoordinator
	queryGroup execution.QueryGroupIdentity
	viewGate   *viewExecutionGate
	session    *ownership.Session
	runner     *scheduler.Runner
	observer   observability.Observer
	now        func() time.Time
	// release is what letting the Query Group go must also do: the view gate
	// forgets it, so it counts neither as executed from the view nor as
	// short of it.
	release func()
}

type observedProductionSlotSource struct {
	next     scheduler.SlotSource
	observer observability.Observer
}

func (source observedProductionSlotSource) RangeCreationEnabled() bool {
	next, ok := source.next.(interface{ RangeCreationEnabled() bool })
	return ok && next.RangeCreationEnabled()
}

func (source observedProductionSlotSource) Next(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
	defer startSlotTiming(ctx, source.observer, observability.StageSlotSourceCompleted, time.Now)()
	// One Slot decision reads the activation header several times over. Scoping
	// it to this call reads it live once and reuses it, so a publication is
	// still observed on the next call while every read inside this one observes
	// the same control version.
	ctx = controlplane.WithControlVersionScope(ctx)
	slot, due, facts, err := source.next.Next(ctx, queryGroup)
	var retry *scheduler.SourceRetryError
	var blocked *scheduler.SourceBlockedError
	var notExecutable *scheduler.ViewNotExecutableError
	if errors.As(err, &notExecutable) {
		// The view did not allow the round (decision-016 batch 4b). The
		// line carries the gate's own word for which of its checks failed;
		// the fleet reads the reason code, and a Query Group refused on
		// every round is a blocked run under it.
		observeRuntime(ctx, source.observer, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
			Result: observability.Result(observability.ResultRetrying), ReasonCode: observability.ReasonCode(contract.ReasonViewNotExecutable),
			Direction: observability.DirectionInternal,
			Trace:     observability.TraceFields{QueryGroupKey: string(queryGroup)}, Err: notExecutable,
		})
	} else if errors.As(err, &retry) || errors.As(err, &blocked) {
		reason := observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable)
		var cause error
		if retry != nil {
			reason = observability.ReasonCode(contract.ReasonSlotSourceRetry)
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
	return slot, due, facts, err
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
	if err == nil && result.Completed && result.CompletionKind != "" && observability.IsShortPeriodCohort(request.ShortPeriodCohort) {
		shortCompletion = &observability.ShortPeriodCompletionFacts{Cohort: request.ShortPeriodCohort, CompletionKind: string(result.CompletionKind),
			LagSeconds: time.Since(time.Unix(int64(request.Contract.Slot.EvaluationTime), 0)).Seconds(), AttemptNo: request.AttemptNo}
	}
	// What held the round before this one, on the completions where that is
	// the question. Any cohort: it used to ride inside the short-period
	// bundle, and the Query Groups on sixty seconds and slower -- the bulk of
	// the ones whose Slots are being skipped -- have no such bundle, so their
	// completion lines named the outcome and never the cause.
	var heldBy *observability.HeldByFacts
	var gapApplySite string
	if result.CompletionKind == execution.CompletionGapSkipped || request.ReplayExpired ||
		result.ReasonCode == execution.ReasonCode(contract.ReasonGapSkipped) {
		// The reason is asked as well as the kind, because the line reports the
		// reason and the two do not have to agree.
		//
		// A Slot that ran - queried, evaluated, wrote its state - and whose
		// Level outcomes are all UNKNOWN completes COMPLETED_WITH_UNAVAILABLE
		// and copies GAP_SKIPPED up from the Level into the reason, while its
		// completion kind is whatever the run produced (read on a deployment:
		// a warming Query Group, 249 Levels UNKNOWN, every round shaped so).
		// Gating only on the kind left exactly that population without a
		// cause field: a Query Group skipping every round showed GAP_SKIPPED
		// and nothing about why, which is the reading this field was added
		// to provide. A cause that is absent precisely in the state it exists
		// to explain is worse than no field, because its absence cannot be
		// told from a build that does not report it.
		heldBy = scheduler.HeldByFromContext(ctx)
	}
	observedResult := result.Result
	reason := result.ReasonCode
	observedErr := err
	var notExecutable *scheduler.ViewNotExecutableError
	if err != nil {
		if errors.As(err, &notExecutable) {
			// The view did not allow the round at execution (decision-016
			// batch 4b): retrying by name, with the gate's word, not a
			// failure of this deployment.
			observedResult = observability.ResultRetrying
			reason = observability.ReasonCode(contract.ReasonViewNotExecutable)
		} else if _, deferred := access.ReadinessDeferredAt(err); deferred {
			// The Slot's data is not in yet: the normal pacing of every Slot,
			// and the single highest-volume completion line alarmd writes. The
			// query stage already names it QUERY_NOT_READY; this line said
			// nothing, and an empty reason on a retrying result normalizes to
			// reason_not_reported -- the word reserved for a site that failed
			// to report -- so the most common line in the log, and the
			// slot_completed reason label with it, read as a defect at this
			// site on every round of every Query Group.
			observedResult = observability.ResultRetrying
			reason = observability.ReasonCode(contract.ReasonQueryNotReady)
			observedErr = nil
		} else {
			observedResult = observability.ResultFailed
			reason, gapApplySite = slotFailureReason(err)
		}
	} else if observedResult == "" {
		observedResult = observability.ResultSuccess
	}
	observeRuntime(ctx, executor.observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Operation: observability.Operation(request.Operation), ShortPeriodCompletion: shortCompletion,
		HeldBy: heldBy, GapApplySite: gapApplySite,
		// The completion the Slot reached, beside the reason it reports. They
		// are separate fields and disagree in the case this line is hardest to
		// read: a Slot whose Level outcomes are UNKNOWN completes
		// COMPLETED_WITH_UNAVAILABLE and copies GAP_SKIPPED up from the Level,
		// which is indistinguishable on the reason alone from a Slot that was
		// given up on before it ran.
		SlotCompletionKind: string(result.CompletionKind),
		ExecuteOutcome:     executeReturnOutcome(result, err),
		Result:             observedResult, ReasonCode: reason, Direction: observability.DirectionInternal,
		Duration: time.Since(started), Trace: trace, Err: observedErr,
		SlotBudgetUsage: slotBudgetUsageFacts(result.Usage),
		SlotTiming:      slotTimingFacts(result.Timing),
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
	return runtime.runner.RunOne(ctx)
}

func (runtime *productionPhaseTwoQueryGroup) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	return runtime.runner.RunOneAdmitted(ctx, admission)
}

func (runtime *productionPhaseTwoQueryGroup) NextReadyAt() time.Time {
	if runtime == nil || runtime.runner == nil {
		return time.Time{}
	}
	return runtime.runner.NextReadyAt()
}

func (runtime *productionPhaseTwoQueryGroup) DueBound() scheduler.RunnerDueBound {
	if runtime == nil || runtime.runner == nil {
		return scheduler.RunnerDueBound{}
	}
	return runtime.runner.DueBound()
}

func (runtime *productionPhaseTwoQueryGroup) NextDeadline() time.Time {
	if runtime == nil || runtime.runner == nil {
		return time.Time{}
	}
	return runtime.runner.NextDeadline()
}

// MaintainLease renews the Query Group lease every interval. A failure to
// reach the Ownership Store is retried inside the interval and again on the
// following ticks for as long as the lease is still inside its TTL; the
// Session keeps validating every side effect against the store meanwhile.
// The error is returned, and the Query Group reported lost by the caller,
// only when the store answers that the fence is stale, when the TTL has run
// out, on an invariant error or on cancellation.
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
		case <-ticker.C:
		}
		err := renewPhaseTwoWithinInterval(ctx, interval, func(attemptCtx context.Context) error {
			return runtime.session.Renew(attemptCtx, runtime.clock(), ttl)
		}, func(err error) {
			observeProductionRenewalFailure(ctx, runtime.observer, observability.StageLeaseRenewed, err)
		}, func() bool {
			return runtime.session.Deadline().After(runtime.clock())
		})
		if err == nil {
			observeProductionOwnership(ctx, runtime.observer, observability.StageLeaseRenewed, nil)
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isPhaseTwoInvariantError(err) || ownership.IsLeaseDecision(err) {
			observeProductionOwnership(ctx, runtime.observer, observability.StageLeaseRenewed, err)
			return err
		}
		if !runtime.session.Deadline().After(runtime.clock()) {
			return err
		}
	}
}

func (runtime *productionPhaseTwoQueryGroup) clock() time.Time {
	if runtime.now == nil {
		return time.Now()
	}
	return runtime.now()
}

func (runtime *productionPhaseTwoQueryGroup) Release(ctx context.Context) error {
	if runtime.release != nil {
		runtime.release()
	}
	return runtime.session.Release(ctx)
}

// observeProductionRenewalFailure reports one failed renewal attempt that is
// going to be retried. It carries the shared retryable dependency reason so
// the bounded log policy folds repeats into one limited bucket.
func observeProductionRenewalFailure(
	ctx context.Context,
	observer observability.Observer,
	stage observability.Stage,
	err error,
) {
	observeRuntime(ctx, observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: stage, Result: observability.ResultFailed,
		Direction: observability.DirectionInternal, ReasonCode: phaseTwoControlDependencyReason, Err: err,
	})
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
	if err != nil {
		result = observability.ResultFailed
	}
	observeRuntime(ctx, observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: stage, Result: result,
		Direction: observability.DirectionInternal, ReasonCode: ownershipObservationReason(err), Err: err,
	})
}

func executeReturnOutcome(result execution.SlotExecutionResult, err error) string {
	if err != nil {
		if _, ok := access.ReadinessDeferredAt(err); ok {
			return "readiness_deferred"
		}
		var notExecutable *scheduler.ViewNotExecutableError
		if errors.As(err, &notExecutable) {
			return "view_not_executable"
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

func (acquirer productionQueryPermitAcquirer) AcquireRecoveryChannels(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time, maximum int, beforeWait func()) (access.RecoveryChannels, error) {
	channels, err := acquirer.flights.AcquireRecoveryChannels(ctx, slot, operation, deadline, maximum, beforeWait)
	if err != nil {
		return nil, err
	}
	return productionRecoveryChannels{channels}, nil
}

type productionRecoveryChannels struct{ channels *scheduler.RecoveryChannels }

func (channels productionRecoveryChannels) Release() { channels.channels.Release() }
func (channels productionRecoveryChannels) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time) (access.QueryPermit, error) {
	return channels.channels.AcquireQueryPermit(ctx, slot, operation, deadline)
}

// Phase-two diagnostic log budget: per (reason or stage, Query Group) bucket,
// with at most phaseTwoDiagnosticLogMaxScopes live scope buckets. Suppressed
// lines are counted and reported on the next admitted line of the same bucket.
//
// Opened up for the development phase. At one line per minute per bucket and
// 4096 scopes against 2075 objects, a live read dropped 78 lines for every line
// it kept -- 186,966 evicted in 19 seconds on one replica at its worst. The
// eviction count rides on the emitted lines, so the only way to learn how much
// was lost is to read the log, which is the thing being lost.
//
// That is the wrong trade while the system is still being built: what these
// logs are for right now is finding out what alarmd actually does, and a
// budget tuned for a steady production stream answers that question with a
// sample whose bias is the very repetition being investigated.
//
// MaxScopes covers the fleet with room for several concurrent reasons per
// object, so scope eviction -- the part that loses lines without any per-bucket
// summary -- stops being the binding constraint.
//
// Exit condition: this goes back to a production budget when the system is
// stable enough that the repeated lines are noise rather than the subject.
// Until then a dropped line costs more than a written one.
const (
	phaseTwoDiagnosticLogWindow    = time.Minute
	phaseTwoDiagnosticLogMaxEvents = 600
	phaseTwoDiagnosticLogMaxScopes = 65536
)

// newPhaseTwoRuntimeObserver mirrors newPhaseOneRuntimeObserver but uses the
// scoped limiter so one noisy Query Group cannot hide every other Query
// Group's diagnostics behind a per-reason budget.
func newPhaseTwoRuntimeObserver(recorder *metric.Recorder, logger *observability.Logger) (observability.Observer, error) {
	if recorder == nil || logger == nil {
		return nil, errors.New("alarmd runtime: recorder and logger are required")
	}
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{
		Window: phaseTwoDiagnosticLogWindow, MaxEvents: phaseTwoDiagnosticLogMaxEvents, MaxScopes: phaseTwoDiagnosticLogMaxScopes,
	})
	if err != nil {
		return nil, err
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		return nil, err
	}
	return observability.Multi(recorder, observability.NewLoggingObserver(logger, policy)), nil
}

// publishedComposition is what a round hands the catalog gauges.
//
// A composed Catalog is told from an absent one by its maps: ComposeCatalog
// pre-creates every partition it publishes, so a round that did not get that
// far leaves them nil rather than empty. That matters because an absent
// composition and one full of zeros read the same on a scrape.
func publishedComposition(
	refreshErr error,
	composition controlplane.CatalogComposition,
	_ phaseTwoControlRefreshStatus,
) *controlplane.CatalogComposition {
	if refreshErr != nil || composition.Objects == nil {
		return nil
	}
	return &composition
}

// slotBudgetUsageFacts pairs what a Slot used with the budgets it was admitted
// against. Both halves arrive from the execution that produced them, so the
// row is readable against the numbers that were in force when it was written
// rather than against a configuration a reader looks up later.
func slotBudgetUsageFacts(usage execution.SlotBudgetUsage) *observability.SlotBudgetUsageFacts {
	return &observability.SlotBudgetUsageFacts{
		StateMutations: usage.StateMutations, GapMutations: usage.GapMutations, Events: usage.Events,
		EventsWithoutMessage: usage.EventsWithoutMessage,
		RetainedBytes:        usage.RetainedBytes, Series: usage.Series,
		RetainedInputBytes: usage.RetainedInputBytes, RetainedGapBytes: usage.RetainedGapBytes,
		RetainedOutputBytes: usage.RetainedOutputBytes,
		RetainedStateBytes:  usage.RetainedStateBytes,
		StateMutationsLimit: usage.StateMutationsLimit, GapMutationsLimit: usage.GapMutationsLimit,
		EventsLimit: usage.EventsLimit, RetainedBytesLimit: usage.RetainedBytesLimit, SeriesLimit: usage.SeriesLimit,
		RetainedShareBytes: usage.RetainedShareBytes,
	}
}

// slotTimingFacts is where the Slot's clock went, for its completion row.
func slotTimingFacts(timing execution.SlotTiming) *observability.SlotTimingFacts {
	return &observability.SlotTimingFacts{
		Slot: timing.Slot, Input: timing.Input, Preflight: timing.Preflight, Evaluate: timing.Evaluate,
	}
}

// ReleaseControlLeader gives up the Control Leader lease this process holds,
// so the next leader is elected now rather than when the lease expires. It is
// called at shutdown only once every leader task has stopped; see Shutdown.
// Not holding the lease, or finding it no longer this process's, is not an
// error: there is nothing to give up.
func (runtime *productionPhaseTwoOwnership) ReleaseControlLeader(ctx context.Context) error {
	if runtime == nil || runtime.dependencies.Store == nil {
		return nil
	}
	runtime.mu.Lock()
	authority := runtime.authority
	runtime.mu.Unlock()
	if authority.Fence.QueryGroup == "" {
		return nil
	}
	err := runtime.dependencies.Store.Release(ctx, authority.Fence)
	runtime.clearControlAuthority(authority)
	if errors.Is(err, ownership.ErrStaleFence) {
		return nil
	}
	return err
}
