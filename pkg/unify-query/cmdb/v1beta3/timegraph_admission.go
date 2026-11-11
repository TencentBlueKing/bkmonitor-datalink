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
// 期限和取消约束，不因排队人数或内存估算被拒绝。每个等待者有自己的唤醒通道，
// 执行槽空出时只唤醒下一个可执行的等待者，不广播给所有等待者。
var topologyAdmission struct {
	sync.Mutex
	active  int
	waiters []*topologyWaiter
	// wakeups 统计等待者被唤醒的次数，供测试确认没有广播式唤醒。
	wakeups int
}

type topologyAdmissionKey struct{}

type topologyLease struct {
	released bool
}

// topologyWaiter 是一个排队位置。ready 表示排队准备（读取请求体）已完成，可以接收执行槽；
// granted 表示执行槽已经分配给它并计入 active，wake 只在分配时发送一次。
type topologyWaiter struct {
	ready   bool
	granted bool
	wake    chan struct{}
}

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
	if err = waitTopologySlotLocked(ctx, span, prepare); err != nil {
		lease.released = true
		return ctx, nil, err
	}
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
			grantTopologySlotsLocked()
			releaseSpan.Set("admission-active", topologyAdmission.active)
			metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		})
	}
	return ctx, release, nil
}

func topologyAdmissionLimit() int { return effectiveTimeGraphLimit(MaxSharedTopologyConcurrent, 8) }

// firstReadyTopologyWaiterLocked 返回最早到达且已完成准备的等待者下标；仍在读取请求体的
// 等待者保留排队位置，但不占用空闲执行槽。
func firstReadyTopologyWaiterLocked() int {
	for index, waiter := range topologyAdmission.waiters {
		if waiter.ready {
			return index
		}
	}
	return -1
}

// grantTopologySlotsLocked 把空闲执行槽依次分配给最早就绪的等待者：执行槽在这里计入 active，
// 只唤醒拿到执行槽的等待者。
func grantTopologySlotsLocked() {
	limit := topologyAdmissionLimit()
	for topologyAdmission.active < limit {
		index := firstReadyTopologyWaiterLocked()
		if index < 0 {
			break
		}
		waiter := topologyAdmission.waiters[index]
		removeTopologyWaiterAtLocked(index)
		waiter.granted = true
		topologyAdmission.active++
		waiter.wake <- struct{}{}
	}
	metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
}

func removeTopologyWaiterAtLocked(index int) {
	waiters := topologyAdmission.waiters
	copy(waiters[index:], waiters[index+1:])
	waiters[len(waiters)-1] = nil
	topologyAdmission.waiters = waiters[:len(waiters)-1]
}

func removeTopologyWaiterLocked(target *topologyWaiter) {
	for index, waiter := range topologyAdmission.waiters {
		if waiter == target {
			removeTopologyWaiterAtLocked(index)
			metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
			return
		}
	}
}

// waitTopologySlotLocked 在持锁时进入并返回；成功返回时本请求已占用一个执行槽。有空闲执行槽且
// 没有已就绪的等待者排在前面时直接执行；否则排队，排队后在锁外完成准备。就绪等待者按到达顺序
// 接收执行槽；队列不设长度，等待者只因执行槽分配、自己的期限或取消离开队列。准入之后的资源
// 增长不在这里等待，排队与执行共用原请求期限。
func waitTopologySlotLocked(ctx context.Context, span *trace.Span, prepare func(context.Context) error) error {
	started := time.Now()
	defer func() { span.Set("admission-wait-seconds", time.Since(started).Seconds()) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if topologyAdmission.active < topologyAdmissionLimit() && firstReadyTopologyWaiterLocked() < 0 {
		topologyAdmission.active++
		return nil
	}
	waiter := &topologyWaiter{ready: prepare == nil, wake: make(chan struct{}, 1)}
	topologyAdmission.waiters = append(topologyAdmission.waiters, waiter)
	metric.CMDBTopologyWaitersSet(len(topologyAdmission.waiters))
	if prepare != nil {
		// 先取得排队位置再读取请求体；读取期间其他请求可以释放、取消或取得执行槽。
		topologyAdmission.Unlock()
		err := prepare(ctx)
		topologyAdmission.Lock()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			// 准备中的等待者不会被分配执行槽，直接离开队列。
			removeTopologyWaiterLocked(waiter)
			return err
		}
		waiter.ready = true
		// 准备期间可能已有执行槽空出。
		grantTopologySlotsLocked()
	}
	for !waiter.granted {
		topologyAdmission.Unlock()
		select {
		case <-ctx.Done():
		case <-waiter.wake:
		}
		topologyAdmission.Lock()
		topologyAdmission.wakeups++
		if !waiter.granted {
			removeTopologyWaiterLocked(waiter)
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		// 取消与分配同时发生：交还执行槽，由下一个就绪等待者使用。
		topologyAdmission.active--
		grantTopologySlotsLocked()
		return err
	}
	return nil
}
