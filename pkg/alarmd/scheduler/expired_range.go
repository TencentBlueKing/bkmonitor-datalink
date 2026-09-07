package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type rangeFlightContextKey struct{}

func rangeFitsProgress(current execution.ScheduleProgress, proof *execution.ExpiredRangeProjectionV1) bool {
	current.UnfinishedRange = proof
	raw, err := json.Marshal(current)
	// The stored envelope has a fixed schema string and two field names.
	// Reserve 128 bytes for that framing before any Begin/Guard.
	return err == nil && len(raw) <= execution.MaxExpiredRangeProjectionBytes-128
}

func WithExpiredRangeCreation(enabled bool) ProductionSlotSourceOption {
	return func(source *ProductionSlotSource) error { source.expiredRangeEnabled = enabled; return nil }
}

func (source *ProductionSlotSource) RangeCreationEnabled() bool { return source.expiredRangeEnabled }

func (source *ProductionSlotSource) buildExpiredRange(ctx context.Context, first FrozenSlot, schedule execution.FrozenQueryGroupSchedule, at time.Time) (FrozenSlot, bool, error) {
	if source.recovery == nil || len(schedule.Plans) == 0 {
		return FrozenSlot{}, false, nil
	}
	spec := schedule.Plans[0].Spec
	for _, plan := range schedule.Plans {
		if plan.Spec.EvaluationIntervalSeconds != spec.EvaluationIntervalSeconds || plan.Spec.Alignment != spec.Alignment {
			return FrozenSlot{}, false, nil
		}
	}
	if len(first.DuePlanTargets.Plans) != len(schedule.Plans) {
		return FrozenSlot{}, false, nil
	}
	start := first.Contract.Slot.EvaluationTime
	// Subtract from the already validated first deadline, avoiding negative
	// floor division and products involving unbounded absolute timestamps.
	eligibility := &execution.ExpiredRangeEligibilityV2{Reason: execution.RangeAgeExpired}
	steps := int64(0)
	if at.UnixMilli() >= first.RecoveryUntilUnixMilli {
		steps = (at.UnixMilli() - first.RecoveryUntilUnixMilli) / 1000 / spec.EvaluationIntervalSeconds
	} else {
		// Prove the suffix using this real homogeneous segment. At cutovers we
		// conservatively stop unless this segment alone witnesses K successors.
		if at.UnixMilli() < first.EarliestQueryDeadlineUnixMilli {
			return FrozenSlot{}, false, nil
		}
		headSteps := (at.Unix() - int64(start)) / spec.EvaluationIntervalSeconds
		if schedule.Segment.End != nil {
			endSteps := (int64(*schedule.Segment.End) - 1 - int64(start)) / spec.EvaluationIntervalSeconds
			if endSteps < headSteps {
				headSteps = endSteps
			}
		}
		steps = headSteps - int64(source.recovery.MaxReplaySlots)
		deadlineSteps := (at.UnixMilli() - first.EarliestQueryDeadlineUnixMilli) / 1000 / spec.EvaluationIntervalSeconds
		if deadlineSteps < steps {
			steps = deadlineSteps
		}
		eligibility = &execution.ExpiredRangeEligibilityV2{Reason: execution.RangeDistanceExpired,
			MaxReplaySlots: source.recovery.MaxReplaySlots, DistanceHead: execution.EvaluationTime(int64(start) + headSteps*spec.EvaluationIntervalSeconds)}
	}
	if steps > math.MaxUint32-1 {
		steps = math.MaxUint32 - 1
	}
	if schedule.Segment.End != nil {
		boundarySteps := (int64(*schedule.Segment.End) - 1 - int64(start)) / spec.EvaluationIntervalSeconds
		if boundarySteps < steps {
			steps = boundarySteps
		}
	}
	if steps < 1 {
		return FrozenSlot{}, false, nil
	}
	if steps > (math.MaxInt64-int64(start))/spec.EvaluationIntervalSeconds {
		return FrozenSlot{}, false, ErrSlotContractDrift
	}
	last := execution.EvaluationTime(int64(start) + steps*spec.EvaluationIntervalSeconds)
	freeze := execution.FreezeSlotContractRequest{QueryGroup: source.queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: last, DuePlans: schedule.DuePlanRefs(last)}
	fact, err := source.catalog.FreezeSlotContract(ctx, freeze)
	if err != nil {
		return FrozenSlot{}, false, nil
	} // no pending or Guard yet: original single Slot remains valid.
	if err := fact.Validate(freeze); err != nil {
		return FrozenSlot{}, false, &SourceBlockedError{Err: err}
	}
	targets, deadline, err := frozenSlotExecutionFacts(fact)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	recovery, keep, err := source.recoveryBoundaries(deadline)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	next, err := source.catalog.NextSlotAfter(ctx, source.queryGroup, last)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	proof, err := execution.SealExpiredRange(execution.ExpiredRangeProjectionV1{
		Schedule: schedule, First: execution.UnfinishedSlotProjection{Contract: first.Contract, DuePlanTargets: first.DuePlanTargets,
			EarliestQueryDeadlineUnixMilli: first.EarliestQueryDeadlineUnixMilli, KeepUntilUnixMilli: first.KeepUntilUnixMilli},
		Last: execution.UnfinishedSlotProjection{Contract: fact.Contract, DuePlanTargets: targets, EarliestQueryDeadlineUnixMilli: deadline, KeepUntilUnixMilli: keep},
		Next: next, Count: uint32(steps + 1), QueryReserveMillis: source.queryReserve.Milliseconds(),
		ReplayAgeMillis: source.recovery.MaxReplayAge.Milliseconds(), JudgedAtMillis: at.UnixMilli(),
		EligibilityV2: eligibility,
	})
	if err != nil {
		if errors.Is(err, execution.ErrExpiredRangeProofTooLarge) {
			return FrozenSlot{}, false, nil
		}
		return FrozenSlot{}, false, &SourceBlockedError{Err: err}
	}
	first.Contract, first.DuePlanTargets = proof.Last.Contract, proof.Last.DuePlanTargets.Clone()
	first.EarliestQueryDeadlineUnixMilli, first.KeepUntilUnixMilli, first.RecoveryUntilUnixMilli = deadline, keep, recovery
	first.ExpiredRange = &proof
	return first, true, nil
}

func (source *ProductionSlotSource) resumeExpiredRange(ctx context.Context, p execution.ExpiredRangeProjectionV1, initial ownership.AssignmentRecord, fence execution.OwnerFence) (FrozenSlot, bool, error) {
	if err := p.Validate(); err != nil {
		return FrozenSlot{}, false, &SourceBlockedError{Err: err}
	}
	for _, at := range []execution.EvaluationTime{p.First.Contract.Slot.EvaluationTime, p.Last.Contract.Slot.EvaluationTime} {
		schedule, err := source.catalog.ReadFrozenSchedule(ctx, source.queryGroup, at)
		if err != nil {
			if errors.Is(err, ErrProgressOffSchedule) || errors.Is(err, controlplane.ErrScheduleUnavailable) {
				return FrozenSlot{}, false, &SourceBlockedError{Err: err}
			}
			return FrozenSlot{}, false, err
		}
		if err := schedule.Validate(); err != nil {
			return FrozenSlot{}, false, &SourceBlockedError{Err: err}
		}
		a, b := schedule.Segment, p.Schedule.Segment
		if !a.Contains(at) || a.Start != b.Start || a.Publication != b.Publication || a.QueryRevision != b.QueryRevision || a.ScheduleRevision != b.ScheduleRevision {
			return FrozenSlot{}, false, &SourceBlockedError{Err: fmt.Errorf("expired range live Schedule no longer proves frozen span")}
		}
	}
	next, err := source.catalog.NextSlotAfter(ctx, source.queryGroup, p.Last.Contract.Slot.EvaluationTime)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if next != p.Next {
		return FrozenSlot{}, false, &SourceBlockedError{Err: ErrProgressOffSchedule}
	}
	assignment, current, err := source.currentOwnership(ctx, source.now())
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if !sameAssignment(initial, assignment) || current != fence {
		return FrozenSlot{}, false, ErrSlotOwnershipChanged
	}
	copy := p.Clone()
	slot := FrozenSlot{ExpiredRange: &copy, Contract: p.Last.Contract, DuePlanTargets: p.Last.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: p.Last.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         p.Last.EarliestQueryDeadlineUnixMilli + p.ReplayAgeMillis,
		KeepUntilUnixMilli:             p.Last.KeepUntilUnixMilli,
		Dispatch:                       SlotDispatchContext{Operation: execution.OperationNormal, OwnerFence: current, AssignmentGeneration: assignment.AssignmentGeneration},
		ExpectedNextSlot:               p.First.Contract.Slot.EvaluationTime,
		Recovery:                       SlotRecoveryFacts{Disposition: ReplayExpired, Distance: 1},
	}
	if err := slot.Validate(source.queryGroup); err != nil {
		return FrozenSlot{}, false, err
	}
	return slot, true, nil
}
