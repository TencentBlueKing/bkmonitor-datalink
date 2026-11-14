// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// phaseTwoRenewMaxAttempts bounds how many times one renewal round may call
// the Ownership Store inside a single renew interval.
const phaseTwoRenewMaxAttempts = 3

// phaseTwoRenewAttemptTimeout derives the deadline of one registration,
// lease or Control Leader renewal call from its renew interval: half the
// interval, never below one second. A hung store call is therefore cut well
// before the TTL instead of blocking the renewal loop indefinitely. No
// separate configuration key exists for it on purpose.
func phaseTwoRenewAttemptTimeout(interval time.Duration) time.Duration {
	timeout := interval / 2
	if timeout < time.Second {
		timeout = time.Second
	}
	return timeout
}

// phaseTwoRenewRetryDelay is the distance between the starts of two attempts of
// one renewal round: the attempts spread evenly over the interval. Bunched at its
// start they left most of it idle, and a lease whose TTL is three intervals could
// not outlive a failure of 12 s: the third interval's first attempt falls on the
// deadline, so the last attempt that could save the lease was 22 s after the
// renewal it extends, not 30.
func phaseTwoRenewRetryDelay(interval time.Duration) time.Duration {
	delay := interval / phaseTwoRenewMaxAttempts
	if delay <= 0 {
		delay = time.Millisecond
	}
	return delay
}

// phaseTwoRenewMinimumRest is the least a failed attempt waits before the next,
// a tenth of the renew interval.
func phaseTwoRenewMinimumRest(interval time.Duration) time.Duration {
	return interval / 10
}

// phaseTwoRenewNextAttempt is when the attempt after this one starts, this one
// having failed at failedAt, and whether there is one. A fast failure waits for
// its place in the spread. A failure that took its time still rests: going straight
// on would ask a store that is already slow more often than it was asked before the
// attempts were spread, so a slow failure paces as it always did.
func phaseTwoRenewNextAttempt(started, failedAt time.Time, interval time.Duration, attempt int) (time.Time, bool) {
	if attempt >= phaseTwoRenewMaxAttempts {
		return time.Time{}, false
	}
	next := started.Add(time.Duration(attempt) * phaseTwoRenewRetryDelay(interval))
	if rested := failedAt.Add(phaseTwoRenewMinimumRest(interval)); rested.After(next) {
		next = rested
	}
	return next, next.Before(started.Add(interval))
}

// renewPhaseTwoWithinInterval runs one renewal round: renew is called up to
// phaseTwoRenewMaxAttempts times, each call under its own deadline, spread over
// the interval, as long as the next would still start inside the interval
// and mayRetry (when given) still allows another attempt, which the
// lease loops use to stop once the lease or authority is outside its TTL. A
// single transient store failure therefore does not cost a whole interval.
// The round ends early on success, when ctx is cancelled, on a phase-two
// invariant error or on an authoritative lease decision such as a stale
// fence; those are returned as-is. Every other failure is reported through
// onFailure and retried. The last error is returned when the round gives up.
func renewPhaseTwoWithinInterval(
	ctx context.Context,
	interval time.Duration,
	renew func(context.Context) error,
	onFailure func(error),
	mayRetry func() bool,
) error {
	timeout := phaseTwoRenewAttemptTimeout(interval)
	started := time.Now()
	var err error
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		err = renew(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isPhaseTwoInvariantError(err) || ownership.IsLeaseDecision(err) {
			return err
		}
		if onFailure != nil {
			onFailure(err)
		}
		if mayRetry != nil && !mayRetry() {
			return err
		}
		next, again := phaseTwoRenewNextAttempt(started, time.Now(), interval, attempt)
		if !again {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(next)):
		}
	}
}
