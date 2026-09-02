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
	MaxReplaySlots        uint32
	MaxReplayAge          time.Duration
	RetryMinDelay         time.Duration
	RetryMaxDelay         time.Duration
}

func (limits RecoveryLimits) Validate() error {
	if limits.ProcessQueryPermits <= 0 || limits.RecoveryQueryPermits < 0 ||
		limits.RecoveryQueryPermits >= limits.ProcessQueryPermits ||
		limits.ReadyQueueCapacity <= 0 || limits.RecoveryQueueCapacity <= 0 ||
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
) (execution.Operation, bool, error) {
	if !runner.flights.recoveryEnabled {
		return slot.Dispatch.Operation, true, nil
	}
	if slot.Recovery.Disposition == ReplayExpired {
		runner.attempt = nil
		return execution.OperationNormal, true, nil
	}
	if runner.attempt == nil {
		return slot.Dispatch.Operation, true, nil
	}
	if runner.attempt.contract != slot.Contract {
		runner.attempt = nil
		return slot.Dispatch.Operation, true, nil
	}
	if at.Before(runner.attempt.nextAt) {
		return "", false, nil
	}
	return runner.attempt.next, true, nil
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

type QueryPermitSnapshot struct {
	Inflight         int
	RecoveryInflight int
	NormalWaiting    int
	RecoveryWaiting  int
}

type QueryPermit struct {
	coordinator *FlightCoordinator
	recovery    *execution.RecoveryPermit
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
	permit.once.Do(func() { permit.coordinator.releaseQueryPermit(permit.recovery != nil) })
}

func (coordinator *FlightCoordinator) AcquireQueryPermit(
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
		return nil, err
	}
	if !deadline.After(coordinator.now()) {
		return nil, context.DeadlineExceeded
	}
	waiter := &queryPermitWaiter{slot: slot, operation: operation, deadline: deadline, grant: make(chan *QueryPermit, 1)}
	recovery := operation != execution.OperationNormal
	if recovery && coordinator.limits.RecoveryQueryPermits == 0 {
		return nil, ErrRecoveryPermitsOff
	}
	coordinator.mu.Lock()
	queue := &coordinator.normalWaiters
	capacity := coordinator.limits.ReadyQueueCapacity
	if recovery {
		queue = &coordinator.recoveryWaiters
		capacity = coordinator.limits.RecoveryQueueCapacity
	}
	if len(*queue) >= capacity {
		coordinator.mu.Unlock()
		return nil, ErrQueryPermitQueueFull
	}
	*queue = append(*queue, waiter)
	coordinator.dispatchQueryPermitsLocked()
	coordinator.mu.Unlock()

	select {
	case permit := <-waiter.grant:
		return permit, nil
	case <-ctx.Done():
		coordinator.mu.Lock()
		removed := coordinator.removeWaiterLocked(waiter, recovery)
		coordinator.dispatchQueryPermitsLocked()
		coordinator.mu.Unlock()
		if !removed {
			permit := <-waiter.grant
			permit.Release()
		}
		return nil, ctx.Err()
	}
}

func (coordinator *FlightCoordinator) QueryPermitSnapshot() QueryPermitSnapshot {
	if coordinator == nil {
		return QueryPermitSnapshot{}
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return QueryPermitSnapshot{Inflight: coordinator.queryInflight, RecoveryInflight: coordinator.recoveryInflight,
		NormalWaiting: len(coordinator.normalWaiters), RecoveryWaiting: len(coordinator.recoveryWaiters)}
}

func (coordinator *FlightCoordinator) releaseQueryPermit(recovery bool) {
	coordinator.mu.Lock()
	if coordinator.queryInflight > 0 {
		coordinator.queryInflight--
	}
	if recovery && coordinator.recoveryInflight > 0 {
		coordinator.recoveryInflight--
	}
	coordinator.dispatchQueryPermitsLocked()
	coordinator.mu.Unlock()
}

func (coordinator *FlightCoordinator) dispatchQueryPermitsLocked() {
	for coordinator.queryInflight < coordinator.limits.ProcessQueryPermits {
		canRecovery := len(coordinator.recoveryWaiters) > 0 &&
			coordinator.recoveryInflight < coordinator.limits.RecoveryQueryPermits
		canNormal := len(coordinator.normalWaiters) > 0
		if !canRecovery && !canNormal {
			return
		}
		useRecovery := canRecovery && (!canNormal || coordinator.nextRecovery)
		var waiter *queryPermitWaiter
		if useRecovery {
			waiter = coordinator.recoveryWaiters[0]
			coordinator.recoveryWaiters = coordinator.recoveryWaiters[1:]
			coordinator.recoveryInflight++
			coordinator.nextRecovery = false
		} else {
			waiter = coordinator.normalWaiters[0]
			coordinator.normalWaiters = coordinator.normalWaiters[1:]
			if canRecovery {
				coordinator.nextRecovery = true
			}
		}
		coordinator.queryInflight++
		coordinator.permitSequence++
		permit := &QueryPermit{coordinator: coordinator}
		if useRecovery {
			permit.recovery = &execution.RecoveryPermit{PermitID: fmt.Sprintf("query-recovery-%d", coordinator.permitSequence),
				Slot: waiter.slot, Operation: waiter.operation, ExpiresAtUnixMilli: waiter.deadline.UnixMilli()}
		}
		waiter.grant <- permit
	}
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
