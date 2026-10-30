// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func topologyAdmissionTestEnvironment(t *testing.T, slots int) {
	t.Helper()
	oldConcurrent, oldReserved, oldBase, oldMargin, oldYolo := MaxSharedTopologyConcurrent, MaxSharedTopologyReservedBytes, SharedTopologyRequestMemoryBytes, SharedTopologyMemoryHeadroom, yoloMode
	oldHeadroom := topologyMemoryHeadroom
	require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
	MaxSharedTopologyConcurrent, MaxSharedTopologyReservedBytes = slots, 4096
	SharedTopologyRequestMemoryBytes, SharedTopologyMemoryHeadroom, yoloMode = 1, 10, false
	topologyMemoryHeadroom = func() (int64, bool) { return 10000, true }
	t.Cleanup(func() {
		require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
		MaxSharedTopologyConcurrent, MaxSharedTopologyReservedBytes = oldConcurrent, oldReserved
		SharedTopologyRequestMemoryBytes, SharedTopologyMemoryHeadroom, yoloMode = oldBase, oldMargin, oldYolo
		topologyMemoryHeadroom = oldHeadroom
	})
}

func topologyWaitState(t *testing.T, active, waiters int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		s := topologyAdmissionTestState()
		return s["active"] == active && s["waiters"] == waiters
	}, time.Second, time.Millisecond)
}

type topologyAdmissionTestResult struct {
	ctx     context.Context
	release func()
	err     error
}

func topologyAcquireAsync(ctx context.Context) <-chan topologyAdmissionTestResult {
	done := make(chan topologyAdmissionTestResult, 1)
	go func() { c, r, e := AcquireSharedTopology(ctx); done <- topologyAdmissionTestResult{c, r, e} }()
	return done
}

func topologyReceive(t *testing.T, done <-chan topologyAdmissionTestResult) topologyAdmissionTestResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(time.Second):
		t.Fatal("admission did not finish")
		return topologyAdmissionTestResult{}
	}
}

func TestTopologyAdmissionWaiting(t *testing.T) {
	t.Run("wait_release_nested_and_same_deadline", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		holder, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		parent, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		expected, _ := parent.Deadline()
		started := time.Now()
		done := topologyAcquireAsync(parent)
		topologyWaitState(t, 1, 1)
		require.Zero(t, topologyAdmissionTestState()["reserved"])
		_, nr, e := AcquireSharedTopology(holder)
		require.NoError(t, e)
		nr()
		topologyWaitState(t, 1, 1)
		release()
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		defer r.release()
		actual, _ := r.ctx.Deadline()
		require.Equal(t, expected, actual)
		require.Less(t, time.Until(actual), time.Second-time.Since(started)+10*time.Millisecond)
		topologyWaitState(t, 1, 0)
		r.release()
		require.ErrorIs(t, r.ctx.Err(), context.Canceled)
		t.Logf("queue elapsed=%s; waiting held no reservation; parent deadline preserved", time.Since(started))
	})
	t.Run("queued_cancel", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		parent, cancel := context.WithCancel(context.Background())
		done := topologyAcquireAsync(parent)
		topologyWaitState(t, 1, 1)
		cancel()
		r := topologyReceive(t, done)
		require.ErrorIs(t, r.err, context.Canceled)
		require.Nil(t, r.release)
		topologyWaitState(t, 1, 0)
		require.Zero(t, topologyAdmissionTestState()["reserved"])
	})
	t.Run("queued_deadline", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		done := topologyAcquireAsync(parent)
		topologyWaitState(t, 1, 1)
		r := topologyReceive(t, done)
		require.ErrorIs(t, r.err, context.DeadlineExceeded)
		topologyWaitState(t, 1, 0)
	})
	t.Run("bounded_queue", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := topologyAcquireAsync(parent)
		topologyWaitState(t, 1, 1)
		_, r, e := AcquireSharedTopology(context.Background())
		var limit *ResultLimitError
		require.ErrorAs(t, e, &limit)
		require.Equal(t, "max_topology_admission_waiters", limit.Reason)
		require.Nil(t, r)
		cancel()
		require.ErrorIs(t, topologyReceive(t, done).err, context.Canceled)
		topologyWaitState(t, 1, 0)
	})
	t.Run("execution_cancel_release_wakes_next", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		parent, cancel := context.WithCancel(context.Background())
		holder, release, e := AcquireSharedTopology(parent)
		require.NoError(t, e)
		defer release()
		require.NoError(t, ReserveTopologyStage(holder, "graph", 100))
		require.NoError(t, CommitTopologyStage(holder, "graph", 40))
		done := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 1, 1)
		cancel()
		require.ErrorIs(t, ReserveTopologyStage(holder, "graph", 200), context.Canceled)
		require.ErrorIs(t, CommitTopologyStage(holder, "graph", 50), context.Canceled)
		require.NoError(t, ReserveTopologyStage(holder, "graph", 0))
		require.Zero(t, topologyAdmissionTestState()["materialized"])
		release()
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		r.release()
	})
	t.Run("resource_failure_does_not_hold_fifo_head", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 2)
		_, a, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer a()
		_, b, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer b()
		done := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 2, 1)
		topologyMemoryHeadroom = func() (int64, bool) { return 10, true }
		// No concurrent reads of this hook until the release wakes the waiter.
		a()
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		require.Zero(t, topologyAdmissionTestState()["reserved"])
		var limit *ResultLimitError
		require.ErrorAs(t, ReserveTopologyStage(r.ctx, "request", 100), &limit)
		require.Equal(t, "max_topology_memory_headroom", limit.Reason)
		r.release()
		topologyWaitState(t, 1, 0)
	})
}

func TestTopologyAdmissionMaterializedAccounting(t *testing.T) {
	t.Run("pending_other_request_still_protected", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 2)
		a, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		require.NoError(t, ReserveTopologyStage(a, "graph", 1000))
		topologyMemoryHeadroom = func() (int64, bool) { return 310, true }
		b, releaseB, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer releaseB()
		var limit *ResultLimitError
		require.ErrorAs(t, ReserveTopologyStage(b, "matrix", 1), &limit)
		require.Equal(t, "max_topology_memory_headroom", limit.Reason)
		require.Equal(t, int64(1000), topologyAdmissionTestState()["reserved"])
		require.Zero(t, topologyAdmissionTestState()["materialized"])
	})
	t.Run("owned_lower_bound_credit_and_residual_pending", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 2)
		a, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		require.NoError(t, ReserveTopologyStage(a, "graph", 1000))
		require.NoError(t, CommitTopologyStage(a, "graph", 800))
		topologyMemoryHeadroom = func() (int64, bool) { return 310, true }
		b, releaseB, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer releaseB()
		require.NoError(t, ReserveTopologyStage(b, "matrix", 100))
		var limit *ResultLimitError
		require.ErrorAs(t, ReserveTopologyStage(b, "matrix", 101), &limit)
		require.Equal(t, "max_topology_memory_headroom", limit.Reason)
		require.Equal(t, map[string]int64{"active": 2, "waiters": 0, "reserved": 1100, "materialized": 800}, topologyAdmissionTestState())
		require.NoError(t, CommitTopologyStage(a, "graph", 0))
		require.ErrorAs(t, ReserveTopologyStage(b, "matrix", 101), &limit)
		require.NoError(t, ReserveTopologyStage(a, "graph", 0))
		require.NoError(t, ReserveTopologyStage(b, "matrix", 101))
	})
	t.Run("shrink_clear_and_invalid_commit", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		a, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		require.NoError(t, ReserveTopologyStage(a, "graph", 1000))
		require.NoError(t, CommitTopologyStage(a, "graph", 800))
		require.Error(t, CommitTopologyStage(a, "graph", 1001))
		require.Error(t, CommitTopologyStage(a, "missing", 1))
		require.Error(t, CommitTopologyStage(a, "graph", -1))
		require.NoError(t, ReserveTopologyStage(a, "graph", 500))
		require.Equal(t, int64(500), topologyAdmissionTestState()["materialized"])
		require.NoError(t, ReserveTopologyStage(a, "graph", 0))
		require.Zero(t, topologyAdmissionTestState()["materialized"])
		release()
		release()
		require.ErrorIs(t, CommitTopologyStage(a, "graph", 0), context.Canceled)
	})
	t.Run("static_budget_and_unknown_headroom_remain", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
		a, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		require.NoError(t, ReserveTopologyStage(a, "graph", 4096))
		require.NoError(t, CommitTopologyStage(a, "graph", 4096))
		var limit *ResultLimitError
		require.ErrorAs(t, ReserveTopologyStage(a, "graph", 4097), &limit)
		require.Equal(t, "max_topology_reserved_bytes", limit.Reason)
	})
}

func TestTopologyAdmissionConcurrentStress(t *testing.T) {
	topologyAdmissionTestEnvironment(t, 2)
	var workers sync.WaitGroup
	var maxActive atomic.Int64
	var successes atomic.Int64
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < 8; j++ {
				parent, cancel := context.WithTimeout(context.Background(), time.Second)
				c, r, e := AcquireSharedTopology(parent)
				if e != nil {
					var limit *ResultLimitError
					if !errors.As(e, &limit) || limit.Reason != "max_topology_admission_waiters" {
						t.Errorf("unexpected acquire: %v", e)
					}
					cancel()
					continue
				}
				successes.Add(1)
				active := topologyAdmissionTestState()["active"]
				for old := maxActive.Load(); active > old; old = maxActive.Load() {
					if maxActive.CompareAndSwap(old, active) {
						break
					}
				}
				if e = ReserveTopologyStage(c, "graph", 100); e != nil {
					t.Error(e)
				}
				if e = CommitTopologyStage(c, "graph", 40); e != nil {
					t.Error(e)
				}
				time.Sleep(time.Millisecond)
				if e = ReserveTopologyStage(c, "graph", 0); e != nil {
					t.Error(e)
				}
				r()
				r()
				cancel()
			}
		}()
	}
	close(start)
	workers.Wait()
	require.Greater(t, successes.Load(), int64(0))
	require.LessOrEqual(t, maxActive.Load(), int64(2))
	t.Logf("successes=%d max_active=%d final=%v", successes.Load(), maxActive.Load(), topologyAdmissionTestState())
}

func TestTopologyAdmissionNoArtificialFloorAndConcurrentGrowth(t *testing.T) {
	t.Run("eight_light_requests_allow_real_growth", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 8)
		const mib = 1024 * 1024
		MaxSharedTopologyReservedBytes = 4 * 1024 * mib
		SharedTopologyRequestMemoryBytes = 512 * mib // Retained legacy config must not create fictitious residency.
		SharedTopologyMemoryHeadroom = 512 * mib
		topologyMemoryHeadroom = func() (int64, bool) { return 64 * 1024 * mib, true }
		var contexts []context.Context
		var releases []func()
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		for i := 0; i < 8; i++ {
			c, release, e := AcquireSharedTopology(context.Background())
			require.NoError(t, e)
			contexts = append(contexts, c)
			releases = append(releases, release)
			// Same bounded request buffer reservation currently used by the HTTP handler.
			require.NoError(t, ReserveTopologyStage(c, "request", 6*mib))
		}
		require.Equal(t, int64(48*mib), topologyAdmissionTestState()["reserved"])
		require.NoError(t, ReserveTopologyStage(contexts[0], "graph", 800*mib))
		require.Equal(t, int64(848*mib), topologyAdmissionTestState()["reserved"])
		t.Logf("8 live request buffers + 800MiB graph accepted under unchanged 4GiB: %v", topologyAdmissionTestState())
	})
	t.Run("concurrent_pending_growth_serialized", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 2)
		topologyMemoryHeadroom = func() (int64, bool) { return 1110, true } // 1100 bytes usable.
		a, ra, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer ra()
		b, rb, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer rb()
		start := make(chan struct{})
		done := make(chan error, 2)
		for _, ctx := range []context.Context{a, b} {
			go func(c context.Context) { <-start; done <- ReserveTopologyStage(c, "graph", 800) }(ctx)
		}
		close(start)
		successes := 0
		rejections := 0
		for i := 0; i < 2; i++ {
			if e := <-done; e == nil {
				successes++
			} else {
				var limit *ResultLimitError
				require.ErrorAs(t, e, &limit)
				require.Equal(t, "max_topology_memory_headroom", limit.Reason)
				rejections++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, rejections)
		require.Equal(t, int64(800), topologyAdmissionTestState()["reserved"])
		require.Zero(t, topologyAdmissionTestState()["materialized"])
	})
	t.Run("fifo_no_barging", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 2)
		_, ra, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer ra()
		_, rb, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer rb()
		first := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 2, 1)
		second := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 2, 2)
		ra()
		one := topologyReceive(t, first)
		require.NoError(t, one.err)
		defer one.release()
		topologyWaitState(t, 2, 1)
		select {
		case v := <-second:
			if v.release != nil {
				v.release()
			}
			t.Fatal("second waiter barged ahead of first release")
		default:
		}
		one.release()
		two := topologyReceive(t, second)
		require.NoError(t, two.err)
		two.release()
	})
}

func topologyAdmissionTestState() map[string]int64 {
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	return map[string]int64{"active": int64(topologyAdmission.active), "waiters": int64(len(topologyAdmission.waiters)), "reserved": topologyAdmission.reserved, "materialized": topologyAdmission.materialized}
}
