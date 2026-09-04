// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

var (
	ErrScheduleFactsInvalid          = errors.New("alarmd scheduler: frozen schedule facts are invalid")
	ErrProgressOffSchedule           = errors.New("alarmd scheduler: Progress next Slot is outside the frozen schedule")
	ErrSlotContractDrift             = errors.New("alarmd scheduler: frozen Slot contract differs from requested facts")
	ErrSlotOwnershipChanged          = errors.New("alarmd scheduler: ownership changed while freezing Slot")
	ErrSnapshotRetentionInsufficient = errors.New("alarmd scheduler: Snapshot retention cannot cover recovery contract")
)

const slotFreezeFailureMessage = "alarmd scheduler: FreezeSlotContract failed"

// These concrete error types are the bounded cause classes exposed as
// error_type by the runtime logger. Their messages deliberately omit the
// underlying control fact while Unwrap preserves errors.Is/errors.As.
type slotFreezeFailure struct{ err error }

func (err slotFreezeFailure) Unwrap() error { return err.err }

type slotFreezeSnapshotUnavailableFailure struct{ slotFreezeFailure }

func (*slotFreezeSnapshotUnavailableFailure) Error() string { return slotFreezeFailureMessage }

type slotFreezeSnapshotCorruptFailure struct{ slotFreezeFailure }

func (*slotFreezeSnapshotCorruptFailure) Error() string { return slotFreezeFailureMessage }

type slotFreezeScheduleUnavailableFailure struct{ slotFreezeFailure }

func (*slotFreezeScheduleUnavailableFailure) Error() string { return slotFreezeFailureMessage }

type slotFreezeCatalogObjectUnavailableFailure struct{ slotFreezeFailure }

func (*slotFreezeCatalogObjectUnavailableFailure) Error() string { return slotFreezeFailureMessage }

type slotFreezeOtherFailure struct{ slotFreezeFailure }

func (*slotFreezeOtherFailure) Error() string { return slotFreezeFailureMessage }

func classifySlotFreezeFailure(err error) error {
	failure := slotFreezeFailure{err: err}
	var corrupt *controlplane.PersistedSnapshotCorruptError
	switch {
	case errors.As(err, &corrupt):
		return &slotFreezeSnapshotCorruptFailure{slotFreezeFailure: failure}
	case errors.Is(err, controlplane.ErrSnapshotUnavailable):
		return &slotFreezeSnapshotUnavailableFailure{slotFreezeFailure: failure}
	case errors.Is(err, controlplane.ErrScheduleUnavailable):
		return &slotFreezeScheduleUnavailableFailure{slotFreezeFailure: failure}
	case errors.Is(err, controlplane.ErrCatalogObjectUnavailable):
		return &slotFreezeCatalogObjectUnavailableFailure{slotFreezeFailure: failure}
	default:
		return &slotFreezeOtherFailure{slotFreezeFailure: failure}
	}
}

type AssignmentReader interface {
	ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error)
}

type SlotCatalogReader interface {
	ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error)
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error)
	ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error)
	NextSlotAfter(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.EvaluationTime, error)
	FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

type ScheduleProgressReader interface {
	LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error)
}

// ProductionSlotSource is bound to one owned Query Group. It reads current
// control facts and returns one normal due Slot; it never executes queries,
type ProductionSlotSource struct {
	queryGroup                execution.QueryGroupIdentity
	workerID                  string
	assignments               AssignmentReader
	session                   OwnerSession
	catalog                   SlotCatalogReader
	progress                  ScheduleProgressReader
	now                       func() time.Time
	recovery                  *RecoveryLimits
	terminalDelay             time.Duration
	queryReserve              time.Duration
	snapshotRetention         time.Duration
	publicationDelayAllowance time.Duration
}

type ProductionSlotSourceOption func(*ProductionSlotSource) error

func WithRecoveryLimits(limits RecoveryLimits) ProductionSlotSourceOption {
	return func(source *ProductionSlotSource) error {
		if err := limits.Validate(); err != nil {
			return err
		}
		source.recovery = &limits
		source.terminalDelay = limits.RetryMaxDelay
		return nil
	}
}

func WithPostRecoveryTerminalDelay(delay time.Duration) ProductionSlotSourceOption {
	return func(source *ProductionSlotSource) error {
		if delay <= 0 {
			return ErrRecoveryLimitsInvalid
		}
		source.terminalDelay = delay
		return nil
	}
}

func WithQueryDeadlineReserve(reserve time.Duration) ProductionSlotSourceOption {
	return func(source *ProductionSlotSource) error {
		if reserve <= 0 {
			return ErrRecoveryLimitsInvalid
		}
		source.queryReserve = reserve
		return nil
	}
}

func WithSnapshotRetention(retention, publicationDelayAllowance time.Duration) ProductionSlotSourceOption {
	return func(source *ProductionSlotSource) error {
		if retention <= 0 || publicationDelayAllowance <= 0 || retention <= publicationDelayAllowance {
			return ErrSnapshotRetentionInsufficient
		}
		source.snapshotRetention = retention
		source.publicationDelayAllowance = publicationDelayAllowance
		return nil
	}
}

func NewProductionSlotSource(
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	now func() time.Time,
	options ...ProductionSlotSourceOption,
) (*ProductionSlotSource, error) {
	if queryGroup == "" || workerID == "" || assignments == nil || session == nil || catalog == nil || progress == nil || now == nil {
		return nil, errors.New("alarmd scheduler: complete production SlotSource dependencies are required")
	}
	source := &ProductionSlotSource{queryGroup: queryGroup, workerID: workerID, assignments: assignments,
		session: session, catalog: catalog, progress: progress, now: now}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("alarmd scheduler: nil production SlotSource option")
		}
		if err := option(source); err != nil {
			return nil, err
		}
	}
	return source, nil
}

func (source *ProductionSlotSource) Next(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (FrozenSlot, bool, error) {
	if source == nil || queryGroup == "" || queryGroup != source.queryGroup {
		return FrozenSlot{}, false, errors.New("alarmd scheduler: SlotSource Query Group mismatch")
	}
	if err := ctx.Err(); err != nil {
		return FrozenSlot{}, false, err
	}
	at := source.now()
	if at.IsZero() {
		return FrozenSlot{}, false, errors.New("alarmd scheduler: current time is required")
	}
	initialAssignment, initialFence, err := source.currentOwnership(ctx, at)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	identity := execution.ProgressIdentity{QueryGroup: source.queryGroup}
	load, err := source.progress.LoadProgress(ctx, identity)
	if err != nil {
		var deterministic interface{ DeterministicControlFact() }
		if errors.As(err, &deterministic) {
			return FrozenSlot{}, false, &SourceBlockedError{Err: err}
		}
		return FrozenSlot{}, false, err
	}
	if err := load.Validate(identity); err != nil {
		return FrozenSlot{}, false, err
	}
	var schedule execution.FrozenQueryGroupSchedule
	var nextSlot execution.EvaluationTime
	if load.Status == execution.ProgressMissing {
		schedule, err = source.catalog.ReadInitialFrozenSchedule(ctx, source.queryGroup)
		if err == nil {
			var retired bool
			schedule, nextSlot, retired, err = source.firstAvailableSchedule(ctx, schedule)
			if retired {
				return FrozenSlot{}, false, nil
			}
		}
	} else {
		retired, retirementErr := source.isRetiredBoundary(ctx, load.Progress.NextSlot)
		if retirementErr != nil {
			return FrozenSlot{}, false, retirementErr
		}
		if retired {
			return FrozenSlot{}, false, nil
		}
		schedule, nextSlot, err = source.nextSlotAfterProgress(ctx, *load.Progress)
	}
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := source.validateSchedule(schedule, nextSlot); err != nil {
		return FrozenSlot{}, false, err
	}
	duePlans := schedule.DuePlanRefs(nextSlot)
	if len(duePlans) == 0 {
		return FrozenSlot{}, false, ErrProgressOffSchedule
	}
	if at.Unix() < int64(nextSlot) {
		return FrozenSlot{}, false, nil
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: source.queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: nextSlot, DuePlans: duePlans,
	}
	fact, err := source.catalog.FreezeSlotContract(ctx, request)
	if err != nil {
		if load.Progress != nil && load.Progress.UnfinishedSlot != nil {
			return source.slotFromProjection(ctx, initialAssignment, initialFence, *load.Progress.UnfinishedSlot, at)
		}
		deadline, deadlineErr := source.scheduleQueryDeadline(schedule, nextSlot)
		if deadlineErr != nil {
			return FrozenSlot{}, false, deadlineErr
		}
		recoveryUntil, _, boundaryErr := source.recoveryBoundaries(deadline)
		if boundaryErr != nil {
			return FrozenSlot{}, false, boundaryErr
		}
		var corrupt *controlplane.PersistedSnapshotCorruptError
		cause := classifySlotFreezeFailure(err)
		if errors.As(err, &corrupt) || at.UnixMilli() >= recoveryUntil {
			return FrozenSlot{}, false, &SourceBlockedError{Err: cause}
		}
		return FrozenSlot{}, false, &SourceRetryError{Err: cause}
	}
	if err := fact.Validate(request); err != nil {
		return FrozenSlot{}, false, fmt.Errorf("%w: %v", ErrSlotContractDrift, err)
	}
	if fact.Contract.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
		fact.Contract.QueryRevision != schedule.Segment.QueryRevision {
		return FrozenSlot{}, false, ErrSlotContractDrift
	}
	targets, queryDeadline, err := frozenSlotExecutionFacts(fact)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	recoveryUntil, keepUntil, err := source.recoveryBoundaries(queryDeadline)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := source.validateSnapshotRetention(fact.Contract.Slot.EvaluationTime, queryDeadline); err != nil {
		return FrozenSlot{}, false, err
	}
	operation, recovery, err := source.classifyRecovery(ctx, fact.Contract.Slot.EvaluationTime, queryDeadline, at)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	currentAssignment, currentFence, err := source.currentOwnership(ctx, source.now())
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if !sameAssignment(initialAssignment, currentAssignment) || initialFence != currentFence {
		return FrozenSlot{}, false, ErrSlotOwnershipChanged
	}
	slot := FrozenSlot{
		Contract:                       fact.Contract,
		DuePlanTargets:                 targets.Clone(),
		EarliestQueryDeadlineUnixMilli: queryDeadline,
		RecoveryUntilUnixMilli:         recoveryUntil,
		KeepUntilUnixMilli:             keepUntil,
		Dispatch: SlotDispatchContext{Operation: operation, OwnerFence: currentFence,
			AssignmentGeneration: currentAssignment.AssignmentGeneration},
		ExpectedNextSlot: nextSlot,
		Recovery:         recovery,
	}
	if err := slot.Validate(queryGroup); err != nil {
		return FrozenSlot{}, false, err
	}
	return slot, true, nil
}

func (source *ProductionSlotSource) scheduleQueryDeadline(schedule execution.FrozenQueryGroupSchedule, slot execution.EvaluationTime) (int64, error) {
	if source.queryReserve <= 0 {
		return 0, ErrRecoveryLimitsInvalid
	}
	deadline := int64(0)
	for _, plan := range schedule.Plans {
		if !plan.Spec.IsAligned(slot) {
			continue
		}
		candidate := time.Unix(int64(slot), 0).Add(time.Duration(plan.Spec.EvaluationIntervalSeconds)*time.Second - source.queryReserve).UnixMilli()
		if candidate <= int64(slot)*1000 {
			return 0, ErrSlotContractDrift
		}
		if deadline == 0 || candidate < deadline {
			deadline = candidate
		}
	}
	if deadline == 0 {
		return 0, ErrSlotContractDrift
	}
	return deadline, nil
}

func (source *ProductionSlotSource) slotFromProjection(
	ctx context.Context,
	initialAssignment ownership.AssignmentRecord,
	initialFence execution.OwnerFence,
	projection execution.UnfinishedSlotProjection,
	at time.Time,
) (FrozenSlot, bool, error) {
	if err := projection.Validate(); err != nil || projection.Contract.Slot.QueryGroup != source.queryGroup {
		if err == nil {
			err = ErrSlotContractDrift
		}
		return FrozenSlot{}, false, &SourceBlockedError{Err: err}
	}
	recoveryUntil, _, err := source.recoveryBoundaries(projection.EarliestQueryDeadlineUnixMilli)
	if err != nil || projection.KeepUntilUnixMilli <= recoveryUntil {
		return FrozenSlot{}, false, &SourceBlockedError{Err: ErrSlotContractDrift}
	}
	operation, recovery, err := source.classifyRecovery(ctx, projection.Contract.Slot.EvaluationTime, projection.EarliestQueryDeadlineUnixMilli, at)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	currentAssignment, currentFence, err := source.currentOwnership(ctx, source.now())
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if !sameAssignment(initialAssignment, currentAssignment) || initialFence != currentFence {
		return FrozenSlot{}, false, ErrSlotOwnershipChanged
	}
	slot := FrozenSlot{
		Contract: projection.Contract, DuePlanTargets: projection.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: projection.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         recoveryUntil, KeepUntilUnixMilli: projection.KeepUntilUnixMilli,
		Dispatch:         SlotDispatchContext{Operation: operation, OwnerFence: currentFence, AssignmentGeneration: currentAssignment.AssignmentGeneration},
		ExpectedNextSlot: projection.Contract.Slot.EvaluationTime, Recovery: recovery,
	}
	if err := slot.Validate(source.queryGroup); err != nil {
		return FrozenSlot{}, false, &SourceBlockedError{Err: err}
	}
	return slot, true, nil
}

func (source *ProductionSlotSource) recoveryBoundaries(deadline int64) (int64, int64, error) {
	if source.recovery == nil || source.terminalDelay <= 0 {
		return 0, 0, ErrRecoveryLimitsInvalid
	}
	recoveryUntil := time.UnixMilli(deadline).Add(source.recovery.MaxReplayAge).UnixMilli()
	keepUntil := time.UnixMilli(recoveryUntil).Add(source.terminalDelay).UnixMilli()
	if recoveryUntil <= deadline || keepUntil <= recoveryUntil {
		return 0, 0, ErrRecoveryLimitsInvalid
	}
	return recoveryUntil, keepUntil, nil
}

func (source *ProductionSlotSource) validateSnapshotRetention(
	evaluationTime execution.EvaluationTime,
	queryDeadline int64,
) error {
	if source.snapshotRetention == 0 {
		return nil
	}
	deadlineOffset := time.Duration(queryDeadline-int64(evaluationTime)*1000) * time.Millisecond
	required := source.publicationDelayAllowance + deadlineOffset + source.recovery.MaxReplayAge + source.terminalDelay
	if deadlineOffset <= 0 || required <= 0 || source.snapshotRetention < required {
		return ErrSnapshotRetentionInsufficient
	}
	return nil
}

func (source *ProductionSlotSource) classifyRecovery(
	ctx context.Context,
	evaluationTime execution.EvaluationTime,
	deadline int64,
	at time.Time,
) (execution.Operation, SlotRecoveryFacts, error) {
	if source.recovery == nil {
		return execution.OperationNormal, SlotRecoveryFacts{}, nil
	}
	if deadline <= 0 {
		return "", SlotRecoveryFacts{}, ErrSlotContractDrift
	}
	if at.UnixMilli() < deadline {
		return execution.OperationNormal, SlotRecoveryFacts{Disposition: ReplayLive}, nil
	}
	age := at.Sub(time.UnixMilli(deadline))
	distance, err := source.replayDistance(ctx, evaluationTime, at)
	if err != nil {
		return "", SlotRecoveryFacts{}, err
	}
	facts := SlotRecoveryFacts{Disposition: ReplayEligible, Distance: distance, Age: age}
	if age > source.recovery.MaxReplayAge || distance > source.recovery.MaxReplaySlots {
		facts.Disposition = ReplayExpired
		return execution.OperationNormal, facts, nil
	}
	return execution.OperationReplay, facts, nil
}

func frozenSlotExecutionFacts(
	fact execution.FrozenSlotContractFact,
) (execution.FrozenDuePlanTargets, int64, error) {
	deadline := int64(0)
	for _, requirement := range fact.Requirements {
		for _, consumer := range requirement.Consumers {
			if consumer.ConsumerDeadlineUnixMilli <= 0 || consumer.DownstreamExecutionReserveMilliSec <= 0 ||
				consumer.DownstreamExecutionReserveMilliSec >= consumer.ConsumerDeadlineUnixMilli {
				return execution.FrozenDuePlanTargets{}, 0, ErrSlotContractDrift
			}
			candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
			if deadline == 0 || candidate < deadline {
				deadline = candidate
			}
		}
	}
	targets := execution.FrozenDuePlanTargets{
		DuePlanSetDigest: fact.Contract.DuePlanSetDigest,
		Plans:            make([]execution.PlanIdentity, len(fact.DuePlans)),
	}
	for index := range fact.DuePlans {
		targets.Plans[index] = fact.DuePlans[index].Identity
	}
	if deadline <= int64(fact.Contract.Slot.EvaluationTime)*1000 || targets.Validate(fact.Contract) != nil {
		return execution.FrozenDuePlanTargets{}, 0, ErrSlotContractDrift
	}
	return targets, deadline, nil
}

func (source *ProductionSlotSource) replayDistance(
	ctx context.Context,
	first execution.EvaluationTime,
	at time.Time,
) (uint32, error) {
	distance := uint32(1)
	cursor := first
	for distance <= source.recovery.MaxReplaySlots {
		next, err := source.catalog.NextSlotAfter(ctx, source.queryGroup, cursor)
		if err != nil {
			return 0, err
		}
		if next <= cursor {
			return 0, ErrScheduleFactsInvalid
		}
		if int64(next) > at.Unix() {
			return distance, nil
		}
		distance++
		cursor = next
	}
	return distance, nil
}

func (source *ProductionSlotSource) isRetiredBoundary(
	ctx context.Context,
	boundary execution.EvaluationTime,
) (bool, error) {
	retiredAt, retired, err := source.catalog.ReadScheduleRetirement(ctx, source.queryGroup)
	if err != nil || !retired {
		return false, err
	}
	return retiredAt == boundary, nil
}

func (source *ProductionSlotSource) nextSlotAfterProgress(
	ctx context.Context,
	progress execution.ScheduleProgress,
) (execution.FrozenQueryGroupSchedule, execution.EvaluationTime, error) {
	completed := progress.LastFullSlot
	if progress.CurrentOrRecentGap != nil && progress.CurrentOrRecentGap.LastSlot > completed {
		completed = progress.CurrentOrRecentGap.LastSlot
	}
	if completed <= 0 {
		schedule, err := source.catalog.ReadFrozenSchedule(ctx, source.queryGroup, progress.NextSlot)
		if err == nil {
			return schedule, progress.NextSlot, nil
		}
		schedule, err = source.catalog.ReadSuccessorFrozenSchedule(ctx, source.queryGroup, progress.NextSlot)
		if err != nil {
			return execution.FrozenQueryGroupSchedule{}, 0, err
		}
		schedule, next, retired, err := source.firstAvailableSchedule(ctx, schedule)
		if retired && err == nil {
			return execution.FrozenQueryGroupSchedule{}, 0, ErrScheduleFactsInvalid
		}
		return schedule, next, err
	}
	next, err := source.catalog.NextSlotAfter(ctx, source.queryGroup, completed)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, 0, err
	}
	if next <= completed {
		return execution.FrozenQueryGroupSchedule{}, 0, ErrScheduleFactsInvalid
	}
	schedule, err := source.catalog.ReadFrozenSchedule(ctx, source.queryGroup, next)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, 0, err
	}
	return schedule, next, nil
}

func (source *ProductionSlotSource) validateSchedule(
	schedule execution.FrozenQueryGroupSchedule,
	nextSlot execution.EvaluationTime,
) error {
	if err := schedule.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrScheduleFactsInvalid, err)
	}
	if schedule.Segment.QueryGroup != source.queryGroup || !schedule.Segment.Contains(nextSlot) {
		return ErrProgressOffSchedule
	}
	return nil
}

func (source *ProductionSlotSource) firstAvailableSchedule(
	ctx context.Context,
	schedule execution.FrozenQueryGroupSchedule,
) (execution.FrozenQueryGroupSchedule, execution.EvaluationTime, bool, error) {
	for {
		if err := schedule.Validate(); err != nil || schedule.Segment.QueryGroup != source.queryGroup {
			return execution.FrozenQueryGroupSchedule{}, 0, false, ErrScheduleFactsInvalid
		}
		if next, ok := schedule.FirstSlot(); ok {
			return schedule, next, false, nil
		}
		if schedule.Segment.End == nil {
			return execution.FrozenQueryGroupSchedule{}, 0, false, ErrScheduleFactsInvalid
		}
		boundary := *schedule.Segment.End
		retired, err := source.isRetiredBoundary(ctx, boundary)
		if err != nil {
			return execution.FrozenQueryGroupSchedule{}, 0, false, err
		}
		if retired {
			return schedule, boundary, true, nil
		}
		successor, err := source.catalog.ReadSuccessorFrozenSchedule(ctx, source.queryGroup, boundary)
		if err != nil {
			return execution.FrozenQueryGroupSchedule{}, 0, false, err
		}
		if successor.Segment.Start < boundary {
			return execution.FrozenQueryGroupSchedule{}, 0, false, ErrScheduleFactsInvalid
		}
		schedule = successor
	}
}

func (source *ProductionSlotSource) currentOwnership(
	ctx context.Context,
	at time.Time,
) (ownership.AssignmentRecord, execution.OwnerFence, error) {
	assignment, err := source.assignments.ReadAssignment(ctx, source.queryGroup)
	if err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if err := assignment.Validate(); err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if assignment.QueryGroup != source.queryGroup || assignment.DesiredWorkerID != source.workerID {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, ownership.ErrNotDesired
	}
	fence, err := source.session.ValidateCurrent(ctx, at)
	if err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if fence.QueryGroup != source.queryGroup || fence.OwnerID != source.workerID || fence.OwnerEpoch == 0 || fence.LeaseToken == "" {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, ownership.ErrStaleFence
	}
	return assignment, fence, nil
}

func sameAssignment(left, right ownership.AssignmentRecord) bool {
	return left.QueryGroup == right.QueryGroup && left.DesiredWorkerID == right.DesiredWorkerID &&
		left.AssignmentGeneration == right.AssignmentGeneration
}

var _ SlotSource = (*ProductionSlotSource)(nil)
