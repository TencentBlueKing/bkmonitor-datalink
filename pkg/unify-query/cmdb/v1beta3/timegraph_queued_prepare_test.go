// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTopologyAdmissionQueuedPreparation(t *testing.T) {
	t.Run("available_slot_preserves_existing_order", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error {
			t.Fatal("immediate admission must not prepare in queue")
			return nil
		})
		require.NoError(t, err)
		release()
	})
	t.Run("queued_preparation_and_cancel_cleans", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		prepared := make(chan struct{})
		done := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(parent, func(context.Context) error {
				close(prepared)
				return nil
			})
			done <- topologyAdmissionTestResult{c, r, e}
		}()
		select {
		case <-prepared:
		case <-time.After(time.Second):
			t.Fatal("preparation stalled")
		}
		topologyWaitState(t, 1, 1)
		// A second queued request also prepares and waits instead of being rejected.
		secondPrepared := make(chan struct{})
		secondCtx, cancelSecond := context.WithCancel(context.Background())
		defer cancelSecond()
		second := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(secondCtx, func(context.Context) error {
				close(secondPrepared)
				return nil
			})
			second <- topologyAdmissionTestResult{c, r, e}
		}()
		select {
		case <-secondPrepared:
		case <-time.After(time.Second):
			t.Fatal("second preparation stalled")
		}
		topologyWaitState(t, 1, 2)
		cancel()
		require.ErrorIs(t, topologyReceive(t, done).err, context.Canceled)
		topologyWaitState(t, 1, 1)
		cancelSecond()
		require.ErrorIs(t, topologyReceive(t, second).err, context.Canceled)
		topologyWaitState(t, 1, 0)
	})
	t.Run("prepare_error_leaves_queue", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		failure := errors.New("bounded body read failed")
		_, _, err = AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error {
			return failure
		})
		require.ErrorIs(t, err, failure)
		topologyWaitState(t, 1, 0)
	})
	// 读取请求体中的排头保留排队位置，但不占用空闲执行槽：后到的请求可以直接执行；
	// 排头准备完成后在执行槽空出时执行。
	t.Run("preparing_head_does_not_block_a_free_slot", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		entered, proceed := make(chan struct{}), make(chan struct{})
		done := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error {
				close(entered)
				<-proceed
				return nil
			})
			done <- topologyAdmissionTestResult{c, r, e}
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("preparation stalled")
		}
		release() // must not wait for body preparation's I/O
		topologyWaitState(t, 0, 1)
		later := topologyReceive(t, topologyAcquireAsync(context.Background()))
		require.NoError(t, later.err, "a later arrival takes the free slot while the head prepares")
		topologyWaitState(t, 1, 1)
		close(proceed)
		select {
		case r := <-done:
			if r.release != nil {
				r.release()
			}
			t.Fatal("the head must wait for a free slot after preparing")
		case <-time.After(20 * time.Millisecond):
		}
		later.release()
		head := topologyReceive(t, done)
		require.NoError(t, head.err)
		head.release()
	})
	// 排头还在读取请求体时，执行槽空出后分给已就绪的后一个等待者。
	t.Run("ready_waiter_admitted_while_head_prepares", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		entered, proceed := make(chan struct{}), make(chan struct{})
		head := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error {
				close(entered)
				<-proceed
				return nil
			})
			head <- topologyAdmissionTestResult{c, r, e}
		}()
		<-entered
		topologyWaitState(t, 1, 1)
		ready := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error { return nil })
			ready <- topologyAdmissionTestResult{c, r, e}
		}()
		topologyWaitState(t, 1, 2)
		release()
		second := topologyReceive(t, ready)
		require.NoError(t, second.err)
		topologyWaitState(t, 1, 1)
		close(proceed)
		second.release()
		first := topologyReceive(t, head)
		require.NoError(t, first.err)
		first.release()
	})
}

// 每个等待者只在拿到执行槽时被唤醒一次：N 个等待者依次排空共唤醒 N 次，不随等待人数平方增长。
func TestTopologyAdmissionWakesOnlyTheNextWaiter(t *testing.T) {
	topologyAdmissionTestEnvironment(t, 1)
	_, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	const waiting = 64
	pending := make([]<-chan topologyAdmissionTestResult, 0, waiting)
	for i := 0; i < waiting; i++ {
		pending = append(pending, topologyAcquireAsync(context.Background()))
		topologyWaitState(t, 1, int64(i+1))
	}
	topologyAdmission.Lock()
	before := topologyAdmission.wakeups
	topologyAdmission.Unlock()
	release()
	for i, done := range pending {
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		topologyWaitState(t, 1, int64(waiting-i-1))
		r.release()
	}
	topologyAdmission.Lock()
	wakeups := topologyAdmission.wakeups - before
	topologyAdmission.Unlock()
	require.Equal(t, waiting, wakeups)
}
