// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
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

// FrozenPlanSchedule is the Scheduler projection of one active Plan schedule.
// Alignment is an already resolved Unix-second grid anchor. Parsing source
// configuration and compiling Plans remain control-plane responsibilities.
type FrozenPlanSchedule struct {
	Identity         execution.PlanIdentity
	ScheduleRevision execution.PlanScheduleRevision
	IntervalSeconds  int64
	Alignment        execution.EvaluationTime
}

type FrozenPlanScheduleRef struct {
	Identity         execution.PlanIdentity
	ScheduleRevision execution.PlanScheduleRevision
}

// FrozenQueryGroupSchedule contains only immutable facts needed to enumerate
// Slots. FirstEvaluationTime is the control plane's resolved activation/cutover
// Slot; the Scheduler must not derive it from local process time.
type FrozenQueryGroupSchedule struct {
	QueryGroup          execution.QueryGroupIdentity
	ScheduleRevision    execution.ScheduleRevision
	FirstEvaluationTime execution.EvaluationTime
	Plans               []FrozenPlanSchedule
}

func (schedule FrozenQueryGroupSchedule) validate(
	queryGroup execution.QueryGroupIdentity,
	scheduleRevision execution.ScheduleRevision,
) error {
	if schedule.QueryGroup != queryGroup || schedule.ScheduleRevision == "" || schedule.ScheduleRevision != scheduleRevision ||
		schedule.FirstEvaluationTime <= 0 || len(schedule.Plans) == 0 {
		return ErrScheduleFactsInvalid
	}
	seen := make(map[execution.PlanIdentity]struct{}, len(schedule.Plans))
	for _, plan := range schedule.Plans {
		if plan.Identity.TenantID == "" || plan.Identity.BusinessID == "" || plan.Identity.StrategyID == "" ||
			plan.ScheduleRevision == "" || plan.IntervalSeconds <= 0 || plan.Alignment < 0 {
			return ErrScheduleFactsInvalid
		}
		if _, duplicate := seen[plan.Identity]; duplicate {
			return ErrScheduleFactsInvalid
		}
		seen[plan.Identity] = struct{}{}
	}
	if len(schedule.duePlans(schedule.FirstEvaluationTime)) == 0 {
		return ErrScheduleFactsInvalid
	}
	return nil
}

func (schedule FrozenQueryGroupSchedule) duePlans(at execution.EvaluationTime) []FrozenPlanScheduleRef {
	if at < schedule.FirstEvaluationTime {
		return nil
	}
	due := make([]FrozenPlanScheduleRef, 0, len(schedule.Plans))
	for _, plan := range schedule.Plans {
		if onGrid(at, plan.Alignment, plan.IntervalSeconds) {
			due = append(due, FrozenPlanScheduleRef{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision})
		}
	}
	sort.Slice(due, func(left, right int) bool { return lessPlan(due[left].Identity, due[right].Identity) })
	return due
}

func (schedule FrozenQueryGroupSchedule) nextSlotAfter(current execution.EvaluationTime) (execution.EvaluationTime, bool) {
	if current >= execution.EvaluationTime(math.MaxInt64) {
		return 0, false
	}
	next := execution.EvaluationTime(math.MaxInt64)
	for _, plan := range schedule.Plans {
		candidate, ok := alignedAtOrAfter(current+1, plan.Alignment, plan.IntervalSeconds)
		if ok && candidate < next {
			next = candidate
		}
	}
	return next, next != execution.EvaluationTime(math.MaxInt64)
}

func onGrid(at, alignment execution.EvaluationTime, interval int64) bool {
	if interval <= 0 || at < alignment {
		return false
	}
	return (int64(at)-int64(alignment))%interval == 0
}

func alignedAtOrAfter(at, alignment execution.EvaluationTime, interval int64) (execution.EvaluationTime, bool) {
	if interval <= 0 {
		return 0, false
	}
	if at <= alignment {
		return alignment, true
	}
	delta := int64(at) - int64(alignment)
	remainder := delta % interval
	if remainder == 0 {
		return at, true
	}
	increment := interval - remainder
	if int64(at) > math.MaxInt64-increment {
		return 0, false
	}
	return execution.EvaluationTime(int64(at) + increment), true
}

func lessPlan(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

// FreezeSlotContractRequest keeps Scheduler enumeration separate from Catalog
// materialization. The Catalog adapter selects the full frozen Plan/query facts
// by the persisted cutover evaluation time and derives DuePlanSetDigest for
// this exact due set; it must not resolve the request from latest content.
type FreezeSlotContractRequest struct {
	QueryGroup       execution.QueryGroupIdentity
	ScheduleRevision execution.ScheduleRevision
	EvaluationTime   execution.EvaluationTime
	DuePlans         []FrozenPlanScheduleRef
}

type AssignmentReader interface {
	ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error)
}

type SlotCatalogReader interface {
	ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.ScheduleRevision) (FrozenQueryGroupSchedule, error)
	FreezeSlotContract(context.Context, FreezeSlotContractRequest) (execution.FrozenExecutionContractRef, error)
}

type ScheduleProgressReader interface {
	LoadProgress(context.Context, execution.ProgressNamespace) (execution.ProgressLoadResult, error)
}

// ProductionSlotSource is bound to one owned Query Group. It reads current
// control facts and returns one normal due Slot; it never executes queries,
// evaluates data, commits state/Progress, or creates recovery state machines.
type ProductionSlotSource struct {
	queryGroup       execution.QueryGroupIdentity
	scheduleRevision execution.ScheduleRevision
	workerID         string
	assignments      AssignmentReader
	session          OwnerSession
	catalog          SlotCatalogReader
	progress         ScheduleProgressReader
	now              func() time.Time
}

func NewProductionSlotSource(
	queryGroup execution.QueryGroupIdentity,
	scheduleRevision execution.ScheduleRevision,
	workerID string,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	now func() time.Time,
) (*ProductionSlotSource, error) {
	if queryGroup == "" || scheduleRevision == "" || workerID == "" || assignments == nil || session == nil || catalog == nil || progress == nil || now == nil {
		return nil, errors.New("alarmd scheduler: complete production SlotSource dependencies are required")
	}
	return &ProductionSlotSource{queryGroup: queryGroup, scheduleRevision: scheduleRevision, workerID: workerID, assignments: assignments,
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
	schedule, err := source.catalog.ReadFrozenSchedule(ctx, queryGroup, source.scheduleRevision)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := schedule.validate(queryGroup, source.scheduleRevision); err != nil {
		return FrozenSlot{}, false, err
	}
	namespace := execution.ProgressNamespace{QueryGroup: queryGroup, ScheduleRevision: schedule.ScheduleRevision}
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
	duePlans := schedule.duePlans(nextSlot)
	if len(duePlans) == 0 {
		return FrozenSlot{}, false, ErrProgressOffSchedule
	}
	if at.Unix() < int64(nextSlot) {
		return FrozenSlot{}, false, nil
	}
	nextAfterCompletion, ok := schedule.nextSlotAfter(nextSlot)
	if !ok {
		return FrozenSlot{}, false, ErrScheduleFactsInvalid
	}
	request := FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: schedule.ScheduleRevision, EvaluationTime: nextSlot, DuePlans: duePlans,
	}
	contractRef, err := source.catalog.FreezeSlotContract(ctx, request)
	if err != nil {
		return FrozenSlot{}, false, err
	}
	if err := validateFrozenContract(request, contractRef); err != nil {
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
		Contract: contractRef,
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

func validateFrozenContract(request FreezeSlotContractRequest, contractRef execution.FrozenExecutionContractRef) error {
	if err := contractRef.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrSlotContractDrift, err)
	}
	if contractRef.Slot.QueryGroup != request.QueryGroup || contractRef.Slot.EvaluationTime != request.EvaluationTime ||
		contractRef.Slot.ScheduleRevision != request.ScheduleRevision || contractRef.ScheduleRevision != request.ScheduleRevision {
		return ErrSlotContractDrift
	}
	return nil
}

func sameAssignment(left, right ownership.AssignmentRecord) bool {
	return left.QueryGroup == right.QueryGroup && left.DesiredWorkerID == right.DesiredWorkerID &&
		left.AssignmentGeneration == right.AssignmentGeneration
}

var _ SlotSource = (*ProductionSlotSource)(nil)
