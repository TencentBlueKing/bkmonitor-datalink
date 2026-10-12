// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The watchdog that lets go of a hung execution, run on the production
// bundle over a real redis-server (design 02 section 6.5). It judges an
// execution by the deadline that execution runs under, and never inside its
// commit. Both were wrong once, in the same way: the deadline it read was the
// frozen Slot's, which is the Slot's first attempt's. A replay of a Slot
// taken over from another worker runs minutes after that deadline by design,
// under a deadline access derives from the replay's own arrival, so every
// replay that started more than a minute late was declined on arrival, its
// lease released, and the next holder replayed it and declined it the same
// way. And a decline that landed between a Slot's acknowledged output and its
// State write left that output unapplied, for the next holder to send again.

// lateReplay is a Slot worker 0 began and left unfinished, taken over by
// worker 1 and being replayed there, late, with its query held.
type lateReplay struct {
	fixture *lifecycleFixture
	// evaluation is the Slot's evaluation time, frozenDeadline the query
	// deadline frozen into it at its first attempt, and arrival the instant
	// the replay arrived at access: the bundle's clock, which stands still
	// while the query is held.
	evaluation     int64
	frozenDeadline int64
	arrival        int64
	release        func()
	ran            chan error
}

// startLateTakeoverReplay has worker 0 run round 1, which raises host A, and
// not learn whether its output was acknowledged, so the Slot is left
// unfinished in Progress; stops worker 0 and starts worker 1, which takes
// the Query Group over; and has worker 1's dispatcher run the Slot two and a
// half minutes after its frozen deadline, with the replay's query held until
// release is called.
func startLateTakeoverReplay(t *testing.T) *lateReplay {
	t.Helper()
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true,
		targets: []string{lifecycleHostA, lifecycleHostB}}, nil)
	fixture.serve(false, 95, lifecycleHostA)
	fixture.sink.setMode(followsACKUnknown)
	if !fixture.attempt(1, 0) {
		t.Fatal("worker 0 did not attempt round 1")
	}
	evaluation, frozenDeadline, found := fixture.unfinishedSlot()
	if !found || evaluation != fixture.evaluationAt(1) {
		t.Fatalf("Progress after the unacknowledged round holds unfinished Slot %d (found %t), want round 1's, %d: "+
			"the takeover below has no Slot to replay", evaluation, found, fixture.evaluationAt(1))
	}
	fixture.sink.setMode(followsOK)

	fixture.replace("alarmd-worker-1")
	gate, entered := holdQueries(fixture)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	// Two and a half minutes past the frozen deadline, inside the replay age
	// and well past the minute's grace. Ahead of the wall clock, as the
	// fixture's base is: the query deadline is handed to the HTTP client as
	// an absolute instant, which must not have passed in real time.
	arrival := frozenDeadline + (150 * time.Second).Milliseconds()
	fixture.clock.Store(arrival)
	ran := make(chan error, 1)
	go func() { ran <- runScheduledOnceSettled(context.Background(), fixture.bundle) }()
	select {
	case <-entered:
	case <-time.After(lifecycleWatchdog):
		t.Fatalf("worker 1 sent no query for the taken-over Slot within %s", lifecycleWatchdog)
	}
	return &lateReplay{fixture: fixture, evaluation: evaluation, frozenDeadline: frozenDeadline, arrival: arrival,
		release: release, ran: ran}
}

// holdQueries holds every query from now on until gate is closed, telling
// entered (without blocking) each time one arrives. A held query is answered
// as the fixture answers when the gate opens, and not at all once its request
// is cancelled.
func holdQueries(fixture *lifecycleFixture) (gate, entered chan struct{}) {
	gate, entered = make(chan struct{}), make(chan struct{}, 8)
	fixture.interceptQueries(func(_ http.ResponseWriter, request *http.Request) bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
			return false
		case <-request.Context().Done():
			return true
		}
	})
	return gate, entered
}

// unfinishedSlot is the Slot the stored Progress has begun and not
// finished, and the query deadline frozen into it at its first attempt.
func (fixture *lifecycleFixture) unfinishedSlot() (evaluation, frozenDeadline int64, found bool) {
	fixture.t.Helper()
	raw, err := fixture.redis.Get(context.Background(), fixture.progressKey()).Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	var stored struct {
		Progress struct {
			UnfinishedSlot *struct {
				Contract struct {
					Slot struct{ EvaluationTime int64 }
				}
				EarliestQueryDeadlineUnixMilli int64
			}
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		fixture.t.Fatalf("Progress %q does not decode: %v", raw, err)
	}
	unfinished := stored.Progress.UnfinishedSlot
	if unfinished == nil {
		return 0, 0, false
	}
	return unfinished.Contract.Slot.EvaluationTime, unfinished.EarliestQueryDeadlineUnixMilli, true
}

// hungFacts is every hung execution observed so far, declined or returned.
func (fixture *lifecycleFixture) hungFacts() []observability.ExecutionHungFacts {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	var facts []observability.ExecutionHungFacts
	for _, observation := range fixture.observations {
		if observation.ExecutionHung != nil {
			facts = append(facts, *observation.ExecutionHung)
		}
	}
	return facts
}

// replayedAfterTakeover says whether the Slot at evaluation was classified a
// replay of a Slot due before the takeover.
func (fixture *lifecycleFixture) replayedAfterTakeover(evaluation int64) bool {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	for _, observation := range fixture.observations {
		if observation.ReplayTakeover != nil && observation.ReplayTakeover.Outcome == observability.ReplayTakeoverReplayed &&
			observation.Trace.EvaluationTime == evaluation {
			return true
		}
	}
	return false
}

// waitScheduled waits for the dispatcher pass a case started to return.
func waitScheduled(t *testing.T, ran <-chan error) {
	t.Helper()
	select {
	case err := <-ran:
		if err != nil {
			t.Fatalf("the dispatcher pass returned %v", err)
		}
	case <-time.After(lifecycleWatchdog):
		t.Fatalf("the dispatcher pass did not return within %s", lifecycleWatchdog)
	}
}

// A takeover replay that starts two and a half minutes after its Slot's
// frozen query deadline and is working normally - its query in flight,
// inside the deadline access derived from its arrival - is not declined, and
// completes. Judged by the frozen deadline it was declined on arrival, which
// is the loop the replicas fell into: each new holder replayed the Slot and
// declined it again.
func TestALateTakeoverReplayWorkingNormallyIsNotDeclined(t *testing.T) {
	replay := startLateTakeoverReplay(t)
	fixture := replay.fixture
	if !fixture.replayedAfterTakeover(replay.evaluation) {
		t.Fatalf("worker 1 did not replay Slot %d as one due before its takeover; the case is about that replay",
			replay.evaluation)
	}
	now := time.UnixMilli(fixture.clock.Load())
	if lateBy := now.Sub(time.UnixMilli(replay.frozenDeadline)); lateBy <= executionPastDeadlineGrace {
		t.Fatalf("the replay runs %s after its frozen deadline, want more than the %s grace: otherwise the "+
			"frozen deadline would not have declined it either", lateBy, executionPastDeadlineGrace)
	}

	fixture.bundle.declineHungExecutions(now)
	if heldRunner(fixture.bundle, fixture.queryGroup) == nil {
		t.Fatal("the replay's Query Group was let go while its query was in flight inside its own deadline")
	}
	if declined := fixture.bundle.declinedRegistration(); declined != nil {
		t.Fatalf("worker 1 declines %+v while the replay works normally, want nothing declined", declined)
	}
	if hung := fixture.hungFacts(); len(hung) != 0 {
		t.Fatalf("hung executions %+v while the replay works normally, want none", hung)
	}

	replay.release()
	waitScheduled(t, replay.ran)
	if _, _, unfinished := fixture.unfinishedSlot(); unfinished {
		t.Fatal("the replay returned and the Slot is still unfinished")
	}
	if next, _ := fixture.progressSlots(); next <= replay.evaluation {
		t.Fatalf("Progress next Slot %d after the replay, want past Slot %d", next, replay.evaluation)
	}
	if keys := fixture.runtimeStateKeys(); len(keys) == 0 {
		t.Fatal("the replay completed and wrote no runtime state")
	}
	fixture.bundle.declineHungExecutions(time.UnixMilli(fixture.clock.Load()))
	if hung := fixture.hungFacts(); len(hung) != 0 {
		t.Fatalf("hung executions %+v after the replay completed, want none", hung)
	}
}

// The same replay, its query still held a minute past the replay's own
// deadline - its arrival plus the period less the downstream reserve, which
// is how long access gives a recovery to read - is hung, and declined, named
// by that deadline and the stage it is stuck in. The bound moved; the
// watchdog still has one. When the held query does return, the declined
// execution writes nothing: its Query Group is already let go, and an output
// written now is one the next holder would send again.
func TestATakeoverReplayHungPastItsOwnDeadlineIsDeclined(t *testing.T) {
	replay := startLateTakeoverReplay(t)
	fixture := replay.fixture
	budget := time.Duration(fixture.interval)*time.Second - fixture.cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	ownDeadline := time.UnixMilli(replay.arrival).Add(budget)
	now := ownDeadline.Add(executionPastDeadlineGrace + time.Second)
	fixture.clock.Store(now.UnixMilli())

	fixture.bundle.declineHungExecutions(now)
	if heldRunner(fixture.bundle, fixture.queryGroup) != nil {
		t.Fatal("a replay hung a minute past its own deadline is still a current runner")
	}
	var declined []observability.ExecutionHungFacts
	for deadline := time.Now().Add(lifecycleWatchdog); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		declined = declined[:0]
		for _, facts := range fixture.hungFacts() {
			if facts.Outcome == observability.ExecutionHungDeclined {
				declined = append(declined, facts)
			}
		}
		if len(declined) > 0 {
			break
		}
	}
	if len(declined) != 1 {
		t.Fatalf("declined hung executions %+v, want the one replay", declined)
	}
	if declined[0].DeadlineUnixMilli != ownDeadline.UnixMilli() || declined[0].Stage != execution.SlotStageQuery {
		t.Fatalf("the decline names deadline %d in stage %q, want the replay's own deadline %d (its arrival %d plus "+
			"%s), not the frozen %d, in stage %q", declined[0].DeadlineUnixMilli, declined[0].Stage, ownDeadline.UnixMilli(),
			replay.arrival, budget, replay.frozenDeadline, execution.SlotStageQuery)
	}
	if past := now.Sub(ownDeadline).Milliseconds(); declined[0].PastDeadlineMS < past {
		t.Fatalf("the decline says %d ms past the deadline, want at least %d", declined[0].PastDeadlineMS, past)
	}

	// The decline does not wait for the execution; it returns when its query
	// does, with an answer that raises host A.
	attempted := fixture.sink.attemptedEvents()
	replay.release()
	waitScheduled(t, replay.ran)
	returned := false
	for _, facts := range fixture.hungFacts() {
		returned = returned || facts.Outcome == observability.ExecutionHungReturned
	}
	if !returned {
		t.Fatalf("hung executions %+v after the declined replay returned, want its return recorded", fixture.hungFacts())
	}
	if after := fixture.sink.attemptedEvents(); after != attempted {
		t.Fatalf("the declined replay handed %d events to the sink after its decline, want none", after-attempted)
	}
	if evaluation, _, unfinished := fixture.unfinishedSlot(); !unfinished || evaluation != replay.evaluation {
		t.Fatalf("after the declined replay returned, Progress holds unfinished Slot %d (found %t), want Slot %d "+
			"left for the next holder", evaluation, unfinished, replay.evaluation)
	}
}

// attemptedEvents is how many events the sink has been handed, written or
// not.
func (sink *followsSink) attemptedEvents() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return len(sink.attempted)
}

// An execution that has begun to write its output is never declined, however
// far past its deadline: the State and Progress writes after the output run
// under their own bounded timeouts, and releasing the lease between them is
// what left acknowledged output unapplied. Here round 1 raises host A and
// its output write is held while the clock moves ten minutes past the
// Slot's deadline; the watchdog leaves it alone, and once the write returns
// the Slot's State and Progress land.
func TestAnExecutionInsideItsCommitIsNotDeclinedAndItsStateLands(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true,
		targets: []string{lifecycleHostA, lifecycleHostB}}, nil)
	fixture.serve(false, 95, lifecycleHostA)
	gate, entered := make(chan struct{}), make(chan struct{}, 8)
	fixture.sink.holdWrites(gate, entered)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	if keys := fixture.runtimeStateKeys(); len(keys) != 0 {
		t.Fatalf("runtime state keys %v before round 1, want none", keys)
	}

	fixture.clock.Store((fixture.evaluationAt(1) + fixture.interval/2) * 1000)
	ran := make(chan error, 1)
	go func() { ran <- runScheduledOnceSettled(context.Background(), fixture.bundle) }()
	select {
	case <-entered:
	case <-time.After(lifecycleWatchdog):
		t.Fatalf("round 1 wrote no output within %s", lifecycleWatchdog)
	}
	evaluation, frozenDeadline, found := fixture.unfinishedSlot()
	if !found || evaluation != fixture.evaluationAt(1) {
		t.Fatalf("Progress during round 1's output holds unfinished Slot %d (found %t), want round 1's, %d",
			evaluation, found, fixture.evaluationAt(1))
	}
	now := time.UnixMilli(frozenDeadline).Add(10 * time.Minute)
	fixture.clock.Store(now.UnixMilli())

	fixture.bundle.declineHungExecutions(now)
	if heldRunner(fixture.bundle, fixture.queryGroup) == nil {
		t.Fatal("the Query Group was let go while its Slot was inside its output write")
	}
	if declined := fixture.bundle.declinedRegistration(); declined != nil {
		t.Fatalf("the replica declines %+v while a Slot is inside its commit, want nothing declined", declined)
	}

	release()
	waitScheduled(t, ran)
	if hung := fixture.hungFacts(); len(hung) != 0 {
		t.Fatalf("hung executions %+v, want none: the Slot was inside its commit", hung)
	}
	if raised := fixture.thresholdEventsFor(lifecycleHostA); len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 wrote host A's events %+v, want one ABNORMAL", raised)
	}
	if keys := fixture.runtimeStateKeys(); len(keys) == 0 {
		t.Fatal("no runtime state was written: the output landed and the State it follows did not")
	}
	if _, _, unfinished := fixture.unfinishedSlot(); unfinished {
		t.Fatal("round 1's Slot is still unfinished after its output was written")
	}
	if next, _ := fixture.progressSlots(); next <= evaluation {
		t.Fatalf("Progress next Slot %d after round 1, want past Slot %d", next, evaluation)
	}
}

// A rollover with several unfinished Slots - one per Query Group, as
// Progress keeps one each - has every one of them replayed late by the new
// holder and none declined. Each replay is judged at the moment its query
// arrives, while it is in flight, and a sweep judges the pass throughout,
// so a replay is judged in whatever stage the sweep finds it.
func TestARolloverWithSeveralUnfinishedSlotsDeclinesNoReplay(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true,
		targets: []string{lifecycleHostA, lifecycleHostB}}, nil)
	fixture.serve(false, 95, lifecycleHostA)
	fixture.addSecondStrategy()
	fixture.queryGroups = 2

	// Worker 0 runs both Query Groups and learns of no output's
	// acknowledgement: two Slots unfinished, one per Query Group. The second
	// Query Group's first Segment starts where the Leader put it when it
	// was published, which may be a round later than the first's.
	fixture.sink.setMode(followsACKUnknown)
	var unfinished map[string]unfinishedProgressSlot
	for round := int64(1); round <= 3 && len(unfinished) < 2; round++ {
		fixture.clock.Store((fixture.evaluationAt(round) + fixture.interval/2) * 1000)
		if err := runScheduledOnceSettled(context.Background(), fixture.bundle); err != nil {
			t.Fatalf("worker 0's pass of round %d: %v", round, err)
		}
		unfinished = fixture.unfinishedSlotsByProgress()
	}
	if len(unfinished) != 2 {
		t.Fatalf("unfinished Slots after worker 0's pass %+v, want one in each of the two Query Groups", unfinished)
	}
	latestDeadline := int64(0)
	for _, slot := range unfinished {
		latestDeadline = max(latestDeadline, slot.frozenDeadline)
	}
	fixture.sink.setMode(followsOK)

	fixture.replace("alarmd-worker-1")
	// Every Slot two and a half minutes past its frozen deadline at least.
	now := time.UnixMilli(latestDeadline + (150 * time.Second).Milliseconds())
	fixture.clock.Store(now.UnixMilli())
	var judgedMu sync.Mutex
	judgedAtQuery := 0
	fixture.interceptQueries(func(http.ResponseWriter, *http.Request) bool {
		fixture.bundle.declineHungExecutions(now)
		judgedMu.Lock()
		judgedAtQuery++
		judgedMu.Unlock()
		return false
	})
	ran := make(chan error, 1)
	go func() { ran <- runScheduledOnceSettled(context.Background(), fixture.bundle) }()
	sweeps := 0
sweep:
	for {
		select {
		case err := <-ran:
			if err != nil {
				t.Fatalf("worker 1's pass: %v", err)
			}
			break sweep
		case <-time.After(time.Millisecond):
			fixture.bundle.declineHungExecutions(now)
			sweeps++
		}
	}

	if hung := fixture.hungFacts(); len(hung) != 0 {
		t.Fatalf("hung executions %+v across the replays of a rollover, want none", hung)
	}
	judgedMu.Lock()
	judged := judgedAtQuery
	judgedMu.Unlock()
	if judged < len(unfinished) {
		t.Fatalf("the watchdog judged %d replays at their query, want each of the %d", judged, len(unfinished))
	}
	for key, slot := range unfinished {
		if !fixture.replayedAfterTakeover(slot.evaluation) {
			t.Fatalf("Slot %d of %s was not replayed as one due before the takeover", slot.evaluation, key)
		}
		if lateBy := now.Sub(time.UnixMilli(slot.frozenDeadline)); lateBy <= executionPastDeadlineGrace {
			t.Fatalf("Slot %d is replayed %s after its frozen deadline, want past the grace", slot.evaluation, lateBy)
		}
	}
	if left := fixture.unfinishedSlotsByProgress(); len(left) != 0 {
		t.Fatalf("Slots still unfinished after worker 1's pass: %+v (swept %d times)", left, sweeps)
	}
}

// unfinishedProgressSlot is one Query Group's unfinished Slot as its stored
// Progress holds it.
type unfinishedProgressSlot struct {
	evaluation     int64
	frozenDeadline int64
}

// unfinishedSlotsByProgress is every stored Progress's unfinished Slot, by
// Progress key; a Progress with none is left out.
func (fixture *lifecycleFixture) unfinishedSlotsByProgress() map[string]unfinishedProgressSlot {
	fixture.t.Helper()
	ctx := context.Background()
	keys, err := fixture.redis.Keys(ctx, fixture.cfg.Redis.StatePrefix+":*:schedule:progress").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	slots := map[string]unfinishedProgressSlot{}
	for _, key := range keys {
		raw, err := fixture.redis.Get(ctx, key).Result()
		if err != nil {
			fixture.t.Fatal(err)
		}
		var stored struct {
			Progress struct {
				UnfinishedSlot *struct {
					Contract struct {
						Slot struct{ EvaluationTime int64 }
					}
					EarliestQueryDeadlineUnixMilli int64
				}
			} `json:"progress"`
		}
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			fixture.t.Fatalf("Progress %q does not decode: %v", raw, err)
		}
		if unfinished := stored.Progress.UnfinishedSlot; unfinished != nil {
			slots[key] = unfinishedProgressSlot{evaluation: unfinished.Contract.Slot.EvaluationTime,
				frozenDeadline: unfinished.EarliestQueryDeadlineUnixMilli}
		}
	}
	return slots
}

// addSecondStrategy stores a second strategy beside the fixture's, the same
// but for the metric it reads - so its query is another and it is a Query
// Group of its own - and has the Leader publish it, waiting until the
// current bundle owns both Query Groups.
func (fixture *lifecycleFixture) addSecondStrategy() {
	t := fixture.t
	t.Helper()
	ctx := context.Background()
	raw, err := fixture.redis.Get(ctx, "alarm-config.strategy_"+lifecycleStrategyID).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["id"] = 102
	document["name"] = "hung execution second"
	item := document["items"].([]any)[0].(map[string]any)
	item["id"] = 12
	item["query_md5"] = "hung-wiring-second-query"
	item["query_configs"].([]any)[0].(map[string]any)["metric_field"] = "idle"
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids": `[` + lifecycleStrategyID + `,102]`,
		"alarm-config.strategy_102": encoded,
		"alarm-config.last_updated": "1725000100",
	} {
		if err := fixture.redis.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(lifecycleWatchdog); ; time.Sleep(20 * time.Millisecond) {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after the second strategy: %v", err)
		}
		fixture.bundle.mu.RLock()
		groups := append([]execution.QueryGroupIdentity(nil), fixture.bundle.queryGroups...)
		fixture.bundle.mu.RUnlock()
		settled := len(groups) == 2
		for _, group := range groups {
			settled = settled && settledRunner(fixture.bundle, group) != nil
		}
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the bundle owns %v %s after the second strategy, want two Query Groups", groups, lifecycleWatchdog)
		}
	}
}
