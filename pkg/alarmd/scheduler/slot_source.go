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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

var (
	ErrScheduleFactsInvalid = errors.New("alarmd scheduler: frozen schedule facts are invalid")
	ErrProgressOffSchedule  = errors.New("alarmd scheduler: Progress next Slot is outside the frozen schedule")
	ErrSlotContractDrift    = errors.New("alarmd scheduler: frozen Slot contract differs from requested facts")
	ErrSlotOwnershipChanged = errors.New("alarmd scheduler: ownership changed while freezing Slot")
)

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
// evaluates data, commits state/Progress, or creates recovery state machines.
type ProductionSlotSource struct {
	queryGroup  execution.QueryGroupIdentity
	workerID    string
	assignments AssignmentReader
	session     OwnerSession
	catalog     SlotCatalogReader
	progress    ScheduleProgressReader
	now         func() time.Time
}

func NewProductionSlotSource(
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	now func() time.Time,
) (*ProductionSlotSource, error) {
	if queryGroup == "" || workerID == "" || assignments == nil || session == nil || catalog == nil || progress == nil || now == nil {
		return nil, errors.New("alarmd scheduler: complete production SlotSource dependencies are required")
	}
	return &ProductionSlotSource{queryGroup: queryGroup, workerID: workerID, assignments: assignments,
		session: session, catalog: catalog, progress: progress, now: now}, nil
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
		return FrozenSlot{}, false, err
	}
	if err := fact.Validate(request); err != nil {
		return FrozenSlot{}, false, fmt.Errorf("%w: %v", ErrSlotContractDrift, err)
	}
	if fact.Contract.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
		fact.Contract.QueryRevision != schedule.Segment.QueryRevision {
		return FrozenSlot{}, false, ErrSlotContractDrift
	}
	currentAssignment, currentFence, err := source.currentOwnership(ctx, source.now())
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if !sameAssignment(initialAssignment, currentAssignment) || initialFence != currentFence {
		return FrozenSlot{}, false, ErrSlotOwnershipChanged
	}
	slot := FrozenSlot{
		Contract: fact.Contract,
		Dispatch: SlotDispatchContext{Operation: execution.OperationNormal, OwnerFence: currentFence,
			AssignmentGeneration: currentAssignment.AssignmentGeneration},
		ExpectedNextSlot: nextSlot,
	}
	if err := slot.Validate(queryGroup); err != nil {
		return FrozenSlot{}, false, err
	}
	return slot, true, nil
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
