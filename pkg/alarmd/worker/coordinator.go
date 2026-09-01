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
	"sort"
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
	ports  Ports
	budget ProvisionalBudget
}

type ProvisionalBudget struct {
	MaxSeries         uint64
	MaxRetainedBytes  uint64
	MaxStateMutations uint64
	MaxEvents         uint64
	MaxGapMutations   uint64
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
	finalization, err := coordinator.ports.Finalization.ResolveFinalization(ctx, request)
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: resolve finalization: %w", err)
	}
	if err := finalization.Validate(request); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid finalization: %w", err)
	}
	if finalization.Mode != execution.FinalizationQueryRequired {
		if err := coordinator.ports.Finalization.VerifyFrozenDuePlanTargets(ctx, request.Contract, finalization.Targets); err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: verify frozen due Plan targets: %w", err)
		}
		return coordinator.executeQueryFreeFinalization(ctx, request, finalization)
	}
	started := time.Now()
	stream := &streamedExecution{coordinator: coordinator, request: request}
	completion, err := coordinator.ports.Query.Execute(ctx, execution.QueryExecutionRequest{
		Contract: request.Contract, Operation: request.Operation,
	}, stream)
	if err != nil {
		coordinator.observe(ctx, observability.ComponentAccess, observability.StageQueryCompleted, request.Operation, started, "", "", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: query: %w", err)
	}
	if err := stream.complete(completion); err != nil {
		coordinator.observe(ctx, observability.ComponentAccess, observability.StageQueryCompleted, request.Operation, started, "", "", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid query result: %w", err)
	}
	queryResult, queryReason := provisionalResult(stream.evaluated)
	coordinator.observe(ctx, observability.ComponentAccess, observability.StageQueryCompleted, request.Operation, started, queryResult, queryReason, nil)
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

func (coordinator *SlotExecutionCoordinator) executeQueryFreeFinalization(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	finalization execution.QueryFreeFinalization,
) (execution.SlotExecutionResult, error) {
	plans := append([]execution.PlanIdentity(nil), finalization.Targets.Plans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left], plans[right]) })
	activationRequest := execution.PlanActivationRequest{Contract: request.Contract, Plans: plans}
	guardFacts, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonProviderUnavailable)), nil
	}

	var result execution.SlotExecutionResult
	var changedActivations *execution.PlanActivationResult
	err = coordinator.ports.Sequencer.Sequence(ctx, activatedPlanSequencingScope(request.Contract.Slot, guardFacts), func(sequenceCtx context.Context) error {
		if _, err := coordinator.ensureActivatedPlanGaps(sequenceCtx, request, finalization.ReasonCode, guardFacts); err != nil {
			return err
		}
		progressFacts, err := coordinator.loadActivations(sequenceCtx, activationRequest)
		if err != nil {
			result = activationRetry(execution.ReasonCode(contract.ReasonProviderUnavailable))
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
		if err := coordinator.protectActivatedPlanGaps(ctx, request, finalization.ReasonCode, *changedActivations); err != nil {
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
) error {
	return coordinator.ports.Sequencer.Sequence(
		ctx,
		activatedPlanSequencingScope(request.Contract.Slot, activations),
		func(sequenceCtx context.Context) error {
			_, err := coordinator.ensureActivatedPlanGaps(sequenceCtx, request, reason, activations)
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
			)
			if err != nil || !alreadyProtected {
				return err
			}
			progressFacts, err := coordinator.loadActivations(sequenceCtx, protection.activationRequest)
			if err != nil {
				result = activationRetry(execution.ReasonCode(contract.ReasonProviderUnavailable))
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
) (bool, error) {
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
	loaded, err := coordinator.ports.GapGuard.LoadGaps(ctx, loadRequest)
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
				if marker.Status != execution.GapFound || marker.PersistedMutationDigest != mutation.MutationDigest {
					return false, errors.New("alarmd worker: activated Plan gap marker conflicts with the Slot")
				}
				requireAlready[item.Identity] = struct{}{}
			}
		}
		mutations = append(mutations, mutation)
	}
	return coordinator.applyActivatedPlanGaps(ctx, request.Operation, request.Contract, mutations, requireAlready)
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
	started := time.Now()
	result, err := coordinator.ports.GapGuard.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: contractRef, Items: items})
	var reason execution.ReasonCode
	allAlready := len(items) > 0
	if err == nil {
		if err = result.Validate(); err == nil {
			expected := make([]execution.PlanGapIdentity, len(items))
			actual := make([]execution.PlanGapIdentity, len(result.Items))
			for index, item := range items {
				expected[index] = item.Identity
			}
			for index, item := range result.Items {
				actual[index] = item.Identity
				reason = item.ReasonCode
				if item.Status != execution.GapGuardAlreadyApplied {
					allAlready = false
				}
				if _, redo := requireAlready[item.Identity]; redo && item.Status != execution.GapGuardAlreadyApplied {
					err = fmt.Errorf("activated Plan gap redo did not converge: %s", item.Status)
					break
				}
				if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
					err = fmt.Errorf("activated Plan gap guard did not complete: %s", item.Status)
					break
				}
			}
			if err == nil {
				err = validateIdentitySet(expected, actual, "activated Plan gap guard")
			}
		}
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageGapGuardCommitted, operation, started, "", reason, err)
	if err != nil {
		return false, fmt.Errorf("alarmd worker: apply activated Plan gap guard: %w", err)
	}
	return allAlready, nil
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
		return activationRetry(execution.ReasonCode(contract.ReasonProviderUnavailable)), nil
	}
	forced := unsatisfiedForcedWarmingActivations(guardActivations, loadedGaps)
	if len(forced.Facts) > 0 {
		if _, err := coordinator.ensureActivatedPlanGaps(
			ctx, request, execution.ReasonCode(contract.ReasonConfigDrift), forced,
		); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		return activationRetry(execution.ReasonCode(contract.ReasonConfigDrift)), nil
	}
	changedPlans, changedActivations := changedDuePlanActivations(header.DuePlans, guardActivations)

	planResults := append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
	sort.Slice(planResults, func(left, right int) bool {
		return lessPlanIdentity(planResults[left].Plan, planResults[right].Plan)
	})
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
			view, found := loadedState.Find(stateResult.Mutation.Identity)
			if !found {
				return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluation returned state without preflight view")
			}
			started := time.Now()
			disposition := execution.ClassifyStateMutation(view, stateResult.Mutation)
			switch disposition {
			case execution.StateProceed:
				coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
				mutations = append(mutations, stateResult.Mutation)
				eventsByState[stateResult.Mutation.Identity] = append([]contract.TriggerEventV1(nil), stateResult.Events...)
			case execution.StateAlreadyApplied:
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
			rejected, err := coordinator.admitState(ctx, request.Operation, request.Contract, mutations)
			if err != nil {
				return execution.SlotExecutionResult{}, err
			}
			accepted := make([]execution.StateMutation, 0, len(mutations)-len(rejected))
			events := make([]contract.TriggerEventV1, 0)
			for _, mutation := range mutations {
				if reason, terminal := rejected[mutation.Identity]; terminal {
					if stateAdmissionTerminalReason == "" {
						stateAdmissionTerminalReason = reason
					}
					continue
				}
				accepted = append(accepted, mutation)
				events = append(events, eventsByState[mutation.Identity]...)
			}
			sortTriggerEvents(events)
			if err := coordinator.writeEvents(ctx, request.Operation, events); err != nil {
				return execution.SlotExecutionResult{}, err
			}
			if len(accepted) > 0 {
				rejectedApply, err := coordinator.applyState(ctx, request.Operation, request.Contract, accepted)
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
		return activationRetry(execution.ReasonCode(contract.ReasonProviderUnavailable)), nil
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
) execution.PlanActivationResult {
	result := execution.PlanActivationResult{Contract: activations.Contract}
	for _, fact := range activations.Facts {
		if fact.Selection == execution.ActivationNone || !fact.Selected.ForceWarming {
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
	started := time.Now()
	progressRequest := execution.ProgressCommitRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, ExpectedNextSlot: request.ExpectedNextSlot, Completion: completion,
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
	coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted, request.Operation, started, observationResult, observationReason, nil)
	return execution.SlotExecutionResult{Completed: true, Result: completion.Result, ReasonCode: completion.ReasonCode}, nil
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

func (coordinator *SlotExecutionCoordinator) applyGap(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
) error {
	if len(items) == 0 {
		return nil
	}
	started := time.Now()
	result, err := coordinator.ports.GapGuard.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: contractRef, Items: items})
	var reason execution.ReasonCode
	if err == nil {
		if err = result.Validate(); err != nil {
			// Keep exact typed receipt errors instead of normalizing them to an
			// unrelated successful status.
		} else {
			expected := make([]execution.PlanGapIdentity, len(items))
			actual := make([]execution.PlanGapIdentity, len(result.Items))
			for index, item := range items {
				expected[index] = item.Identity
			}
			for index, item := range result.Items {
				actual[index] = item.Identity
				reason = item.ReasonCode
				if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
					err = fmt.Errorf("gap guard did not complete: %s", item.Status)
					break
				}
			}
			if err == nil {
				err = validateIdentitySet(expected, actual, "gap guard")
			}
		}
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageGapGuardCommitted, operation, started, "", reason, err)
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
	started := time.Now()
	err := coordinator.ports.Events.WriteBatch(ctx, events)
	reason := execution.ReasonCode(observability.ReasonNone)
	if err != nil {
		reason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
	}
	coordinator.observeWithCounts(ctx, observability.ComponentOutput, observability.StageEventACKed, operation, started,
		"", reason, observability.Counts{Events: int64(len(events))}, err)
	if err != nil {
		return fmt.Errorf("alarmd worker: acknowledge events: %w", err)
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) admitState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	mutations []execution.StateMutation,
) (map[execution.StateKeyIdentity]execution.ReasonCode, error) {
	started := time.Now()
	result, err := coordinator.ports.State.AdmitRuntime(ctx, execution.StateApplyRequest{Contract: contractRef, Items: mutations})
	var reason execution.ReasonCode
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	if err == nil {
		if err = result.Validate(); err != nil {
		} else {
			expected := make([]execution.StateKeyIdentity, len(mutations))
			actual := make([]execution.StateKeyIdentity, len(result.Items))
			for index, mutation := range mutations {
				expected[index] = mutation.Identity
			}
			for index, item := range result.Items {
				actual[index] = item.Identity
			}
			err = validateIdentitySet(expected, actual, "state admission")
			if err == nil {
				reason = firstStateAdmissionFailureReason(result.Items)
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateAdmissionAccepted:
					case execution.StateAdmissionDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
					default:
						err = fmt.Errorf("state admission did not complete: %s", item.Status)
					}
				}
			}
		}
	}
	observationResult := observability.Result(observability.ResultSuccess)
	if len(deterministic) > 0 {
		observationResult = observability.ResultTerminal
	}
	coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageStateAdmission, operation, started,
		observationResult, reason, observability.Counts{Keys: int64(len(mutations))}, err)
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: state admission: %w", err)
	}
	return deterministic, nil
}

func (coordinator *SlotExecutionCoordinator) applyState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	mutations []execution.StateMutation,
) (map[execution.StateKeyIdentity]execution.ReasonCode, error) {
	started := time.Now()
	result, err := coordinator.ports.State.ApplyRuntime(ctx, execution.StateApplyRequest{Contract: contractRef, Items: mutations})
	var reason execution.ReasonCode
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	if err == nil {
		if err = result.Validate(); err != nil {
		} else {
			expected := make([]execution.StateKeyIdentity, len(mutations))
			actual := make([]execution.StateKeyIdentity, len(result.Items))
			for index, mutation := range mutations {
				expected[index] = mutation.Identity
			}
			for index, item := range result.Items {
				actual[index] = item.Identity
			}
			err = validateIdentitySet(expected, actual, "state apply")
			if err == nil {
				reason = firstStateApplyFailureReason(result.Items)
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateApplied, execution.StateApplyAlreadyApplied:
					case execution.StateApplyDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
					default:
						err = fmt.Errorf("state apply did not complete: %s", item.Status)
					}
				}
			}
		}
	}
	observationResult := observability.Result(observability.ResultSuccess)
	if len(deterministic) > 0 {
		observationResult = observability.ResultTerminal
	}
	coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageStateApplied, operation, started,
		observationResult, reason, observability.Counts{Keys: int64(len(mutations))}, err)
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
	if err != nil {
		result = observability.Result(observability.ResultFailed)
		if reason == "" || reason == observability.ReasonNone {
			reason = observability.ReasonInternalUnknown
		}
	} else {
		if result == "" {
			result = observability.ResultSuccess
		}
		if reason == "" {
			reason = observability.ReasonNone
		}
	}
	observation := observability.Observation{
		Component: component, Stage: stage, Result: result,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		ReasonCode: reason, Duration: time.Since(started), Counts: counts, Err: err,
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
