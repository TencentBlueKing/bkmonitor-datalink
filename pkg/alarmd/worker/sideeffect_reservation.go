package worker

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type effectCounts struct{ states, events, gaps uint64 }

func checkEffectCounts(count effectCounts, budget ProvisionalBudget) error {
	for _, check := range []struct {
		count, limit uint64
		kind         observability.CapacityBudget
	}{
		{count.states, budget.MaxStateMutations, observability.CapacityBudgetStateMutations},
		{count.events, budget.MaxEvents, observability.CapacityBudgetEvents},
		{count.gaps, budget.MaxGapMutations, observability.CapacityBudgetGapMutations},
	} {
		if check.count > check.limit {
			return &provisionalBudgetExceededError{budget: check.kind}
		}
	}
	return nil
}

func countEffects(result execution.EvaluationResult) (count effectCounts) {
	for _, plan := range result.Plans {
		count.states += uint64(len(plan.StateResults))
		count.gaps += uint64(len(plan.GuardBeforeEvents) + len(plan.GuardAfterState))
		for _, state := range plan.StateResults {
			count.events += uint64(len(state.Events))
		}
	}
	return count
}

func newGapCount(current, next []execution.PlanGapMutation) uint64 {
	var count uint64
	for index, candidate := range next {
		if !containsGap(current, candidate) && !containsGap(next[:index], candidate) {
			count++
		}
	}
	return count
}

func containsGap(current []execution.PlanGapMutation, candidate execution.PlanGapMutation) bool {
	for _, existing := range current {
		if existing.Identity == candidate.Identity && existing.MutationDigest == candidate.MutationDigest {
			return true
		}
	}
	return false
}

// Counts only additions; existing State/Event collections are never revisited.
func addedEffectCounts(current, next execution.EvaluationResult) effectCounts {
	var count effectCounts
	for _, plan := range next.Plans {
		count.states += uint64(len(plan.StateResults))
		for _, state := range plan.StateResults {
			count.events += uint64(len(state.Events))
		}
		var previous *execution.PlanEvaluationResult
		for index := range current.Plans {
			if current.Plans[index].Plan == plan.Plan {
				previous = &current.Plans[index]
				break
			}
		}
		if previous == nil {
			count.gaps += uint64(len(plan.GuardBeforeEvents) + len(plan.GuardAfterState))
		} else {
			count.gaps += newGapCount(previous.GuardBeforeEvents, plan.GuardBeforeEvents) + newGapCount(previous.GuardAfterState, plan.GuardAfterState)
		}
	}
	return count
}

func (stream *streamedExecution) mergeProvisional(ctx context.Context, next execution.EvaluationResult, retained uint64) error {
	if stream.evaluated.Contract != (execution.FrozenExecutionContractRef{}) && stream.evaluated.Contract != next.Contract {
		return errors.New("alarmd worker: series evaluations changed frozen contract")
	}
	delta := addedEffectCounts(stream.evaluated, next)
	count := effectCounts{stream.effects.states + delta.states, stream.effects.events + delta.events, stream.effects.gaps + delta.gaps}
	// The Slot's own output is checked against the per-Slot caps before the
	// shared reservation: a Slot above them can never be applied by this
	// process, so that rejection is deterministic and carries the Slot's own
	// facts instead of the shared usage.
	slotBudget := stream.coordinator.slotBudget()
	if err := checkEffectCounts(count, slotBudget); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			err = stream.slotBudgetRejection(exceeded.budget, count, delta, slotBudget)
			stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
		}
		return err
	}
	retained += newEffectBytes(stream.evaluated, next)
	if err := stream.coordinator.acquireEffects(delta, retained, stream, stream.reservationPhase("normal_output")); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
		}
		return err
	}
	// The contract check and shared reservation make this append infallible.
	// Loaded Gap facts remain independently accounted in stream.gapFacts.
	appendProvisional(&stream.evaluated, next)
	stream.effects.states += delta.states
	stream.effects.events += delta.events
	stream.effects.gaps += delta.gaps
	stream.retained += retained
	return nil
}

func newEffectBytes(current, next execution.EvaluationResult) uint64 {
	var retained uint64
	for _, plan := range next.Plans {
		var previous *execution.PlanEvaluationResult
		for index := range current.Plans {
			if current.Plans[index].Plan == plan.Plan {
				previous = &current.Plans[index]
				break
			}
		}
		if previous == nil {
			retained += 2 * retainedObjectBytes(plan)
			continue
		}
		if len(plan.LevelOutcomes) != 0 {
			retained += 2 * retainedObjectBytes(plan.LevelOutcomes)
		}
		if len(plan.StateResults) != 0 {
			retained += 2 * retainedObjectBytes(plan.StateResults)
		}
		for _, pair := range []struct{ previous, next []execution.PlanGapMutation }{{previous.GuardBeforeEvents, plan.GuardBeforeEvents}, {previous.GuardAfterState, plan.GuardAfterState}} {
			for index, candidate := range pair.next {
				if !containsGap(pair.previous, candidate) && !containsGap(pair.next[:index], candidate) {
					retained += 2 * retainedObjectBytes(candidate)
				}
			}
		}
	}
	return retained
}

func (coordinator *SlotExecutionCoordinator) acquireEffects(delta effectCounts, retained uint64, stream *streamedExecution, phase string) error {
	reservation := &coordinator.reservations
	reservation.mu.Lock()
	defer reservation.mu.Unlock()
	remaining := ProvisionalBudget{MaxStateMutations: coordinator.budget.MaxStateMutations - reservation.states,
		MaxEvents: coordinator.budget.MaxEvents - reservation.events, MaxGapMutations: coordinator.budget.MaxGapMutations - reservation.gaps}
	if err := checkEffectCounts(delta, remaining); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			var used, requested, limit uint64
			switch exceeded.budget {
			case observability.CapacityBudgetStateMutations:
				used, requested, limit = reservation.states, delta.states, coordinator.budget.MaxStateMutations
			case observability.CapacityBudgetEvents:
				used, requested, limit = reservation.events, delta.events, coordinator.budget.MaxEvents
			case observability.CapacityBudgetGapMutations:
				used, requested, limit = reservation.gaps, delta.gaps, coordinator.budget.MaxGapMutations
			}
			return budgetRejection(exceeded.budget, phase, used, requested, limit, stream.ownBudget(exceeded.budget))
		}
		return err
	}
	if retained > coordinator.budget.MaxRetainedBytes-reservation.retainedBytes {
		return budgetRejection(observability.CapacityBudgetRetainedBytes, phase, reservation.retainedBytes, retained, coordinator.budget.MaxRetainedBytes, stream.ownBudget(observability.CapacityBudgetRetainedBytes))
	}
	reservation.states += delta.states
	reservation.events += delta.events
	reservation.gaps += delta.gaps
	reservation.retainedBytes += retained
	return nil
}

func (coordinator *SlotExecutionCoordinator) releaseEffects(count effectCounts) {
	reservation := &coordinator.reservations
	reservation.mu.Lock()
	reservation.states -= count.states
	reservation.events -= count.events
	reservation.gaps -= count.gaps
	reservation.mu.Unlock()
}
