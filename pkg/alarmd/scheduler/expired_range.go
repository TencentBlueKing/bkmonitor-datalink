// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
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

// rangeBuildRefusal is why the builder produced no range, and the two
// candidate bounds behind it when the branch that computes them was reached.
//
// The bounds are carried rather than recomputed because they are locals of one
// call and because which of the two bound first is the finding: a steps_below_one
// with the head bound at zero is a Query Group barely past its window, and the
// same word with the deadline bound at zero is a Slot whose own query deadline
// has only just passed. Told apart they are two different situations; told as
// one word they are the bucket this replaced.
type rangeBuildRefusal struct {
	word          string
	boundsKnown   bool
	distanceBound int64
	deadlineBound int64
}

func refusedRange(word string) rangeBuildRefusal { return rangeBuildRefusal{word: word} }

// buildExpiredRange returns the range, or the word for why there is none. An
// empty word means a range was built.
func (source *ProductionSlotSource) buildExpiredRange(ctx context.Context, first FrozenSlot, schedule execution.FrozenQueryGroupSchedule, at time.Time) (FrozenSlot, rangeBuildRefusal, error) {
	if source.recovery == nil {
		return FrozenSlot{}, refusedRange(observability.RangeGateRecoveryDisabled), nil
	}
	if len(schedule.Plans) == 0 {
		return FrozenSlot{}, refusedRange(observability.RangeGatePlansMismatch), nil
	}
	spec := schedule.Plans[0].Spec
	for _, plan := range schedule.Plans {
		if plan.Spec.EvaluationIntervalSeconds != spec.EvaluationIntervalSeconds || plan.Spec.Alignment != spec.Alignment {
			return FrozenSlot{}, refusedRange(observability.RangeGatePlansMismatch), nil
		}
	}
	if len(first.DuePlanTargets.Plans) != len(schedule.Plans) {
		return FrozenSlot{}, refusedRange(observability.RangeGatePlansMismatch), nil
	}
	start := first.Contract.Slot.EvaluationTime
	// Subtract from the already validated first deadline, avoiding negative
	// floor division and products involving unbounded absolute timestamps.
	eligibility := &execution.ExpiredRangeEligibilityV2{Reason: execution.RangeAgeExpired}
	steps := int64(0)
	// Filled in only by the distance branch, because only that branch has
	// these numbers. An age-expired range is bounded by the replay age and
	// never compares steps, so zero-filling them here would put two numbers
	// that were never computed next to two that were.
	var distance *rangeDistanceReport
	if at.UnixMilli() >= first.RecoveryUntilUnixMilli {
		steps = (at.UnixMilli() - first.RecoveryUntilUnixMilli) / 1000 / spec.EvaluationIntervalSeconds
	} else {
		// Prove the suffix using this real homogeneous segment. At cutovers we
		// conservatively stop unless this segment alone witnesses K successors.
		if at.UnixMilli() < first.EarliestQueryDeadlineUnixMilli {
			return FrozenSlot{}, refusedRange(observability.RangeGateDeadlineNotReached), nil
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
		// The three numbers that decided it, taken here because here is the
		// only place they exist. They are locals of this call: headSteps is
		// derived from the clock against this Query Group's first unfinished
		// Slot, deadlineSteps from that Slot's own query deadline, and steps
		// is whichever of the two bound first. Nothing downstream keeps them,
		// so a reader asking why a Query Group skipped four intervals rather
		// than three has been reduced to arithmetic on the completion counts.
		//
		// Recomputing them later from the sealed proof would not answer the
		// same question: the proof carries the range that was produced, not
		// the two candidate bounds that produced it, and which of the two
		// bound first is the whole finding.
		distance = &rangeDistanceReport{
			headSteps: headSteps, deadlineSteps: deadlineSteps,
			firstEvaluationTime: start, intervalSeconds: spec.EvaluationIntervalSeconds,
			maxReplaySlots: source.recovery.MaxReplaySlots,
		}
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
		refusal := refusedRange(observability.RangeGateStepsBelowOne)
		if distance != nil {
			refusal.boundsKnown = true
			refusal.distanceBound = distance.headSteps - int64(distance.maxReplaySlots)
			refusal.deadlineBound = distance.deadlineSteps
		}
		return FrozenSlot{}, refusal, nil
	}
	if steps > (math.MaxInt64-int64(start))/spec.EvaluationIntervalSeconds {
		return FrozenSlot{}, rangeBuildRefusal{}, ErrSlotContractDrift
	}
	last := execution.EvaluationTime(int64(start) + steps*spec.EvaluationIntervalSeconds)
	freeze := execution.FreezeSlotContractRequest{QueryGroup: source.queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: last, DuePlans: schedule.DuePlanRefs(last)}
	fact, err := source.catalog.FreezeSlotContract(ctx, freeze)
	if err != nil {
		return FrozenSlot{}, refusedRange(observability.RangeGateFreezeFailed), nil
	} // no pending or Guard yet: original single Slot remains valid.
	if err := fact.Validate(freeze); err != nil {
		return FrozenSlot{}, rangeBuildRefusal{}, &SourceBlockedError{Err: err}
	}
	targets, deadline, err := frozenSlotExecutionFacts(fact)
	if err != nil {
		return FrozenSlot{}, rangeBuildRefusal{}, err
	}
	recovery, keep, err := source.recoveryBoundaries(deadline)
	if err != nil {
		return FrozenSlot{}, rangeBuildRefusal{}, err
	}
	next, err := source.catalog.NextSlotAfter(ctx, source.queryGroup, last)
	if err != nil {
		return FrozenSlot{}, rangeBuildRefusal{}, err
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
			return FrozenSlot{}, refusedRange(observability.RangeGateProofTooLarge), nil
		}
		return FrozenSlot{}, rangeBuildRefusal{}, &SourceBlockedError{Err: err}
	}
	first.Contract, first.DuePlanTargets = proof.Last.Contract, proof.Last.DuePlanTargets.Clone()
	first.EarliestQueryDeadlineUnixMilli, first.KeepUntilUnixMilli, first.RecoveryUntilUnixMilli = deadline, keep, recovery
	first.ExpiredRange = &proof
	// Reported once the range exists, with the steps the range was actually
	// built to rather than the branch's candidate: steps is clamped twice
	// after the branch, by the segment boundary and by the width of the
	// count, and reporting the pre-clamp value would describe a range that
	// was not created.
	if distance != nil {
		distance.steps, distance.count = steps, proof.Count
		source.observeRangeDistanceExpiry(ctx, *distance)
	}
	return first, rangeBuildRefusal{}, nil
}

// rangeDistanceReport is what the distance branch decided with, carried from
// the branch to the point the range is sealed so the two can be reported
// together.
type rangeDistanceReport struct {
	headSteps           int64
	deadlineSteps       int64
	steps               int64
	count               uint32
	firstEvaluationTime execution.EvaluationTime
	intervalSeconds     int64
	maxReplaySlots      uint32
}

// observeRangeDistanceExpiry reports one range given up on for distance, with
// the numbers that decided its size.
//
// The reason this exists: a Query Group whose Slots are being skipped by
// distance reports GAP_SKIPPED completions and nothing else, and the count of
// those answers "how many" without any of "how far behind", "which of the two
// bounds bound first" or "from which Slot". Those are three locals of one
// call, discarded when it returns, so every question about the shape of the
// skipping has been answered by arithmetic on completion counts -- which is
// how a cohort skipping forty percent of its Slots went a day without a
// mechanism.
//
// Both bounds travel, not just the one that won. Which of the two is smaller
// is the finding: headSteps-MaxReplaySlots winning means the Query Group is
// far behind the head, and deadlineSteps winning means it is only just past
// its own deadline, and those are different problems with the same
// completion kind.
func (source *ProductionSlotSource) observeRangeDistanceExpiry(ctx context.Context, report rangeDistanceReport) {
	if source.observer == nil {
		return
	}
	// Observability is a fail-open side channel, as everywhere else here.
	defer func() { _ = recover() }()
	source.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageRangeDistanceExpired,
		Result: observability.ResultDegraded, Direction: observability.DirectionInternal,
		ReasonCode: observability.ReasonCode(contract.ReasonGapSkipped),
		Trace: observability.TraceFields{
			QueryGroupKey: string(source.queryGroup), EvaluationTime: int64(report.firstEvaluationTime),
		},
		RangeDistance: &observability.RangeDistanceFacts{
			HeadSteps: report.headSteps, DeadlineSteps: report.deadlineSteps, Steps: report.steps,
			SlotCount: report.count, FirstEvaluationTime: int64(report.firstEvaluationTime),
			IntervalSeconds: report.intervalSeconds, MaxReplaySlots: report.maxReplaySlots,
			BoundBy: report.boundBy(),
		},
	})
}

// boundBy names which of the two candidate bounds produced the range.
//
// Derived from the same two numbers that are reported beside it, so a reader
// can check the label against them rather than take it on trust.
func (report rangeDistanceReport) boundBy() string {
	distanceBound := report.headSteps - int64(report.maxReplaySlots)
	switch {
	case distanceBound < report.deadlineSteps:
		return observability.RangeBoundByDistance
	case report.deadlineSteps < distanceBound:
		return observability.RangeBoundByDeadline
	default:
		return observability.RangeBoundByBoth
	}
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
		// A range is a replay of the past by construction and declares no
		// content (declaredContentScope): its writes run under the lease.
		Dispatch:         SlotDispatchContext{Operation: execution.OperationNormal, OwnerFence: current, AssignmentGeneration: assignment.AssignmentGeneration},
		ExpectedNextSlot: p.First.Contract.Slot.EvaluationTime,
		Recovery:         SlotRecoveryFacts{Disposition: ReplayExpired, Reason: ReplayExpiredRange, Distance: 1},
	}
	if err := slot.Validate(source.queryGroup); err != nil {
		return FrozenSlot{}, false, err
	}
	// Counted here and not where the range was first classified: this is a
	// projection persisted by an earlier round, and resuming it is the only
	// event this process sees. The classification that created it was counted
	// by whichever process made it, which may no longer exist.
	source.observeReplayExpiry(ctx, p.First.Contract.Slot.EvaluationTime, slot.Recovery)
	return slot, true, nil
}

// rangeGateRefusal names which of the gate's conditions refused to let a
// replay-expired Slot reach the range builder.
//
// The conditions are evaluated in the order the gate writes them, so the word
// is the first one that was false -- the same one a reader stepping through
// the source would blame. They are a closed set: every refusal lands on one of
// them, and the caller reports the applied and post-build words for the rest,
// so the family totals to "every round that gave up on a Slot".
//
// It is a separate function from the gate rather than the gate rewritten to
// produce its own reason, because the gate decides and this only describes:
// an instrument that restructures the decision it measures can change it.
func rangeGateRefusal(
	source *ProductionSlotSource,
	load execution.ProgressLoadResult,
	nextSlot execution.EvaluationTime,
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) string {
	switch {
	case !source.expiredRangeEnabled:
		return observability.RangeGateCreationDisabled
	case load.Progress == nil:
		return observability.RangeGateProgressMissing
	case load.Progress.NextSlot != nextSlot:
		return observability.RangeGateNextSlotMoved
	case load.Progress.UnfinishedSlot != nil:
		return observability.RangeGateUnfinishedSlotPresent
	case ctx.Value(rangeFlightContextKey{}) != queryGroup:
		return observability.RangeGateNoRangeFlight
	default:
		// The gate and this description disagree, which is a defect in one of
		// them rather than a state of the Query Group. Naming it is what keeps
		// the two from drifting silently.
		return observability.RangeGateUnexplained
	}
}

// observeRangeGate reports what became of the catch-up path on one round that
// gave up on a Slot.
//
// Reported on every such round including the ones that did catch up, so the
// family is total: the share that never reaches the builder is readable
// against the share that does, from one sample, with no memory of a previous
// round. Reporting only the refusals would answer "how many were refused"
// without the denominator that says whether refusal is the normal case.
func (source *ProductionSlotSource) observeRangeGate(
	ctx context.Context,
	evaluationTime execution.EvaluationTime,
	outcome rangeBuildRefusal,
	load execution.ProgressLoadResult,
	nextSlot execution.EvaluationTime,
) {
	if source.observer == nil {
		return
	}
	// Observability is a fail-open side channel, as everywhere else here.
	defer func() { _ = recover() }()
	facts := &observability.RangeGateFacts{
		Outcome: outcome.word, ExpectedNextSlot: int64(nextSlot),
		RangeCreationEnabled: source.expiredRangeEnabled,
		BoundsKnown:          outcome.boundsKnown,
		DistanceBound:        outcome.distanceBound,
		DeadlineBound:        outcome.deadlineBound,
	}
	if load.Progress != nil {
		facts.ProgressPresent = true
		facts.ProgressNextSlot = int64(load.Progress.NextSlot)
		if load.Progress.UnfinishedSlot != nil {
			facts.UnfinishedSlotPresent = true
			facts.UnfinishedSlotEvaluationTime = int64(load.Progress.UnfinishedSlot.Contract.Slot.EvaluationTime)
		}
	}
	source.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageRangeGateDecided,
		Result: observability.ResultDegraded, Direction: observability.DirectionInternal,
		Trace: observability.TraceFields{
			QueryGroupKey: string(source.queryGroup), EvaluationTime: int64(evaluationTime),
		},
		RangeGate: facts,
	})
}
