package worker

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"

// Called only on rejection while the shared reservation lock is held.
func budgetRejection(kind observability.CapacityBudget, phase string, shared, requested, limit uint64, own *uint64) error {
	return &provisionalBudgetExceededError{budget: kind, facts: &observability.CapacityRejectionFacts{
		Phase: phase, OwnUsed: own, SharedUsed: shared, Requested: requested, Limit: limit,
	}}
}

// slotBudgetRejection describes the Slot's own output exceeding a per-Slot
// cap. No shared usage is involved: own_used is the total the Slot would
// reach, requested the addition that crossed the cap and limit the cap.
func (stream *streamedExecution) slotBudgetRejection(kind observability.CapacityBudget, total, delta effectCounts, budget ProvisionalBudget) error {
	var used, requested, limit uint64
	switch kind {
	case observability.CapacityBudgetStateMutations:
		used, requested, limit = total.states, delta.states, budget.MaxStateMutations
	case observability.CapacityBudgetEvents:
		used, requested, limit = total.events, delta.events, budget.MaxEvents
	case observability.CapacityBudgetGapMutations:
		used, requested, limit = total.gaps, delta.gaps, budget.MaxGapMutations
	}
	return &provisionalBudgetExceededError{budget: kind, slot: true, facts: &observability.CapacityRejectionFacts{
		Phase: stream.reservationPhase("slot_output"), OwnUsed: stream.ownReservation(used), Requested: requested, Limit: limit,
	}}
}

func (stream *streamedExecution) reservationPhase(normal string) string {
	if stream != nil && stream.began {
		return normal
	}
	return "query_free"
}

func (stream *streamedExecution) ownReservation(value uint64) *uint64 {
	// Query-free finalization can have nested owners. An inner owner's value
	// would not represent the whole execution, so do not publish it as zero
	// or as the execution's own usage. Normal streams have one serial owner.
	if stream == nil || !stream.began {
		return nil
	}
	return &value
}

func (stream *streamedExecution) ownBudget(kind observability.CapacityBudget) *uint64 {
	if stream == nil || !stream.began {
		return nil
	}
	var value uint64
	switch kind {
	case observability.CapacityBudgetSeries:
		value = stream.series
	case observability.CapacityBudgetRetainedBytes:
		value = stream.retained
	case observability.CapacityBudgetStateMutations:
		value = stream.effects.states
	case observability.CapacityBudgetEvents:
		value = stream.effects.events
	case observability.CapacityBudgetGapMutations:
		value = stream.effects.gaps
	}
	return stream.ownReservation(value)
}
