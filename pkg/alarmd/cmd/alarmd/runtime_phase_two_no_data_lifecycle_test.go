// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var lifecycleHostPair = []string{"bk_target_ip", "bk_target_cloud_id"}

// The no-data trigger is counted from what the store holds, so a process that
// restarts between every round counts it exactly as one that never stopped.
//
// Every round below runs on a bundle started for that round, over the same
// redis-server: nothing a round decides from was in the memory of the process
// that runs it. Host B is in the target and does not report until round 6;
// host A reports throughout, so the item has data and B's silence is B's.
//
// The sequence and its expected readings come from P9 of the no-data progress
// report (2026-10-09, section 2): the trigger counts ANOMALY points inside a
// window of continuous periods (AB/service/trigger/checker.py:190-208), with
// window and count both equal to continuous (AB/core/control/strategy.py:
// 395-408), and a round whose query was not FULL contributes no point and
// bridges nothing. With continuous 3 the sequence absent, not FULL, absent,
// absent, absent therefore raises on round 5 and not before: round 4's window
// holds rounds 2 to 4, two absent points; round 5's holds rounds 3 to 5, three.
// A trigger that bridged the gap would raise on round 3, and one that counted
// points rather than periods on round 4. The stored absence of B is read back
// every round: opened on round 1 and untouched by the round that was not FULL
// (capability decomposition, section 1.1 and A1).
//
// The returning data closes the alert in one round (P11), under the alert's
// own identity. Which process may send that close is the open alert gate's
// rule, not no-data's: a process answers from the consumer's published set
// plus what it sent itself, and holds a recovery for an alert it neither sent
// nor sees published (openalerts/cache.go, the self-maintain ruling of
// 2026-09-14; no-data tracking retention proposal, section 5). So round 6, on
// a process that did not raise the alert and with nothing published, decides
// the recovery and holds it by that name. Once the consumer publishes the
// alert as open, the next round sends the recovery, under the identity a
// different process minted for the alert - which is the property here: an
// identity that moved across processes would close nothing.
func TestANoDataTriggerIsCountedAcrossProcessRestarts(t *testing.T) {
	const (
		absent  = 'A'
		notFull = 'U'
	)
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, continuous: 3,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}, nil)
	opened := fixture.evaluationAt(1)
	rounds := []struct {
		kind  rune
		raise bool
	}{{absent, false}, {notFull, false}, {absent, false}, {absent, false}, {absent, true}}
	var raised contract.TriggerEventV1
	var heldInRound5 uint64
	for index, step := range rounds {
		round := int64(index + 1)
		if round > 1 {
			fixture.replace("alarmd-worker-0")
		}
		fixture.serve(step.kind == notFull, 5, lifecycleHostA)
		before, heldBefore := len(fixture.noDataEventsFor(lifecycleHostB)), fixture.heldForNoOpenAlert()
		fixture.mustAttempt(round)
		heldInRound5 = fixture.heldForNoOpenAlert() - heldBefore
		sent := fixture.noDataEventsFor(lifecycleHostB)[before:]
		switch {
		case !step.raise && len(sent) != 0:
			t.Fatalf("round %d (%c) sent host B's no-data events %+v, want none", round, step.kind, sent)
		case step.raise && (len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal):
			t.Fatalf("round %d (%c) sent host B's no-data events %+v, want one ABNORMAL", round, step.kind, sent)
		case step.raise:
			raised = sent[0]
		}
		if firstAbsent, _, _ := fixture.absenceOf(lifecycleHostB); firstAbsent != opened {
			t.Fatalf("after round %d (%c) B's stored absence starts at %d, want %d: the round that opened it is round 1, "+
				"and a round not FULL moves nothing", round, step.kind, firstAbsent, opened)
		}
	}
	if raised.EvaluationTime != fixture.evaluationAt(5) || raised.DedupeMD5 == "" {
		t.Fatalf("raised %+v, want the alert of round 5 with its identity", raised)
	}

	// Round 6: B reports, on a process that did not raise the alert.
	fixture.replace("alarmd-worker-0")
	fixture.serve(false, 5, lifecycleHostA, lifecycleHostB)
	heldBefore := fixture.heldForNoOpenAlert()
	fixture.mustAttempt(6)
	if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 1 {
		t.Fatalf("round 6 sent host B's no-data events %+v beside the alert; with nothing published a process that did "+
			"not raise it holds the close", sent[1:])
	}
	// The gate counts by Plan, host A's series included. Rounds 5 and 6 run
	// on fresh processes that differ only in B reporting, so what round 6
	// holds beyond round 5 is B's.
	if held := fixture.heldForNoOpenAlert() - heldBefore; held <= heldInRound5 {
		t.Fatalf("round 6 held %d recoveries for no open alert and round 5 held %d; want more, B's among them: the "+
			"recovery is decided and held by name", held, heldInRound5)
	}
	if firstAbsent, _, held := fixture.absenceOf(lifecycleHostB); !held || firstAbsent != 0 {
		t.Fatalf("after B reported, its memory holds an absence from %d (held %t); want it present", firstAbsent, held)
	}

	// The consumer publishes the alert it holds, and the next round closes it.
	fixture.publishOpenAlert(raised.DedupeMD5)
	fixture.waitOpenAlertSetRead(6)
	fixture.mustAttempt(7)
	sent := fixture.noDataEventsFor(lifecycleHostB)[1:]
	if len(sent) != 1 || sent[0].EventKind != contract.TriggerEventRecovery || sent[0].EvaluationTime != fixture.evaluationAt(7) {
		t.Fatalf("round 7 sent host B's no-data events %+v, want one RECOVERY", sent)
	}
	closed := sent[0]
	if closed.DedupeMD5 != raised.DedupeMD5 || closed.RecordRef.DimensionIdentityDigest != raised.RecordRef.DimensionIdentityDigest {
		t.Fatalf("the closing record is alert %q identity %q and the raising one, minted by another process, was alert %q "+
			"identity %q; a close under another identity leaves the alert open", closed.DedupeMD5,
			closed.RecordRef.DimensionIdentityDigest, raised.DedupeMD5, raised.RecordRef.DimensionIdentityDigest)
	}
}

// A Query Group handed to another Worker carries its no-data memory and its
// trigger with it, because both live in the store and not in the Worker.
//
// Worker 0 runs the first two absent rounds and stops; worker 1, a different
// identity, takes the Query Group over and runs the third. With continuous 3
// the third consecutive absent point raises (P9), so the alert can only be
// raised on round 3 if worker 1 counts the two points worker 0 recorded, and
// B's absence still starts at round 1 only if worker 1 reads the memory
// worker 0 wrote rather than starting its own (no-data tracking retention
// proposal, section 9, storage). Worker 1 then closes the alert it raised
// when B returns, under the alert's identity.
func TestANoDataAbsenceCarriesOverAWorkerHandover(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, continuous: 3,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}, nil)
	opened := fixture.evaluationAt(1)
	fixture.serve(false, 5, lifecycleHostA)
	for round := int64(1); round <= 2; round++ {
		fixture.mustAttempt(round)
		if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 0 {
			t.Fatalf("worker 0 sent host B's no-data events %+v by round %d; continuous 3 raises on the third point", sent, round)
		}
	}
	keyBefore := fixture.memoryKeys()

	fixture.replace("alarmd-worker-1")
	if firstAbsent, _, _ := fixture.absenceOf(lifecycleHostB); firstAbsent != opened {
		t.Fatalf("before worker 1 runs a round, B's absence starts at %d, want %d", firstAbsent, opened)
	}
	fixture.mustAttempt(3)
	sent := fixture.noDataEventsFor(lifecycleHostB)
	if len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal || sent[0].EvaluationTime != fixture.evaluationAt(3) {
		t.Fatalf("worker 1's first round sent host B's no-data events %+v, want the ABNORMAL of round 3: the two points "+
			"worker 0 recorded count toward it", sent)
	}
	if firstAbsent, _, _ := fixture.absenceOf(lifecycleHostB); firstAbsent != opened {
		t.Fatalf("after worker 1's round, B's absence starts at %d, want %d, the round worker 0 opened it on", firstAbsent, opened)
	}
	if keyAfter := fixture.memoryKeys(); !reflect.DeepEqual(keyAfter, keyBefore) {
		t.Fatalf("no-data memory keys %v after the handover, %v before; worker 1 must read and write the record worker 0 kept",
			keyAfter, keyBefore)
	}

	fixture.serve(false, 5, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(4)
	sent = fixture.noDataEventsFor(lifecycleHostB)
	if len(sent) != 2 || sent[1].EventKind != contract.TriggerEventRecovery || sent[1].DedupeMD5 != sent[0].DedupeMD5 ||
		sent[1].RecordRef.DimensionIdentityDigest != sent[0].RecordRef.DimensionIdentityDigest {
		t.Fatalf("after B returned, host B's no-data events are %+v; want the ABNORMAL then one RECOVERY under its identity", sent)
	}
}

// A new Segment whose only change is no-data's keeps the Plan's state
// generation, so the absence it was tracking goes on from where it started.
//
// The strategy changes its tracking horizon to 180 seconds. The horizon is
// frozen into the Plan's content, so the change cuts a new Segment, and it is
// kept out of the state-compatible hash on purpose (decision-018, section 5.1
// item 3: the no-data memory is keyed by the state generation, and a horizon
// in the hash would move the key and restart every absence). So across the
// Segment change the memory record and the runtime state keep their keys, and
// B's absence keeps the round it started on, round 1.
//
// The horizon then shows both facts at once. An absence ends when the round's
// evaluation time minus its first absent round reaches the horizon (no-data
// tracking retention proposal, section 1 item 2), the comparison inclusive:
// counted from round 1 that is round 4, or the new Segment's first round if it
// starts later. An absence restarted by the new Segment would run three rounds
// past that Segment's start instead, and a Segment that kept the old horizon
// would never end it.
func TestANewSegmentThatOnlyChangesNoDataKeepsTheAbsenceItTracks(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, continuous: 1,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}
	fixture := startLifecycleFixture(t, strategy, nil)
	opened := fixture.evaluationAt(1)
	fixture.serve(false, 5, lifecycleHostA)
	fixture.mustAttempt(1)
	if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host B's no-data events %+v, want one ABNORMAL", sent)
	}
	memoryBefore, runtimeBefore := fixture.memoryKeys(), generationsOf(t, fixture.runtimeStateKeys())
	segmentBefore := fixture.segmentAt(1)

	changed := strategy
	changed.revision, changed.horizon = 8, 180
	first := fixture.change(changed, 1)
	if segment := fixture.segmentAt(first); segment.ObjectDigest == segmentBefore.ObjectDigest {
		t.Fatalf("round %d is in a Segment with the old content digest; the horizon is part of the Plan's content", first)
	}
	expires := max(int64(4), first)
	for round := int64(2); round <= expires+1; round++ {
		before := len(fixture.noDataEventsFor(lifecycleHostB))
		fixture.mustAttempt(round)
		sent := fixture.noDataEventsFor(lifecycleHostB)[before:]
		firstAbsent, suppressedAt, _ := fixture.absenceOf(lifecycleHostB)
		if firstAbsent != opened {
			t.Fatalf("after round %d B's absence starts at %d, want %d: the new Segment (from round %d) keeps it",
				round, firstAbsent, opened, first)
		}
		switch {
		case round < expires:
			if suppressedAt != 0 || len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("round %d (new Segment from %d, absence ends on %d): suppressed at %d, sent %+v; want B still "+
					"tracked and raised", round, first, expires, suppressedAt, sent)
			}
		default:
			if suppressedAt != fixture.evaluationAt(expires) || len(sent) != 0 {
				t.Fatalf("round %d (new Segment from %d): suppressed at %d, sent %+v; want B's tracking stopped on round "+
					"%d (%d), and nothing sent for it", round, first, suppressedAt, sent, expires, fixture.evaluationAt(expires))
			}
		}
	}
	if memoryAfter := fixture.memoryKeys(); !reflect.DeepEqual(memoryAfter, memoryBefore) {
		t.Fatalf("no-data memory keys %v after the new Segment, %v before: the state generation moved", memoryAfter, memoryBefore)
	}
	if runtimeAfter := generationsOf(t, fixture.runtimeStateKeys()); !reflect.DeepEqual(runtimeAfter, runtimeBefore) {
		t.Fatalf("runtime state generations %v after the new Segment, %v before: no-data moved the state-compatible hash",
			runtimeAfter, runtimeBefore)
	}
}

// Turning no-data on for a strategy that detects thresholds does not move the
// thresholds' state generation: no-data is not in the state-compatible hash
// (decision-018, section 5.1 item 3; the no-data Level is outside the
// generation, counterexample 4 of the d21 carry-history design). A generation
// that moved would restart every threshold series' window the moment somebody
// switched no-data on.
func TestTurningNoDataOnKeepsTheThresholdsStateGeneration(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, targets: []string{lifecycleHostA, lifecycleHostB}}
	fixture := startLifecycleFixture(t, strategy, nil)
	fixture.serve(false, 95, lifecycleHostA)
	fixture.mustAttempt(1)
	if raised := fixture.thresholdEventsFor(lifecycleHostA); len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host A's threshold events %+v, want one ABNORMAL", raised)
	}
	before := generationsOf(t, fixture.runtimeStateKeys())
	if len(before) != 1 {
		t.Fatalf("runtime state generations %v before no-data is turned on, want one", before)
	}
	changed := strategy
	changed.revision, changed.noDataOff, changed.continuous, changed.dimensions = 8, false, 1, lifecycleHostPair
	first := fixture.change(changed, 1)
	for round := int64(2); round <= first; round++ {
		fixture.mustAttempt(round)
	}
	if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) == 0 {
		t.Fatalf("no no-data event for B by round %d, the first under the new Segment; the change did not take", first)
	}
	if after := generationsOf(t, fixture.runtimeStateKeys()); !reflect.DeepEqual(after, before) {
		t.Fatalf("runtime state generations %v after no-data was turned on, %v before", after, before)
	}
}

// generationsOf is the set of state generations a list of the Plan's runtime
// state keys is written under: the segment after the strategy id.
func generationsOf(t *testing.T, keys []string) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, key := range keys {
		_, rest, found := strings.Cut(key, ":"+lifecycleStrategyID+":")
		if !found {
			t.Fatalf("runtime state key %q does not name the strategy", key)
		}
		generation, _, _ := strings.Cut(rest, ":")
		seen[generation] = true
	}
	generations := make([]string, 0, len(seen))
	for generation := range seen {
		generations = append(generations, generation)
	}
	if len(generations) > 1 {
		t.Fatalf("runtime state keys under %d generations: %v", len(generations), keys)
	}
	return generations
}

// A crash after the round's output was acknowledged and its no-data memory
// written, and before its Progress committed, is retried by the next process
// without a second alert and without the memory moving again.
//
// The window is the one between the last two of the round's ordered writes
// (no-data tracking retention proposal, section 4 item 5: the output is
// acknowledged, then State and the no-data memory, then Progress). The case
// builds it from what the store holds after such a crash rather than by
// killing a process: round 2 runs to completion, and Progress is then put back
// to what it held after round 1, which is the store's content had the process
// died before the commit. A new process then finds round 2 due again.
//
// What the retry may do is fixed by decision-008, section 6: the memory's
// statement identity is its apply version and memory digest, judged before
// any revision, so a same-Slot retry after a half commit is ALREADY_APPLIED
// and never a conflict; and a round whose memory would be unchanged writes
// nothing at all. Either way the stored record is byte for byte the one round
// 2 wrote. The alert is not sent twice: the State written before the crash
// already carries round 2's decision, as for a threshold series in the same
// window (the worker's crash-window case "state after progress before": one
// event). Progress then moves past round 2, and round 3 carries on from B's
// absence as round 2 opened it.
func TestARetryAfterACrashBetweenTheMemoryAndProgressSendsNothingTwice(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, continuous: 1,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}, nil)
	ctx := context.Background()
	fixture.serve(false, 5, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	if sent := fixture.allNoDataEvents(); len(sent) != 0 {
		t.Fatalf("round 1, with both hosts reporting, sent no-data events %+v", sent)
	}
	progressKey := fixture.progressKey()
	progressAfterRound1, err := fixture.redis.Dump(ctx, progressKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	valueAfterRound1, err := fixture.redis.Get(ctx, progressKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := fixture.redis.PTTL(ctx, progressKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl < 0 {
		ttl = 0
	}

	// Round 2: B stops reporting; its absence opens and its alert is raised.
	fixture.serve(false, 5, lifecycleHostA)
	fixture.mustAttempt(2)
	raised := fixture.noDataEventsFor(lifecycleHostB)
	if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal || raised[0].EvaluationTime != fixture.evaluationAt(2) {
		t.Fatalf("round 2 sent host B's no-data events %+v, want one ABNORMAL", raised)
	}
	memoryAfterRound2 := fixture.memoryRecord()
	if firstAbsent, _, _ := fixture.absenceOf(lifecycleHostB); firstAbsent != fixture.evaluationAt(2) {
		t.Fatalf("after round 2 B's absence starts at %d, want %d", firstAbsent, fixture.evaluationAt(2))
	}
	if committed, _ := fixture.redis.Get(ctx, progressKey).Result(); committed == valueAfterRound1 {
		t.Fatal("round 2 did not commit Progress, so putting it back builds nothing")
	}

	// The crash: the process is gone and Progress never moved past round 1.
	fixture.closeBundle(fixture.bundle)
	if err := fixture.redis.RestoreReplace(ctx, progressKey, ttl, progressAfterRound1).Err(); err != nil {
		t.Fatal(err)
	}
	if next, _ := fixture.progressSlots(); next != fixture.evaluationAt(2) {
		t.Fatalf("after the crash Progress names %d as next, want round 2's Slot %d", next, fixture.evaluationAt(2))
	}
	writesBefore := len(fixture.memoryWrites())
	fixture.open("alarmd-worker-0", nil)
	fixture.mustAttempt(2)
	if next, lastFull := fixture.progressSlots(); lastFull != fixture.evaluationAt(2) || next != fixture.evaluationAt(3) {
		t.Fatalf("after the retry Progress has last full %d and next %d, want round 2's Slot %d completed and round 3's "+
			"%d next", lastFull, next, fixture.evaluationAt(2), fixture.evaluationAt(3))
	}

	if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 1 {
		t.Fatalf("the retry of round 2 sent host B's no-data events %+v beside the first; the alert went out once already",
			sent[1:])
	}
	for _, outcome := range fixture.memoryWrites()[writesBefore:] {
		if outcome != "ALREADY_APPLIED" {
			t.Fatalf("the retry of round 2 wrote the memory with outcome %s; a same-Slot retry is ALREADY_APPLIED or "+
				"writes nothing", outcome)
		}
	}
	if memory := fixture.memoryRecord(); !reflect.DeepEqual(memory, memoryAfterRound2) {
		t.Fatalf("the memory after the retry is %v, and round 2 left %v; the retry must leave round 2's record as it was",
			memory, memoryAfterRound2)
	}

	fixture.mustAttempt(3)
	if firstAbsent, _, _ := fixture.absenceOf(lifecycleHostB); firstAbsent != fixture.evaluationAt(2) {
		t.Fatalf("after round 3 B's absence starts at %d, want %d, the round that opened it", firstAbsent, fixture.evaluationAt(2))
	}
	if sent := fixture.noDataEventsFor(lifecycleHostB); len(sent) != 2 || sent[1].EvaluationTime != fixture.evaluationAt(3) {
		t.Fatalf("by round 3 host B's no-data events are %+v; want round 2's and round 3's", sent)
	}
}
