// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var (
	ErrRecoveryLimitsInvalid = errors.New("alarmd scheduler: recovery limits are invalid")
	// ErrQueryPermitQueueFull and ErrRecoveryPermitsOff are permits not
	// granted, and say so to whatever reads the failure: the query whose
	// permit it was completes with the admission word the query_admission
	// line already named, not internal_unknown and code OTHER.
	ErrQueryPermitQueueFull error = &queryPermitRefusal{code: string(permitQueueFull), text: "alarmd scheduler: query permit queue is full"}
	ErrRecoveryPermitsOff   error = &queryPermitRefusal{code: string(permitRecoveryOff), text: "alarmd scheduler: recovery query permits are disabled"}
)

// queryPermitRefusal is a permit refused for want of room or of recovery
// permits. Each is one value, compared with errors.Is as before.
type queryPermitRefusal struct {
	code string
	text string
}

func (err *queryPermitRefusal) Error() string { return err.text }

// QueryFailure is the admission category and the refusal's word, as the
// query failure facts read it.
func (err *queryPermitRefusal) QueryFailure() (string, string) {
	return observability.QueryFailureCategoryAdmission, err.code
}

// RecoveryLimits are process-wide fixed bounds. They deliberately do not
// contain QG-specific weights or adaptive controls.
type RecoveryLimits struct {
	ProcessQueryPermits   int
	RecoveryQueryPermits  int
	ReadyQueueCapacity    int
	RecoveryQueueCapacity int
	MaxQueuedItemsPerQG   int
	MaxReplaySlots        uint32
	MaxReplayAge          time.Duration
	RetryMinDelay         time.Duration
	RetryMaxDelay         time.Duration
	// TakeoverDivertFloor is the least query budget a round must have left to
	// run as a normal round, when the group has no reading of its own query's
	// duration (WithQueryLatency) and its read-hold record keeps none from
	// its previous owner (WithInheritedQueryLatency): a round due before this
	// process took its Query Group over, or one whose readiness landed at the
	// edge of its deadline (divertShortOfTime). Below it the round goes to
	// replay at once, under replay's own deadline, instead of starting a
	// normal attempt that cannot finish. A group that never completes a first
	// read in time never gets a reading, and stays on the fallback, as does a
	// group whose record keeps no reading (a group never measured keeps no record).
	// Zero turns the diversion off where there is no reading.
	TakeoverDivertFloor time.Duration
}

func (limits RecoveryLimits) Validate() error {
	if limits.ProcessQueryPermits <= 0 || limits.RecoveryQueryPermits < 0 ||
		limits.RecoveryQueryPermits >= limits.ProcessQueryPermits ||
		limits.ReadyQueueCapacity <= 0 || limits.RecoveryQueueCapacity <= 0 ||
		limits.MaxQueuedItemsPerQG <= 0 || limits.MaxQueuedItemsPerQG >= limits.ReadyQueueCapacity ||
		limits.MaxQueuedItemsPerQG >= limits.RecoveryQueueCapacity ||
		limits.MaxReplaySlots == 0 || limits.MaxReplayAge <= 0 ||
		limits.RetryMinDelay <= 0 || limits.RetryMaxDelay < limits.RetryMinDelay ||
		limits.TakeoverDivertFloor < 0 || limits.TakeoverDivertFloor >= limits.MaxReplayAge {
		return ErrRecoveryLimitsInvalid
	}
	return nil
}

type ReplayDisposition string

const (
	ReplayLive     ReplayDisposition = "LIVE"
	ReplayEligible ReplayDisposition = "REPLAY_ELIGIBLE"
	ReplayExpired  ReplayDisposition = "REPLAY_EXPIRED"
)

// ReplayExpiryReason says which of the several conditions that all expire a
// replay actually happened. They need different responses and one of them is a
// defect report, so "REPLAY_EXPIRED" alone is not something anyone can act on.
type ReplayExpiryReason string

const (
	// ReplayExpiredByAge is the ordinary one: the Slot is older than the
	// replay window allows, whatever the schedule under it.
	ReplayExpiredByAge ReplayExpiryReason = "REPLAY_AGE_EXCEEDED"
	// ReplayExpiredByDistance is the other ordinary one: too many grid points
	// have passed since the Slot, so replaying it would compete with Slots the
	// worker still has to run.
	ReplayExpiredByDistance ReplayExpiryReason = "REPLAY_DISTANCE_EXCEEDED"
	// ReplayExpiredByWait is a defect report: the readiness rule would hold the
	// read until after the replay window closes, so the replay would be
	// dispatched, wait, and be abandoned for being late. Whenever this is
	// counted, the two rules have been derived from settings that disagree.
	//
	// Counting zero of these does not mean it is not happening. The readiness
	// instant the guard compares is not the one access waits for: the guard
	// uses evaluationTime plus the settling wait, while access computes it
	// from the query window's end plus the source delay plus that same wait
	// (access/source.go frozenConsumerReadyAt). The guard's number is a lower
	// bound on the real one and is documented as deliberately under-firing, so
	// a Query Group whose real readiness lands well past the window can be in
	// this contradiction on every Slot and never be counted here. Read it as
	// "the cheap comparison caught one", never as "there are none".
	ReplayExpiredByWait ReplayExpiryReason = "REPLAY_WAIT_EXCEEDS_DISTANCE"
	// ReplayExpiredRange is the whole-range finalization of a run of Slots
	// already past their replay window, which carries one set of facts for the
	// range rather than per Slot.
	ReplayExpiredRange ReplayExpiryReason = "REPLAY_RANGE_EXPIRED"
)

type SlotRecoveryFacts struct {
	// RecheckAtUnixMilli is the next time these recovery facts can change.
	// It is derived from reads the source already made, not a guessed interval.
	RecheckAtUnixMilli int64
	Disposition        ReplayDisposition
	Reason             ReplayExpiryReason
	Distance           uint32
	Age                time.Duration
	// ReadyAtUnixMilli and DistanceBoundaryUnixMilli are the two instants
	// ReplayExpiredByWait compared. A reason without the values behind it says
	// a rule was broken without saying by how much, which is the difference
	// between a report someone can act on and one they have to reproduce.
	ReadyAtUnixMilli          int64
	DistanceBoundaryUnixMilli int64
	// SlotAgeMillis, AgeLimit and DistanceLimit are what an expiry was decided
	// against, taken where it was decided (expiryDecided): the decision
	// instant less the Slot's evaluation time, and the two configured limits
	// the age and the distance were compared with. Set on REPLAY_EXPIRED only,
	// for the slot_skipped line of the Slot it gives up.
	SlotAgeMillis int64
	AgeLimit      time.Duration
	DistanceLimit uint32
	// RangeDistance is the distance a range built this round was sized by,
	// for the same line.
	RangeDistance *observability.RangeDistanceFacts
	// Decision is what the source judged when it let a live Slot run as a
	// normal round (classifyRecoveryOf), carried to the Slot's completion so
	// a round that then times out says on its own line why it was not sent
	// to replay. Nil on every other disposition.
	Decision *observability.DivertDecisionFacts
}

func (facts SlotRecoveryFacts) validate(operation execution.Operation) error {
	switch facts.Disposition {
	case "":
		return nil
	case ReplayLive:
		if operation != execution.OperationNormal || facts.Distance != 0 || facts.Age != 0 {
			return errors.New("alarmd scheduler: live Slot has recovery facts")
		}
	case ReplayEligible:
		if operation != execution.OperationReplay || facts.Distance == 0 || facts.Age < 0 {
			return errors.New("alarmd scheduler: eligible replay has invalid recovery facts")
		}
	case ReplayExpired:
		if operation != execution.OperationNormal || facts.Distance == 0 || facts.Age < 0 {
			return errors.New("alarmd scheduler: expired replay has invalid recovery facts")
		}
		// Every expiry names itself. The three conditions below are answered
		// differently -- wait it out, accept the gap, or fix a misconfiguration
		// -- and a Slot that expired without saying which was indistinguishable
		// from the other two on every page that showed it.
		if !facts.Reason.valid() {
			return errors.New("alarmd scheduler: expired replay does not say why")
		}
	default:
		return errors.New("alarmd scheduler: unknown replay disposition")
	}
	return nil
}

func (reason ReplayExpiryReason) valid() bool {
	for _, known := range observability.ReplayExpiryReasons {
		if string(reason) == known {
			return true
		}
	}
	return false
}

type recoveryAttempt struct {
	contract  execution.FrozenExecutionContractRef
	next      execution.Operation
	nextAt    time.Time
	failures  uint32
	probeUsed bool
}

func (runner *Runner) operationFor(
	slot FrozenSlot,
	at time.Time,
) (execution.Operation, bool) {
	if !runner.flights.recoveryEnabled {
		return slot.Dispatch.Operation, true
	}
	if runner.attempt != nil && runner.attempt.contract != slot.Contract {
		runner.attempt = nil
	}
	if runner.attempt != nil && at.Before(runner.attempt.nextAt) {
		return "", false
	}
	if slot.Recovery.Disposition == ReplayExpired {
		// Expired replay always uses normal dispatch. The failure count of the
		// same frozen contract is kept so repeated failures still back off.
		return execution.OperationNormal, true
	}
	if runner.attempt == nil {
		return slot.Dispatch.Operation, true
	}
	return runner.attempt.next, true
}

// recordExecutionFailure treats a Go error from Execute as one failed attempt
// of the frozen Slot so NextReadyAt becomes non-zero and the dispatcher parks
// the Query Group in its delayed queue instead of retrying on every tick.
// There is deliberately no attempt-count cap here: the Slot is already time
// bounded by recovery_until, after which the source finalizes it.
func (runner *Runner) recordExecutionFailure(slot FrozenSlot, at time.Time) {
	if !runner.flights.recoveryEnabled {
		return
	}
	if runner.attempt == nil || runner.attempt.contract != slot.Contract {
		runner.attempt = &recoveryAttempt{contract: slot.Contract}
	}
	runner.attempt.failures++
	runner.attempt.next = execution.OperationRetry
	runner.attempt.nextAt = at.Add(retryDelay(runner.flights.limits, runner.queryGroup, runner.attempt.failures))
}

func (runner *Runner) recordResult(
	slot FrozenSlot,
	result execution.SlotExecutionResult,
	at time.Time,
) {
	if !runner.flights.recoveryEnabled {
		return
	}
	if result.Completed || result.Result != observability.ResultRetrying {
		runner.attempt = nil
		return
	}
	if runner.attempt == nil || runner.attempt.contract != slot.Contract {
		runner.attempt = &recoveryAttempt{contract: slot.Contract}
	}
	runner.attempt.failures++
	if result.ReasonCode == execution.ReasonCode(contract.ReasonQueryPartial) && !runner.attempt.probeUsed {
		runner.attempt.next = execution.OperationProbe
		runner.attempt.probeUsed = true
	} else {
		runner.attempt.next = execution.OperationRetry
	}
	runner.attempt.nextAt = at.Add(retryDelay(runner.flights.limits, runner.queryGroup, runner.attempt.failures))
}

func retryDelay(limits RecoveryLimits, queryGroup execution.QueryGroupIdentity, failures uint32) time.Duration {
	delay := limits.RetryMinDelay
	for step := uint32(1); step < failures && delay < limits.RetryMaxDelay; step++ {
		if delay > limits.RetryMaxDelay/2 {
			delay = limits.RetryMaxDelay
			break
		}
		delay *= 2
	}
	if delay >= limits.RetryMaxDelay || failures <= 1 {
		return delay
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(queryGroup))
	_, _ = hash.Write([]byte{byte(failures), byte(failures >> 8)})
	jitterRange := delay / 4
	if jitterRange <= 0 {
		return delay
	}
	jitter := time.Duration(uint64(hash.Sum32()) % uint64(jitterRange+1))
	if delay+jitter > limits.RetryMaxDelay {
		return limits.RetryMaxDelay
	}
	return delay + jitter
}

type queryPermitWaiter struct {
	slot      execution.SlotIdentity
	operation execution.Operation
	deadline  time.Time
	grant     chan *QueryPermit
}

type queryPermitSnapshot struct {
	Inflight         int
	RecoveryInflight int
	NormalWaiting    int
	RecoveryWaiting  int
	NormalInflight   int
	RetryInflight    int
	ReplayInflight   int
	ProbeInflight    int
}

type QueryPermit struct {
	coordinator    *FlightCoordinator
	recovery       *execution.RecoveryPermit
	operation      execution.Operation
	id             uint64
	once           sync.Once
	releaseChannel func()
	// waited is whether the query found no free permit and queued for this
	// one.
	waited bool
}

// Waited reports whether the query this permit admits had to queue for it:
// the moment it was sent this process's own query budget was full. The
// downstream's errors are read against it, to tell errors this process's
// contention brought on from errors that arrive whatever it does.
func (permit *QueryPermit) Waited() bool {
	return permit != nil && permit.waited
}

func (permit *QueryPermit) RecoveryPermit() *execution.RecoveryPermit {
	if permit == nil || permit.recovery == nil {
		return nil
	}
	copy := *permit.recovery
	return &copy
}

func (permit *QueryPermit) Release() {
	if permit == nil || permit.coordinator == nil {
		return
	}
	permit.once.Do(func() {
		permit.coordinator.releaseQueryPermit(permit.operation, permit.id)
		if permit.releaseChannel != nil {
			permit.releaseChannel()
		}
	})
}

func (coordinator *FlightCoordinator) acquireProcessQueryPermit(
	ctx context.Context,
	slot execution.SlotIdentity,
	operation execution.Operation,
	deadline time.Time,
) (*QueryPermit, error) {

	if coordinator == nil || !coordinator.recoveryEnabled {
		return nil, ErrRecoveryLimitsInvalid
	}
	if slot.QueryGroup == "" || slot.EvaluationTime <= 0 || operation.Validate() != nil || deadline.IsZero() {
		return nil, errors.New("alarmd scheduler: valid query permit request is required")
	}
	if err := ctx.Err(); err != nil {
		coordinator.observeQueryPermit(ctx, operation, observability.ResultPaused, true, coordinator.queryPermitSnapshot())
		return nil, err
	}
	if !deadline.After(coordinator.now()) {
		coordinator.observeQueryPermit(ctx, operation, observability.ResultTimeout, true, coordinator.queryPermitSnapshot())
		return nil, context.DeadlineExceeded
	}
	waiter := &queryPermitWaiter{slot: slot, operation: operation, deadline: deadline, grant: make(chan *QueryPermit, 1)}
	// A recovery operation arrives here only holding a recovery channel, and
	// a channel is granted only while recovery permits are above zero: the
	// recovery-off refusal is the channel's.
	recovery := operation != execution.OperationNormal
	coordinator.mu.Lock()
	queue := &coordinator.normalWaiters
	capacity := coordinator.limits.ReadyQueueCapacity
	if recovery {
		queue = &coordinator.recoveryWaiters
		capacity = coordinator.limits.RecoveryQueueCapacity
	}
	coordinator.expireWaitersLocked(queue)
	if len(*queue) >= capacity || queuedForQueryGroup(*queue, slot.QueryGroup) >= coordinator.limits.MaxQueuedItemsPerQG {
		coordinator.mu.Unlock()
		coordinator.observeQueryPermitReason(ctx, operation, observability.ResultFailed, permitQueueFull, true, coordinator.queryPermitSnapshot())
		return nil, ErrQueryPermitQueueFull
	}
	*queue = append(*queue, waiter)
	coordinator.dispatchQueryPermitsLocked()
	queued := coordinator.waiterQueuedLocked(waiter, recovery)
	// Counted here, where both facts are known at once: this caller asked, and
	// this caller did or did not have to wait. Anything downstream reading the
	// two gauges instead sees the moment after it resolved.
	coordinator.permitAcquires++
	if recovery {
		coordinator.recoveryAcquires++
	} else {
		coordinator.normalRequests.Add(coordinator.now())
	}
	if queued {
		coordinator.permitQueued++
		// A formal query waits: no lookback read may be why.
		coordinator.yieldLookbackLocked()
	}
	snapshot := coordinator.queryPermitSnapshotLocked()
	coordinator.mu.Unlock()
	if queued {
		coordinator.observeQueryPermit(ctx, operation, observability.ResultStarted, true, snapshot)
		observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "waiting_P"})
	}

	timer := time.NewTimer(deadline.Sub(coordinator.now()))
	defer timer.Stop()
	select {
	case permit := <-waiter.grant:
		if permit == nil {
			coordinator.observeQueryPermit(ctx, operation, observability.ResultTimeout, true, coordinator.queryPermitSnapshot())
			return nil, context.DeadlineExceeded
		}
		if !deadline.After(coordinator.now()) {
			permit.Release()
			coordinator.observeQueryPermit(ctx, operation, observability.ResultTimeout, true, coordinator.queryPermitSnapshot())
			return nil, context.DeadlineExceeded
		}
		coordinator.observeQueryPermit(ctx, operation, observability.ResultSuccess, true, coordinator.queryPermitSnapshot())
		observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "P_acquired"})
		permit.waited = queued
		return permit, nil
	case <-ctx.Done():
		coordinator.mu.Lock()
		removed := coordinator.removeWaiterLocked(waiter, recovery)
		coordinator.dispatchQueryPermitsLocked()
		coordinator.mu.Unlock()
		if !removed {
			permit := <-waiter.grant
			if permit != nil {
				permit.Release()
			}
		}
		coordinator.observeQueryPermit(ctx, operation, observability.ResultPaused, true, coordinator.queryPermitSnapshot())
		return nil, ctx.Err()
	case <-timer.C:
		coordinator.mu.Lock()
		removed := coordinator.removeWaiterLocked(waiter, recovery)
		coordinator.dispatchQueryPermitsLocked()
		coordinator.mu.Unlock()
		if !removed {
			permit := <-waiter.grant
			if permit != nil {
				permit.Release()
			}
		}
		coordinator.observeQueryPermit(ctx, operation, observability.ResultTimeout, true, coordinator.queryPermitSnapshot())
		return nil, context.DeadlineExceeded
	}
}

func (coordinator *FlightCoordinator) queryPermitSnapshot() queryPermitSnapshot {
	if coordinator == nil {
		return queryPermitSnapshot{}
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return queryPermitSnapshot{Inflight: coordinator.queryInflight, RecoveryInflight: coordinator.recoveryInflight,
		NormalWaiting: len(coordinator.normalWaiters), RecoveryWaiting: len(coordinator.recoveryWaiters) + len(coordinator.recoveryChannelWaiters),
		NormalInflight: coordinator.inflightByOp[execution.OperationNormal],
		RetryInflight:  coordinator.inflightByOp[execution.OperationRetry],
		ReplayInflight: coordinator.inflightByOp[execution.OperationReplay],
		ProbeInflight:  coordinator.inflightByOp[execution.OperationProbe]}
}

func (coordinator *FlightCoordinator) queryPermitSnapshotLocked() queryPermitSnapshot {
	return queryPermitSnapshot{Inflight: coordinator.queryInflight, RecoveryInflight: coordinator.recoveryInflight,
		NormalWaiting: len(coordinator.normalWaiters), RecoveryWaiting: len(coordinator.recoveryWaiters) + len(coordinator.recoveryChannelWaiters),
		NormalInflight: coordinator.inflightByOp[execution.OperationNormal],
		RetryInflight:  coordinator.inflightByOp[execution.OperationRetry],
		ReplayInflight: coordinator.inflightByOp[execution.OperationReplay],
		ProbeInflight:  coordinator.inflightByOp[execution.OperationProbe]}
}

func (coordinator *FlightCoordinator) releaseQueryPermit(operation execution.Operation, permitID uint64) {
	coordinator.mu.Lock()
	if coordinator.queryInflight > 0 {
		coordinator.queryInflight--
	}

	if coordinator.inflightByOp[operation] > 0 {
		coordinator.inflightByOp[operation]--
	}
	if held, ok := coordinator.heldPermits[permitID]; ok {
		delete(coordinator.heldPermits, permitID)
		if elapsed := coordinator.now().Sub(held.since); elapsed > 0 && coordinator.permitSecondsByOp != nil {
			coordinator.permitSecondsByOp[held.operation] += elapsed.Seconds()
			coordinator.recordHoldLocked(held.operation, elapsed)
		}
	}
	coordinator.dispatchQueryPermitsLocked()
	snapshot := coordinator.queryPermitSnapshotLocked()
	coordinator.mu.Unlock()
	coordinator.observeQueryPermit(context.Background(), operation, observability.ResultSuccess, false, snapshot)
}

func (coordinator *FlightCoordinator) dispatchQueryPermitsLocked() {
	// The hold reference scans a day of hours; read it once, and only when a
	// recovery turn is actually contested by normal waiters.
	hold := time.Duration(-1)
	for coordinator.queryInflight < coordinator.limits.ProcessQueryPermits {
		coordinator.expireWaitersLocked(&coordinator.normalWaiters)
		coordinator.expireWaitersLocked(&coordinator.recoveryWaiters)
		canRecovery := len(coordinator.recoveryWaiters) > 0
		canNormal := len(coordinator.normalWaiters) > 0
		if !canRecovery && !canNormal {
			return
		}
		// Recovery takes every other permit only while no normal waiter is
		// about to miss its deadline. A replay burst (a handover's takeovers)
		// arriving in the last seconds of a Slot's permit window otherwise
		// halves the normal queue's drain and turns its tail away, though the
		// replays' own deadlines are far off.
		useRecovery := canRecovery && !canNormal
		if canRecovery && canNormal && coordinator.nextRecovery {
			if hold < 0 {
				hold = coordinator.holds.MeanReference(coordinator.now())
			}
			useRecovery = !coordinator.normalDueLocked(hold)
		}
		var waiter *queryPermitWaiter
		if useRecovery {
			waiter = coordinator.popFairWaiterLocked(&coordinator.recoveryWaiters, &coordinator.lastRecoveryQG)
			coordinator.nextRecovery = false
		} else {
			waiter = coordinator.popFairWaiterLocked(&coordinator.normalWaiters, &coordinator.lastNormalQG)
			if canRecovery {
				coordinator.nextRecovery = true
			}
		}
		coordinator.queryInflight++
		coordinator.heldPeak.Observe(coordinator.now(), coordinator.queryInflight)
		coordinator.permitSequence++
		permit := &QueryPermit{coordinator: coordinator, operation: waiter.operation, id: coordinator.permitSequence}
		coordinator.inflightByOp[waiter.operation]++
		if coordinator.heldPermits != nil {
			coordinator.heldPermits[permit.id] = heldPermit{operation: waiter.operation, since: coordinator.now()}
		}
		if useRecovery {
			permit.recovery = &execution.RecoveryPermit{PermitID: fmt.Sprintf("query-recovery-%d", coordinator.permitSequence),
				Slot: waiter.slot, Operation: waiter.operation, ExpiresAtUnixMilli: waiter.deadline.UnixMilli()}
		}
		waiter.grant <- permit
	}
}

// normalDueLocked reports whether a queued normal waiter's deadline falls
// within the time the normal queue needs to drain with every permit: one hold
// per ProcessQueryPermits waiters ahead, plus the hold of its own. hold is the
// normal permit hold when the query service is not under pressure
// (HoldReference); zero before any hold, when no waiter is due and recovery
// keeps its turn.
func (coordinator *FlightCoordinator) normalDueLocked(hold time.Duration) bool {
	if hold <= 0 || len(coordinator.normalWaiters) == 0 || coordinator.limits.ProcessQueryPermits <= 0 {
		return false
	}
	rounds := 1 + len(coordinator.normalWaiters)/coordinator.limits.ProcessQueryPermits
	due := coordinator.now().Add(time.Duration(rounds) * hold)
	for _, waiter := range coordinator.normalWaiters {
		if !waiter.deadline.After(due) {
			return true
		}
	}
	return false
}

func (coordinator *FlightCoordinator) waiterQueuedLocked(waiter *queryPermitWaiter, recovery bool) bool {
	queue := coordinator.normalWaiters
	if recovery {
		queue = coordinator.recoveryWaiters
	}
	for _, candidate := range queue {
		if candidate == waiter {
			return true
		}
	}
	return false
}

func (coordinator *FlightCoordinator) observeQueryPermit(
	ctx context.Context,
	operation execution.Operation,
	result observability.Result,
	admission bool,
	snapshot queryPermitSnapshot,
) {
	// A permit not granted names why, as the query that needed it does.
	reason := observability.ReasonNone
	switch result {
	case observability.Result(observability.ResultTimeout):
		reason = permitDeadline
	case observability.ResultPaused:
		reason = permitCancelled
	}
	coordinator.observeQueryPermitReason(ctx, operation, result, reason, admission, snapshot)
}

// The words a permit that was not granted names. The deadline is the same
// word the query that needed it carries.
const (
	permitDeadline    observability.ReasonCode = "QUERY_PERMIT_DEADLINE"
	permitQueueFull   observability.ReasonCode = "QUERY_PERMIT_QUEUE_FULL"
	permitRecoveryOff observability.ReasonCode = "QUERY_PERMIT_RECOVERY_OFF"
	permitCancelled   observability.ReasonCode = "QUERY_PERMIT_CANCELLED"
)

func (coordinator *FlightCoordinator) observeQueryPermitReason(
	ctx context.Context,
	operation execution.Operation,
	result observability.Result,
	reason observability.ReasonCode,
	admission bool,
	snapshot queryPermitSnapshot,
) {
	if coordinator == nil || coordinator.observer == nil {
		return
	}
	queueKind := observability.QueryQueueRecovery
	if operation == execution.OperationNormal {
		queueKind = observability.QueryQueueNormal
	}
	defer func() { _ = recover() }()
	coordinator.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryAdmission,
		Result: result, ReasonCode: reason, Operation: observability.Operation(operation),
		QueryPermit: &observability.QueryPermitFacts{
			QueueKind: queueKind, Admission: admission,
			NormalWaiting: snapshot.NormalWaiting, RecoveryWaiting: snapshot.RecoveryWaiting,
			NormalInflight: snapshot.NormalInflight, RetryInflight: snapshot.RetryInflight,
			ReplayInflight: snapshot.ReplayInflight, ProbeInflight: snapshot.ProbeInflight,
			RecoveryInflight: snapshot.RecoveryInflight,
		},
	})
}

func queuedForQueryGroup(queue []*queryPermitWaiter, queryGroup execution.QueryGroupIdentity) int {
	count := 0
	for _, waiter := range queue {
		if waiter.slot.QueryGroup == queryGroup {
			count++
		}
	}
	return count
}

func (coordinator *FlightCoordinator) expireWaitersLocked(queue *[]*queryPermitWaiter) {
	now := coordinator.now()
	kept := (*queue)[:0]
	for _, waiter := range *queue {
		if !waiter.deadline.After(now) {
			waiter.grant <- nil
			continue
		}
		kept = append(kept, waiter)
	}
	*queue = kept
}

func (coordinator *FlightCoordinator) popFairWaiterLocked(
	queue *[]*queryPermitWaiter,
	last *execution.QueryGroupIdentity,
) *queryPermitWaiter {
	// Each QG contributes its earliest-deadline query to a stable round. The
	// selected QG then moves behind its healthy siblings for the next grant.
	firstByGroup := make(map[execution.QueryGroupIdentity]int)
	groups := make([]execution.QueryGroupIdentity, 0, len(*queue))
	for index, waiter := range *queue {
		queryGroup := waiter.slot.QueryGroup
		candidate, exists := firstByGroup[queryGroup]
		if !exists {
			firstByGroup[queryGroup] = index
			groups = append(groups, queryGroup)
			continue
		}
		if waiter.deadline.Before((*queue)[candidate].deadline) {
			firstByGroup[queryGroup] = index
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		left := (*queue)[firstByGroup[groups[i]]]
		right := (*queue)[firstByGroup[groups[j]]]
		if left.deadline.Equal(right.deadline) {
			return groups[i] < groups[j]
		}
		return left.deadline.Before(right.deadline)
	})
	selectedGroup := groups[0]
	for index, queryGroup := range groups {
		if queryGroup == *last {
			selectedGroup = groups[(index+1)%len(groups)]
			break
		}
	}
	selected := firstByGroup[selectedGroup]
	waiter := (*queue)[selected]
	copy((*queue)[selected:], (*queue)[selected+1:])
	*queue = (*queue)[:len(*queue)-1]
	*last = selectedGroup
	return waiter
}

func (coordinator *FlightCoordinator) removeWaiterLocked(waiter *queryPermitWaiter, recovery bool) bool {
	queue := &coordinator.normalWaiters
	if recovery {
		queue = &coordinator.recoveryWaiters
	}
	for index, candidate := range *queue {
		if candidate == waiter {
			copy((*queue)[index:], (*queue)[index+1:])
			*queue = (*queue)[:len(*queue)-1]
			return true
		}
	}
	return false
}
