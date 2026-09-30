// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
)

// A failed state read schedules a retry of the Slot, and cancellation does not.
//
// This is what the Slot-level retry guarantees, and it is why the Redis
// client's own retries are redundant rather than load-bearing: the layer above
// already records a failed attempt, parks the Query Group with exponential
// backoff and no attempt cap, and re-reads current state on the way back. The
// client's three resend the same request to the same server and buy nothing -
// they only make the attempt take 12 s instead of 3.
//
// The completion deadline is deliberately not part of this. It is derived
// inside the coordinator and never reaches here, so a Slot that blew it still
// returns an ordinary error to a live runner context and is still retried:
// the completion deadline bounds one attempt, recovery_until bounds the
// retrying, and they are two bounds on purpose.
//
// Both directions are stated because either alone is satisfied by the wrong
// rule: always backing off would lose cancellation, never backing off would
// lose every retry.
func TestAFailedStateReadBacksOffUnlessItOutlivedTheSlot(t *testing.T) {
	readTimedOut := errors.New("alarmd worker: series state preflight: state: Redis MGET: i/o timeout")

	if !executionErrorBacksOff(context.Background(), readTimedOut) {
		t.Fatal("a state read that failed inside the Slot's deadline recorded no attempt, so the Slot is " +
			"never retried - the read is retryable and the next attempt re-reads current state")
	}

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if executionErrorBacksOff(stopped, readTimedOut) {
		t.Fatal("the same failure backed off after the runner's own context had gone; a Slot abandoned " +
			"because the process is stopping or the lease was withdrawn is not a failed attempt of it")
	}
	if executionErrorBacksOff(context.Background(), context.Canceled) {
		t.Fatal("cancellation counted as a failed attempt of the frozen Slot")
	}
	if executionErrorBacksOff(context.Background(), nil) {
		t.Fatal("a Slot that returned no error counted as a failed attempt")
	}
}
