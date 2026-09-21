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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type Ports struct {
	Finalization execution.QueryFreeFinalizationSource
	Activation   execution.PlanActivationSource
	Query        execution.QueryExecutionSource
	Sequencer    execution.SideEffectSequencer
	Evaluator    execution.Evaluator
	Admission    execution.SideEffectAdmitter
	GapGuard     execution.GapGuardStore
	// NoData is required, like every other port here. A worker without it would
	// evaluate every Plan's thresholds and none of their absence, and the only
	// sign would be no-data alerts that never fire - which is indistinguishable
	// from nothing being absent.
	NoData execution.PlanNoDataStore
	// Hosts resolves which business a host belongs to, which decides both what
	// a no-data roster expects and which of its groups have left. Required for
	// the same reason: without it every declared host would read as unknown, so
	// every static target would expect nothing and no absence would ever be
	// reported - a silence that looks exactly like health.
	Hosts    execution.HostBusiness
	Events   execution.EventSink
	State    execution.StateStore
	Progress execution.ProgressStore
	Observer execution.Observer
	// OpenAlerts is required: a worker that evaluates without it sends every
	// RECOVERY envelope, and the trigger counts that as not_configured, which
	// on a production worker is the wiring having come apart.
	OpenAlerts execution.OpenAlertCopy
	// ExecutionEvidence records and reads how far an earlier attempt at a Slot
	// got. It is the one optional port: nil keeps exactly the behaviour of
	// builds before it existed, which is that a Slot finalized after its replay
	// window reads as never evaluated. A deployment without it loses a reading,
	// not a detection.
	ExecutionEvidence execution.SlotExecutionEvidenceStore
}

type SlotExecutionCoordinator struct {
	ports        Ports
	budget       ProvisionalBudget
	reservations processProvisionalReservations
	// noDataSkips is how long each Plan's no-data detection has been skipping.
	// One per process, because a streak is about rounds rather than about one
	// Slot. See no_data_skip_streak.go.
	noDataSkips noDataSkipStreaks
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
	// completionCause travels with the completion because the converge path
	// commits that completion rather than deriving its own. Without it the
	// whole path reported the completion and dropped the one field that says
	// which of the conditions folded into UNAVAILABLE actually happened.
	completionCause execution.CompletionAttribution
}

func (*activationProtectionRequiredError) Error() string {
	return "alarmd worker: changed activation requires Guard protection"
}

func NewSlotExecutionCoordinator(ports Ports, budget ProvisionalBudget) (*SlotExecutionCoordinator, error) {
	if ports.Finalization == nil || ports.Activation == nil || ports.Query == nil || ports.Sequencer == nil ||
		ports.Evaluator == nil || ports.Admission == nil || ports.GapGuard == nil || ports.NoData == nil || ports.Hosts == nil ||
		ports.Events == nil || ports.State == nil || ports.Progress == nil || ports.Observer == nil ||
		ports.OpenAlerts == nil {
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
	ctx, applied := withAppliedPlans(ctx)
	// On the way out, and only when this attempt wrote state and then could not
	// write the Slot down. Best-effort by construction: the mark is what lets a
	// later query-free completion say "this was evaluated", and failing to
	// leave it costs that completion its evidence -- it must not also change
	// what this attempt reports to the scheduler, which is about the Slot.
	defer func() { coordinator.recordExecutionEvidence(ctx, request, applied) }()
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
		OwnerFence: request.OwnerFence, Projection: request.UnfinishedProjection(), ContentScope: request.ContentScope,
	})
	// Timed whichever way it went. A BeginSlot that fails says so; one that
	// simply took twenty seconds used to say nothing at all, and an attempt
	// stuck here is indistinguishable from one stuck anywhere else between
	// slot_started and slot_completed.
	observability.ObserveSlotWait(ctx, coordinator.ports.Observer, observability.SlotWaitProgressBegin,
		observability.Operation(request.Operation), beginStarted, time.Now)
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
	finalizationStarted := time.Now()
	finalization, err := coordinator.ports.Finalization.ResolveFinalization(ctx, request)
	// This step reads the frozen Plan, and so the Segment's content objects.
	// It is where the twenty-two second silence in the production evidence
	// falls: after the frozen Plan was generated and before access planned a
	// query, with no line on either side of it.
	observability.ObserveSlotWait(ctx, coordinator.ports.Observer, observability.SlotWaitFinalization,
		observability.Operation(request.Operation), finalizationStarted, time.Now)
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
			// A readiness deferral is the normal pacing of a Slot whose data is
			// not in yet, and it is the single highest-volume line alarmd emits.
			// It has to say so: an empty reason on a non-success result
			// normalizes to internal_unknown (observability.NormalizeReason),
			// which made every deferral read as an unclassified internal error -
			// in the logs and in the stage counter's reason label alike.
			coordinator.observe(ctx, observability.ComponentAccess, observability.StageQueryCompleted, request.Operation,
				started, observability.ResultRetrying, observability.ReasonCode(contract.ReasonQueryNotReady), nil)
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
	coordinator.observeQueryCompleted(ctx, request.Operation, started, queryResult, queryReason, completion, stream.evaluated)
	if len(stream.evaluated.Plans) == 0 {
		return execution.SlotExecutionResult{Result: queryResult, ReasonCode: queryReason}, nil
	}

	var result execution.SlotExecutionResult
	err = coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, stream.stateItems, stream.gapItems), func(sequenceCtx context.Context) error {
		var executeErr error
		result, executeErr = coordinator.finalizePreparedWithGaps(
			sequenceCtx, request, stream.header, stream.bindings, stream.state, stream.gaps, stream.evaluated,
			stream.noDataMutations, stream.queryEvidence.availability(), stream.seriesCensus,
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
			// Both query-free modes may reuse protection that is already
			// sufficient. Gating this on the mode made a Slot with a marker
			// from an earlier streaming attempt unfinishable: a GAP_SKIPPED
			// finalization compared the two digests, found them different --
			// legitimately, because a streaming attempt and a query-free
			// finalization write different content for the same Slot -- and
			// refused, on every round, for as long as the marker stood.
			//
			// The mode was never what made reuse safe. queryFreeGapAlreadyProtects
			// is, and it checks sufficiency directly: same Plan, same
			// generation, same ApplyVersion, same schedule revision, a
			// Plan-wide scope whose protection is at least as strong as the
			// requested protection. The same Slot must not reset observations
			// already committed by an earlier attempt.
			true,
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
		// What an earlier attempt at this Slot got to, read here because this
		// is the last moment anything knows the frozen due set and the first
		// moment the completion is being built. Both query-free modes ask: each
		// of them can be the second half of an attempt that evaluated, alerted
		// and then failed to write the Slot down.
		evidence := coordinator.readExecutionEvidence(sequenceCtx, plans, request.Contract.Slot)
		result, err = coordinator.commitProgress(sequenceCtx, request, execution.SlotCompletion{
			Contract: request.Contract, Kind: completionKind,
			Result: observability.ResultDegraded, ReasonCode: finalization.ReasonCode,
			Evidence: evidence,
		}, execution.CompletionAttribution{})
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
			// The same, for the activations that changed underneath this
			// round. Reuse is decided by whether the protection is sufficient,
			// not by which query-free mode asked.
			true,
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
	reuseCommittedQueryFreeStatement bool,
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
				reuseCommittedQueryFreeStatement,
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
			result, err = coordinator.commitProgress(
				sequenceCtx, request, protection.completion, protection.completionCause)
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
	reuseCommittedQueryFreeStatement bool,
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
		// Query-free finalization cannot replace a statement already committed
		// by this Slot, including a tombstone left by successful gap recovery.
		if reuseCommittedQueryFreeStatement && sameSlotGapCommitted(marker, item, plan) {
			continue
		}
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
				return false, newGapGuardConflict(marker, item, mutation, reason, plan)
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

// sameSlotGapCommitted fences reuse with the selected identity and versions.
// A tombstone is this Slot's conclusion too, not active protection to strengthen.
func sameSlotGapCommitted(marker execution.GapGuardSnapshot, item execution.PlanGapLoadItem, plan execution.ActivatedPlan) bool {
	return (marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone) && marker.Identity == item.Identity &&
		plan.Identity == item.Identity.Plan && plan.StateGeneration == item.Identity.StateGeneration &&
		plan.StateApplyEpoch == item.ApplyVersion.StateApplyEpoch && plan.ScheduleRevision == item.ScheduleRevision &&
		execution.CompareApplyVersion(marker.PersistedApplyVersion, item.ApplyVersion) == execution.ApplyVersionEqual &&
		marker.LastScheduleRevision == item.ScheduleRevision
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
	extensions ...map[execution.PlanGapIdentity]*observability.GapExtensionFacts,
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
		}, extensions...)
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
	extensionMaps ...map[execution.PlanGapIdentity]*observability.GapExtensionFacts,
) error {
	started := time.Now()
	var totals applyTotals
	return forEachChunk(ctx, len(items), coordinator.applyChunkItems(coordinator.budget.MaxGapMutations), func(chunk applyChunk) error {
		chunkItems := items[chunk.start:chunk.end]
		var extensions []*observability.GapExtensionFacts
		if len(extensionMaps) > 0 {
			for _, item := range chunkItems {
				if facts := extensionMaps[0][item.Identity]; facts != nil {
					extensions = append(extensions, facts)
				}
			}
		}
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
			chunk, totals, observability.Counts{}, err, nil, extensions...)
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
		ctx, request, header, bindings, loadedState, execution.GapLoadResult{}, evaluated, nil,
		execution.QueryAvailabilityUnknown,
		// The census a caller with no stream can state: the loaded views are
		// the series it read, and it meant to evaluate exactly those.
		seriesCensus{Due: len(loadedState.Items), Read: len(loadedState.Items)},
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
	noDataMemory []execution.PlanNoDataMutation,
	queryAvailability execution.QueryAvailability,
	census seriesCensus,
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
		// The forced WARMING marker is written at this Slot's ApplyVersion,
		// and the Gap store accepts one write per version: a retry of this
		// same Slot would evaluate under the marker and try to advance it
		// with its own mutation at the same version, which the store refuses
		// as a conflict, on every retry, until the Slot ages out. So this
		// Slot is the one the marker consumes: it completes query-free with
		// the drift completion, and warming starts at the next Slot, whose
		// version is newer than the marker's.
		if _, err := coordinator.ensureActivatedPlanGaps(
			ctx, request, execution.ReasonCode(contract.ReasonConfigDrift), forced, false,
		); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		primary, err := execution.DeriveStreamingPrimaryInputFact(header, bindings)
		if err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive PRIMARY input fact: %w", err)
		}
		if err := coordinator.admitActivatedPlans(ctx, request, guardActivations); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		driftCompletion, driftCause := configDriftCompletion(request.Contract, &primary)
		return coordinator.commitProgress(ctx, request, driftCompletion,
			execution.CompletionAttribution{Cause: driftCause})
	}

	planResults := append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
	sort.Slice(planResults, func(left, right int) bool {
		return lessPlanIdentity(planResults[left].Plan, planResults[right].Plan)
	})
	stateIndex := indexStatePreflight(loadedState)
	var retryPendingReason execution.ReasonCode
	// The first deterministic refusal that ends a Plan by name: a State
	// admission that will not take its writes, or an output the sink will not
	// write. Either finishes the Slot as TERMINAL with that name; the other
	// Plans run, and Progress advances by the rule every terminal completion
	// follows. Neither waits on anything, so neither is retried.
	var deterministicTerminalReason execution.ReasonCode
	// One reading per Slot rather than per Plan: the question is how many of
	// this Slot's keys were read and not written, and a Plan is not a
	// population anyone reads that against.
	var frozenRenewals observability.FrozenStateRenewalFacts
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
		// Gap statements commit independently from series State. This also
		// covers retries whose query became partial or exceeded its budget,
		// which never enter the evaluator.
		planResult.GuardBeforeEvents = uncommittedGapMutations(loadedGaps, planResult.GuardBeforeEvents)
		planResult.GuardAfterState = uncommittedGapMutations(loadedGaps, planResult.GuardAfterState)

		if err := coordinator.applyGap(ctx, request.Operation, request.Contract, planResult.GuardBeforeEvents); err != nil {
			return execution.SlotExecutionResult{}, err
		}

		mutations := make([]execution.StateMutation, 0, len(planResult.StateResults))
		eventsByState := make(map[execution.StateKeyIdentity][]contract.TriggerEventV1, len(planResult.StateResults))
		stateResults := append([]execution.StateEvaluation(nil), planResult.StateResults...)
		sort.Slice(stateResults, func(left, right int) bool {
			return lessStateIdentity(stateResults[left].Mutation.Identity, stateResults[right].Mutation.Identity)
		})
		// How much of what each admitted write carries is already stored. This
		// changes nothing: every mutation below is written exactly as before.
		// It is here rather than beside the write because this is where the
		// witnessed view and the mutation are both in hand, and the question
		// only has meaning for mutations that are actually about to be written.
		var writeReuse observability.StateWriteReuseFacts
		var alreadyApplied observability.StateAlreadyAppliedFacts
		for _, stateResult := range stateResults {
			statePosition, found := stateIndex[stateResult.Mutation.Identity]
			if !found {
				return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluation returned state without preflight view")
			}
			view := loadedState.Items[statePosition]
			started := time.Now()
			classified := execution.ClassifyStateMutationDetail(view, stateResult.Mutation)
			switch classified.Disposition {
			case execution.StateProceed:
				reuseClass, reuseReason := execution.ClassifyStateWriteReuse(view, stateResult.Mutation)
				writeReuse.Record(
					observability.StateWriteReuseClass(reuseClass),
					observability.StateWriteChangeReason(reuseReason),
					storedStateLabel(view.Status),
				)
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
				mutations = append(mutations, stateResult.Mutation)
				eventsByState[stateResult.Mutation.Identity] = append([]contract.TriggerEventV1(nil), stateResult.Events...)
			case execution.StateAlreadyApplied:
				alreadyApplied.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateAlreadyAppliedKind(classified.AlreadyApplied),
					string(stateResult.Mutation.Identity.SeriesIdentityDigest), stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision)
				execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
					if c.PriorStateApplied != nil {
						c.PriorStateApplied()
					}
				})
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
				continue
			case execution.StateStaleVersion, execution.StateVersionConflict:
				// The refusal names itself and the line carries the name:
				// the reason from the error, and for a conflict the kind
				// with the two revisions compared, so the line does not read
				// internal_unknown beside an error_type that already knew.
				refusal := &StateConflictError{Stage: "state mutation preflight", Status: string(classified.Disposition)}
				var conflicts *observability.StateVersionConflictFacts
				if classified.Disposition == execution.StateVersionConflict {
					refusal.Kind, refusal.ExpectedRevision, refusal.StoredRevision, refusal.VersionComparison =
						classified.VersionConflict, stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision, view.VersionComparison
					conflicts = &observability.StateVersionConflictFacts{}
					conflicts.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateVersionConflictKind(classified.VersionConflict),
						string(stateResult.Mutation.Identity.SeriesIdentityDigest), stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision,
						string(view.VersionComparison), false)
				}
				err = refusal
				reason, _ := StateConflictReason(err)
				coordinator.emitObservation(ctx, observability.Observation{
					Component: observability.ComponentState, Stage: observability.StageMutationCompared,
					Operation: observability.Operation(request.Operation), Direction: observability.DirectionInternal,
					ReasonCode: observability.ReasonCode(reason), Duration: time.Since(started), Err: err,
					StateVersionConflict: conflicts,
				})
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
			default:
				err = errors.New("unknown state mutation preflight result")
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, "", "", err)
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
			}
		}

		if !writeReuse.Empty() {
			facts := writeReuse
			coordinator.emitObservation(ctx, observability.Observation{
				Component: observability.ComponentState, Stage: observability.StageMutationCompared,
				Result: observability.ResultSuccess, Operation: observability.Operation(request.Operation),
				Direction: observability.DirectionInternal, ReasonCode: observability.ReasonNone,
				StateWriteReuse: &facts,
			})
		}
		if !alreadyApplied.Empty() {
			coordinator.emitAlreadyApplied(ctx, request.Operation, alreadyApplied)
		}

		// The retention need travels with the Plan's mutations so the store
		// sizes their TTL against the Plan frozen with this Slot. The frozen
		// series below need it for the same reason and must get the same
		// answer: a renewal computed from a different retention would give a
		// key a different life from the one its write gave it.
		frozen := frozenSeriesOf(planResult.Plan, loadedState, planResult.StateResults)
		var retention []execution.StateRetentionRequirement
		if len(mutations) > 0 || len(frozen) > 0 {
			retention, err = execution.DeriveStateRetentionRequirement(due.CompiledPlan)
			if err != nil {
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
			}
		}
		// Before the writes, not after. A renewal is about the keys this Plan
		// is not writing, so nothing below can change what it decides -- but
		// an apply that fails returns from this function, and the keys that
		// were about to expire would then go one more Slot without anyone
		// asking about them, on the Slot that already went wrong.
		coordinator.renewFrozenState(ctx, request, retention, frozen, &frozenRenewals)

		if len(mutations) > 0 {
			if err := coordinator.admit(ctx, request, due); err != nil {
				return execution.SlotExecutionResult{}, err
			}
			rejected, encodedBytes, err := coordinator.admitState(ctx, request.Operation, request.Contract, retention, mutations)
			if err != nil {
				return execution.SlotExecutionResult{}, err
			}
			accepted := make([]execution.StateMutation, 0, len(mutations)-len(rejected))
			acceptedBytes := make([]int64, 0, len(mutations)-len(rejected))
			events := make([]contract.TriggerEventV1, 0)
			for index, mutation := range mutations {
				if reason, terminal := rejected[mutation.Identity]; terminal {
					if deterministicTerminalReason == "" {
						deterministicTerminalReason = reason
					}
					continue
				}
				accepted = append(accepted, mutation)
				acceptedBytes = append(acceptedBytes, encodedBytes[index])
				events = append(events, eventsByState[mutation.Identity]...)
			}
			sortTriggerEvents(events)
			if err := coordinator.writeEvents(ctx, request.Operation, events); err != nil {
				if reason, deferred := outputDeferralReason(err); deferred {
					// The sink did not start the batch: the lease has less
					// life left than one batch needs to land. Nothing is
					// unknown and nothing is wrong with the content; the
					// Plan waits, by that name, for the next renewal.
					if retryPendingReason == "" {
						retryPendingReason = reason
					}
					continue
				}
				if reason, rejected := outputRejectionReason(err); rejected {
					// Decided in this process, from this Plan's own decisions
					// or this deployment's own client: the same events meet
					// the same refusal on every retry. This Plan's State is
					// not applied, because its output was not written and
					// State follows the ACK; the Slot completes by the name,
					// the sibling Plans go on, and Progress moves past it.
					// Retrying instead would report a Kafka that is up as
					// down, every round, and commit nothing.
					if deterministicTerminalReason == "" {
						deterministicTerminalReason = reason
					}
					continue
				}
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
				rejectedApply, err := coordinator.applyState(ctx, request.Operation, request.Contract, request.OwnerFence, request.ContentScope, retention, accepted, acceptedBytes)
				if err != nil {
					return execution.SlotExecutionResult{}, err
				}
				// Counted after the write, not from the mutations built: a
				// key whose write was refused is a key that did not have its
				// life refreshed, and it belongs on the other side of the
				// census.
				census.Written += len(accepted) - len(rejectedApply)
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
	frozenRenewals.RecordCensus(census.Due, census.Read, census.Written)
	coordinator.observeFrozenStateRenewal(ctx, request.Operation, frozenRenewals)
	// After every Plan's state and gap, inside the same sequenced scope. The
	// memory only changes what the next round reports as a duration and which
	// groups it expects, never whether this round fired - so it follows the
	// writes that do decide that, rather than racing them.
	if err := coordinator.applyNoDataMemory(ctx, request, noDataMemory); err != nil {
		return execution.SlotExecutionResult{}, err
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
	// Observation only. The cause is deliberately not put on
	// completion.ReasonCode, which is persisted and decides how consecutive gaps
	// fold into a Progress gap summary; changing that is a separate decision
	// about a durable structure.
	var attribution execution.CompletionAttribution
	for _, plan := range evaluated.Plans {
		attribution.Coverage.Merge(plan.HistoryCoverage)
	}
	if len(changedPlans) > 0 {
		var cause execution.CompletionCause
		completion, cause = configDriftCompletion(request.Contract, &primary)
		attribution.Cause = cause
	} else {
		completion.Kind, attribution.Cause, attribution.Reason, err =
			execution.DeriveStreamingCompletionDetail(header, bindings, evaluated)
		if err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive completion: %w", err)
		}
		completion.Result = evaluated.Result
		completion.ReasonCode = evaluated.ReasonCode
	}
	if deterministicTerminalReason != "" {
		completion.Kind = execution.CompletionTerminal
		completion.Result = observability.ResultTerminal
		completion.ReasonCode = deterministicTerminalReason
		// A terminal Slot is no longer an unavailable one, so whatever the
		// traversal was about to say is now about a completion that did not
		// happen. deriveCompletion reports no cause for TERMINAL for the same
		// reason; the override has to follow it or the two disagree here.
		//
		// The coverage goes with it. The windows really were summarised and
		// the counts are not wrong, but they would arrive as the evidence for
		// a reason no longer being reported, and evidence with no claim beside
		// it gets read as whatever the reader came expecting.
		attribution = execution.CompletionAttribution{}
	}
	if len(changedPlans) > 0 {
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: activationRequest,
			currentFacts:      guardActivations,
			activations:       changedActivations,
			completion:        completion,
			completionCause:   attribution,
		}
	}
	progressActivations, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}
	if !guardActivations.SameSelections(progressActivations) {
		driftCompletion, driftCause := configDriftCompletion(request.Contract, &primary)
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: activationRequest,
			currentFacts:      progressActivations,
			activations:       changedSelectedActivations(guardActivations, progressActivations),
			completion:        driftCompletion,
			completionCause:   execution.CompletionAttribution{Cause: driftCause},
		}
	}
	if err := coordinator.admitDuePlans(ctx, request, header.DuePlans); err != nil {
		return execution.SlotExecutionResult{}, err
	}

	result, err := coordinator.commitProgress(ctx, request, completion, attribution)
	if err == nil && result.Completed {
		result.QueryAvailability = queryAvailability
	}
	return result, err
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

// completionCause is observation only: it says which of the conditions that
// share the UNAVAILABLE completion kind actually happened. It is a separate
// parameter rather than a field on the completion because the completion is
// persisted and its reason decides how consecutive gaps fold in a Progress gap
// summary.
func (coordinator *SlotExecutionCoordinator) commitProgress(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	completion execution.SlotCompletion,
	completionCause execution.CompletionAttribution,
) (execution.SlotExecutionResult, error) {
	if request.ExpiredRange != nil {
		return coordinator.commitExpiredRange(ctx, request, completion)
	}
	started := time.Now()
	progressRequest := execution.ProgressCommitRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, ExpectedNextSlot: request.ExpectedNextSlot, Completion: completion,
		Projection: request.UnfinishedProjection(), ContentScope: request.ContentScope,
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
	// The Slot is written down. Whatever this attempt applied is now recorded
	// where it belongs, so it must leave no mark behind: the mark answers a
	// question only an attempt that never got here can raise.
	appliedPlansFrom(ctx).progressCommitted()
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
	coordinator.observeCommittedProgress(ctx, request.Operation, started, observationResult, observationReason,
		string(completion.Kind), string(completionCause.Cause), string(completionCause.Reason), completionCause.Coverage,
		completion.Evidence)
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
	// A fence refusal comes back as the store's typed error with no reason
	// on the result; without naming it here the admission line -- and the
	// fence_checked line relayed from it -- said internal_unknown for a
	// stale fence, a Query Group assigned elsewhere and a moved scope alike.
	reason := result.ReasonCode
	if named, ok := ownership.RefusalReason(err); ok && reason == "" {
		reason = execution.ReasonCode(named)
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageSideEffectAdmission, request.Operation, started, "", reason, err)
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
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: events[0].PlanRef.StrategyID, BusinessID: events[0].BusinessID})
	// The sink's own count of what it handed the broker, for the line: a
	// batch the protocol has no message for is a success that wrote nothing.
	ctx, outputWrite := observability.ContextWithOutputWriteReport(ctx)
	started := time.Now()
	err := coordinator.ports.Events.WriteBatch(ctx, events)
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.OutputWritten != nil {
			c.OutputWritten(events, err)
		}
	})
	reason := execution.ReasonCode(observability.ReasonNone)
	var rejection *observability.OutputRejectionFacts
	if err != nil {
		reason = execution.ReasonCode(observability.ReasonInternalUnknown)
		if isRetryableOutputDependency(err) {
			reason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
		}
		// The sink's own refusal is named by the sink: the reason word is the
		// observation's reason, and the sentence travels as facts beside it
		// rather than being read back out of the error chain.
		if named, rejected := outputRejectionReason(err); rejected {
			reason = named
			rejection = &observability.OutputRejectionFacts{Reason: string(named)}
			var detailed outputRejectionDetail
			if errors.As(err, &detailed) {
				rejection.Detail = detailed.OutputRejectionDetail()
			}
		}
		if deferral, deferred := outputDeferralReason(err); deferred {
			reason = deferral
		}
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		ReasonCode: observability.ReasonCode(reason), Duration: time.Since(started),
		Counts: observability.Counts{Events: int64(len(events))}, Err: err, OutputRejection: rejection,
		OutputWrite: outputWrite(),
	})
	if err != nil {
		return fmt.Errorf("alarmd worker: acknowledge events: %w", err)
	}
	// Only after the ACK: a batch the sink did not take opened nothing at
	// the consumer, and the copy must not say it did. The constructor
	// requires the port; the guard is for tests that build the struct.
	if coordinator.ports.OpenAlerts != nil {
		coordinator.ports.OpenAlerts.Acknowledged(events)
	}
	return nil
}

// outputRejectionDetail is the sentence the sink's refusal carries beside its
// reason word -- the converter's or the client's, without identity. Declared
// here rather than imported so the coordinator names the shape it reads and
// not the package that produces it, the way outputRejectionReason does.
type outputRejectionDetail interface {
	OutputRejectionDetail() string
}

func isRetryableOutputDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableOutputDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}

// outputDeferralReason reports whether the sink declined to start the batch
// because the Slot's lease has less life left than the batch needs
// (kafka.OutputDeferredError), and the reason it names. Retryable by
// construction -- the next renewal changes the answer -- but not an unknown
// acknowledgement: no broker was asked.
func outputDeferralReason(err error) (execution.ReasonCode, bool) {
	if err == nil {
		return "", false
	}
	var deferral interface{ OutputDeferralReason() string }
	if !errors.As(err, &deferral) || deferral == nil || deferral.OutputDeferralReason() == "" {
		return "", false
	}
	return execution.ReasonCode(deferral.OutputDeferralReason()), true
}

// outputRejectionReason reports whether the sink refused to write the events
// on its own account -- the converter would not represent them, or the client
// refused them before any broker -- and the reason it names for the Slot's
// completion (kafka.OutputRejectedError). Such an error is checked before the
// dependency marker on purpose: it never carries that marker, and a sink that
// gave it both would have a reader retry a refusal.
func outputRejectionReason(err error) (execution.ReasonCode, bool) {
	if err == nil {
		return "", false
	}
	var rejection interface{ OutputRejectionReason() string }
	if !errors.As(err, &rejection) || rejection == nil || rejection.OutputRejectionReason() == "" {
		return "", false
	}
	return execution.ReasonCode(rejection.OutputRejectionReason()), true
}

// admitState admits one Plan's mutations in Store-sized chunks. It returns the
// deterministic rejections by identity and, aligned with mutations, the
// encoded size the store measured for each admitted mutation.
func (coordinator *SlotExecutionCoordinator) admitState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	retention []execution.StateRetentionRequirement,
	mutations []execution.StateMutation,
) (map[execution.StateKeyIdentity]execution.ReasonCode, []int64, error) {
	started := time.Now()
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	encodedBytes := make([]int64, len(mutations))
	var totals applyTotals
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		chunkStarted := time.Now()
		result, err := coordinator.ports.State.AdmitRuntime(ctx, execution.StateApplyRequest{
			Contract: contractRef, Retention: retention, Items: chunkItems,
		})
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
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err, nil)
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
	contentScope string,
	retention []execution.StateRetentionRequirement,
	mutations []execution.StateMutation,
	encodedBytes []int64,
) (map[execution.StateKeyIdentity]execution.ReasonCode, error) {
	started := time.Now()
	fenced, ok := coordinator.ports.State.(execution.FencedStateStore)
	useFence := ok && fence.Validate(contractRef) == nil
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	var totals applyTotals
	var alreadyApplied observability.StateAlreadyAppliedFacts
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		expectedRevisions := make(map[execution.StateKeyIdentity]uint64, len(chunkItems))
		for _, mutation := range chunkItems {
			expectedRevisions[mutation.Identity] = mutation.ExpectedBlobRevision
		}
		applyRequest := execution.StateApplyRequest{Contract: contractRef, Retention: retention, Items: chunkItems}
		chunkStarted := time.Now()
		var result execution.StateApplyResult
		var err error
		if useFence {
			result, err = fenced.ApplyRuntimeFenced(ctx, applyRequest, execution.StateApplyFence{Fence: fence, ContentScope: contentScope})
		} else {
			result, err = coordinator.ports.State.ApplyRuntime(ctx, applyRequest)
		}
		var reason execution.ReasonCode
		var conflicts observability.StateVersionConflictFacts
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
				var refusal *StateConflictError
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateApplied:
						// This attempt wrote this Plan's state. Noted here
						// because this is the only place that knows, and used
						// only if the attempt then fails to write its Progress.
						appliedPlansFrom(ctx).recordApplied(item.Identity.Plan)
					case execution.StateApplyAlreadyApplied:
						// The store says how it decided; a store that does not
						// is read as stable, so a missing kind cannot pose as
						// the one reading this family exists to catch.
						kind := observability.StateAlreadyAppliedKind(item.AlreadyApplied)
						if kind == "" {
							kind = observability.StateAlreadyAppliedStable
						}
						alreadyApplied.Record(observability.StateAlreadyAppliedAtApply, kind,
							string(item.Identity.SeriesIdentityDigest), expectedRevisions[item.Identity], item.StoredBlobRevision)
					case execution.StateApplyDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
						rejected++
					case execution.StateApplyStale, execution.StateApplyVersionConflict:
						// Every refused item is counted by the comparison
						// that refused it; the error carries the first one's
						// values. A store that does not say which comparison
						// counts as other, so a missing kind cannot pose as
						// the one a reader would act on.
						named := &StateConflictError{Stage: "state apply did not complete", Status: string(item.Status), RepeatedKey: item.RepeatedKey}
						if item.Status == execution.StateApplyVersionConflict {
							named.Kind, named.ExpectedRevision, named.StoredRevision, named.VersionComparison =
								item.VersionConflict, expectedRevisions[item.Identity], item.StoredBlobRevision, item.StoredVersionComparison
							conflicts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictKind(item.VersionConflict),
								string(item.Identity.SeriesIdentityDigest), expectedRevisions[item.Identity], item.StoredBlobRevision,
								string(item.StoredVersionComparison), item.RepeatedKey)
						}
						if refusal == nil {
							refusal = named
						}
					default:
						err = fmt.Errorf("state apply did not complete: %s", item.Status)
					}
				}
				if err == nil && refusal != nil {
					err = refusal
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
		// A version refusal is a typed error with a name of its own. The
		// item's reason is empty for it -- the store names the status, not a
		// reason -- so without this the chunk's line normalised to
		// internal_unknown beside an error_type that already said which
		// refusal it was, while the terminal line for the same round named it.
		if named, ok := StateConflictReason(err); ok {
			reason = named
		}
		// A fenced write the store refused is the store's typed error too: a
		// stale fence, or a content scope that moved under the write. Named
		// the same way, so the three numbers a scope move is verified by --
		// old scope written before it took effect, old scope refused after,
		// new scope written -- are readable from this line's reason.
		if named, ok := ownership.RefusalReason(err); ok {
			reason = execution.ReasonCode(named)
		}
		totals.keys += int64(len(chunkItems))
		totals.bytes += chunkBytes
		var conflictFacts *observability.StateVersionConflictFacts
		if !conflicts.Empty() {
			conflictFacts = &conflicts
		}
		coordinator.observeChunk(ctx, observability.StageStateApplied, operation, chunkStarted, started, observationResult, reason,
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err, conflictFacts)
		return err
	})
	if !alreadyApplied.Empty() {
		coordinator.emitAlreadyApplied(ctx, operation, alreadyApplied)
	}
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: apply state: %w", err)
	}
	return deterministic, nil
}

// emitAlreadyApplied publishes one Slot's ALREADY_APPLIED decisions from one
// site. It is published before the apply error is returned, so a chunk that
// met its own landed write and a later chunk that failed are both on the
// record for the round the retry follows.
func (coordinator *SlotExecutionCoordinator) emitAlreadyApplied(ctx context.Context, operation execution.Operation, facts observability.StateAlreadyAppliedFacts) {
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, Operation: observability.Operation(operation),
		Direction: observability.DirectionInternal, ReasonCode: observability.ReasonNone,
		StateAlreadyApplied: &facts,
	})
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
func (coordinator *SlotExecutionCoordinator) observeCommittedProgress(ctx context.Context, operation execution.Operation, started time.Time, result observability.Result, reason observability.ReasonCode, kind, cause, causeReason string, coverage execution.HistoryCoverage, evidence *execution.ExecutionEvidence) {
	if reason == "" {
		reason = observability.ReasonNone
	}
	defer func() { _ = recover() }()
	coverageFacts := historyCoverageFacts(coverage)
	coordinator.ports.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		Result: result, ReasonCode: reason, Duration: time.Since(started), ProgressCompletionKind: kind,
		ProgressCompletionCause: cause, ProgressCompletionReason: causeReason,
		HistoryCoverage: coverageFacts, ExecutionEvidence: executionEvidenceFacts(evidence),
	})
}

// executionEvidenceFacts carries what an earlier attempt got to onto the
// completion's observation, in the same hand-copied shape as the coverage
// counts above and for the same reason: a field computed where the decision is
// made and dropped on the way out is this codebase's most frequent defect.
func executionEvidenceFacts(evidence *execution.ExecutionEvidence) *observability.ExecutionEvidenceFacts {
	if evidence == nil {
		return nil
	}
	return &observability.ExecutionEvidenceFacts{
		Kind: string(evidence.Kind), PlansApplied: evidence.PlansApplied, PlansTotal: evidence.PlansTotal,
	}
}

// historyCoverageFacts carries the Slot's window counts onto the observation.
//
// A named function rather than a literal inside the observer call, because this
// is a hand-written copy between two structs that have to hold the same numbers,
// and this codebase's most frequent defect by a wide margin is exactly that: a
// field that is computed where the decision is made, used there, and dropped on
// one of the handoffs on the way out. Inline, nothing could execute this
// translation on its own, so a field added to one side and forgotten on the
// other was invisible -- the page would render a deployment it could not have
// observed, with nothing on it disagreeing.
//
// Levels == 0 yields nil on purpose. A Slot that summarised no window at all and
// a Slot whose windows were all complete both report zero short, and they are
// opposite statements; absence is how the first one stays sayable.
func historyCoverageFacts(coverage execution.HistoryCoverage) *observability.HistoryCoverageFacts {
	if coverage.Levels == 0 {
		return nil
	}
	return &observability.HistoryCoverageFacts{
		Levels: coverage.Levels, Short: coverage.Short, Empty: coverage.Empty,
		WorstValid: coverage.WorstValid, WorstRequired: coverage.WorstRequired,
		Guarded: coverage.Guarded,
		Fresh:   coverage.Fresh, ShortFresh: coverage.ShortFresh,
		Abnormal: coverage.Abnormal, AbnormalOnIncomplete: coverage.AbnormalOnIncomplete,
		Unusable: coverage.Unusable, UnusableReason: coverage.UnusableReason,
	}
}

// configDriftCompletion builds the completion of a Slot whose activations moved
// under it, and keeps it honest about its PRIMARY.
//
// Drift is reported as a partial gap, which is the right shape when the query
// itself answered. It is not when the PRIMARY was unavailable: PARTIAL says
// "some of it arrived", and the completion contract exists precisely to stop
// that from hiding "none of it did". The contract does catch the pair - but at
// progress commit, so the Slot is never recorded and runs again the next
// minute, indefinitely. The pair has to not be produced rather than be
// rejected after the fact.
//
// Both kinds carry the same result and the same reason class, so drift stays
// the reason either way; only the claim about the data changes.
//
// There is one constructor because there were two producers: the Plan drift
// branch and the activation-selection race, the second an inline literal that
// a fix to the first does not reach. Two places that must agree about an
// invariant will eventually stop agreeing.
// The cause is returned beside the kind rather than derived by the caller for
// the reason DeriveCompletion gives: two functions that must agree about the
// same Slot will eventually disagree, and then the page explains a completion
// that did not happen. Here the two answers come from the one fact this
// constructor already reads.
//
// With a usable primary the Slot completes as COMPLETED_WITH_PARTIAL_GAP and
// names CONFIG_DRIFT as its cause. That kind used to reach the commit line
// with no cause at all, listed beside the partial gaps a provider caused;
// the two call for opposite responses, since an edited strategy needs nobody
// and clears on the next Slot.
func configDriftCompletion(
	contractRef execution.FrozenExecutionContractRef,
	primary *execution.PrimaryInputFact,
) (execution.SlotCompletion, execution.CompletionCause) {
	kind := execution.CompletionPartialGap
	cause := execution.CauseConfigDrift
	if primary != nil && primary.Completeness == execution.CompletenessUnavailable {
		kind = execution.CompletionUnavailable
		cause = execution.CausePrimaryInputUnavailable
	}
	return execution.SlotCompletion{
		Contract: contractRef, Kind: kind, Primary: primary,
		Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift),
	}, cause
}

// storedStateLabel names the stored state a write-reuse comparison was made
// against. The two steady states a deployment actually spends its rounds in --
// a series that keeps recovering and one that never leaves history warming --
// are both written every round and are separated here, because a single rate
// over both describes neither.
func storedStateLabel(status execution.StateLoadStatus) observability.StateWriteReuseStored {
	switch status {
	case execution.StateFoundReady:
		return observability.StateWriteReuseStoredReady
	case execution.StateFoundWarming:
		return observability.StateWriteReuseStoredWarming
	case execution.StateFoundGapped:
		return observability.StateWriteReuseStoredGapped
	case execution.StateMissingWarming:
		return observability.StateWriteReuseStoredMissing
	default:
		return observability.StateWriteReuseStoredOther
	}
}

func gapExtensionFacts(marker execution.GapGuardSnapshot, mutation execution.PlanGapMutation) *observability.GapExtensionFacts {
	facts := &observability.GapExtensionFacts{StrategyID: mutation.Identity.Plan.StrategyID, MarkerRevision: marker.MarkerRevision}
	for _, scope := range marker.Scopes {
		facts.Persisted = append(facts.Persisted, observability.GapScopeFacts{LevelID: scope.Scope.LevelID, HasLevel: scope.Scope.HasLevel,
			Status: string(scope.Status), Reason: string(scope.ReasonCode), Required: scope.RequiredFullSlots, Observed: scope.ObservedFullSlots})
	}
	for _, scope := range mutation.Scopes {
		facts.Proposed = append(facts.Proposed, observability.GapScopeFacts{LevelID: scope.Scope.LevelID, HasLevel: scope.Scope.HasLevel,
			Kind: string(scope.Kind), Status: string(execution.GapStatusGapped), Reason: string(scope.ReasonCode), Required: scope.RequiredFullSlots})
	}
	return facts
}
