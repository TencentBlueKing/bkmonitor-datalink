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
	// ValidateCurrentWithAssignment is ValidateCurrent plus the Assignment
	// record naming the fence owner, from one store round trip. The Runner
	// opens every attempt with it and hands the result to the SlotSource, which
	// is what lets an idle attempt cost one round trip instead of three. It is
	// a required method rather than an optional one a type assertion discovers,
	// because a session without it would quietly restore the old cost on the
	// hottest path in the process with nothing failing to say so.
	ValidateCurrentWithAssignment(context.Context, time.Time) (execution.OwnerFence, ownership.AssignmentRecord, error)
}

// verifiedOwnership carries ownership facts the Runner has already confirmed
// with the store into the SlotSource call that runs inside the same attempt.
//
// It travels on the context rather than on the SlotSource interface because it
// is an optimization one particular pairing can make, not a fact every
// SlotSource has to accept: a source that does not know about it, or a session
// that cannot produce a record, still reads the facts itself and behaves as it
// always did.
type verifiedOwnership struct {
	queryGroup execution.QueryGroupIdentity
	assignment ownership.AssignmentRecord
	fence      execution.OwnerFence
}

type verifiedOwnershipKey struct{}

// withVerifiedOwnership publishes facts only if they are complete and internally
// consistent. Anything less is dropped rather than passed on, so a partial
// reading can never reach a Slot decision as if it had been confirmed: the
// source simply reads for itself, exactly as it did before this hand-off.
func withVerifiedOwnership(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	assignment ownership.AssignmentRecord,
	fence execution.OwnerFence,
) context.Context {
	if assignment.Validate() != nil || assignment.QueryGroup != queryGroup ||
		assignment.DesiredWorkerID != fence.OwnerID || fence.QueryGroup != queryGroup {
		return ctx
	}
	return context.WithValue(ctx, verifiedOwnershipKey{},
		verifiedOwnership{queryGroup: queryGroup, assignment: assignment, fence: fence})
}

func verifiedOwnershipFrom(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (verifiedOwnership, bool) {
	verified, ok := ctx.Value(verifiedOwnershipKey{}).(verifiedOwnership)
	return verified, ok && verified.queryGroup == queryGroup
}

// SlotDueFacts is what one Next call learned about its Query Group's schedule,
// over and above the Slot itself. A caller that indexes Query Groups by when
// they can next become due reads it instead of paying for a second call to
// find out.
//
// It is a return value rather than something a caller discovers with a type
// assertion. A source that did not fill it in would leave every Query Group it
// serves without a bound, every idle dispatch would be paid in full again, and
// no metric would separate that from a deployment where the index is simply
// not helping.
type SlotDueFacts struct {
	// NotDueUntilUnix is the wall-clock second before which the facts this call
	// read say Next cannot return a due Slot. It is filled in only on a not-due
	// return; zero means the call carries no bound and the Query Group must be
	// treated as due now. Truncation to the second is toward the past, which is
	// the safe direction: an early bound costs one more call, a late one would
	// hold back a Slot that is already due.
	NotDueUntilUnix int64
	// IntervalSeconds is the shortest evaluation interval among the Plans due at
	// the Slot this call resolved. It is the number shortPeriodCohort already
	// derives its cohort from, so carrying it out costs no extra read, and it is
	// what tells a reader how late a wake that has not happened really is: sixty
	// seconds is nothing to an hourly Plan and six missed evaluations to a
	// ten-second one.
	IntervalSeconds int64
	// Retired reports that this Query Group has no successor Slot because its
	// schedule is retired. A retirement can be revoked by a later publication,
	// so how long to wait before asking again is the caller's decision; the
	// source only says that this, and not a future Slot, is why it is not due.
	Retired bool
}

type SlotSource interface {
	Next(context.Context, execution.QueryGroupIdentity) (FrozenSlot, bool, SlotDueFacts, error)
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
	// permitSecondsByOp accumulates how long permits were actually held.
	//
	// The inflight counts above are an instantaneous reading, and an
	// instantaneous reading cannot answer "how full was the budget over the last
	// five minutes" -- the question the budget is sized against. Occupancy
	// integrated over time can, and being a counter it survives being sampled at
	// an arbitrary moment, which a gauge does not.
	permitSecondsByOp map[execution.Operation]float64
	// heldPermits is what is occupied right now, with the moment it was granted.
	// It exists so occupancy can include permits that have not been released
	// yet; without it a permit that never comes back contributes nothing, which
	// is the opposite of what should happen.
	heldPermits map[uint64]heldPermit
}

type heldPermit struct {
	operation execution.Operation
	since     time.Time
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
		inflightByOp:      make(map[execution.Operation]int),
		permitSecondsByOp: make(map[execution.Operation]float64),
		heldPermits:       make(map[uint64]heldPermit)}, nil
}

// QueryPermitOccupancy is how much of the query permit budget is in use.
//
// It is read at scrape time rather than pushed on permit events. A pushed
// gauge reports whatever the last event left behind, so between two events it
// says nothing about now -- and permit events are exactly the moments when the
// count is about to change, which biases the reading toward the boundary.
type QueryPermitOccupancy struct {
	// Inflight is how many permits are held right now, per operation.
	Inflight map[execution.Operation]int
	// Waiting is how many callers are queued for one, by queue.
	Waiting map[string]int
	// HeldSeconds is cumulative permit-hold time per operation. Its rate over a
	// window is the mean number of permits occupied in that window, which is
	// the number to compare against the configured budget.
	HeldSeconds map[execution.Operation]float64
	// Budget is the configured ceiling, so a reader does not have to find the
	// deployment's configuration to know what the occupancy is out of.
	Budget         int
	RecoveryBudget int
}

// QueryPermitOccupancySource reports live occupancy.
type QueryPermitOccupancySource func() QueryPermitOccupancy

// QueryPermitOccupancy reports the current occupancy of the permit budget.
func (coordinator *FlightCoordinator) QueryPermitOccupancy() QueryPermitOccupancy {
	occupancy := QueryPermitOccupancy{
		Inflight: make(map[execution.Operation]int, 4), Waiting: make(map[string]int, 2),
		HeldSeconds: make(map[execution.Operation]float64, 4),
	}
	if coordinator == nil {
		return occupancy
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	for operation, count := range coordinator.inflightByOp {
		occupancy.Inflight[operation] = count
	}
	for operation, seconds := range coordinator.permitSecondsByOp {
		occupancy.HeldSeconds[operation] = seconds
	}
	// Permits still held have not been added to the total yet, so a query that
	// ran for the whole window would otherwise contribute nothing to it -- and a
	// permanently stuck permit, the one most worth seeing, would contribute
	// nothing forever. Counting the elapsed part keeps the total continuous:
	// when the permit is finally released its full duration moves from here into
	// the accumulator, and nothing is double counted.
	at := coordinator.now()
	for _, held := range coordinator.heldPermits {
		if elapsed := at.Sub(held.since); elapsed > 0 {
			occupancy.HeldSeconds[held.operation] += elapsed.Seconds()
		}
	}
	occupancy.Waiting["normal"] = len(coordinator.normalWaiters)
	occupancy.Waiting["recovery"] = len(coordinator.recoveryWaiters) + len(coordinator.recoveryChannelWaiters)
	occupancy.Budget = coordinator.limits.ProcessQueryPermits
	occupancy.RecoveryBudget = coordinator.limits.RecoveryQueryPermits
	return occupancy
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
	dueBound       RunnerDueBound
	queryCooldown  queryCooldownState
}

// DueVerdict is what one round was able to say about whether its Query Group
// was due.
//
// A round that never reached the schedule - it lost a single-flight race, it
// was cancelled, its ownership check failed - says nothing about dueness.
// Counting that silence as either answer would put noise into the one counter
// whose whole purpose is to falsify the index, and a falsifier that also counts
// non-violations cannot be read.
type DueVerdict uint8

const (
	DueVerdictUnknown DueVerdict = iota
	DueVerdictDue
	DueVerdictNotDue
)

// RunnerDueBound is what the Runner knows, once a round has returned, about
// when this Query Group is worth running again.
//
// It is read after the round rather than returned by it for the same reason
// NextReadyAt is: the dispatcher already reads the Runner's own view of its
// next attempt at exactly that point, and the two answers belong together.
type RunnerDueBound struct {
	// NotDueUntilUnix is the wall-clock second before which nothing this Runner
	// can do will produce a due Slot. Zero means due now, which is what every
	// round that could not establish a bound reports.
	NotDueUntilUnix int64
	// IntervalSeconds is the shortest evaluation interval among the Plans due at
	// that second, when the round got far enough to know it.
	IntervalSeconds int64
	// Deferred says the bound came from this Runner's own backoff rather than
	// from the schedule. The distinction decides two things: a publication can
	// only make a schedule bound arrive earlier, never a backoff one, and a
	// matured backoff belongs in the recovery queue, not the ready queue.
	Deferred bool
	// QueryCooldown suppresses queries, but must be revalidated on publication.
	// Unlike Deferred it must not enter the execution-retry delayed queue.
	QueryCooldown bool
	// Retired says the Query Group has no successor Slot at all. A revocation
	// can bring it back, so the caller bounds how long it waits.
	Retired bool
	// Executed says this round resolved a frozen Slot and went on to run it.
	Executed bool
	Verdict  DueVerdict
}

// DueBound reports what the last completed round concluded. A Runner that has
// not run yet reports the zero bound, which reads as due now - the same answer
// as no entry at all, which is what a caller that indexes these will see for a
// Query Group it has just taken over.
func (runner *Runner) DueBound() RunnerDueBound {
	if runner == nil {
		return RunnerDueBound{}
	}
	return runner.dueBound
}

// recordDueBound turns the decision the round reached into the bound a
// dispatcher can act on. Every return path is named here on purpose: a path
// that fell through to the default would be given "due now", which costs a
// round trip and never hides a Slot, but it should be a decision rather than an
// omission.
func (runner *Runner) recordDueBound(decision string, facts SlotDueFacts) {
	bound := RunnerDueBound{IntervalSeconds: facts.IntervalSeconds}
	switch decision {
	case "source_not_due":
		// The source read the authoritative cursor and the Slot is in the future,
		// or the schedule is retired. This is the bound worth having.
		bound.Verdict = DueVerdictNotDue
		bound.NotDueUntilUnix, bound.Retired = facts.NotDueUntilUnix, facts.Retired
	case "source_backoff":
		// The local gate answered before any store read. The Query Group is not
		// due for a reason of its own, which is exactly what deferred means.
		bound.Verdict = DueVerdictNotDue
		bound.Deferred = true
		bound.NotDueUntilUnix = runnerBoundSecond(runner.NextReadyAt())
	case "source_retry", "source_blocked":
		// A failed read says nothing about the schedule, so there is no verdict
		// to compare against. The retry backoff is still a real bound.
		bound.Deferred = true
		bound.NotDueUntilUnix = runnerBoundSecond(runner.NextReadyAt())
	case "query_readiness_deferred":
		bound.Verdict, bound.Executed, bound.Deferred = DueVerdictDue, true, true
		bound.NotDueUntilUnix = runnerBoundSecond(runner.NextReadyAt())
	case "query_cooldown":
		bound.Verdict = DueVerdictDue
		bound.QueryCooldown = true
		bound.NotDueUntilUnix = runnerBoundSecond(runner.queryCooldown.wakeAt)
	case "execute", "execution_returned", "operation_not_ready", "admission_denied":
		bound.Verdict, bound.Executed = DueVerdictDue, true
		// A completed Slot leaves no backoff and therefore no bound: the cursor
		// has moved and the next call is what establishes the new one. A failed
		// one leaves an attempt backoff, and that is a deferred bound.
		if readyAt := runner.NextReadyAt(); !readyAt.IsZero() {
			bound.Deferred, bound.NotDueUntilUnix = true, runnerBoundSecond(readyAt)
		}
	default:
		// preflight, cancelled, single_flight_busy, ownership_rejected,
		// source_error: no verdict and no bound, so the Query Group is due now.
	}
	runner.dueBound = bound
}

// runnerBoundSecond floors to the second. Flooring is the safe direction: a
// bound one second early costs one extra call, a bound one second late holds
// back a Slot that is already due.
func runnerBoundSecond(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
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
	// Every round rewrites the bound, including the rounds that establish none.
	// Rewriting on return rather than on dispatch is what lets one reading also
	// answer "was this Query Group dispatched and never seen again": a Runner
	// that hangs leaves its old bound standing and falls behind the wall clock,
	// where a scheme that cleared the entry on dispatch would show nothing at all.
	var sourceFacts SlotDueFacts
	defer func() { runner.recordDueBound(decision, sourceFacts) }()
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

	// The local backoff decision comes first because it needs nothing from the
	// store. It is free today -- a normal Query Group has no source backoff
	// pending, so the comparison is against a zero time -- but once due Slots
	// are indexed, every Query Group the index suppresses would otherwise pay a
	// round trip to be told what this comparison already knew.
	if !runner.sourceNextAt.IsZero() && runner.now().Before(runner.sourceNextAt) {
		decision = "source_backoff"
		diagnosticReadyAt = runner.sourceNextAt.UnixMilli()
		return execution.SlotExecutionResult{}, false, nil
	}
	// This stays the Runner's own gate rather than moving into the source. It
	// is what separates losing the Query Group from failing to read it: an
	// ownership rejection surfaced from inside Next would be counted as a Slot
	// source failure, and the run_one return mix would stop distinguishing the
	// two. The SlotSource opens with the same question, so the answer is
	// carried into it instead of being asked again.
	confirmedFence, confirmedAssignment, err := runner.session.ValidateCurrentWithAssignment(ctx, runner.now())
	if err != nil {
		decision = "ownership_rejected"
		return execution.SlotExecutionResult{}, false, err
	}
	ctx = withVerifiedOwnership(ctx, runner.queryGroup, confirmedAssignment, confirmedFence)
	decision = "source_next"
	slot, due, facts, err := runner.source.Next(ctx, runner.queryGroup)
	sourceFacts = facts
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
	// The authoritative Slot supplies the maintenance deadline and configuration.
	// Do not hide this gate in NextReadyAt: publication must be able to recheck it,
	// and expired finalization must remain runnable without a query.
	if runner.deferUnavailableQuery(ctx, slot) {
		decision = "query_cooldown"
		diagnosticReadyAt = runner.queryCooldown.wakeAt.UnixMilli()
		return execution.SlotExecutionResult{}, false, nil
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
		runner.recordQueryAvailability(ctx, slot, result, sourceFacts.IntervalSeconds)
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
