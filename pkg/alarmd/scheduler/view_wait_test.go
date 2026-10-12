// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A round the executable view refused is waiting for one of two things: the
// view's delta, which arrives within the propagation delay, or the record's
// word, which arrives by lease renewal. B4 §2 bounds how far the view lags
// the record by the renewal interval plus that delay, so a refused Query
// Group must be asked again within the renewal interval however many times
// it was refused - not on the blocked source's backoff, which grows to its
// maximum and left a Worker that started without a Leader dark for that
// much longer after the view came.
func TestARoundTheViewRefusedIsAskedAgainWithinTheRenewalInterval(t *testing.T) {
	limits := testRecoveryLimits()
	limits.RetryMaxDelay = 30 * time.Second
	clock := newMutableClock(time.Unix(200, 0))
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	renewal := 10 * time.Second
	source := &fakeSlotSource{err: &ViewNotExecutableError{Reason: "not_in_view", AwaitingView: true, RetryWithin: renewal}}
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	for refusal := 1; refusal <= 8; refusal++ {
		if _, _, err := runner.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		wait := runner.NextReadyAt().Sub(clock.Now())
		if wait <= 0 || wait > renewal {
			t.Fatalf("after refusal %d the Runner waits %s, want at most the renewal interval %s", refusal, wait, renewal)
		}
		clock.Advance(wait)
	}
	if source.calls != 8 {
		t.Fatalf("source asked %d times over eight refusals, want 8", source.calls)
	}
}

// A refusal that names no bound keeps the blocked source's backoff.
func TestARoundTheViewRefusedWithNoBoundKeepsTheSourceBackoff(t *testing.T) {
	limits := testRecoveryLimits()
	limits.RetryMaxDelay = 30 * time.Second
	clock := newMutableClock(time.Unix(200, 0))
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	source := &fakeSlotSource{err: &ViewNotExecutableError{Reason: "not_in_view"}}
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	var wait time.Duration
	for refusal := 1; refusal <= 8; refusal++ {
		if _, _, err := runner.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		wait = runner.NextReadyAt().Sub(clock.Now())
		clock.Advance(wait)
	}
	if wait != limits.RetryMaxDelay {
		t.Fatalf("eighth wait = %s, want the backoff's maximum %s", wait, limits.RetryMaxDelay)
	}
}
