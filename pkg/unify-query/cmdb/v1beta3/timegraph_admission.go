// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// topologyAdmission 是进程内的执行槽。执行中的请求数不超过
// max_shared_topology_concurrent_requests；其余请求按到达顺序等待，只受各自的
// 期限和取消约束，不因排队人数或内存估算被拒绝。
var topologyAdmission struct {
	sync.Mutex
	active  int
	waiters []*topologyWaiter
	changed chan struct{}
}

type topologyAdmissionKey struct{}

type topologyLease struct {
	released bool
}

type topologyWaiter struct{ _ byte }

// AcquireSharedTopology covers the complete HTTP response lifetime. A request
// waits for an execution slot within its own deadline; nested calls reuse the
// admitted lease.
func AcquireSharedTopology(ctx context.Context) (admittedCtx context.Context, releaseFn func(), err error) {
	return acquireSharedTopology(ctx, nil)
}

// AcquireSharedTopologyWithQueuedPreparation lets HTTP drain its bounded body
// after obtaining a FIFO queue position, so net/http can observe peer closure.
// Preparation is called only when queued and runs without the admission lock.
// It must not execute model/graph work.
func AcquireSharedTopologyWithQueuedPreparation(ctx context.Context, prepare func(context.Context) error) (context.Context, func(), error) {
	return acquireSharedTopology(ctx, prepare)
}

func acquireSharedTopology(ctx context.Context, prepare func(context.Context) error) (admittedCtx context.Context, releaseFn func(), err error) {
	started := time.Now()
	_, span := trace.NewSpan(ctx, "timegraph-admission")
	defer finishTimeGraphStage(ctx, span, "admission", started, &err)
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if admitted, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease); admitted != nil {
		topologyAdmission.Lock()
		released := admitted.released
		topologyAdmission.Unlock()
		if released {
			return ctx, nil, context.Canceled
		}
		span.Set("admission-reused", true)
		return ctx, func() {}, nil
	}
	lease := &topologyLease{}
	// Waiting shares the model deadline; admission cannot restart its clock.
	ctx, cancel := context.WithTimeout(ctx, timeGraphQueryTimeout)
	ctx = context.WithValue(ctx, topologyAdmissionKey{}, lease)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	defer func() {
		metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		span.Set("admission-active", topologyAdmission.active)
		span.Set("admission-waiters", len(topologyAdmission.waiters))
	}()
	limit := effectiveTimeGraphLimit(MaxSharedTopologyConcurrent, 8)
	if err = waitTopologySlotLocked(ctx, limit, span, prepare); err != nil {
		lease.released = true
		return ctx, nil, err
	}
	topologyAdmission.active++
	notifyTopologyWaitersLocked()
	var once sync.Once
	release := func() {
		once.Do(func() {
			defer cancel()
			_, releaseSpan := trace.NewSpan(ctx, "timegraph-release-admission")
			var releaseErr error
			defer finishTimeGraphStage(ctx, releaseSpan, "admission-release", time.Now(), &releaseErr)
			topologyAdmission.Lock()
			defer topologyAdmission.Unlock()
			topologyAdmission.active--
			lease.released = true
			notifyTopologyWaitersLocked()
			releaseSpan.Set("admission-active", topologyAdmission.active)
			metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		})
	}
	return ctx, release, nil
}

// notifyTopologyWaitersLocked broadcasts state changes. Waiters recheck the
// execution count and FIFO head under the same mutex before obtaining a lease.
func notifyTopologyWaitersLocked() {
	metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
	if topologyAdmission.changed != nil {
		close(topologyAdmission.changed)
	}
	topologyAdmission.changed = make(chan struct{})
}

func removeTopologyWaiterLocked(target *topologyWaiter) {
	for i, w := range topologyAdmission.waiters {
		if w != target {
			continue
		}
		copy(topologyAdmission.waiters[i:], topologyAdmission.waiters[i+1:])
		topologyAdmission.waiters[len(topologyAdmission.waiters)-1] = nil
		topologyAdmission.waiters = topologyAdmission.waiters[:len(topologyAdmission.waiters)-1]
		metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
		return
	}
}

// waitTopologySlotLocked returns with the lock held. Waiters are admitted in
// FIFO order; the queue has no length cap, so a waiter leaves it only when a
// slot opens or its own deadline/cancellation fires. Resource growth after
// admission never waits here; queued requests share the original deadline.
func waitTopologySlotLocked(ctx context.Context, limit int, span *trace.Span, prepare func(context.Context) error) error {
	var waiter *topologyWaiter
	started := time.Now()
	defer func() { span.Set("admission-wait-seconds", time.Since(started).Seconds()) }()
	for {
		if err := ctx.Err(); err != nil {
			if waiter != nil {
				removeTopologyWaiterLocked(waiter)
				notifyTopologyWaitersLocked()
			}
			return err
		}
		turn := len(topologyAdmission.waiters) == 0 || waiter != nil && topologyAdmission.waiters[0] == waiter
		if topologyAdmission.active < limit && turn {
			if waiter != nil {
				removeTopologyWaiterLocked(waiter)
			}
			return nil
		}
		if waiter == nil {
			waiter = &topologyWaiter{}
			topologyAdmission.waiters = append(topologyAdmission.waiters, waiter)
			metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
			if prepare != nil {
				// The queue position is taken before reading any body. Other
				// requests can release/cancel/acquire the mutex while this I/O runs.
				topologyAdmission.Unlock()
				err := prepare(ctx)
				topologyAdmission.Lock()
				if err != nil {
					removeTopologyWaiterLocked(waiter)
					notifyTopologyWaitersLocked()
					return err
				}
				// Slots may have opened during preparation; recheck immediately.
				continue
			}
		}
		if topologyAdmission.changed == nil {
			topologyAdmission.changed = make(chan struct{})
		}
		changed := topologyAdmission.changed
		topologyAdmission.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		topologyAdmission.Lock()
	}
}
