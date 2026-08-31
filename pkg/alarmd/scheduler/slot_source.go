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
	ReadFrozenSchedule(context.Context, execution.ScheduleLaneIdentity) (execution.FrozenQueryGroupSchedule, error)
	FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

type ScheduleProgressReader interface {
	LoadProgress(context.Context, execution.ProgressNamespace) (execution.ProgressLoadResult, error)
}

// ProductionSlotSource is bound to one owned Query Group. It reads current
// control facts and returns one normal due Slot; it never executes queries,
// evaluates data, commits state/Progress, or creates recovery state machines.
type ProductionSlotSource struct {
	lane        execution.ScheduleLaneIdentity
	workerID    string
	assignments AssignmentReader
	session     OwnerSession
	catalog     SlotCatalogReader
	progress    ScheduleProgressReader
	now         func() time.Time
}

func NewProductionSlotSource(
	lane execution.ScheduleLaneIdentity,
	workerID string,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	now func() time.Time,
) (*ProductionSlotSource, error) {
	if lane.Validate() != nil || workerID == "" || assignments == nil || session == nil || catalog == nil || progress == nil || now == nil {
		return nil, errors.New("alarmd scheduler: complete production SlotSource dependencies are required")
	}
	return &ProductionSlotSource{lane: lane, workerID: workerID, assignments: assignments,
		session: session, catalog: catalog, progress: progress, now: now}, nil
}

func (source *ProductionSlotSource) Next(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (FrozenSlot, bool, error) {
	if source == nil || queryGroup == "" || queryGroup != source.lane.QueryGroup {
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
	schedule, err := source.catalog.ReadFrozenSchedule(ctx, source.lane)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := schedule.Validate(); err != nil {
		return FrozenSlot{}, false, fmt.Errorf("%w: %v", ErrScheduleFactsInvalid, err)
	}
	if schedule.Lane != source.lane {
		return FrozenSlot{}, false, ErrScheduleFactsInvalid
	}
	namespace := schedule.Lane.ProgressNamespace()
	load, err := source.progress.LoadProgress(ctx, namespace)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := load.Validate(namespace); err != nil {
		return FrozenSlot{}, false, err
	}
	nextSlot := schedule.FirstEvaluationTime
	if load.Status == execution.ProgressFound {
		nextSlot = load.Progress.NextSlot
	}
	duePlans := schedule.DuePlanRefs(nextSlot)
	if len(duePlans) == 0 {
		return FrozenSlot{}, false, ErrProgressOffSchedule
	}
	if at.Unix() < int64(nextSlot) {
		return FrozenSlot{}, false, nil
	}
	nextAfterCompletion, ok := schedule.NextSlotAfter(nextSlot)
	if !ok {
		return FrozenSlot{}, false, ErrScheduleFactsInvalid
	}
	request := execution.FreezeSlotContractRequest{
		Lane: schedule.Lane, EvaluationTime: nextSlot, DuePlans: duePlans,
	}
	fact, err := source.catalog.FreezeSlotContract(ctx, request)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := fact.Validate(request); err != nil {
		return FrozenSlot{}, false, fmt.Errorf("%w: %v", ErrSlotContractDrift, err)
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
		ExpectedNextSlot: nextSlot, NextSlotAfterCompletion: nextAfterCompletion,
	}
	if err := slot.Validate(queryGroup); err != nil {
		return FrozenSlot{}, false, err
	}
	return slot, true, nil
}

func (source *ProductionSlotSource) currentOwnership(
	ctx context.Context,
	at time.Time,
) (ownership.AssignmentRecord, execution.OwnerFence, error) {
	assignment, err := source.assignments.ReadAssignment(ctx, source.lane.QueryGroup)
	if err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if err := assignment.Validate(); err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if assignment.QueryGroup != source.lane.QueryGroup || assignment.DesiredWorkerID != source.workerID {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, ownership.ErrNotDesired
	}
	fence, err := source.session.ValidateCurrent(ctx, at)
	if err != nil {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, err
	}
	if fence.QueryGroup != source.lane.QueryGroup || fence.OwnerID != source.workerID || fence.OwnerEpoch == 0 || fence.LeaseToken == "" {
		return ownership.AssignmentRecord{}, execution.OwnerFence{}, ownership.ErrStaleFence
	}
	return assignment, fence, nil
}

func sameAssignment(left, right ownership.AssignmentRecord) bool {
	return left.QueryGroup == right.QueryGroup && left.DesiredWorkerID == right.DesiredWorkerID &&
		left.AssignmentGeneration == right.AssignmentGeneration
}

var _ SlotSource = (*ProductionSlotSource)(nil)
