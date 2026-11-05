// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type topologyWaiter struct{ _ byte }

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

// waitTopologySlotLocked returns with the lock held. The FIFO queue is bounded
// to one pending wave (the execution limit). Resource growth after admission
// never waits here; queued requests share the original request deadline.
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
			if len(topologyAdmission.waiters) >= limit {
				return &ResultLimitError{Reason: "max_topology_admission_waiters", Count: len(topologyAdmission.waiters) + 1, Limit: limit}
			}
			waiter = &topologyWaiter{}
			topologyAdmission.waiters = append(topologyAdmission.waiters, waiter)
			metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
			if prepare != nil {
				// The queue position is bounded before reading any body. Other
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
