// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot given up on names what held the round before it.
//
// This is the reading the four stuck Query Groups could not produce. Their
// completion line said GAP_SKIPPED, which is the outcome; the cause was a
// query cooldown, and it was already being recorded -- on another line, of
// another stage, at another timestamp. Carrying the previous round's word onto
// the line that reports the consequence is what lets one filter answer the
// question instead of a join across three readings.
func TestASlotGivenUpOnNamesWhatHeldTheRoundBefore(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	limits := testRecoveryLimits()
	source, _ := replayClassificationSource(t, 15, slot, 30*time.Second, limits)
	observer := &recordingReplayObserver{}
	source.observer = observer

	held := observability.HeldByFacts{
		Decision: "query_cooldown", AtUnixMilli: int64(slot)*1000 + 31_200,
		QueryCooldownFailures: 14, QueryCooldownUntilMilli: int64(slot)*1000 + 240_000,
	}
	ctx := withHeldBy(context.Background(), held)
	deadline := int64(slot)*1000 + 25_000

	if _, _, err := source.classifyRecovery(ctx, slot, deadline, time.Unix(int64(slot)+46, 0)); err != nil {
		t.Fatalf("classifyRecovery() error = %v", err)
	}
	if len(observer.facts) != 1 {
		t.Fatalf("observed %d replay expiries, want the one this Slot produced", len(observer.facts))
	}
	got := observer.facts[0].HeldBy
	if got == nil {
		t.Fatal("the Slot was given up on and said nothing about what held the round before it; that is the " +
			"reading four Query Groups sat without for hours")
	}
	if got.Decision != "query_cooldown" || got.AtUnixMilli != held.AtUnixMilli {
		t.Fatalf("held_by = %+v, want the previous round's own word and instant", got)
	}
	// The cooldown's two numbers, because the same word covers a cooldown that
	// has failed once and one that has failed fourteen times, and only the
	// instant it runs to says whether this Slot ever had a chance.
	if got.QueryCooldownFailures != 14 || got.QueryCooldownUntilMilli != held.QueryCooldownUntilMilli {
		t.Fatalf("held_by = %+v, want the cooldown's failure count and deadline to travel with the word", got)
	}
}

// A round that ran the Slot holds nothing, and says so with the word for it.
//
// Reported rather than omitted so the family is total: "which Slots were held,
// and by what" is a distribution, and one whose commonest value is an absent
// key cannot be read as one. The word is also what keeps a reader from
// treating "held by nothing" as "we did not record it".
func TestARoundThatRanTheSlotIsRememberedAsHoldingNothing(t *testing.T) {
	for _, test := range []struct {
		decision string
		want     string
	}{
		{decision: "execute_returned", want: observability.HeldByNothing},
		{decision: "execution_returned", want: observability.HeldByNothing},
		// Execute was entered and access handed it back with an instant to
		// wait for: the Slot did not run, so this round held it. Folding it
		// into "nothing held me" is how the commonest reason a short-period
		// Slot misses its window stops being readable -- and it was one of the
		// three candidates that had to be ruled out by hand the last time four
		// Query Groups stalled.
		{decision: "query_readiness_deferred", want: observability.HeldByReadinessDeferred},
		{decision: "query_cooldown", want: "query_cooldown"},
		{decision: "admission_denied", want: "admission_denied"},
		{decision: "single_flight_busy", want: "single_flight_busy"},
		{decision: "source_backoff", want: "source_backoff"},
		// A word outside the published vocabulary is reported as the
		// catch-all, never dropped: a reading nobody can interpret still says
		// a path exists, and a dropped one says the path does not.
		{decision: "some_new_branch", want: "other_error"},
	} {
		t.Run(test.decision, func(t *testing.T) {
			runner := &Runner{now: func() time.Time { return time.Unix(1_700_124_000, 0) }}
			// A cooldown is live on this Runner in every row. The rows that
			// are not the cooldown must leave its numbers behind, and the row
			// that is must pick them up -- reading them off the Runner's own
			// state, not off whatever a caller happened to pass.
			cooldownUntil := time.Unix(1_700_124_240, 0)
			runner.queryCooldown = queryCooldownState{failures: 14, until: cooldownUntil}
			// And a readiness instant, for the same reason: every row has one
			// available, so only the row that should carry it may.
			readyAt := time.Unix(1_700_124_045, 0)
			runner.sourceNextAt = readyAt
			runner.rememberHeldBy(test.decision)
			if runner.heldBy.Decision != test.want {
				t.Fatalf("rememberHeldBy(%q) = %q, want %q", test.decision, runner.heldBy.Decision, test.want)
			}
			if !observability.ValidHeldByDecision(runner.heldBy.Decision) {
				t.Fatalf("remembered %q, which is not in the published vocabulary; a word no partition has "+
					"cannot be counted", runner.heldBy.Decision)
			}
			// Only the cooldown carries the cooldown's numbers, and it does
			// carry them. Without the second half the field is one the Runner
			// declares and never fills: a reader sees held_by=query_cooldown
			// with failures 0 and cannot tell a cooldown that has just started
			// from one that has been extended fourteen times.
			if test.want == "query_cooldown" {
				if runner.heldBy.QueryCooldownFailures != 14 ||
					runner.heldBy.QueryCooldownUntilMilli != cooldownUntil.UnixMilli() {
					t.Fatalf("held_by = %+v, want the Runner's own cooldown failures and deadline", runner.heldBy)
				}
			} else if runner.heldBy.QueryCooldownFailures != 0 || runner.heldBy.QueryCooldownUntilMilli != 0 {
				t.Fatalf("held_by = %+v, want no cooldown numbers beside a word that is not the cooldown",
					runner.heldBy)
			}
			// Same rule for the readiness instant: the word that means "wait
			// until" must say until when, and no other word may carry it.
			if test.want == observability.HeldByReadinessDeferred {
				if runner.heldBy.ReadyAtUnixMilli != readyAt.UnixMilli() {
					t.Fatalf("held_by = %+v, want the instant access told the round to wait for (%d)",
						runner.heldBy, readyAt.UnixMilli())
				}
			} else if runner.heldBy.ReadyAtUnixMilli != 0 {
				t.Fatalf("held_by = %+v, want no readiness instant beside a word that is not the deferral",
					runner.heldBy)
			}
		})
	}
	// And the word for nothing reaches the line as a word. Returning nil for
	// it was the defect production found: the key was simply absent, which a
	// reader cannot tell from a build that does not report held_by at all,
	// and the distribution lost its commonest value.
	if held := HeldByFromContext(withHeldBy(context.Background(),
		observability.HeldByFacts{Decision: observability.HeldByNothing})); held == nil ||
		held.Decision != observability.HeldByNothing {
		t.Fatalf("a round that held nothing produced %+v, want the word for it", held)
	}
	// Including the very first round, which has no round before it at all.
	if held := HeldByFromContext(context.Background()); held == nil ||
		held.Decision != observability.HeldByNothing {
		t.Fatalf("the first round of all produced %+v, want the word for nothing", held)
	}
}

// ctxRecordingSource remembers what the Runner carried into each round.
type ctxRecordingSource struct {
	seen []*observability.HeldByFacts
	slot FrozenSlot
}

func (source *ctxRecordingSource) Next(
	ctx context.Context,
	_ execution.QueryGroupIdentity,
) (FrozenSlot, bool, SlotDueFacts, error) {
	source.seen = append(source.seen, HeldByFromContext(ctx))
	if source.slot.Contract.Slot.QueryGroup == "" {
		return FrozenSlot{}, false, SlotDueFacts{}, nil
	}
	return source.slot, true, SlotDueFacts{}, nil
}

// The Runner actually carries its previous round's word into the next round.
//
// The two cases above prove the word is chosen correctly and reported
// correctly, and both would still pass with nothing in production putting it
// into the context -- a field with no writer, which reads on a dashboard
// exactly like a mechanism that is wired up. This is the case that fails if
// the injection is removed.
func TestTheRunnerCarriesItsPreviousDecisionIntoTheNextRound(t *testing.T) {
	now := time.Unix(1_700_124_000, 0)
	source := &ctxRecordingSource{}
	flights := NewFlightCoordinator()
	runner, err := NewRunner("query-group-1",
		&fakeSession{fence: execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "t"}},
		source, &blockingExecutor{}, flights, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	for round := 0; round < 2; round++ {
		if _, _, err := runner.RunOne(context.Background()); err != nil {
			t.Fatalf("round %d: RunOne() error = %v", round, err)
		}
	}
	if len(source.seen) != 2 {
		t.Fatalf("the source saw %d rounds, want 2", len(source.seen))
	}
	// Nothing ran before the first round, and it says so with the word rather
	// than with an absent key.
	if source.seen[0] == nil || source.seen[0].Decision != observability.HeldByNothing {
		t.Fatalf("first round carried %+v, want the word for nothing", source.seen[0])
	}
	// The first round found no due Slot, which is a word, and the second
	// round has to be able to say it.
	if source.seen[1] == nil || source.seen[1].Decision != "source_not_due" {
		t.Fatalf("second round carried %+v, want the first round's own decision. Without this the field is "+
			"one nothing in production writes", source.seen[1])
	}
	if source.seen[1].AtUnixMilli != now.UnixMilli() {
		t.Fatalf("held_by at = %d, want the instant the previous round decided (%d)",
			source.seen[1].AtUnixMilli, now.UnixMilli())
	}
}

// A Slot deferred for readiness is named as the holder of the Slot after it,
// end to end through the Runner.
//
// The table above pins the word and the instant at the point they are chosen.
// This is the round trip: Execute is entered, access hands the round back with
// an instant to wait for, and the next round has to be able to say that is
// what happened. Before this the deferral was folded into "nothing held me",
// so the commonest reason a short-period Slot misses its window was the one
// reason the line could not report.
func TestAReadinessDeferralHoldsTheNextSlotAndSaysUntilWhen(t *testing.T) {
	current := time.UnixMilli(1_700_000_000_000)
	readyAt := current.Add(30 * time.Second)
	now := current
	slot := frozenSlot("query-group-1")
	source := &ctxRecordingSource{slot: slot}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, source,
		&readinessDeferredExecutor{readyAt: readyAt}, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatalf("deferred round: RunOne() error = %v", err)
	}
	// Past the instant the round was told to wait for, so the next round
	// reaches the source instead of being stopped by the local backoff.
	now = readyAt.Add(time.Second)
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatalf("second round: RunOne() error = %v", err)
	}
	if len(source.seen) != 2 {
		t.Fatalf("the source saw %d rounds, want 2", len(source.seen))
	}
	held := source.seen[1]
	if held == nil || held.Decision != observability.HeldByReadinessDeferred {
		t.Fatalf("second round carried %+v, want the readiness deferral named as the holder", held)
	}
	if held.ReadyAtUnixMilli != readyAt.UnixMilli() {
		t.Fatalf("held_by ready_at = %d, want the instant access gave the previous round (%d). The word alone "+
			"says the data was not ready; only this says whether it would ever be ready in time",
			held.ReadyAtUnixMilli, readyAt.UnixMilli())
	}
}
