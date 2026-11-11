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
	t.Run("prepare_unlocks_and_keeps_fifo_position", func(t *testing.T) {
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
		// A later arrival queues behind the preparing head instead of taking the free slot.
		later := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 0, 2)
		close(proceed)
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		topologyWaitState(t, 1, 1)
		r.release()
		next := topologyReceive(t, later)
		require.NoError(t, next.err)
		next.release()
	})
}
