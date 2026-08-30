package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// streamedExecution is the Coordinator-owned provisional lifetime. It owns no
// external side effect: Begin and ConsumeSeries may only read State/Gap and
// invoke the pure Evaluator.
type streamedExecution struct {
	coordinator *SlotExecutionCoordinator
	request     execution.SlotExecutionRequest
	header      execution.InternalExecutionHeader
	bindings    []execution.NamedInputBinding
	stateItems  []execution.StatePreflightItem
	gapItems    []execution.PlanGapLoadItem
	state       execution.StatePreflightResult
	gaps        execution.GapLoadResult
	evaluated   execution.EvaluationResult
	delivered   []execution.SeriesDelivery
	series      uint64
	retained    uint64
	began       bool
}

func (stream *streamedExecution) Begin(ctx context.Context, header execution.InternalExecutionHeader) error {
	if stream.began {
		return errors.New("alarmd worker: QueryExecutionSource called Begin more than once")
	}
	if err := header.Validate(stream.request.Contract); err != nil {
		return fmt.Errorf("alarmd worker: invalid execution header: %w", err)
	}
	stream.began = true
	stream.header = header
	items, err := gapPreflightForHeader(header)
	if err != nil {
		return err
	}
	stream.gapItems = items
	request := execution.GapLoadRequest{Contract: header.Contract, Items: items}
	started := time.Now()
	stream.gaps, err = stream.coordinator.ports.GapGuard.LoadGaps(ctx, request)
	if err == nil {
		err = execution.ValidateGapLoad(request, stream.gaps)
	}
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentState, observability.StageGapLoaded,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: gap preflight: %w", err)
	}
	gapResult, gapReason := summarizeGapLoad(stream.gaps)
	stream.coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapLoaded,
		stream.request.Operation, started, gapResult, gapReason, observability.Counts{Keys: int64(len(stream.gaps.Items))}, nil)
	return nil
}

func (stream *streamedExecution) ConsumeSeries(ctx context.Context, batch execution.SeriesExecutionBatch) error {
	if !stream.began {
		return errors.New("alarmd worker: QueryExecutionSource delivered series before Begin")
	}
	if err := batch.Validate(stream.header); err != nil {
		return fmt.Errorf("alarmd worker: invalid series batch: %w", err)
	}
	stateItems, err := execution.DeriveSeriesStatePreflight(stream.header, batch)
	if err != nil {
		return fmt.Errorf("alarmd worker: derive series execution: %w", err)
	}
	stateRequest := execution.StatePreflightRequest{Contract: stream.request.Contract, Items: stateItems}
	started := time.Now()
	loaded, err := stream.coordinator.ports.State.LoadRuntime(ctx, stateRequest)
	if err == nil {
		loaded, err = execution.ClassifyStatePreflight(stateRequest, loaded)
	}
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentState, observability.StageStatePreflight,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: series state preflight: %w", err)
	}
	stateResult, stateReason := summarizeStateLoad(loaded)
	stream.coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageStatePreflight,
		stream.request.Operation, started, stateResult, stateReason, observability.Counts{Keys: int64(len(loaded.Items))}, nil)
	evaluationRequest := execution.EvaluationRequest{Header: stream.header, Batch: batch, State: loaded, Gaps: stream.gaps}
	started = time.Now()
	evaluated, err := stream.coordinator.ports.Evaluator.Evaluate(ctx, evaluationRequest)
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentEvaluation, observability.StageEvaluationCompleted,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: evaluate series: %w", err)
	}
	if err := evaluated.Validate(evaluationRequest); err != nil {
		stream.coordinator.observe(ctx, observability.ComponentEvaluation, observability.StageEvaluationCompleted,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: invalid series evaluation: %w", err)
	}
	stream.coordinator.observe(ctx, observability.ComponentEvaluation, observability.StageEvaluationCompleted,
		stream.request.Operation, started, evaluated.Result, evaluated.ReasonCode, nil)
	compactBindings := make([]execution.NamedInputBinding, len(batch.Inputs))
	copy(compactBindings, batch.Inputs)
	for index := range compactBindings {
		compactBindings[index].Dataset = nil
		compactBindings[index].View = nil
	}
	retained, err := provisionalRetainedSize(compactBindings, loaded, evaluated, batch.Delivery)
	if err != nil {
		return fmt.Errorf("alarmd worker: measure provisional retention: %w", err)
	}
	stream.series += batch.Delivery.Series
	stream.retained += retained
	if stream.series > stream.coordinator.budget.MaxSeries || stream.retained > stream.coordinator.budget.MaxRetainedBytes {
		return errors.New("alarmd worker: provisional series/byte budget exceeded")
	}
	if err := mergeProvisional(&stream.evaluated, evaluated, stream.coordinator.budget); err != nil {
		return err
	}
	stream.state.Items = append(stream.state.Items, loaded.Items...)
	stream.stateItems = append(stream.stateItems, stateItems...)
	for _, binding := range compactBindings {
		stream.bindings = append(stream.bindings, binding)
	}
	merged := false
	for index := range stream.delivered {
		if stream.delivered[index].PhysicalQuery == batch.PhysicalQuery {
			stream.delivered[index], err = execution.AccumulateSeriesDelivery(stream.delivered[index], batch.Delivery)
			if err != nil {
				return fmt.Errorf("alarmd worker: accumulate series delivery: %w", err)
			}
			merged = true
			break
		}
	}
	if !merged {
		stream.delivered = append(stream.delivered, batch.Delivery)
	}
	return nil
}

func provisionalRetainedSize(
	bindings []execution.NamedInputBinding,
	state execution.StatePreflightResult,
	result execution.EvaluationResult,
	delivery execution.SeriesDelivery,
) (uint64, error) {
	encoded, err := json.Marshal(struct {
		Bindings []execution.NamedInputBinding
		State    execution.StatePreflightResult
		Result   execution.EvaluationResult
		Delivery execution.SeriesDelivery
	}{Bindings: bindings, State: state, Result: result, Delivery: delivery})
	if err != nil {
		return 0, err
	}
	return uint64(len(encoded)), nil
}

func gapPreflightForHeader(header execution.InternalExecutionHeader) ([]execution.PlanGapLoadItem, error) {
	items := make([]execution.PlanGapLoadItem, 0, len(header.DuePlans))
	for _, due := range header.DuePlans {
		version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
		if err != nil {
			return nil, err
		}
		items = append(items, execution.PlanGapLoadItem{
			Identity:     execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			ApplyVersion: version, ScheduleRevision: due.ScheduleRevision,
		})
	}
	return items, nil
}

func mergeProvisional(target *execution.EvaluationResult, next execution.EvaluationResult, budget ProvisionalBudget) error {
	if target.Contract == (execution.FrozenExecutionContractRef{}) {
		target.Contract, target.Result, target.ReasonCode = next.Contract, next.Result, next.ReasonCode
	} else if target.Contract != next.Contract {
		return errors.New("alarmd worker: series evaluations changed frozen contract")
	} else if resultRank(next.Result) > resultRank(target.Result) {
		target.Result, target.ReasonCode = next.Result, next.ReasonCode
	}
	for _, nextPlan := range next.Plans {
		index := -1
		for current := range target.Plans {
			if target.Plans[current].Plan == nextPlan.Plan {
				index = current
				break
			}
		}
		if index < 0 {
			target.Plans = append(target.Plans, nextPlan)
			continue
		}
		plan := &target.Plans[index]
		if dispositionRank(nextPlan.Disposition) > dispositionRank(plan.Disposition) {
			plan.Disposition, plan.ReasonCode = nextPlan.Disposition, nextPlan.ReasonCode
		}
		plan.LevelOutcomes = append(plan.LevelOutcomes, nextPlan.LevelOutcomes...)
		plan.StateResults = append(plan.StateResults, nextPlan.StateResults...)
		plan.GuardBeforeEvents = appendUniqueGapMutations(plan.GuardBeforeEvents, nextPlan.GuardBeforeEvents)
		plan.GuardAfterState = appendUniqueGapMutations(plan.GuardAfterState, nextPlan.GuardAfterState)
	}
	var states, events, gaps uint64
	for _, plan := range target.Plans {
		states += uint64(len(plan.StateResults))
		gaps += uint64(len(plan.GuardBeforeEvents) + len(plan.GuardAfterState))
		for _, state := range plan.StateResults {
			events += uint64(len(state.Events))
		}
	}
	if states > budget.MaxStateMutations || events > budget.MaxEvents || gaps > budget.MaxGapMutations {
		return errors.New("alarmd worker: provisional result budget exceeded")
	}
	return nil
}

func resultRank(result observability.Result) int {
	switch result {
	case observability.ResultRetrying:
		return 4
	case observability.ResultTerminal:
		return 3
	case observability.ResultDegraded:
		return 2
	case observability.ResultSuccess:
		return 1
	default:
		return 0
	}
}

func dispositionRank(disposition execution.PlanDisposition) int {
	switch disposition {
	case execution.PlanRetryPending:
		return 5
	case execution.PlanTerminal:
		return 4
	case execution.PlanUnavailable, execution.PlanReadinessGap:
		return 3
	case execution.PlanDecidedDegraded:
		return 2
	case execution.PlanDecided:
		return 1
	default:
		return 0
	}
}

func appendUniqueGapMutations(current, next []execution.PlanGapMutation) []execution.PlanGapMutation {
	for _, candidate := range next {
		duplicate := false
		for _, existing := range current {
			if existing.Identity == candidate.Identity && existing.MutationDigest == candidate.MutationDigest {
				duplicate = true
				break
			}
		}
		if !duplicate {
			current = append(current, candidate)
		}
	}
	return current
}

func (stream *streamedExecution) complete(completion execution.QueryExecutionCompletion) error {
	if !stream.began {
		return errors.New("alarmd worker: QueryExecutionSource returned completion before Begin")
	}
	if err := completion.Validate(stream.header, stream.delivered); err != nil {
		return err
	}
	stream.bindings = append(stream.bindings, completion.CompletionBindings...)
	if len(stream.evaluated.Plans) == 0 {
		for _, binding := range completion.CompletionBindings {
			if binding.Disposition == execution.AccessUnavailable {
				stream.evaluated = execution.EvaluationResult{
					Contract: stream.header.Contract, Result: observability.ResultRetrying, ReasonCode: binding.ReasonCode,
				}
				return nil
			}
		}
		return errors.New("alarmd worker: trustworthy completion produced no provisional evaluation")
	}
	return nil
}

func provisionalResult(result execution.EvaluationResult) (observability.Result, execution.ReasonCode) {
	return result.Result, result.ReasonCode
}
