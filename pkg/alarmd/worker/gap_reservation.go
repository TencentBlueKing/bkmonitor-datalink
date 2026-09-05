package worker

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func (stream *streamedExecution) retainTargets(ctx context.Context, count int, targets any) error {
	return stream.retainTargetBytes(ctx, count, retainedObjectBytes(targets))
}

func (stream *streamedExecution) retainTargetBytes(ctx context.Context, count int, size uint64) error {
	if uint64(count) > stream.coordinator.budget.MaxGapMutations {
		err := &provisionalBudgetExceededError{budget: observability.CapacityBudgetGapMutations}
		stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, err.budget, err)
		return err
	}
	// Covers target copies, the selected-Plan index and mutation lookup tables.
	// No Dataset or compiled Plan graph is passed into this measurement.
	retained := 4 * size
	if err := stream.reserveProvisionalAt(ctx, 0, retained, stream.reservationPhase("normal_gap")); err != nil {
		return err
	}
	stream.retained += retained
	return nil
}

func (stream *streamedExecution) loadGapFacts(ctx context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	err := stream.coordinator.ports.GapGuard.LoadGapsInto(ctx, request, func(snapshot execution.GapGuardSnapshot) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Only one decoded value exists outside the shared retained budget.
		// The Store bounds that value and emits before reading the next key.
		retained := 2 * retainedObjectBytes(snapshot)
		reservations := &stream.coordinator.reservations
		reservations.mu.Lock()
		var err error
		if reservations.gapFacts >= stream.coordinator.budget.MaxGapMutations {
			err = budgetRejection(observability.CapacityBudgetGapMutations, stream.reservationPhase("normal_gap"), reservations.gapFacts, 1, stream.coordinator.budget.MaxGapMutations, stream.ownReservation(stream.gapFacts))
		} else if retained > stream.coordinator.budget.MaxRetainedBytes-reservations.retainedBytes {
			err = budgetRejection(observability.CapacityBudgetRetainedBytes, stream.reservationPhase("normal_gap"), reservations.retainedBytes, retained, stream.coordinator.budget.MaxRetainedBytes, stream.ownBudget(observability.CapacityBudgetRetainedBytes))
		} else {
			reservations.gapFacts++
			reservations.retainedBytes += retained
		}
		reservations.mu.Unlock()
		if err != nil {
			var exceeded *provisionalBudgetExceededError
			if errors.As(err, &exceeded) {
				stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
			}
			return err
		}
		stream.gapFacts++
		stream.retained += retained
		stream.gaps.Items = append(stream.gaps.Items, snapshot)
		return nil
	})
	if err != nil {
		return execution.GapLoadResult{}, err
	}
	if err := execution.ValidateGapLoad(request, stream.gaps); err != nil {
		return execution.GapLoadResult{}, err
	}
	return stream.gaps, nil
}

func (stream *streamedExecution) retainGapMutation(ctx context.Context, mutation execution.PlanGapMutation) error {
	retained := 2 * retainedObjectBytes(mutation)
	delta := effectCounts{gaps: 1}
	if err := stream.coordinator.acquireEffects(delta, retained, stream, stream.reservationPhase("normal_gap")); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
		}
		return err
	}
	stream.effects.gaps++
	stream.retained += retained
	return nil
}
