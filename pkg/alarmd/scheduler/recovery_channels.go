package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// RecoveryChannels reserves query concurrency, not a complete Runner. It is
// released after all query bodies/consumers join, before evaluation or commit.
type RecoveryChannels struct {
	coordinator *FlightCoordinator
	slot        execution.SlotIdentity
	operation   execution.Operation
	deadline    time.Time
	available   chan struct{}
	count       int
	once        sync.Once
}

type recoveryChannelWaiter struct {
	slot      execution.SlotIdentity
	operation execution.Operation
	deadline  time.Time
	maximum   int
	grant     chan *RecoveryChannels
}

func (coordinator *FlightCoordinator) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time) (*QueryPermit, error) {
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
	if operation == execution.OperationNormal {
		return coordinator.acquireProcessQueryPermit(ctx, slot, operation, deadline)
	}
	channels, err := coordinator.acquireRecoveryChannels(ctx, slot, operation, deadline, 1)
	if err != nil {
		return nil, err
	}
	permit, err := channels.acquireQueryPermit(ctx, slot, operation, deadline)
	if err != nil {
		channels.Release()
		return nil, err
	}
	release := permit.releaseChannel
	permit.releaseChannel = func() { release(); channels.Release() }
	return permit, nil
}

func (coordinator *FlightCoordinator) AcquireRecoveryChannels(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time, maximum int, beforeWait ...func()) (*RecoveryChannels, error) {
	started := time.Now()
	defer func() {
		if coordinator == nil || coordinator.observer == nil {
			return
		}
		defer func() { _ = recover() }()
		coordinator.observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageQueryPermitWait, Result: observability.ResultTerminal,
			Duration: time.Since(started), PermitWait: &observability.PermitWaitFacts{Recovery: true},
		})
	}()
	return coordinator.acquireRecoveryChannels(ctx, slot, operation, deadline, maximum, beforeWait...)
}

func (coordinator *FlightCoordinator) acquireRecoveryChannels(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time, maximum int, beforeWait ...func()) (*RecoveryChannels, error) {
	if coordinator == nil || !coordinator.recoveryEnabled {
		return nil, ErrRecoveryLimitsInvalid
	}
	if slot.QueryGroup == "" || slot.EvaluationTime <= 0 || operation.Validate() != nil || operation == execution.OperationNormal || maximum <= 0 {
		return nil, errors.New("alarmd scheduler: invalid recovery channel request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !deadline.After(coordinator.now()) {
		return nil, context.DeadlineExceeded
	}
	if coordinator.limits.RecoveryQueryPermits == 0 {
		return nil, ErrRecoveryPermitsOff
	}
	waiter := &recoveryChannelWaiter{slot: slot, operation: operation, deadline: deadline, maximum: maximum, grant: make(chan *RecoveryChannels, 1)}
	coordinator.mu.Lock()
	perQG := 0
	for _, w := range coordinator.recoveryChannelWaiters {
		if w.slot.QueryGroup == slot.QueryGroup {
			perQG++
		}
	}
	if len(coordinator.recoveryChannelWaiters) >= coordinator.limits.RecoveryQueueCapacity || perQG >= coordinator.limits.MaxQueuedItemsPerQG {
		coordinator.mu.Unlock()
		return nil, ErrQueryPermitQueueFull
	}
	coordinator.recoveryChannelWaiters = append(coordinator.recoveryChannelWaiters, waiter)
	coordinator.dispatchRecoveryChannelsLocked()
	waiting := false
	for _, candidate := range coordinator.recoveryChannelWaiters {
		if candidate == waiter {
			waiting = true
			break
		}
	}
	snapshot := coordinator.queryPermitSnapshotLocked()
	coordinator.mu.Unlock()
	if waiting {
		for _, release := range beforeWait {
			if release != nil {
				release()
			}
		}
		observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "waiting_R"})
	}
	coordinator.observeQueryPermit(ctx, operation, observability.ResultStarted, false, snapshot)
	timer := time.NewTimer(deadline.Sub(coordinator.now()))
	defer timer.Stop()
	var err error
	select {
	case channels := <-waiter.grant:
		if channels == nil {
			coordinator.observeQueryPermit(ctx, operation, observability.ResultTimeout, false, coordinator.queryPermitSnapshot())
			return nil, context.DeadlineExceeded
		}
		if ctx.Err() != nil {
			channels.Release()
			return nil, ctx.Err()
		}
		if !deadline.After(coordinator.now()) {
			channels.Release()
			return nil, context.DeadlineExceeded
		}
		coordinator.observeQueryPermit(ctx, operation, observability.ResultSuccess, false, coordinator.queryPermitSnapshot())
		observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "R_acquired"})
		return channels, nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = context.DeadlineExceeded
	}
	coordinator.mu.Lock()
	removed := false
	for i, w := range coordinator.recoveryChannelWaiters {
		if w == waiter {
			coordinator.recoveryChannelWaiters = append(coordinator.recoveryChannelWaiters[:i], coordinator.recoveryChannelWaiters[i+1:]...)
			removed = true
			break
		}
	}
	coordinator.dispatchRecoveryChannelsLocked()
	coordinator.mu.Unlock()
	if !removed {
		if channels := <-waiter.grant; channels != nil {
			channels.Release()
		}
	}
	result := observability.Result(observability.ResultPaused)
	decision := "R_wait_cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		result = observability.ResultTimeout
		decision = "R_wait_timeout"
	}
	coordinator.observeQueryPermit(ctx, operation, result, false, coordinator.queryPermitSnapshot())
	observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: decision})
	return nil, err
}

func (coordinator *FlightCoordinator) dispatchRecoveryChannelsLocked() {
	for len(coordinator.recoveryChannelWaiters) > 0 && coordinator.recoveryInflight < coordinator.limits.RecoveryQueryPermits {
		w := coordinator.recoveryChannelWaiters[0]
		coordinator.recoveryChannelWaiters = coordinator.recoveryChannelWaiters[1:]
		if !w.deadline.After(coordinator.now()) {
			w.grant <- nil
			continue
		}
		count := min(w.maximum, coordinator.limits.RecoveryQueryPermits-coordinator.recoveryInflight)
		c := &RecoveryChannels{coordinator: coordinator, slot: w.slot, operation: w.operation, deadline: w.deadline, count: count, available: make(chan struct{}, count)}
		for range count {
			c.available <- struct{}{}
		}
		coordinator.recoveryInflight += count
		w.grant <- c
	}
}

func (channels *RecoveryChannels) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time) (*QueryPermit, error) {
	if channels == nil {
		return nil, errors.New("alarmd scheduler: nil recovery channels")
	}
	coordinator := channels.coordinator
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
	return channels.acquireQueryPermit(ctx, slot, operation, deadline)
}

func (channels *RecoveryChannels) acquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time) (*QueryPermit, error) {
	if channels == nil || slot != channels.slot || operation != channels.operation || deadline.After(channels.deadline) {
		return nil, errors.New("alarmd scheduler: recovery channel identity or deadline changed")
	}
	timer := time.NewTimer(deadline.Sub(channels.coordinator.now()))
	defer timer.Stop()
	select {
	case <-channels.available:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
	permit, err := channels.coordinator.acquireProcessQueryPermit(ctx, slot, operation, deadline)
	if err != nil {
		channels.available <- struct{}{}
		return nil, err
	}
	permit.releaseChannel = func() { channels.available <- struct{}{} }
	return permit, nil
}

func (channels *RecoveryChannels) Release() {
	if channels == nil {
		return
	}
	channels.once.Do(func() {
		c := channels.coordinator
		c.mu.Lock()
		c.recoveryInflight -= channels.count
		c.dispatchRecoveryChannelsLocked()
		snapshot := c.queryPermitSnapshotLocked()
		c.mu.Unlock()
		c.observeQueryPermit(context.Background(), channels.operation, observability.ResultSuccess, false, snapshot)
	})
}
