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

// phaseTwoRenewRetryDelay is the pause between two attempts of one renewal
// round, a tenth of the renew interval.
func phaseTwoRenewRetryDelay(interval time.Duration) time.Duration {
	delay := interval / 10
	if delay <= 0 {
		delay = time.Millisecond
	}
	return delay
}

// renewPhaseTwoWithinInterval runs one renewal round: renew is called up to
// phaseTwoRenewMaxAttempts times, each call under its own deadline, with a
// short pause between attempts, as long as the round still fits in one renew
// interval and mayRetry (when given) still allows another attempt, which the
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
	delay := phaseTwoRenewRetryDelay(interval)
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
		if attempt >= phaseTwoRenewMaxAttempts || (mayRetry != nil && !mayRetry()) ||
			time.Since(started)+delay >= interval {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}
