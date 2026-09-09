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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

var ErrSlotInFlight = errors.New("alarmd scheduler: Query Group Slot is already in flight")

var errExecutionAdmissionDenied = errors.New("alarmd scheduler: execution admission denied")

type SourceRetryError struct{ Err error }

func (err *SourceRetryError) Error() string {
	return "alarmd scheduler: temporary Slot source failure: " + err.Err.Error()
}
func (err *SourceRetryError) Unwrap() error { return err.Err }

type SourceBlockedError struct{ Err error }

func (err *SourceBlockedError) Error() string {
	return "alarmd scheduler: blocked exact Slot facts: " + err.Err.Error()
}
func (err *SourceBlockedError) Unwrap() error { return err.Err }

type FrozenSlot struct {
	ExpiredRange                   *execution.ExpiredRangeProjectionV1
	ShortPeriodCohort              string
	Contract                       execution.FrozenExecutionContractRef
	DuePlanTargets                 execution.FrozenDuePlanTargets
	EarliestQueryDeadlineUnixMilli int64
	RecoveryUntilUnixMilli         int64
	KeepUntilUnixMilli             int64
	Dispatch                       SlotDispatchContext
	ExpectedNextSlot               execution.EvaluationTime
	Recovery                       SlotRecoveryFacts
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
	if err := slot.DuePlanTargets.Validate(slot.Contract); err != nil {
		return err
	}
	if slot.EarliestQueryDeadlineUnixMilli <= int64(slot.Contract.Slot.EvaluationTime)*1000 {
		return errors.New("alarmd scheduler: earliest query deadline must follow the frozen evaluation time")
	}
	if slot.RecoveryUntilUnixMilli <= slot.EarliestQueryDeadlineUnixMilli ||
		slot.KeepUntilUnixMilli <= slot.RecoveryUntilUnixMilli {
		return errors.New("alarmd scheduler: recovery and retention boundaries are invalid")
	}
	if err := slot.Dispatch.Operation.Validate(); err != nil || slot.Dispatch.AssignmentGeneration == 0 {
		return errors.New("alarmd scheduler: valid dispatch context is required")
	}
	if err := slot.Recovery.validate(slot.Dispatch.Operation); err != nil {
		return err
	}
	if err := slot.Dispatch.OwnerFence.Validate(slot.Contract); err != nil {
		return err
	}
	if slot.ExpiredRange != nil {
		if err := slot.ExpiredRange.Validate(); err != nil {
			return err
		}
		if slot.ExpectedNextSlot != slot.ExpiredRange.First.Contract.Slot.EvaluationTime || slot.Contract != slot.ExpiredRange.Last.Contract {
			return errors.New("alarmd scheduler: range differs from persisted head or frozen tail")
		}
	} else if slot.ExpectedNextSlot != slot.Contract.Slot.EvaluationTime {
		return errors.New("alarmd scheduler: frozen Slot does not match Query Group Progress")
	}
	if slot.Contract.Slot.QueryGroup != queryGroup {
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

// ExecutionAdmission applies process-local concurrency bounds after the
// Runner has resolved the authoritative frozen operation and before it enters
// Access or the execution coordinator.
type ExecutionAdmission func(execution.Operation) (release func(), admitted bool)

// FlightCoordinator is one process-wide gate keyed by Query Group. It keeps
// query execution single-flight without introducing a Redis business lock.
type FlightCoordinator struct {
	mu                     sync.Mutex
	active                 map[execution.QueryGroupIdentity]struct{}
	recoveryEnabled        bool
	limits                 RecoveryLimits
	now                    func() time.Time
	normalWaiters          []*queryPermitWaiter
	recoveryWaiters        []*queryPermitWaiter
	recoveryChannelWaiters []*recoveryChannelWaiter
	lastNormalQG           execution.QueryGroupIdentity
	lastRecoveryQG         execution.QueryGroupIdentity
	queryInflight          int
	recoveryInflight       int
	nextRecovery           bool
	permitSequence         uint64
	observer               observability.Observer
	inflightByOp           map[execution.Operation]int
}

func NewFlightCoordinator() *FlightCoordinator {
	return &FlightCoordinator{active: make(map[execution.QueryGroupIdentity]struct{}), now: time.Now}
}

func NewFlightCoordinatorWithRecovery(
	limits RecoveryLimits,
	now func() time.Time,
	observers ...observability.Observer,
) (*FlightCoordinator, error) {
	if err := limits.Validate(); err != nil || now == nil {
		return nil, ErrRecoveryLimitsInvalid
	}
	return &FlightCoordinator{active: make(map[execution.QueryGroupIdentity]struct{}), recoveryEnabled: true,
		limits: limits, now: now, nextRecovery: true, observer: observability.Multi(observers...),
		inflightByOp: make(map[execution.Operation]int)}, nil
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

// Runner is bound to one owned Query Group. normal, retry, replay and probe use
// this same single-flight path and the same frozen Slot contract.
type Runner struct {
	queryGroup     execution.QueryGroupIdentity
	session        OwnerSession
	source         SlotSource
	executor       Executor
	flights        *FlightCoordinator
	now            func() time.Time
	attempt        *recoveryAttempt
	sourceFailures uint32
	sourceNextAt   time.Time
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

// NextReadyAt reports when the Runner can make its next QG-local attempt.
// A zero value means there is no active source or execution backoff.
func (runner *Runner) NextReadyAt() time.Time {
	if runner == nil {
		return time.Time{}
	}
	nextAt := runner.sourceNextAt
	if runner.attempt != nil && runner.attempt.nextAt.After(nextAt) {
		nextAt = runner.attempt.nextAt
	}
	return nextAt
}

func (runner *Runner) RunOne(
	ctx context.Context,
) (execution.SlotExecutionResult, bool, error) {
	return runner.runOne(ctx, nil)
}

func (runner *Runner) RunOneAdmitted(
	ctx context.Context,
	admission ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	if admission == nil {
		return execution.SlotExecutionResult{}, false, false, errors.New("alarmd scheduler: execution admission is required")
	}
	result, attempted, err := runner.runOne(ctx, admission)
	if errors.Is(err, errExecutionAdmissionDenied) {
		return result, attempted, true, nil
	}
	return result, attempted, false, err
}

func (runner *Runner) runOne(ctx context.Context, admission ExecutionAdmission) (result execution.SlotExecutionResult, attempted bool, err error) {
	outcome := "other_error"
	returned := false
	defer func() {
		if !returned {
			outcome = "panic"
		}
		if runner == nil || runner.flights == nil || runner.flights.observer == nil {
			return
		}
		func() {
			defer func() { _ = recover() }()
			runner.flights.observer.Observe(ctx, observability.Observation{
				Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
				Result: observability.ResultTerminal, RunOutcome: outcome, Attempted: attempted,
				// Carry the object this round belongs to. Observers that only
				// merge context fields would otherwise see an anonymous round:
				// the scheduler path only injects the key into the context for
				// query groups selected for target flow.
				Trace: observability.TraceFields{QueryGroupKey: string(runner.queryGroup)},
			})
		}()
	}()
	result, attempted, err = runner.runOneTracked(ctx, admission, &outcome)
	returned = true
	return
}

func (runner *Runner) runOneTracked(
	ctx context.Context,
	admission ExecutionAdmission,
	outcome *string,
) (flowResult execution.SlotExecutionResult, flowAttempted bool, flowErr error) {
	decision := "preflight"
	defer func() {
		switch decision {
		case "execution_returned", "query_readiness_deferred", "execute":
			*outcome = "execute_returned"
		default:
			if observability.ValidRunOutcome(decision) {
				*outcome = decision
			} else {
				*outcome = "other_error"
			}
		}
	}()
	var diagnosticReadyAt int64
	if observability.TargetFlowEnabled(ctx) {
		defer func() {
			observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{ExecutionOutcomeKnown: true, ReadyAtMS: diagnosticReadyAt, Decision: decision, Attempted: flowAttempted, Completed: flowResult.Completed, Completion: string(flowResult.CompletionKind)})
		}()
	}
	// One invocation executes at most one frozen Slot. Replay therefore has a
	// fixed one-Slot-per-tick bound instead of a configurable batch surface.
	if runner == nil {
		return execution.SlotExecutionResult{}, false, errors.New("alarmd scheduler: initialized Runner is required")
	}
	if err := ctx.Err(); err != nil {
		decision = "cancelled"
		return execution.SlotExecutionResult{}, false, err
	}
	release, acquired := runner.flights.tryAcquire(runner.queryGroup)
	if !acquired {
		decision = "single_flight_busy"
		return execution.SlotExecutionResult{}, false, ErrSlotInFlight
	}
	defer release()
	if source, ok := runner.source.(interface{ RangeCreationEnabled() bool }); ok && source.RangeCreationEnabled() {
		ctx = context.WithValue(ctx, rangeFlightContextKey{}, runner.queryGroup)
	}

	if _, err := runner.session.ValidateCurrent(ctx, runner.now()); err != nil {
		decision = "ownership_rejected"
		return execution.SlotExecutionResult{}, false, err
	}
	if !runner.sourceNextAt.IsZero() && runner.now().Before(runner.sourceNextAt) {
		decision = "source_backoff"
		diagnosticReadyAt = runner.sourceNextAt.UnixMilli()
		return execution.SlotExecutionResult{}, false, nil
	}
	decision = "source_next"
	slot, due, err := runner.source.Next(ctx, runner.queryGroup)
	if err != nil {
		var retry *SourceRetryError
		var blocked *SourceBlockedError
		if errors.As(err, &retry) || errors.As(err, &blocked) {
			decision = "source_blocked"
			if retry != nil {
				decision = "source_retry"
			}
			runner.sourceFailures++
			runner.sourceNextAt = runner.now().Add(retryDelay(runner.flights.limits, runner.queryGroup, runner.sourceFailures))
			reason := execution.ReasonCode(execution.ReasonBlockedExactSetUnavailable)
			if retry != nil {
				reason = execution.ReasonCode(contract.ReasonSlotSourceRetry)
			}
			return execution.SlotExecutionResult{Result: observability.ResultRetrying, ReasonCode: reason, SourceRetry: true}, true, nil
		}
	}
	if err != nil || !due {
		decision = "source_error"
		if err == nil {
			decision = "source_not_due"
		}
		if err == nil && !due {
			runner.attempt = nil
		}
		return execution.SlotExecutionResult{}, false, err
	}
	runner.sourceFailures = 0
	runner.sourceNextAt = time.Time{}
	if err := slot.Validate(runner.queryGroup); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	operation, ready := runner.operationFor(slot, runner.now())
	if !ready {
		decision = "operation_not_ready"
		return execution.SlotExecutionResult{}, false, nil
	}
	fence, err := runner.session.ValidateCurrent(ctx, runner.now())
	if err != nil {
		decision = "ownership_rejected"
		return execution.SlotExecutionResult{}, false, err
	}
	if fence != slot.Dispatch.OwnerFence {
		decision = "ownership_rejected"
		return execution.SlotExecutionResult{}, false, ErrSlotOwnershipChanged
	}
	request := execution.SlotExecutionRequest{
		ExpiredRange:      slot.ExpiredRange,
		ShortPeriodCohort: slot.ShortPeriodCohort,
		Contract:          slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         slot.RecoveryUntilUnixMilli,
		KeepUntilUnixMilli:             slot.KeepUntilUnixMilli,
		ReplayExpired:                  slot.Recovery.Disposition == ReplayExpired,
		Operation:                      operation, AttemptNo: runner.attemptNo(slot), OwnerFence: fence, ExpectedNextSlot: slot.ExpectedNextSlot,
	}
	if err := request.Validate(); err != nil {
		return execution.SlotExecutionResult{}, false, err
	}
	if admission != nil {
		releaseAdmission, admitted := admission(operation)
		if !admitted {
			decision = "admission_denied"
			return execution.SlotExecutionResult{}, false, errExecutionAdmissionDenied
		}
		if releaseAdmission == nil {
			return execution.SlotExecutionResult{}, false, errors.New("alarmd scheduler: admitted execution requires release")
		}
		defer releaseAdmission()
	}
	decision = "execute"
	result, err := runner.executor.Execute(ctx, request)
	if err != nil {
		var deferred interface{ ReadinessReadyAt() time.Time }
		if errors.As(err, &deferred) {
			decision = "query_readiness_deferred"
			readyAt := deferred.ReadinessReadyAt()
			if readyAt.After(runner.now()) {
				runner.sourceNextAt = readyAt
				return execution.SlotExecutionResult{}, true, nil
			}
		} else if executionErrorBacksOff(ctx, err) {
			runner.recordExecutionFailure(slot, runner.now())
		}
	}
	if err == nil {
		runner.recordResult(slot, result, runner.now())
	}
	decision = "execution_returned"
	return result, true, err
}

// executionErrorBacksOff reports whether a Go error returned by Execute counts
// as a failed attempt of the frozen Slot. Cancellation is not a Slot failure,
// and ownership errors already stop the Runner through the dispatcher, so
// neither may schedule a retry of this Slot.
func executionErrorBacksOff(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	return !errors.Is(err, ownership.ErrStaleFence) && !errors.Is(err, ownership.ErrNotDesired) &&
		!errors.Is(err, ErrSlotOwnershipChanged)
}

func (runner *Runner) attemptNo(slot FrozenSlot) uint32 {
	if runner.attempt == nil || runner.attempt.contract != slot.Contract {
		return 1
	}
	return runner.attempt.failures + 1
}
