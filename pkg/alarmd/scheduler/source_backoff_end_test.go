// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A source backoff ends with the first read of the source that succeeds,
// whatever that read finds. A Worker that starts before its view arrives
// refuses every Query Group once, and most of them then read a next Slot in
// the future: when only a due Slot ended the backoff, those kept a ready time
// already past until their next Slot ran, the dispatcher queued them again on
// every return, and a Worker re-read up to an hour of its Query Groups'
// schedules several times a second after each rollout.
func TestASourceReadThatFindsNothingDueEndsTheSourceBackoff(t *testing.T) {
	for name, refusal := range map[string]error{
		"view not executable": &ViewNotExecutableError{Reason: "not_in_view", AwaitingView: true, RetryWithin: 10 * time.Second},
		"source retry":        &SourceRetryError{Err: context.DeadlineExceeded},
		"source blocked":      &SourceBlockedError{Err: ErrProgressOffSchedule},
	} {
		t.Run(name, func(t *testing.T) {
			limits := testRecoveryLimits()
			clock := newMutableClock(time.Unix(200, 0))
			fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
			source := &fakeSlotSource{err: refusal}
			flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			// Refused twice, so the backoff has grown past its first step.
			for range 2 {
				if _, _, err := runner.RunOne(context.Background()); err != nil {
					t.Fatal(err)
				}
				clock.Advance(runner.NextReadyAt().Sub(clock.Now()))
			}
			// The read now succeeds and finds the next Slot an hour away.
			next := clock.Now().Add(time.Hour).Unix()
			source.err = nil
			source.facts = SlotDueFacts{NotDueUntilUnix: next, NextSlotUnix: next, IntervalSeconds: 3600}
			if _, _, err := runner.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if ready := runner.NextReadyAt(); !ready.IsZero() {
				t.Fatalf("after a read that found the next Slot at %d the Runner is ready again at %d, "+
					"%s ago: the dispatcher queues it again on every return until that Slot runs",
					next, ready.Unix(), clock.Now().Sub(ready))
			}
			if bound := runner.DueBound(); bound.Verdict != DueVerdictNotDue || bound.Deferred || bound.NotDueUntilUnix != next {
				t.Fatalf("due bound = %+v, want not due until %d and not deferred", bound, next)
			}
			// And the failures that grew it are over: the next refusal starts
			// the backoff again from its first step.
			source.err = refusal
			if _, _, err := runner.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if wait := runner.NextReadyAt().Sub(clock.Now()); wait != limits.RetryMinDelay {
				t.Fatalf("a refusal after a successful read waits %s, want the first step %s", wait, limits.RetryMinDelay)
			}
		})
	}
}

// The same holds for a round that ends some other way once the backoff is
// past: an error the source names neither a retry nor a block sets no
// backoff of its own, and must not leave the spent one behind as a ready
// time already gone.
func TestARoundPastTheSourceBackoffLeavesNoReadyTimeBehind(t *testing.T) {
	limits := testRecoveryLimits()
	clock := newMutableClock(time.Unix(200, 0))
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	source := &fakeSlotSource{err: &SourceRetryError{Err: context.DeadlineExceeded}}
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(runner.NextReadyAt().Sub(clock.Now()) + time.Second)
	source.err = errors.New("a source failure of no named kind")
	if _, _, err := runner.RunOne(context.Background()); err == nil {
		t.Fatal("the source's error was not returned")
	}
	if ready := runner.NextReadyAt(); !ready.IsZero() {
		t.Fatalf("after a round past its backoff the Runner is ready again at %d, %s ago", ready.Unix(), clock.Now().Sub(ready))
	}
}

// The execution backoff is the other half of NextReadyAt and is spent the
// same way: a failed Slot's backoff that the round is past must not outlive
// a round that never reaches the Slot - a source error of no named kind, or
// an ownership the session no longer confirms - as a ready time already gone.
func TestARoundPastTheExecutionBackoffLeavesNoReadyTimeBehind(t *testing.T) {
	for name, fail := range map[string]func(*fakeSlotSource, *fakeSession){
		"source error": func(source *fakeSlotSource, _ *fakeSession) {
			source.err = errors.New("a source failure of no named kind")
		},
		"ownership rejected": func(_ *fakeSlotSource, session *fakeSession) {
			session.err = errors.New("the lease is not this Worker's")
		},
	} {
		t.Run(name, func(t *testing.T) {
			clock := newMutableClock(time.Unix(200, 0))
			slot := frozenSlot("query-group-1")
			flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			source := &fakeSlotSource{slot: slot}
			session := &fakeSession{fence: slot.Dispatch.OwnerFence}
			runner, err := NewRunner("query-group-1", session, source, &failingExecutor{err: errors.New("commit progress: redis timeout"), failures: -1}, flights, clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
				t.Fatalf("the Slot's execution attempted=%t err=%v, want a failed attempt", attempted, err)
			}
			if runner.NextReadyAt().IsZero() {
				t.Fatal("a failed execution set no backoff")
			}
			clock.Advance(runner.NextReadyAt().Sub(clock.Now()) + time.Second)
			fail(source, session)
			if _, _, err := runner.RunOne(context.Background()); err == nil {
				t.Fatal("the round's error was not returned")
			}
			if ready := runner.NextReadyAt(); !ready.IsZero() {
				t.Fatalf("after a round past its execution backoff the Runner is ready again at %d, %s ago", ready.Unix(), clock.Now().Sub(ready))
			}
		})
	}
}
