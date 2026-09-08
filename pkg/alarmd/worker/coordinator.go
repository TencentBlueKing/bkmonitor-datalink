// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package worker contains the single in-process phase-two execution
// coordinator. It is not a service and owns no retry queue or dependency
// implementation.
package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type Ports struct {
	Finalization execution.QueryFreeFinalizationSource
	Activation   execution.PlanActivationSource
	Query        execution.QueryExecutionSource
	Sequencer    execution.SideEffectSequencer
	Evaluator    execution.Evaluator
	Admission    execution.SideEffectAdmitter
	GapGuard     execution.GapGuardStore
	Events       execution.EventSink
	State        execution.StateStore
	Progress     execution.ProgressStore
	Observer     execution.Observer
}

type SlotExecutionCoordinator struct {
	ports        Ports
	budget       ProvisionalBudget
	reservations processProvisionalReservations
}

type processProvisionalReservations struct {
	mu            sync.Mutex
	series        uint64
	retainedBytes uint64
	states        uint64
	events        uint64
	gaps          uint64
	gapFacts      uint64
}

type ProvisionalBudget struct {
	MaxSeries         uint64
	MaxRetainedBytes  uint64
	MaxStateMutations uint64
	MaxEvents         uint64
	MaxGapMutations   uint64
	// StoreMaxItems is the State store's per-call item bound. One Plan's
	// State or Gap mutations beyond it are applied in successive calls of at
	// most this size, up to execution.StateApplyMaxChunks calls, which also
	// caps what one Slot may produce (see slotBudget). Zero means the store
	// has no bound below the process budget.
	StoreMaxItems uint64
}

type activationProtectionRequiredError struct {
	activationRequest execution.PlanActivationRequest
	currentFacts      execution.PlanActivationResult
	activations       execution.PlanActivationResult
	completion        execution.SlotCompletion
}

func (*activationProtectionRequiredError) Error() string {
	return "alarmd worker: changed activation requires Guard protection"
}

func NewSlotExecutionCoordinator(ports Ports, budget ProvisionalBudget) (*SlotExecutionCoordinator, error) {
	if ports.Finalization == nil || ports.Activation == nil || ports.Query == nil || ports.Sequencer == nil ||
		ports.Evaluator == nil || ports.Admission == nil || ports.GapGuard == nil ||
		ports.Events == nil || ports.State == nil || ports.Progress == nil || ports.Observer == nil {
		return nil, errors.New("alarmd worker: all C0 execution ports are required")
	}
	if budget.MaxSeries == 0 || budget.MaxRetainedBytes == 0 || budget.MaxStateMutations == 0 ||
		budget.MaxEvents == 0 || budget.MaxGapMutations == 0 {
		return nil, errors.New("alarmd worker: positive process provisional budgets are required")
	}
	return &SlotExecutionCoordinator{ports: ports, budget: budget}, nil
}

func (coordinator *SlotExecutionCoordinator) acquireProvisional(series, retainedBytes uint64, stream *streamedExecution, phase string) error {
	coordinator.reservations.mu.Lock()
	defer coordinator.reservations.mu.Unlock()
	if series > coordinator.budget.MaxSeries-coordinator.reservations.series {
		return budgetRejection(observability.CapacityBudgetSeries, phase, coordinator.reservations.series, series, coordinator.budget.MaxSeries, stream.ownBudget(observability.CapacityBudgetSeries))
	}
	if retainedBytes > coordinator.budget.MaxRetainedBytes-coordinator.reservations.retainedBytes {
		return budgetRejection(observability.CapacityBudgetRetainedBytes, phase, coordinator.reservations.retainedBytes, retainedBytes, coordinator.budget.MaxRetainedBytes, stream.ownBudget(observability.CapacityBudgetRetainedBytes))
	}
	coordinator.reservations.series += series
	coordinator.reservations.retainedBytes += retainedBytes
	return nil
}

func (coordinator *SlotExecutionCoordinator) releaseProvisional(series, retainedBytes uint64) {
	coordinator.reservations.mu.Lock()
	coordinator.reservations.series -= series
	coordinator.reservations.retainedBytes -= retainedBytes
	coordinator.reservations.mu.Unlock()
}

// Execute performs one already-scheduled attempt. normal, retry, replay and
// probe differ only by request.Operation; retry policy and queues stay outside
// this single completion path.
func (coordinator *SlotExecutionCoordinator) Execute(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	if coordinator == nil {
		return execution.SlotExecutionResult{}, errors.New("alarmd worker: initialized coordinator is required")
	}
	if err := ctx.Err(); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if err := request.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid execution request: %w", err)
	}
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{
		QueryGroupKey:        string(request.Contract.Slot.QueryGroup),
		SnapshotRevision:     string(request.Contract.SnapshotRevision),
		QueryRevision:        string(request.Contract.QueryRevision),
		ScheduleRevision:     string(request.Contract.ScheduleRevision),
		ScheduleSegmentStart: int64(request.Contract.ScheduleSegmentStart),
		DuePlanSetDigest:     string(request.Contract.DuePlanSetDigest),
		OwnerID:              request.OwnerFence.OwnerID, OwnerEpoch: request.OwnerFence.OwnerEpoch,
		EvaluationTime: int64(request.Contract.Slot.EvaluationTime),
	})
	if request.ExpiredRange != nil {
		return coordinator.executeExpiredRange(ctx, request)
	}
	beginStarted := time.Now()
	begin, err := coordinator.ports.Progress.BeginSlot(ctx, execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, Projection: request.UnfinishedProjection(),
	})
	if err != nil {
		return coordinator.progressBeginFailure(ctx, request, beginStarted, err), nil
	}
	if err := begin.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid Progress BeginSlot result: %w", err)
	}
	if begin.Status != execution.ProgressCommitted {
		reason := begin.ReasonCode
		if reason == "" {
			reason = execution.ReasonCode(contract.ReasonProgressBeginRejected)
		}
		return activationRetry(reason), nil
	}
	finalization, err := coordinator.ports.Finalization.ResolveFinalization(ctx, request)
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: resolve finalization: %w", err)
	}
	if err := finalization.Validate(request); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid finalization: %w", err)
	}
	if finalization.Mode == execution.FinalizationSnapshotRetry || finalization.Mode == execution.FinalizationExactSetBlocked {
		return activationRetry(finalization.ReasonCode), nil
	}
	if finalization.Mode != execution.FinalizationQueryRequired {
		return coordinator.executeQueryFreeFinalization(ctx, request, finalization)
	}
	started := time.Now()
	stream := &streamedExecution{coordinator: coordinator, request: request}
	defer stream.releaseProvisional()
	queryRequest := execution.QueryExecutionRequest{
		Contract: request.Contract, Operation: request.Operation, AttemptNo: request.AttemptNo,
	}
	completion, err := coordinator.ports.Query.Execute(ctx, queryRequest, stream)
	if err != nil {
		if isReadinessDeferred(err) {
			coordinator.observe(ctx, observability.ComponentAccess, observability.StageQueryCompleted, request.Operation,
				started, observability.ResultRetrying, observability.ReasonNone, nil)
		} else {
			coordinator.observeQueryFailure(ctx, request.Operation, started, "execute", err)
		}
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: query: %w", err)
	}
	ctx, cancelCompletion := shortPeriodCompletionContext(ctx, request.Operation, stream.header)
	defer cancelCompletion()
	if err := ctx.Err(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: completion deadline: %w", err)
	}
	if err := stream.complete(ctx, completion); err != nil {
		coordinator.observeQueryFailure(ctx, request.Operation, started, "stream_complete", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid query result: %w", err)
	}
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.InputCompleted != nil {
			c.InputCompleted(completion)
		}
	})
	queryResult, queryReason := provisionalResult(stream.evaluated)
	coordinator.observeQueryCompleted(ctx, request.Operation, started, queryResult, queryReason, completion)
	if len(stream.evaluated.Plans) == 0 {
		return execution.SlotExecutionResult{Result: queryResult, ReasonCode: queryReason}, nil
	}

	var result execution.SlotExecutionResult
	err = coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, stream.stateItems, stream.gapItems), func(sequenceCtx context.Context) error {
		var executeErr error
		result, executeErr = coordinator.finalizePreparedWithGaps(
			sequenceCtx, request, stream.header, stream.bindings, stream.state, stream.gaps, stream.evaluated,
		)
		return executeErr
	})
	if err != nil {
		var protection *activationProtectionRequiredError
		if errors.As(err, &protection) {
			result, convergeErr := coordinator.convergeNormalActivation(ctx, request, *protection)
			if convergeErr != nil {
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: converge changed activation: %w", convergeErr)
			}
			return result, nil
		}
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: execute frozen Slot: %w", err)
	}
	return result, nil
}

func isReadinessDeferred(err error) bool {
	var deferred interface{ ReadinessReadyAt() time.Time }
	return errors.As(err, &deferred) && !deferred.ReadinessReadyAt().IsZero()
}

func (coordinator *SlotExecutionCoordinator) executeQueryFreeFinalization(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	finalization execution.QueryFreeFinalization,
) (execution.SlotExecutionResult, error) {
	owner := &streamedExecution{coordinator: coordinator, request: request}
	defer owner.releaseProvisional()
	if err := owner.retainTargets(ctx, len(finalization.Targets.Plans), finalization.Targets.Plans); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	plans := append([]execution.PlanIdentity(nil), finalization.Targets.Plans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left], plans[right]) })
	activationRequest := execution.PlanActivationRequest{Contract: request.Contract, Plans: plans}
	guardFacts, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}

	var result execution.SlotExecutionResult
	var changedActivations *execution.PlanActivationResult
	err = coordinator.ports.Sequencer.Sequence(ctx, activatedPlanSequencingScope(request.Contract.Slot, guardFacts), func(sequenceCtx context.Context) error {
		if _, err := coordinator.ensureActivatedPlanGaps(
			sequenceCtx,
			request,
			finalization.ReasonCode,
			guardFacts,
			finalization.Mode == execution.FinalizationSnapshotUnavailable,
		); err != nil {
			return err
		}
		progressFacts, err := coordinator.loadActivations(sequenceCtx, activationRequest)
		if err != nil {
			result = activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed))
			return nil
		}
		if !guardFacts.SameSelections(progressFacts) {
			changed := changedSelectedActivations(guardFacts, progressFacts)
			changedActivations = &changed
			result = activationRetry(execution.ReasonCode(contract.ReasonConfigDrift))
			return nil
		}
		if err := coordinator.admitActivatedPlans(sequenceCtx, request, progressFacts); err != nil {
			return err
		}
		completionKind := execution.CompletionSnapshotUnavailable
		if finalization.Mode == execution.FinalizationGapSkipped {
			completionKind = execution.CompletionGapSkipped
		}
		result, err = coordinator.commitProgress(sequenceCtx, request, execution.SlotCompletion{
			Contract: request.Contract, Kind: completionKind,
			Result: observability.ResultDegraded, ReasonCode: finalization.ReasonCode,
		})
		return err
	})
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: finalize query-free Slot: %w", err)
	}
	if changedActivations != nil {
		if err := coordinator.protectActivatedPlanGaps(
			ctx,
			request,
			finalization.ReasonCode,
			*changedActivations,
			finalization.Mode == execution.FinalizationSnapshotUnavailable,
		); err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: protect changed query-free activation: %w", err)
		}
	}
	return result, nil
}

func (coordinator *SlotExecutionCoordinator) loadActivations(
	ctx context.Context,
	request execution.PlanActivationRequest,
) (execution.PlanActivationResult, error) {
	result, err := coordinator.ports.Activation.LoadActivations(ctx, request)
	if err == nil {
		err = result.Validate(request)
	}
	return result, err
}

func duePlanActivationRequest(
	contractRef execution.FrozenExecutionContractRef,
	duePlans []execution.DuePlan,
) execution.PlanActivationRequest {
	plans := make([]execution.PlanIdentity, len(duePlans))
	for index, plan := range duePlans {
		plans[index] = plan.Identity
	}
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left], plans[right]) })
	return execution.PlanActivationRequest{Contract: contractRef, Plans: plans}
}

// progressBeginFailure observes a BeginSlot error with its text under the Slot
// coordinates already attached to ctx and keeps the Slot retrying. A
// deterministic cause (persisted-fact, validation or identity error) is
// reported as PROGRESS_BEGIN_FAILED; a transport or context failure keeps the
// retryable PROGRESS_BEGIN_REJECTED. Both reasons are observation-only.
func (coordinator *SlotExecutionCoordinator) progressBeginFailure(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	started time.Time,
	err error,
) execution.SlotExecutionResult {
	reason := execution.ReasonCode(contract.ReasonProgressBeginFailed)
	var deterministic interface{ DeterministicControlFact() }
	var transport net.Error
	if !errors.As(err, &deterministic) &&
		(errors.As(err, &transport) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		reason = execution.ReasonCode(contract.ReasonProgressBeginRejected)
	}
	coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted,
		request.Operation, started, "", observability.ReasonCode(reason), err)
	return activationRetry(reason)
}

func activationRetry(reason execution.ReasonCode) execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: false, Result: observability.ResultRetrying, ReasonCode: reason}
}

func changedSelectedActivations(
	before execution.PlanActivationResult,
	after execution.PlanActivationResult,
) execution.PlanActivationResult {
	changed := execution.PlanActivationResult{Contract: after.Contract}
	for _, fact := range after.Facts {
		previous, found := before.Find(fact.Plan)
		if found && previous.Equal(fact) {
			continue
		}
		if fact.Selection != execution.ActivationNone {
			changed.Facts = append(changed.Facts, fact)
		}
	}
	return changed
}

func changedDuePlanActivations(
	duePlans []execution.DuePlan,
	activations execution.PlanActivationResult,
) (map[execution.PlanIdentity]struct{}, execution.PlanActivationResult) {
	changedPlans := make(map[execution.PlanIdentity]struct{})
	changedSelected := execution.PlanActivationResult{Contract: activations.Contract}
	for _, due := range duePlans {
		fact, found := activations.Find(due.Identity)
		if found && fact.Selection != execution.ActivationNone &&
			fact.Selected.Identity == due.Identity &&
			fact.Selected.StateGeneration == due.StateGeneration &&
			fact.Selected.StateApplyEpoch == due.StateApplyEpoch &&
			fact.Selected.ScheduleRevision == due.ScheduleRevision {
			continue
		}
		changedPlans[due.Identity] = struct{}{}
		if found && fact.Selection != execution.ActivationNone {
			changedSelected.Facts = append(changedSelected.Facts, fact)
		}
	}
	return changedPlans, changedSelected
}

func (coordinator *SlotExecutionCoordinator) admitActivatedPlans(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	activations execution.PlanActivationResult,
) error {
	facts := append([]execution.PlanActivationFact(nil), activations.Facts...)
	sort.Slice(facts, func(left, right int) bool { return lessPlanIdentity(facts[left].Plan, facts[right].Plan) })
	for _, fact := range facts {
		if fact.Selection == execution.ActivationNone {
			continue
		}
		if err := coordinator.admitPlan(ctx, request, fact.Plan, fact.Selected.StateApplyEpoch); err != nil {
			return err
		}
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) admitDuePlans(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	duePlans []execution.DuePlan,
) error {
	plans := append([]execution.DuePlan(nil), duePlans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left].Identity, plans[right].Identity) })
	for _, plan := range plans {
		if err := coordinator.admit(ctx, request, plan); err != nil {
			return err
		}
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) protectActivatedPlanGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	reason execution.ReasonCode,
	activations execution.PlanActivationResult,
	reuseSufficientQueryFreeProtection bool,
) error {
	return coordinator.ports.Sequencer.Sequence(
		ctx,
		activatedPlanSequencingScope(request.Contract.Slot, activations),
		func(sequenceCtx context.Context) error {
			_, err := coordinator.ensureActivatedPlanGaps(
				sequenceCtx,
				request,
				reason,
				activations,
				reuseSufficientQueryFreeProtection,
			)
			return err
		},
	)
}

func (coordinator *SlotExecutionCoordinator) convergeNormalActivation(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	protection activationProtectionRequiredError,
) (execution.SlotExecutionResult, error) {
	result := activationRetry(execution.ReasonCode(contract.ReasonConfigDrift))
	var changedActivations *execution.PlanActivationResult
	err := coordinator.ports.Sequencer.Sequence(
		ctx,
		activatedPlanSequencingScope(request.Contract.Slot, protection.activations),
		func(sequenceCtx context.Context) error {
			alreadyProtected, err := coordinator.ensureActivatedPlanGaps(
				sequenceCtx,
				request,
				execution.ReasonCode(contract.ReasonConfigDrift),
				protection.activations,
				false,
			)
			if err != nil || !alreadyProtected {
				return err
			}
			progressFacts, err := coordinator.loadActivations(sequenceCtx, protection.activationRequest)
			if err != nil {
				result = activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed))
				return nil
			}
			if !protection.currentFacts.SameSelections(progressFacts) {
				changed := changedSelectedActivations(protection.currentFacts, progressFacts)
				changedActivations = &changed
				return nil
			}
			if err := coordinator.admitActivatedPlans(sequenceCtx, request, progressFacts); err != nil {
				return err
			}
			result, err = coordinator.commitProgress(sequenceCtx, request, protection.completion)
			return err
		},
	)
	if err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if changedActivations != nil {
		if err := coordinator.protectActivatedPlanGaps(
			ctx,
			request,
			execution.ReasonCode(contract.ReasonConfigDrift),
			*changedActivations,
			false,
		); err != nil {
			return execution.SlotExecutionResult{}, err
		}
	}
	return result, nil
}

func (coordinator *SlotExecutionCoordinator) ensureActivatedPlanGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	reason execution.ReasonCode,
	activations execution.PlanActivationResult,
	reuseSufficientQueryFreeProtection bool,
) (bool, error) {
	owner := &streamedExecution{coordinator: coordinator, request: request}
	defer owner.releaseProvisional()
	if err := owner.retainTargets(ctx, len(activations.Facts), activations); err != nil {
		return false, err
	}
	items := make([]execution.PlanGapLoadItem, 0, len(activations.Facts))
	for _, fact := range activations.Facts {
		if fact.Selection == execution.ActivationNone {
			continue
		}
		version, err := execution.BuildApplyVersion(request.Contract, fact.Selected.StateApplyEpoch)
		if err != nil {
			return false, fmt.Errorf("alarmd worker: build activated Plan ApplyVersion: %w", err)
		}
		items = append(items, execution.PlanGapLoadItem{
			Identity:     execution.PlanGapIdentity{Plan: fact.Plan, StateGeneration: fact.Selected.StateGeneration},
			ApplyVersion: version, ScheduleRevision: fact.Selected.ScheduleRevision,
		})
	}
	if len(items) == 0 {
		return true, nil
	}
	sort.Slice(items, func(left, right int) bool {
		return lessPlanIdentity(items[left].Identity.Plan, items[right].Identity.Plan)
	})
	if err := coordinator.admitActivatedPlans(ctx, request, activations); err != nil {
		return false, err
	}
	started := time.Now()
	loadRequest := execution.GapLoadRequest{Contract: request.Contract, Items: items}
	loaded, err := owner.loadGapFacts(ctx, loadRequest)
	if err == nil {
		err = execution.ValidateGapLoad(loadRequest, loaded)
	}
	gapResult, gapReason := summarizeGapLoad(loaded)
	coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapLoaded, request.Operation, started,
		gapResult, gapReason, observability.Counts{Keys: int64(len(loaded.Items))}, err)
	if err != nil {
		return false, fmt.Errorf("alarmd worker: activated Plan gap preflight: %w", err)
	}

	selected := make(map[execution.PlanIdentity]execution.ActivatedPlan, len(activations.Facts))
	for _, fact := range activations.Facts {
		if fact.Selection != execution.ActivationNone {
			selected[fact.Plan] = fact.Selected
		}
	}
	mutations := make([]execution.PlanGapMutation, 0, len(items))
	requireAlready := make(map[execution.PlanGapIdentity]struct{})
	for _, item := range items {
		marker, found := loaded.Find(item.Identity)
		if !found {
			return false, errors.New("alarmd worker: activated Plan gap marker is absent from validated result")
		}
		kind, err := ensureGappedKind(marker)
		if err != nil {
			return false, err
		}
		plan := selected[item.Identity.Plan]
		mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
			Identity: item.Identity, ExpectedMarkerRevision: marker.MarkerRevision,
			ApplyVersion: item.ApplyVersion, ScheduleRevision: item.ScheduleRevision,
			Scopes: []execution.GapScopeMutation{{
				Scope: execution.GapScope{}, Kind: kind,
				ReasonCode: reason, RequiredFullSlots: plan.RequiredFullSlots,
			}},
		})
		if err != nil {
			return false, fmt.Errorf("alarmd worker: build activated Plan gap mutation: %w", err)
		}
		if marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone {
			switch execution.CompareApplyVersion(marker.PersistedApplyVersion, mutation.ApplyVersion) {
			case execution.ApplyVersionPersistedNewer:
				return false, errors.New("alarmd worker: activated Plan gap marker is newer than the Slot")
			case execution.ApplyVersionEqual:
				if marker.Status == execution.GapFound && marker.PersistedMutationDigest == mutation.MutationDigest {
					requireAlready[item.Identity] = struct{}{}
					break
				}
				if reuseSufficientQueryFreeProtection && queryFreeGapAlreadyProtects(marker, item, plan) {
					continue
				}
				return false, errors.New("alarmd worker: activated Plan gap marker conflicts with the Slot")
			}
		}
		if err := owner.retainGapMutation(ctx, mutation); err != nil {
			return false, err
		}
		mutations = append(mutations, mutation)
	}
	if len(mutations) == 0 {
		return true, nil
	}
	return coordinator.applyActivatedPlanGaps(ctx, request.Operation, request.Contract, mutations, requireAlready)
}

func queryFreeGapAlreadyProtects(
	marker execution.GapGuardSnapshot,
	item execution.PlanGapLoadItem,
	plan execution.ActivatedPlan,
) bool {
	// The existing Guard reason explains how recovery protection was established.
	// Query-free finalization records SNAPSHOT_UNAVAILABLE in Progress without rewriting it.
	if marker.Status != execution.GapFound || marker.Identity != item.Identity ||
		plan.Identity != item.Identity.Plan || plan.StateGeneration != item.Identity.StateGeneration ||
		plan.StateApplyEpoch != item.ApplyVersion.StateApplyEpoch || plan.ScheduleRevision != item.ScheduleRevision ||
		execution.CompareApplyVersion(marker.PersistedApplyVersion, item.ApplyVersion) != execution.ApplyVersionEqual ||
		marker.LastScheduleRevision != item.ScheduleRevision {
		return false
	}
	for _, scope := range marker.Scopes {
		if !scope.Scope.HasLevel {
			return scope.Status == execution.GapStatusGapped && scope.ObservedFullSlots == 0 &&
				scope.RequiredFullSlots == plan.RequiredFullSlots
		}
	}
	return false
}

func ensureGappedKind(marker execution.GapGuardSnapshot) (execution.GapMutationKind, error) {
	switch marker.Status {
	case execution.GapMissing, execution.GapClearedTombstone:
		return execution.GapOpen, nil
	case execution.GapFound:
		for _, scope := range marker.Scopes {
			if !scope.Scope.HasLevel {
				return execution.GapStrengthen, nil
			}
		}
		return execution.GapOpen, nil
	case execution.GapUnavailable, execution.GapTerminal:
		return "", fmt.Errorf("alarmd worker: activated Plan gap marker is not writable: %s", marker.Status)
	default:
		return "", fmt.Errorf("alarmd worker: unknown activated Plan gap marker: %s", marker.Status)
	}
}

func (coordinator *SlotExecutionCoordinator) applyActivatedPlanGaps(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
	requireAlready map[execution.PlanGapIdentity]struct{},
) (bool, error) {
	allAlready := len(items) > 0
	err := coordinator.applyGapChunks(ctx, operation, contractRef, items, "activated Plan gap guard",
		func(item execution.GapGuardApplyItemResult) error {
			if item.Status != execution.GapGuardAlreadyApplied {
				allAlready = false
			}
			if _, redo := requireAlready[item.Identity]; redo && item.Status != execution.GapGuardAlreadyApplied {
				return fmt.Errorf("activated Plan gap redo did not converge: %s", item.Status)
			}
			if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
				return fmt.Errorf("activated Plan gap guard did not complete: %s", item.Status)
			}
			return nil
		})
	if err != nil {
		return false, fmt.Errorf("alarmd worker: apply activated Plan gap guard: %w", err)
	}
	return allAlready, nil
}

// applyGapChunks writes Plan gap mutations in Store-sized chunks in slice
// order and validates every receipt; accept decides which item statuses
// complete an item. The chunk loop stops at the first failed chunk, so a
// later chunk is never sent after an earlier one failed.
func (coordinator *SlotExecutionCoordinator) applyGapChunks(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
	subject string,
	accept func(execution.GapGuardApplyItemResult) error,
) error {
	started := time.Now()
	var totals applyTotals
	return forEachChunk(ctx, len(items), coordinator.applyChunkItems(coordinator.budget.MaxGapMutations), func(chunk applyChunk) error {
		chunkItems := items[chunk.start:chunk.end]
		chunkStarted := time.Now()
		result, err := coordinator.ports.GapGuard.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: contractRef, Items: chunkItems})
		var reason execution.ReasonCode
		if err == nil {
			if err = result.Validate(); err == nil {
				expected := make([]execution.PlanGapIdentity, len(chunkItems))
				actual := make([]execution.PlanGapIdentity, len(result.Items))
				for index, item := range chunkItems {
					expected[index] = item.Identity
				}
				for index, item := range result.Items {
					actual[index] = item.Identity
					reason = item.ReasonCode
					if err = accept(item); err != nil {
						break
					}
				}
				if err == nil {
					err = validateIdentitySet(expected, actual, subject)
				}
			}
		}
		totals.keys += int64(len(chunkItems))
		coordinator.observeChunk(ctx, observability.StageGapGuardCommitted, operation, chunkStarted, started, "", reason,
			chunk, totals, observability.Counts{}, err)
		return err
	})
}

func activatedPlanSequencingScope(slot execution.SlotIdentity, activations execution.PlanActivationResult) execution.SequencingScope {
	scope := execution.SequencingScope{Slot: slot}
	for _, fact := range activations.Facts {
		if fact.Selection != execution.ActivationNone {
			scope.GapKeys = append(scope.GapKeys, execution.PlanGapIdentity{
				Plan: fact.Plan, StateGeneration: fact.Selected.StateGeneration,
			})
		}
	}
	sort.Slice(scope.GapKeys, func(left, right int) bool {
		return lessPlanIdentity(scope.GapKeys[left].Plan, scope.GapKeys[right].Plan)
	})
	return scope
}

func (coordinator *SlotExecutionCoordinator) finalizePrepared(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	bindings []execution.NamedInputBinding,
	loadedState execution.StatePreflightResult,
	evaluated execution.EvaluationResult,
) (execution.SlotExecutionResult, error) {
	return coordinator.finalizePreparedWithGaps(
		ctx, request, header, bindings, loadedState, execution.GapLoadResult{}, evaluated,
	)
}

func (coordinator *SlotExecutionCoordinator) finalizePreparedWithGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	bindings []execution.NamedInputBinding,
	loadedState execution.StatePreflightResult,
	loadedGaps execution.GapLoadResult,
	evaluated execution.EvaluationResult,
) (execution.SlotExecutionResult, error) {
	var err error
	activationRequest := duePlanActivationRequest(request.Contract, header.DuePlans)
	guardActivations, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}
	changedPlans, changedActivations := changedDuePlanActivations(header.DuePlans, guardActivations)
	forced := unsatisfiedForcedWarmingActivations(guardActivations, loadedGaps, changedPlans)
	if len(forced.Facts) > 0 {
		if _, err := coordinator.ensureActivatedPlanGaps(
			ctx, request, execution.ReasonCode(contract.ReasonConfigDrift), forced, false,
		); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		return activationRetry(execution.ReasonCode(contract.ReasonConfigDrift)), nil
	}

	planResults := append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
	sort.Slice(planResults, func(left, right int) bool {
		return lessPlanIdentity(planResults[left].Plan, planResults[right].Plan)
	})
	stateIndex := indexStatePreflight(loadedState)
	var retryPendingReason execution.ReasonCode
	var stateAdmissionTerminalReason execution.ReasonCode
	for _, planResult := range planResults {
		if _, changed := changedPlans[planResult.Plan]; changed {
			continue
		}
		due, ok := duePlan(header.DuePlans, planResult.Plan)
		if !ok {
			return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluated plan is not due")
		}
		if err := coordinator.admit(ctx, request, due); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		if err := coordinator.applyGap(ctx, request.Operation, request.Contract, planResult.GuardBeforeEvents); err != nil {
			return execution.SlotExecutionResult{}, err
		}

		mutations := make([]execution.StateMutation, 0, len(planResult.StateResults))
		eventsByState := make(map[execution.StateKeyIdentity][]contract.TriggerEventV1, len(planResult.StateResults))
		stateResults := append([]execution.StateEvaluation(nil), planResult.StateResults...)
		sort.Slice(stateResults, func(left, right int) bool {
			return lessStateIdentity(stateResults[left].Mutation.Identity, stateResults[right].Mutation.Identity)
		})
		for _, stateResult := range stateResults {
			statePosition, found := stateIndex[stateResult.Mutation.Identity]
			if !found {
				return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluation returned state without preflight view")
			}
			view := loadedState.Items[statePosition]
			started := time.Now()
			disposition := execution.ClassifyStateMutation(view, stateResult.Mutation)
			switch disposition {
			case execution.StateProceed:
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
				mutations = append(mutations, stateResult.Mutation)
				eventsByState[stateResult.Mutation.Identity] = append([]contract.TriggerEventV1(nil), stateResult.Events...)
			case execution.StateAlreadyApplied:
				execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
					if c.PriorStateApplied != nil {
						c.PriorStateApplied()
					}
				})
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
				continue
			case execution.StateStaleVersion, execution.StateVersionConflict:
				err = fmt.Errorf("state mutation preflight: %s", disposition)
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, "", "", err)
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
			default:
				err = errors.New("unknown state mutation preflight result")
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, "", "", err)
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
			}
		}

		if len(mutations) > 0 {
			if err := coordinator.admit(ctx, request, due); err != nil {
				return execution.SlotExecutionResult{}, err
			}
			rejected, encodedBytes, err := coordinator.admitState(ctx, request.Operation, request.Contract, mutations)
			if err != nil {
				return execution.SlotExecutionResult{}, err
			}
			accepted := make([]execution.StateMutation, 0, len(mutations)-len(rejected))
			acceptedBytes := make([]int64, 0, len(mutations)-len(rejected))
			events := make([]contract.TriggerEventV1, 0)
			for index, mutation := range mutations {
				if reason, terminal := rejected[mutation.Identity]; terminal {
					if stateAdmissionTerminalReason == "" {
						stateAdmissionTerminalReason = reason
					}
					continue
				}
				accepted = append(accepted, mutation)
				acceptedBytes = append(acceptedBytes, encodedBytes[index])
				events = append(events, eventsByState[mutation.Identity]...)
			}
			sortTriggerEvents(events)
			if err := coordinator.writeEvents(ctx, request.Operation, events); err != nil {
				if !isRetryableOutputDependency(err) {
					return execution.SlotExecutionResult{}, err
				}
				if retryPendingReason == "" {
					retryPendingReason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
				}
				// Event acknowledgement is Plan-local. Keep the Slot retryable and
				// continue healthy sibling Plans, but do not apply this Plan's State
				// or advance Progress until the stable event identity is replayed.
				continue
			}
			if len(accepted) > 0 {
				rejectedApply, err := coordinator.applyState(ctx, request.Operation, request.Contract, request.OwnerFence, accepted, acceptedBytes)
				if err != nil {
					return execution.SlotExecutionResult{}, err
				}
				for _, mutation := range accepted {
					reason, terminal := rejectedApply[mutation.Identity]
					if !terminal {
						continue
					}
					return execution.SlotExecutionResult{}, fmt.Errorf(
						"alarmd worker: deterministic State apply violates its successful admission contract: %s", reason,
					)
				}
			}
		}
		coordinator.observeGapScheduleRestart(ctx, request.Operation, loadedGaps, planResult.GuardAfterState)
		if err := coordinator.applyGap(ctx, request.Operation, request.Contract, planResult.GuardAfterState); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		if planResult.Disposition == execution.PlanRetryPending && retryPendingReason == "" {
			retryPendingReason = planResult.ReasonCode
		}
	}
	if retryPendingReason != "" {
		return execution.SlotExecutionResult{
			Completed: false, Result: observability.ResultRetrying, ReasonCode: retryPendingReason,
		}, nil
	}
	primary, err := execution.DeriveStreamingPrimaryInputFact(header, bindings)
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive PRIMARY input fact: %w", err)
	}
	completion := execution.SlotCompletion{Contract: request.Contract, Primary: &primary}
	if len(changedPlans) > 0 {
		completion.Kind = execution.CompletionPartialGap
		completion.Result = observability.ResultDegraded
		completion.ReasonCode = execution.ReasonCode(contract.ReasonConfigDrift)
	} else {
		completion.Kind, err = execution.DeriveStreamingCompletionKind(header, bindings, evaluated)
		if err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive completion: %w", err)
		}
		completion.Result = evaluated.Result
		completion.ReasonCode = evaluated.ReasonCode
	}
	if stateAdmissionTerminalReason != "" {
		completion.Kind = execution.CompletionTerminal
		completion.Result = observability.ResultTerminal
		completion.ReasonCode = stateAdmissionTerminalReason
	}
	if len(changedPlans) > 0 {
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: activationRequest,
			currentFacts:      guardActivations,
			activations:       changedActivations,
			completion:        completion,
		}
	}
	progressActivations, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}
	if !guardActivations.SameSelections(progressActivations) {
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: activationRequest,
			currentFacts:      progressActivations,
			activations:       changedSelectedActivations(guardActivations, progressActivations),
			completion: execution.SlotCompletion{
				Contract: request.Contract, Kind: execution.CompletionPartialGap, Primary: &primary,
				Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift),
			},
		}
	}
	if err := coordinator.admitDuePlans(ctx, request, header.DuePlans); err != nil {
		return execution.SlotExecutionResult{}, err
	}

	return coordinator.commitProgress(ctx, request, completion)
}

func unsatisfiedForcedWarmingActivations(
	activations execution.PlanActivationResult,
	loaded execution.GapLoadResult,
	changedPlans map[execution.PlanIdentity]struct{},
) execution.PlanActivationResult {
	result := execution.PlanActivationResult{Contract: activations.Contract}
	for _, fact := range activations.Facts {
		if fact.Selection == execution.ActivationNone || !fact.Selected.ForceWarming {
			continue
		}
		if _, changed := changedPlans[fact.Plan]; changed {
			continue
		}
		marker, found := loaded.Find(execution.PlanGapIdentity{
			Plan: fact.Plan, StateGeneration: fact.Selected.StateGeneration,
		})
		if found && (marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone) &&
			marker.PersistedApplyVersion.StateApplyEpoch >= fact.Selected.StateApplyEpoch {
			continue
		}
		result.Facts = append(result.Facts, fact)
	}
	return result
}

func (coordinator *SlotExecutionCoordinator) commitProgress(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	completion execution.SlotCompletion,
) (execution.SlotExecutionResult, error) {
	if request.ExpiredRange != nil {
		return coordinator.commitExpiredRange(ctx, request, completion)
	}
	started := time.Now()
	progressRequest := execution.ProgressCommitRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, ExpectedNextSlot: request.ExpectedNextSlot, Completion: completion,
		Projection: request.UnfinishedProjection(),
	}
	if err := progressRequest.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid progress commit: %w", err)
	}
	progress, err := coordinator.ports.Progress.CommitProgress(ctx, progressRequest)
	if err == nil {
		err = progress.Validate()
	}
	if err != nil {
		coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted, request.Operation, started, "", "", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: commit progress: %w", err)
	}
	if progress.Status != execution.ProgressCommitted {
		err = fmt.Errorf("progress not committed: %s", progress.Status)
		coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted, request.Operation, started, "", progress.ReasonCode, err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
	}
	observationResult := observability.Result(observability.ResultSuccess)
	observationReason := progress.ReasonCode
	if completion.Kind == execution.CompletionGapSkipped || completion.Kind == execution.CompletionSnapshotUnavailable {
		observationResult = observability.ResultDegraded
		observationReason = completion.ReasonCode
	}
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.ProgressCommitted != nil {
			c.ProgressCommitted(progressRequest, progress)
		}
	})
	coordinator.observeCommittedProgress(ctx, request.Operation, started, observationResult, observationReason, string(completion.Kind))
	return execution.SlotExecutionResult{Completed: true, CompletionKind: completion.Kind, Result: completion.Result, ReasonCode: completion.ReasonCode}, nil
}

func (coordinator *SlotExecutionCoordinator) admit(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	plan execution.DuePlan,
) error {
	return coordinator.admitPlan(ctx, request, plan.Identity, plan.StateApplyEpoch)
}

func (coordinator *SlotExecutionCoordinator) admitPlan(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	plan execution.PlanIdentity,
	epoch execution.StateApplyEpoch,
) error {
	started := time.Now()
	result, err := coordinator.ports.Admission.Check(ctx, execution.SideEffectAdmissionRequest{
		Contract: request.Contract, Plan: plan, StateApplyEpoch: epoch, OwnerFence: request.OwnerFence,
	})
	if err == nil {
		if validateErr := result.Validate(); validateErr != nil {
			err = validateErr
		} else if !result.Admitted {
			err = fmt.Errorf("admission denied: %s", result.ReasonCode)
		}
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageSideEffectAdmission, request.Operation, started, "", result.ReasonCode, err)
	if err != nil {
		return fmt.Errorf("alarmd worker: side-effect admission: %w", err)
	}
	return nil
}

// observeGapScheduleRestart names the recovery of a Plan gap marker that was
// left behind by a Plan schedule change: the loaded marker still carries the
// schedule revision it was written under, this Slot warms or clears it under
// the current one, and the store restarts the warmup count of every scope
// whose revision moved. It is observation only: the Slot result, the gap
// mutation and Progress are unchanged, and an operator reading the Query
// Group's flow can tell this restart from an ordinary warmup Slot.
func (coordinator *SlotExecutionCoordinator) observeGapScheduleRestart(
	ctx context.Context,
	operation execution.Operation,
	loaded execution.GapLoadResult,
	mutations []execution.PlanGapMutation,
) {
	for _, mutation := range mutations {
		marker, found := loaded.Find(mutation.Identity)
		if !found || marker.Status != execution.GapFound ||
			marker.LastScheduleRevision == "" || marker.LastScheduleRevision == mutation.ScheduleRevision {
			continue
		}
		coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapGuardCommitted,
			operation, time.Now(), observability.ResultResumed, observability.ReasonNone,
			observability.Counts{Keys: int64(len(mutation.Scopes))}, nil)
	}
}

func (coordinator *SlotExecutionCoordinator) applyGap(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
) error {
	err := coordinator.applyGapChunks(ctx, operation, contractRef, items, "gap guard",
		func(item execution.GapGuardApplyItemResult) error {
			if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
				return fmt.Errorf("gap guard did not complete: %s", item.Status)
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("alarmd worker: apply gap guard: %w", err)
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) writeEvents(
	ctx context.Context,
	operation execution.Operation,
	events []contract.TriggerEventV1,
) error {
	if len(events) == 0 {
		return nil
	}
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: events[0].PlanRef.StrategyID})
	started := time.Now()
	err := coordinator.ports.Events.WriteBatch(ctx, events)
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.OutputWritten != nil {
			c.OutputWritten(events, err)
		}
	})
	reason := execution.ReasonCode(observability.ReasonNone)
	if err != nil {
		reason = execution.ReasonCode(observability.ReasonInternalUnknown)
		if isRetryableOutputDependency(err) {
			reason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
		}
	}
	coordinator.observeWithCounts(ctx, observability.ComponentOutput, observability.StageEventACKed, operation, started,
		"", reason, observability.Counts{Events: int64(len(events))}, err)
	if err != nil {
		return fmt.Errorf("alarmd worker: acknowledge events: %w", err)
	}
	return nil
}

func isRetryableOutputDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableOutputDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}

// admitState admits one Plan's mutations in Store-sized chunks. It returns the
// deterministic rejections by identity and, aligned with mutations, the
// encoded size the store measured for each admitted mutation.
func (coordinator *SlotExecutionCoordinator) admitState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	mutations []execution.StateMutation,
) (map[execution.StateKeyIdentity]execution.ReasonCode, []int64, error) {
	started := time.Now()
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	encodedBytes := make([]int64, len(mutations))
	var totals applyTotals
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		chunkStarted := time.Now()
		result, err := coordinator.ports.State.AdmitRuntime(ctx, execution.StateApplyRequest{Contract: contractRef, Items: chunkItems})
		var reason execution.ReasonCode
		var chunkBytes int64
		rejected := 0
		if err == nil {
			if err = result.Validate(); err == nil {
				actual := make([]execution.StateKeyIdentity, len(result.Items))
				for index, item := range result.Items {
					actual[index] = item.Identity
				}
				err = validateIdentitySet(stateIdentities(chunkItems), actual, "state admission")
			}
			if err == nil {
				reason = firstStateAdmissionFailureReason(result.Items)
				position := make(map[execution.StateKeyIdentity]int, len(chunkItems))
				for index, mutation := range chunkItems {
					position[mutation.Identity] = chunk.start + index
				}
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateAdmissionAccepted:
						encodedBytes[position[item.Identity]] = int64(item.EncodedBytes)
						chunkBytes += int64(item.EncodedBytes)
					case execution.StateAdmissionDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
						rejected++
					default:
						err = fmt.Errorf("state admission did not complete: %s", item.Status)
					}
				}
			}
		}
		observationResult := observability.Result(observability.ResultSuccess)
		if rejected > 0 {
			observationResult = observability.ResultTerminal
		}
		totals.keys += int64(len(chunkItems))
		totals.bytes += chunkBytes
		coordinator.observeChunk(ctx, observability.StageStateAdmission, operation, chunkStarted, started, observationResult, reason,
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err)
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("alarmd worker: state admission: %w", err)
	}
	return deterministic, encodedBytes, nil
}

// applyState writes the accepted mutations of one Plan in Store-sized chunks
// in slice order. When the State store can fence writes and the Slot carries
// a valid owner fence, the fence is verified inside every write so a stale
// owner cannot advance State after admission; the fence instant is taken per
// chunk so a lease that expired while an earlier chunk was written is not
// carried past its deadline. A stale fence surfaces as the ownership error,
// the same as a failed admission. A failed chunk stops the loop: the keys of
// earlier chunks stay written, later chunks are not sent, and the Plan
// neither advances State nor commits Progress until the Slot is re-run, when
// the written keys read back ALREADY_APPLIED. encodedBytes, aligned with
// mutations, only feeds the observation and may be nil.
func (coordinator *SlotExecutionCoordinator) applyState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	fence execution.OwnerFence,
	mutations []execution.StateMutation,
	encodedBytes []int64,
) (map[execution.StateKeyIdentity]execution.ReasonCode, error) {
	started := time.Now()
	fenced, ok := coordinator.ports.State.(execution.FencedStateStore)
	useFence := ok && fence.Validate(contractRef) == nil
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	var totals applyTotals
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		applyRequest := execution.StateApplyRequest{Contract: contractRef, Items: chunkItems}
		chunkStarted := time.Now()
		var result execution.StateApplyResult
		var err error
		if useFence {
			result, err = fenced.ApplyRuntimeFenced(ctx, applyRequest, execution.StateApplyFence{Fence: fence, At: chunkStarted})
		} else {
			result, err = coordinator.ports.State.ApplyRuntime(ctx, applyRequest)
		}
		var reason execution.ReasonCode
		rejected := 0
		if err == nil {
			if err = result.Validate(); err == nil {
				actual := make([]execution.StateKeyIdentity, len(result.Items))
				for index, item := range result.Items {
					actual[index] = item.Identity
				}
				err = validateIdentitySet(stateIdentities(chunkItems), actual, "state apply")
			}
			if err == nil {
				reason = firstStateApplyFailureReason(result.Items)
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateApplied, execution.StateApplyAlreadyApplied:
					case execution.StateApplyDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
						rejected++
					default:
						err = fmt.Errorf("state apply did not complete: %s", item.Status)
					}
				}
			}
		}
		var chunkBytes int64
		if len(encodedBytes) == len(mutations) {
			for _, size := range encodedBytes[chunk.start:chunk.end] {
				chunkBytes += size
			}
		}
		observationResult := observability.Result(observability.ResultSuccess)
		if rejected > 0 {
			observationResult = observability.ResultTerminal
		}
		totals.keys += int64(len(chunkItems))
		totals.bytes += chunkBytes
		coordinator.observeChunk(ctx, observability.StageStateApplied, operation, chunkStarted, started, observationResult, reason,
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: apply state: %w", err)
	}
	return deterministic, nil
}

func firstStateAdmissionFailureReason(items []execution.StateAdmissionItemResult) execution.ReasonCode {
	ordered := append([]execution.StateAdmissionItemResult(nil), items...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Identity != ordered[right].Identity {
			return lessStateIdentity(ordered[left].Identity, ordered[right].Identity)
		}
		return ordered[left].Status < ordered[right].Status
	})
	for _, item := range ordered {
		if item.Status != execution.StateAdmissionAccepted {
			return item.ReasonCode
		}
	}
	return observability.ReasonNone
}

func firstStateApplyFailureReason(items []execution.StateApplyItemResult) execution.ReasonCode {
	ordered := append([]execution.StateApplyItemResult(nil), items...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Identity != ordered[right].Identity {
			return lessStateIdentity(ordered[left].Identity, ordered[right].Identity)
		}
		return ordered[left].Status < ordered[right].Status
	})
	for _, item := range ordered {
		if item.Status != execution.StateApplied && item.Status != execution.StateApplyAlreadyApplied {
			return item.ReasonCode
		}
	}
	return observability.ReasonNone
}

func (coordinator *SlotExecutionCoordinator) observe(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	operation execution.Operation,
	started time.Time,
	result observability.Result,
	reason observability.ReasonCode,
	err error,
) {
	coordinator.observeWithCounts(ctx, component, stage, operation, started, result, reason, observability.Counts{}, err)
}

func (coordinator *SlotExecutionCoordinator) observeWithCounts(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	operation execution.Operation,
	started time.Time,
	result observability.Result,
	reason observability.ReasonCode,
	counts observability.Counts,
	err error,
) {
	coordinator.emitObservation(ctx, observability.Observation{
		Component: component, Stage: stage, Result: result,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		ReasonCode: reason, Duration: time.Since(started), Counts: counts, Err: err,
	})
}

// emitObservation applies the shared result and reason defaults of internal
// stage observations and hands the observation to the Observer; an observer
// panic never fails the Slot.
func (coordinator *SlotExecutionCoordinator) emitObservation(ctx context.Context, observation observability.Observation) {
	if observation.Err != nil {
		observation.Result = observability.Result(observability.ResultFailed)
		if observation.ReasonCode == "" || observation.ReasonCode == observability.ReasonNone {
			observation.ReasonCode = observability.ReasonInternalUnknown
		}
	} else {
		if observation.Result == "" {
			observation.Result = observability.ResultSuccess
		}
		if observation.ReasonCode == "" {
			observation.ReasonCode = observability.ReasonNone
		}
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

func (coordinator *SlotExecutionCoordinator) observeCapacityRejection(
	ctx context.Context,
	operation execution.Operation,
	budget observability.CapacityBudget,
	err error,
) {
	observation := observability.Observation{
		Component: observability.ComponentResource, Stage: observability.StageResourceHard,
		Result: observability.ResultPaused, Operation: observability.Operation(operation),
		Direction: observability.DirectionInternal, ReasonCode: observability.ReasonCode(contract.ReasonResourceHardStop),
		CapacityBudget: budget, Err: err,
	}
	var exceeded *provisionalBudgetExceededError
	if errors.As(err, &exceeded) {
		observation.CapacityRejection = exceeded.facts
		if exceeded.slot {
			// The Slot itself is too large for this process: not a pause that
			// resumes when shared capacity frees up.
			observation.ReasonCode = observability.ReasonCode(contract.ReasonSlotBudgetExceeded)
		}
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

func summarizeStateLoad(result execution.StatePreflightResult) (observability.Result, observability.ReasonCode) {
	var degraded observability.ReasonCode
	for _, item := range result.Items {
		switch item.Status {
		case execution.StateDeterministicInvalid:
			return observability.ResultTerminal, item.ReasonCode
		case execution.StateRetryableIO:
			if degraded == "" {
				degraded = item.ReasonCode
			}
		}
	}
	if degraded != "" {
		return observability.ResultDegraded, degraded
	}
	return observability.ResultSuccess, observability.ReasonNone
}

func summarizeGapLoad(result execution.GapLoadResult) (observability.Result, observability.ReasonCode) {
	var degraded observability.ReasonCode
	for _, item := range result.Items {
		switch item.Status {
		case execution.GapTerminal:
			return observability.ResultTerminal, item.ReasonCode
		case execution.GapUnavailable:
			if degraded == "" {
				degraded = item.ReasonCode
			}
		}
	}
	if degraded != "" {
		return observability.ResultDegraded, degraded
	}
	return observability.ResultSuccess, observability.ReasonNone
}

func sequencingScope(
	header execution.InternalExecutionHeader,
	stateItems []execution.StatePreflightItem,
	gapItems []execution.PlanGapLoadItem,
) execution.SequencingScope {
	states := make([]execution.StateKeyIdentity, 0, len(stateItems))
	for _, item := range stateItems {
		states = append(states, item.Identity)
	}
	gaps := make([]execution.PlanGapIdentity, 0, len(gapItems))
	for _, item := range gapItems {
		gaps = append(gaps, item.Identity)
	}
	sort.Slice(states, func(left, right int) bool { return lessStateIdentity(states[left], states[right]) })
	sort.Slice(gaps, func(left, right int) bool {
		if gaps[left].Plan != gaps[right].Plan {
			return lessPlanIdentity(gaps[left].Plan, gaps[right].Plan)
		}
		return gaps[left].StateGeneration < gaps[right].StateGeneration
	})
	return execution.SequencingScope{Slot: header.Contract.Slot, StateKeys: states, GapKeys: gaps}
}

func duePlan(plans []execution.DuePlan, identity execution.PlanIdentity) (execution.DuePlan, bool) {
	for _, plan := range plans {
		if plan.Identity == identity {
			return plan, true
		}
	}
	return execution.DuePlan{}, false
}

func validateIdentitySet[T comparable](expected, actual []T, subject string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s result cardinality mismatch", subject)
	}
	wanted := make(map[T]struct{}, len(expected))
	for _, identity := range expected {
		if _, duplicate := wanted[identity]; duplicate {
			return fmt.Errorf("%s request contains a duplicate identity", subject)
		}
		wanted[identity] = struct{}{}
	}
	seen := make(map[T]struct{}, len(actual))
	for _, identity := range actual {
		if _, ok := wanted[identity]; !ok {
			return fmt.Errorf("%s result contains an unknown identity", subject)
		}
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("%s result contains a duplicate identity", subject)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func lessPlanIdentity(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

func lessStateIdentity(left, right execution.StateKeyIdentity) bool {
	if left.Plan != right.Plan {
		return lessPlanIdentity(left.Plan, right.Plan)
	}
	if left.StateGeneration != right.StateGeneration {
		return left.StateGeneration < right.StateGeneration
	}
	return left.SeriesIdentityDigest < right.SeriesIdentityDigest
}

func sortTriggerEvents(events []contract.TriggerEventV1) {
	sort.Slice(events, func(left, right int) bool {
		leftEvent, rightEvent := events[left], events[right]
		leftPlan := execution.PlanIdentity{TenantID: leftEvent.TenantID, BusinessID: leftEvent.BusinessID, StrategyID: leftEvent.PlanRef.StrategyID}
		rightPlan := execution.PlanIdentity{TenantID: rightEvent.TenantID, BusinessID: rightEvent.BusinessID, StrategyID: rightEvent.PlanRef.StrategyID}
		if leftPlan != rightPlan {
			return lessPlanIdentity(leftPlan, rightPlan)
		}
		if leftEvent.RecordRef.DimensionIdentityDigest != rightEvent.RecordRef.DimensionIdentityDigest {
			return leftEvent.RecordRef.DimensionIdentityDigest < rightEvent.RecordRef.DimensionIdentityDigest
		}
		if leftEvent.RecordRef.SourceTime != rightEvent.RecordRef.SourceTime {
			return leftEvent.RecordRef.SourceTime < rightEvent.RecordRef.SourceTime
		}
		if leftEvent.RecordRef.RecordID != rightEvent.RecordRef.RecordID {
			return leftEvent.RecordRef.RecordID < rightEvent.RecordRef.RecordID
		}
		return leftEvent.EventID < rightEvent.EventID
	})
}

// indexStatePreflight is call-local and preserves Find's first-match behavior.
// Values refer to the already retained views; no history or level data is copied.
func indexStatePreflight(result execution.StatePreflightResult) map[execution.StateKeyIdentity]int {
	index := make(map[execution.StateKeyIdentity]int, len(result.Items))
	for position := range result.Items {
		identity := result.Items[position].Identity
		if _, exists := index[identity]; !exists {
			index[identity] = position
		}
	}
	return index
}

// Called only after this invocation received and validated ProgressCommitted.
func (coordinator *SlotExecutionCoordinator) observeCommittedProgress(ctx context.Context, operation execution.Operation, started time.Time, result observability.Result, reason observability.ReasonCode, kind string) {
	if reason == "" {
		reason = observability.ReasonNone
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		Result: result, ReasonCode: reason, Duration: time.Since(started), ProgressCompletionKind: kind,
	})
}
