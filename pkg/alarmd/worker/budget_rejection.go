package worker

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Called only on rejection while the shared reservation lock is held.
func budgetRejection(
	kind observability.CapacityBudget, phase string, shared, requested, limit uint64, own *uint64,
	usage []observability.CapacityBudgetUsage,
) error {
	return &provisionalBudgetExceededError{budget: kind, facts: &observability.CapacityRejectionFacts{
		Phase: phase, OwnUsed: own, SharedUsed: shared, Requested: requested, Limit: limit, Usage: usage,
	}}
}

// ownBudgetUsage is what this execution had taken of every budget, each beside
// the limit it is measured against.
//
// Every budget, not the one that refused: a rejection that reports only the
// budget it hit cannot be read against the others, and the whole question this
// reporting exists to answer is whether the budget that refused was the one
// under real pressure. A count at its ceiling beside a byte budget at a tenth
// of its own is a different incident from both at once, and they arrive as the
// same line without this.
func (stream *streamedExecution) ownBudgetUsage(budget ProvisionalBudget) []observability.CapacityBudgetUsage {
	if stream == nil || !stream.began {
		// Same reason ownReservation withholds a value: a nested owner's counts
		// are not the execution's, and publishing them as its own would be
		// worse than publishing nothing.
		return nil
	}
	return []observability.CapacityBudgetUsage{
		{Budget: observability.CapacityBudgetStateMutations, OwnUsed: stream.effects.states, Limit: budget.MaxStateMutations},
		{Budget: observability.CapacityBudgetEvents, OwnUsed: stream.effects.events, Limit: budget.MaxEvents},
		{Budget: observability.CapacityBudgetGapMutations, OwnUsed: stream.effects.gaps, Limit: budget.MaxGapMutations},
		{Budget: observability.CapacityBudgetRetainedBytes, OwnUsed: stream.retainedTotal(), Limit: budget.MaxRetainedBytes},
		{Budget: observability.CapacityBudgetSeries, OwnUsed: stream.series, Limit: budget.MaxSeries},
	}
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
		// The per-Slot caps this was judged against, not the process budget:
		// a Slot refused by its own cap is a different incident from one
		// refused by the shared pool, and reporting the pool's limits here
		// would describe the wrong comparison.
		Usage: stream.ownBudgetUsage(budget),
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
		value = stream.retainedTotal()
	case observability.CapacityBudgetStateMutations:
		value = stream.effects.states
	case observability.CapacityBudgetEvents:
		value = stream.effects.events
	case observability.CapacityBudgetGapMutations:
		value = stream.effects.gaps
	}
	return stream.ownReservation(value)
}

// poolHeldByOthersRejection describes one Query Group's Slot within its share
// that the pool has no room for: own is what the Slot holds, pool what the
// pool has reserved in all, and the difference is what others hold - other
// Query Groups and the worker's own preparation. The three are on the line so
// that "the pool was full" is read with whose bytes filled it.
func poolHeldByOthersRejection(phase string, own, pool, requested, limit uint64, usage []observability.CapacityBudgetUsage) error {
	others := pool - min(own, pool)
	return &provisionalBudgetExceededError{
		budget: observability.CapacityBudgetRetainedBytes, others: true,
		facts: &observability.CapacityRejectionFacts{
			Phase: phase, OwnUsed: &own, SharedUsed: pool, OthersUsed: &others, Requested: requested, Limit: limit, Usage: usage,
		},
	}
}

// PoolHeldByOthers reports whether err is a Query Group's Slot within its
// share refused by the pool for what others hold.
func PoolHeldByOthers(err error) bool {
	var exceeded *provisionalBudgetExceededError
	return errors.As(err, &exceeded) && exceeded.others
}

// shareRejection describes one Query Group's Slot over the share a single
// object may hold of the process pool.
//
// Reported in bytes, never in mutations. The same byte figure converts to
// counts that differ eightfold by strategy shape, so a share stated as a count
// would mean a different amount of memory for every strategy it was applied to
// - which is the substitution this whole decision exists to undo.
func shareRejection(phase string, own, requested, share uint64, usage []observability.CapacityBudgetUsage) error {
	return &provisionalBudgetExceededError{
		budget: observability.CapacityBudgetRetainedBytes, share: true,
		facts: &observability.CapacityRejectionFacts{
			Phase: phase, OwnUsed: &own, Requested: requested, Limit: share, Usage: usage,
		},
	}
}
