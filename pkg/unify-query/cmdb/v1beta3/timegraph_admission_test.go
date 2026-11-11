// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func topologyAdmissionTestEnvironment(t *testing.T, slots int) {
	t.Helper()
	oldConcurrent := MaxSharedTopologyConcurrent
	require.Equal(t, map[string]int64{"active": 0, "waiters": 0}, topologyAdmissionTestState())
	MaxSharedTopologyConcurrent = slots
	t.Cleanup(func() {
		require.Equal(t, map[string]int64{"active": 0, "waiters": 0}, topologyAdmissionTestState())
		MaxSharedTopologyConcurrent = oldConcurrent
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
		nested, nr, e := AcquireSharedTopology(holder)
		require.NoError(t, e)
		require.Equal(t, holder, nested, "nested calls reuse the admitted lease")
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
		_, _, e = AcquireSharedTopology(r.ctx)
		require.ErrorIs(t, e, context.Canceled, "a released lease cannot be reused")
		t.Logf("queue elapsed=%s; parent deadline preserved", time.Since(started))
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
	// 等待人数不是拒绝理由：超过执行槽数的请求都排队，按到达顺序完成或被自己的期限取消。
	t.Run("waiters_beyond_slots_wait_and_complete", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		_, release, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		defer release()
		const waiting = 4
		pending := make([]<-chan topologyAdmissionTestResult, 0, waiting)
		for i := 0; i < waiting; i++ {
			pending = append(pending, topologyAcquireAsync(context.Background()))
			topologyWaitState(t, 1, int64(i+1))
		}
		expiring, cancelExpiring := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancelExpiring()
		expired := topologyAcquireAsync(expiring)
		require.ErrorIs(t, topologyReceive(t, expired).err, context.DeadlineExceeded)
		topologyWaitState(t, 1, waiting)
		release()
		for i, done := range pending {
			r := topologyReceive(t, done)
			require.NoError(t, r.err)
			topologyWaitState(t, 1, int64(waiting-i-1))
			r.release()
		}
	})
	t.Run("execution_cancel_release_wakes_next", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		parent, cancel := context.WithCancel(context.Background())
		_, release, e := AcquireSharedTopology(parent)
		require.NoError(t, e)
		defer release()
		done := topologyAcquireAsync(context.Background())
		topologyWaitState(t, 1, 1)
		cancel()
		release()
		r := topologyReceive(t, done)
		require.NoError(t, r.err)
		r.release()
	})
}

// 并发请求只在执行槽上等待：执行中的请求数不超过槽数，所有请求都在自己的期限内完成。
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
				parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, r, e := AcquireSharedTopology(parent)
				if e != nil {
					t.Errorf("unexpected acquire: %v", e)
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
				time.Sleep(time.Millisecond)
				r()
				r()
				cancel()
			}
		}()
	}
	close(start)
	workers.Wait()
	require.EqualValues(t, 64, successes.Load())
	require.LessOrEqual(t, maxActive.Load(), int64(2))
	t.Logf("successes=%d max_active=%d final=%v", successes.Load(), maxActive.Load(), topologyAdmissionTestState())
}

func TestTopologyAdmissionFIFONoBarging(t *testing.T) {
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
}

func topologyAdmissionTestState() map[string]int64 {
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	return map[string]int64{"active": int64(topologyAdmission.active), "waiters": int64(len(topologyAdmission.waiters))}
}
