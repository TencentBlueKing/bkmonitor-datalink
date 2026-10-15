// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"sync/atomic"
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
	t.Run("queue_owns_only_request_and_cancel_cleans", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		holder, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		require.NoError(t, ReserveTopologyStage(holder, "graph", 100))
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		prepared := make(chan struct{})
		done := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(parent, func(ctx context.Context) error {
				e := ReserveTopologyStage(ctx, "request", 256)
				close(prepared)
				return e
			})
			done <- topologyAdmissionTestResult{c, r, e}
		}()
		select {
		case <-prepared:
		case <-time.After(time.Second):
			t.Fatal("preparation stalled")
		}
		topologyWaitState(t, 1, 1)
		require.EqualValues(t, 356, topologyAdmissionTestState()["reserved"])
		var overflowPrepared atomic.Int64
		_, _, err = AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(context.Context) error { overflowPrepared.Add(1); return nil })
		require.ErrorContains(t, err, "max_topology_admission_waiters")
		require.Zero(t, overflowPrepared.Load())
		cancel()
		require.ErrorIs(t, topologyReceive(t, done).err, context.Canceled)
		topologyWaitState(t, 1, 0)
		require.EqualValues(t, 100, topologyAdmissionTestState()["reserved"])
	})
	t.Run("prepare_error_releases_its_reservation", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		failure := errors.New("bounded body read failed")
		_, _, err = AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(ctx context.Context) error {
			if e := ReserveTopologyStage(ctx, "request", 128); e != nil {
				return e
			}
			return failure
		})
		require.ErrorIs(t, err, failure)
		topologyWaitState(t, 1, 0)
		require.Zero(t, topologyAdmissionTestState()["reserved"])
	})
	t.Run("prepare_unlocks_and_keeps_fifo_position", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		entered, proceed := make(chan struct{}), make(chan struct{})
		done := make(chan topologyAdmissionTestResult, 1)
		go func() {
			c, r, e := AcquireSharedTopologyWithQueuedPreparation(context.Background(), func(ctx context.Context) error {
				if e := ReserveTopologyStage(ctx, "request", 128); e != nil {
					return e
				}
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
		_, _, err = AcquireSharedTopology(context.Background())
		require.ErrorContains(t, err, "max_topology_admission_waiters")
		close(proceed)
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		require.EqualValues(t, 128, topologyAdmissionTestState()["reserved"])
		r.release()
	})
}
