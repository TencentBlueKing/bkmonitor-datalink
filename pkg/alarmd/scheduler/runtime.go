// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var ErrSlotInFlight = errors.New("alarmd scheduler: Query Group Slot is already in flight")

type FrozenSlot struct {
	Contract                execution.FrozenExecutionContractRef
	Dispatch                SlotDispatchContext
	ExpectedNextSlot        execution.EvaluationTime
	NextSlotAfterCompletion execution.EvaluationTime
}

// SlotDispatchContext contains current, replaceable execution authority. It is
// deliberately separate from FrozenExecutionContractRef and business identity.
type SlotDispatchContext struct {
	Operation            execution.Operation
	OwnerFence           execution.OwnerFence
	AssignmentGeneration uint64
}

func (slot FrozenSlot) Validate(queryGroup execution.QueryGroupIdentity) error {
	if err := slot.Contract.Validate(); err != nil {
		return err
	}
	if slot.Dispatch.Operation != execution.OperationNormal || slot.Dispatch.AssignmentGeneration == 0 {
		return errors.New("alarmd scheduler: normal dispatch context is required")
	}
	if err := slot.Dispatch.OwnerFence.Validate(slot.Contract); err != nil {
		return err
	}
	if slot.Contract.Slot.QueryGroup != queryGroup || slot.ExpectedNextSlot != slot.Contract.Slot.EvaluationTime ||
		slot.NextSlotAfterCompletion <= slot.ExpectedNextSlot {
		return errors.New("alarmd scheduler: frozen Slot does not match Query Group Progress")
	}
	return nil
}

type OwnerSession interface {
	ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error)
}

type SlotSource interface {
	Next(context.Context, execution.QueryGroupIdentity) (FrozenSlot, bool, error)
}

type Executor interface {
	Execute(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error)
}

// FlightCoordinator is one process-wide gate keyed by Query Group. It keeps
// query execution single-flight without introducing a Redis business lock.
type FlightCoordinator struct {
	mu     sync.Mutex
	active map[execution.QueryGroupIdentity]struct{}
}

func NewFlightCoordinator() *FlightCoordinator {
	return &FlightCoordinator{active: make(map[execution.QueryGroupIdentity]struct{})}
}

func (coordinator *FlightCoordinator) tryAcquire(queryGroup execution.QueryGroupIdentity) (func(), bool) {
	if coordinator == nil {
		return nil, false
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if _, exists := coordinator.active[queryGroup]; exists {
		return nil, false
	}
	coordinator.active[queryGroup] = struct{}{}
	return func() {
		coordinator.mu.Lock()
		delete(coordinator.active, queryGroup)
		coordinator.mu.Unlock()
	}, true
}

// Runner is bound to one owned Query Group. G1 deliberately exposes only the
// normal operation; retry, replay and probe remain outside this implementation.
type Runner struct {
	queryGroup execution.QueryGroupIdentity
	session    OwnerSession
	source     SlotSource
	executor   Executor
	flights    *FlightCoordinator
	now        func() time.Time
}

func NewRunner(
	queryGroup execution.QueryGroupIdentity,
	session OwnerSession,
	source SlotSource,
	executor Executor,
	flights *FlightCoordinator,
	now func() time.Time,
) (*Runner, error) {
	if queryGroup == "" || session == nil || source == nil || executor == nil || flights == nil || now == nil {
		return nil, errors.New("alarmd scheduler: complete Runner dependencies are required")
	}
	return &Runner{queryGroup: queryGroup, session: session, source: source, executor: executor, flights: flights, now: now}, nil
}

func (runner *Runner) RunOne(
	ctx context.Context,
) (execution.SlotExecutionResult, bool, error) {
	if runner == nil {
		return execution.SlotExecutionResult{}, false, errors.New("alarmd scheduler: initialized Runner is required")
	}
	if err := ctx.Err(); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	release, acquired := runner.flights.tryAcquire(runner.queryGroup)
	if !acquired {
		return execution.SlotExecutionResult{}, false, ErrSlotInFlight
	}
	defer release()

	if _, err := runner.session.ValidateCurrent(ctx, runner.now()); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	slot, due, err := runner.source.Next(ctx, runner.queryGroup)
	if err != nil || !due {
		return execution.SlotExecutionResult{}, false, err
	}
	if err := slot.Validate(runner.queryGroup); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	fence, err := runner.session.ValidateCurrent(ctx, runner.now())
	if err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	if fence != slot.Dispatch.OwnerFence {
		return execution.SlotExecutionResult{}, false, ErrSlotOwnershipChanged
	}
	request := execution.SlotExecutionRequest{
		Contract: slot.Contract, Operation: slot.Dispatch.Operation,
		OwnerFence: fence, ExpectedNextSlot: slot.ExpectedNextSlot, NextSlotAfterCompletion: slot.NextSlotAfterCompletion,
	}
	if err := request.Validate(); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	result, err := runner.executor.Execute(ctx, request)
	return result, true, err
}
