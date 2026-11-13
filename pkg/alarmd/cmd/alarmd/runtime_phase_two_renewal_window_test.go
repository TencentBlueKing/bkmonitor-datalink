// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A round's attempts spread over the interval: bunched at its start they left
// most of it with nobody asking, whether or not the store had come back.
func TestARenewalRoundSpreadsItsAttemptsOverTheInterval(t *testing.T) {
	t.Parallel()
	// Not a multiple of three, as ten seconds is not: three spacings then fall just
	// short of the interval, and only the count stops a fourth attempt.
	const interval = time.Second
	failure := errors.New("store unreachable")
	var attempts []time.Time
	var reported int
	started := time.Now()
	err := renewPhaseTwoWithinInterval(context.Background(), interval, func(context.Context) error {
		attempts = append(attempts, time.Now())
		return failure
	}, func(error) { reported++ }, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("round error = %v, want the last failure", err)
	}
	// Three is what the store is asked per round in a failure: spreading them is
	// what changed, not how many.
	if len(attempts) != 3 || reported != 3 {
		t.Fatalf("attempts = %d reported = %d, want 3 of each", len(attempts), reported)
	}
	// The last attempt leaves at most one spacing to the next round's first.
	spacing := interval / phaseTwoRenewMaxAttempts
	for index := 1; index < len(attempts); index++ {
		if at := attempts[index].Sub(started); at < time.Duration(index)*spacing {
			t.Fatalf("attempt %d came %s into the round, want at least %s", index, at, time.Duration(index)*spacing)
		}
	}
	if last := attempts[len(attempts)-1].Sub(started); last < interval-spacing || last >= interval {
		t.Fatalf("the last attempt came %s into a round of %s, want within its last spacing", last, interval)
	}
}

// When each attempt of a ten second round starts, by how long each takes to fail. A
// fast failure is spread. A failure past the spacing less the rest paces exactly as
// the attempts did before they were spread (one second after the last failed, and
// no attempt that would start past the interval), so a slow store is asked no more
// often than it was.
func TestWhenEachAttemptOfARoundStarts(t *testing.T) {
	const interval = 10 * time.Second
	for failing, want := range map[time.Duration][]time.Duration{
		time.Millisecond:        {0, interval / 3, 2 * (interval / 3)},
		2800 * time.Millisecond: {0, 3800 * time.Millisecond, 7600 * time.Millisecond},
		4500 * time.Millisecond: {0, 5500 * time.Millisecond},
		5 * time.Second:         {0, 6 * time.Second},
		// Past the spacing less the rest, by a whisker on either side.
		2333 * time.Millisecond: {0, interval / 3, 2 * (interval / 3)},
		2334 * time.Millisecond: {0, 3334 * time.Millisecond, 6668 * time.Millisecond},
	} {
		started := time.Unix(1000, 0)
		starts := []time.Duration{0}
		for attempt := 1; ; attempt++ {
			failedAt := started.Add(starts[attempt-1] + failing)
			next, again := phaseTwoRenewNextAttempt(started, failedAt, interval, attempt)
			if !again {
				break
			}
			starts = append(starts, next.Sub(started))
		}
		if !reflect.DeepEqual(starts, want) {
			t.Errorf("failing in %s: attempts start at %v, want %v", failing, starts, want)
		}
	}
}

// The round itself waits for the time it is told, and rests after a slow failure.
func TestASlowFailureStillRestsBeforeTheNextAttempt(t *testing.T) {
	t.Parallel()
	const (
		interval = 900 * time.Millisecond
		failing  = 360 * time.Millisecond
		rest     = interval / 10
	)
	failure := errors.New("store too slow")
	var attempts []time.Time
	err := renewPhaseTwoWithinInterval(context.Background(), interval, func(context.Context) error {
		attempts = append(attempts, time.Now())
		time.Sleep(failing) // fixed-wait: how long the attempt takes to fail is what is under test
		return failure
	}, nil, nil)
	// 0 to 360 ms, 450 to 810 ms, and a third would start at the interval.
	if !errors.Is(err, failure) || len(attempts) != 2 {
		t.Fatalf("round error = %v after %d attempts, want the failure after 2", err, len(attempts))
	}
	if gap := attempts[1].Sub(attempts[0]); gap < failing+rest {
		t.Fatalf("the next attempt came %s after the last began, want the %s failure and the %s rest", gap, failing, rest)
	}
}

// A round ends with its interval: a store that takes the whole interval to fail
// gets no further attempt in this round, and the next round's is already due.
func TestARenewalRoundEndsWithItsInterval(t *testing.T) {
	t.Parallel()
	const interval = 200 * time.Millisecond
	failure := errors.New("store too slow")
	calls := 0
	err := renewPhaseTwoWithinInterval(context.Background(), interval, func(context.Context) error {
		calls++
		time.Sleep(interval) // fixed-wait: an attempt that outlasts the round is what is under test
		return failure
	}, nil, nil)
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("round error = %v after %d calls, want the failure after one", err, calls)
	}
}

// A lease past its TTL has nothing left to renew: the loops say so through
// mayRetry, and the round stops asking there.
func TestARenewalRoundStopsWhenItMayNoLongerRetry(t *testing.T) {
	failure := errors.New("store unreachable")
	calls := 0
	err := renewPhaseTwoWithinInterval(context.Background(), time.Minute, func(context.Context) error {
		calls++
		return failure
	}, nil, func() bool { return false })
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("round error = %v after %d calls, want the failure after one", err, calls)
	}
}

// The store's answer ends a round at once, however much of it is left.
func TestARenewalRoundStopsAtTheStoresAnswer(t *testing.T) {
	calls := 0
	err := renewPhaseTwoWithinInterval(context.Background(), time.Minute, func(context.Context) error {
		calls++
		return ownership.ErrNotDesired
	}, nil, nil)
	if !errors.Is(err, ownership.ErrNotDesired) || calls != 1 {
		t.Fatalf("round error = %v after %d calls, want the refusal after one", err, calls)
	}
}
