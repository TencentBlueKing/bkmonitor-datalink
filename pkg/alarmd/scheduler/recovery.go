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
	ErrQueryPermitQueueFull  = errors.New("alarmd scheduler: query permit queue is full")
	ErrRecoveryPermitsOff    = errors.New("alarmd scheduler: recovery query permits are disabled")
)

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
}

func (limits RecoveryLimits) Validate() error {
	if limits.ProcessQueryPermits <= 0 || limits.RecoveryQueryPermits < 0 ||
		limits.RecoveryQueryPermits >= limits.ProcessQueryPermits ||
		limits.ReadyQueueCapacity <= 0 || limits.RecoveryQueueCapacity <= 0 ||
		limits.MaxQueuedItemsPerQG <= 0 || limits.MaxQueuedItemsPerQG >= limits.ReadyQueueCapacity ||
		limits.MaxQueuedItemsPerQG >= limits.RecoveryQueueCapacity ||
		limits.MaxReplaySlots == 0 || limits.MaxReplayAge <= 0 ||
		limits.RetryMinDelay <= 0 || limits.RetryMaxDelay < limits.RetryMinDelay {
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

type SlotRecoveryFacts struct {
	Disposition ReplayDisposition
	Distance    uint32
	Age         time.Duration
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
	default:
		return errors.New("alarmd scheduler: unknown replay disposition")
	}
	return nil
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
	if slot.Recovery.Disposition == ReplayExpired {
		runner.attempt = nil
		return execution.OperationNormal, true
	}
	if runner.attempt == nil {
		return slot.Dispatch.Operation, true
	}
	if runner.attempt.contract != slot.Contract {
		runner.attempt = nil
		return slot.Dispatch.Operation, true
	}
	if at.Before(runner.attempt.nextAt) {
		return "", false
	}
	return runner.attempt.next, true
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
	coordinator *FlightCoordinator
	recovery    *execution.RecoveryPermit
	operation   execution.Operation
	once        sync.Once
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
	permit.once.Do(func() { permit.coordinator.releaseQueryPermit(permit.operation) })
}

func (coordinator *FlightCoordinator) AcquireQueryPermit(
	ctx context.Context,
	slot execution.SlotIdentity,
	operation execution.Operation,
	deadline time.Time,
) (*QueryPermit, error) {

	started := time.Now()
	defer func() {
		if coordinator == nil || coordinator.observer == nil {
			return
		}
		defer func() { _ = recover() }()
		coordinator.observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageQueryPermitWait, Result: observability.ResultTerminal,
			Duration: time.Since(started), PermitWait: &observability.PermitWaitFacts{Recovery: operation != execution.OperationNormal},
		})
	}()
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
	recovery := operation != execution.OperationNormal
	if recovery && coordinator.limits.RecoveryQueryPermits == 0 {
		coordinator.observeQueryPermit(ctx, operation, observability.ResultFailed, true, coordinator.queryPermitSnapshot())
		return nil, ErrRecoveryPermitsOff
	}
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
		coordinator.observeQueryPermit(ctx, operation, observability.ResultFailed, true, coordinator.queryPermitSnapshot())
		return nil, ErrQueryPermitQueueFull
	}
	*queue = append(*queue, waiter)
	coordinator.dispatchQueryPermitsLocked()
	queued := coordinator.waiterQueuedLocked(waiter, recovery)
	snapshot := coordinator.queryPermitSnapshotLocked()
	coordinator.mu.Unlock()
	if queued {
		coordinator.observeQueryPermit(ctx, operation, observability.ResultStarted, true, snapshot)
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
		NormalWaiting: len(coordinator.normalWaiters), RecoveryWaiting: len(coordinator.recoveryWaiters),
		NormalInflight: coordinator.inflightByOp[execution.OperationNormal],
		RetryInflight:  coordinator.inflightByOp[execution.OperationRetry],
		ReplayInflight: coordinator.inflightByOp[execution.OperationReplay],
		ProbeInflight:  coordinator.inflightByOp[execution.OperationProbe]}
}

func (coordinator *FlightCoordinator) queryPermitSnapshotLocked() queryPermitSnapshot {
	return queryPermitSnapshot{Inflight: coordinator.queryInflight, RecoveryInflight: coordinator.recoveryInflight,
		NormalWaiting: len(coordinator.normalWaiters), RecoveryWaiting: len(coordinator.recoveryWaiters),
		NormalInflight: coordinator.inflightByOp[execution.OperationNormal],
		RetryInflight:  coordinator.inflightByOp[execution.OperationRetry],
		ReplayInflight: coordinator.inflightByOp[execution.OperationReplay],
		ProbeInflight:  coordinator.inflightByOp[execution.OperationProbe]}
}

func (coordinator *FlightCoordinator) releaseQueryPermit(operation execution.Operation) {
	coordinator.mu.Lock()
	if coordinator.queryInflight > 0 {
		coordinator.queryInflight--
	}
	if operation != execution.OperationNormal && coordinator.recoveryInflight > 0 {
		coordinator.recoveryInflight--
	}
	if coordinator.inflightByOp[operation] > 0 {
		coordinator.inflightByOp[operation]--
	}
	coordinator.dispatchQueryPermitsLocked()
	snapshot := coordinator.queryPermitSnapshotLocked()
	coordinator.mu.Unlock()
	coordinator.observeQueryPermit(context.Background(), operation, observability.ResultSuccess, false, snapshot)
}

func (coordinator *FlightCoordinator) dispatchQueryPermitsLocked() {
	for coordinator.queryInflight < coordinator.limits.ProcessQueryPermits {
		coordinator.expireWaitersLocked(&coordinator.normalWaiters)
		coordinator.expireWaitersLocked(&coordinator.recoveryWaiters)
		canRecovery := len(coordinator.recoveryWaiters) > 0 &&
			coordinator.recoveryInflight < coordinator.limits.RecoveryQueryPermits
		canNormal := len(coordinator.normalWaiters) > 0
		if !canRecovery && !canNormal {
			return
		}
		useRecovery := canRecovery && (!canNormal || coordinator.nextRecovery)
		var waiter *queryPermitWaiter
		if useRecovery {
			waiter = coordinator.popFairWaiterLocked(&coordinator.recoveryWaiters, &coordinator.lastRecoveryQG)
			coordinator.recoveryInflight++
			coordinator.nextRecovery = false
		} else {
			waiter = coordinator.popFairWaiterLocked(&coordinator.normalWaiters, &coordinator.lastNormalQG)
			if canRecovery {
				coordinator.nextRecovery = true
			}
		}
		coordinator.queryInflight++
		coordinator.permitSequence++
		permit := &QueryPermit{coordinator: coordinator, operation: waiter.operation}
		coordinator.inflightByOp[waiter.operation]++
		if useRecovery {
			permit.recovery = &execution.RecoveryPermit{PermitID: fmt.Sprintf("query-recovery-%d", coordinator.permitSequence),
				Slot: waiter.slot, Operation: waiter.operation, ExpiresAtUnixMilli: waiter.deadline.UnixMilli()}
		}
		waiter.grant <- permit
	}
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
		Result: result, Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
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
