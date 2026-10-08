// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"container/heap"
	"context"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A late series that arrives before the Query Group's next Slot reads is
// in that read, and a supplement of its own Slot after it finds the series'
// State already past the Slot (crossed_t): the rung a directed Slot is read
// again at, 1.5 steps after its first read, comes after the next Slot's
// first read whenever the Slot runs every step. So a directed Slot whose
// next Slot reads before its rung is read once more before that: the early
// read. Its late series are supplemented at once, under a guard asked with
// the Query Group's flight held -- no later Slot of the group has begun --
// so a supplement that runs is one ahead of the next Slot (before_next).
//
// The read at the rung still follows, for the series that arrive after the
// next Slot's first read: an early read alone would leave those to be
// decided by the Slot after, with this Slot's supplement already done and
// nothing counted. It reads what the early read has not supplemented.
//
// When the next Slot reads comes from the frozen schedule: the Slot it has
// after this one (Query.FollowingSlot) plus the query's readiness offset,
// the same for the same query. The early read must be done by then, so it
// starts at the latest a lead before: the longest of the Query Group's last
// readingsKept first reads, plus the longest of its last readingsKept
// supplement holds. Early reads due at the same second start earlier by as
// many rounds of the process's free query permits as they need, and one
// refused a permit tries again after a read's time, until its latest start.

// Early read outcomes, closed: what became of each directed Slot's early
// read. Every directed Slot comes to exactly one.
const (
	EarlyBeforeNext          = "before_next"
	EarlyNothingLate         = "nothing_late"
	EarlyRungFirst           = "rung_first"
	EarlyMultiQuery          = "multi_query"
	EarlyFirstReadIncomplete = "first_read_incomplete"
	EarlyFirstReadRefused    = "first_read_refused"
	EarlyOwnerLost           = "owner_lost"
	EarlyAnchorUnknown       = "anchor_unknown"
	EarlyOvertaken           = "overtaken"
	EarlyOlderSlotPending    = "older_slot_pending"
	EarlyYielded             = "yielded"
	EarlyPermitRefused       = "permit_refused"
	EarlyAnchorPassed        = "anchor_passed"
	EarlyReadFailed          = "early_read_failed"
	EarlyMemoryRefused       = "early_memory_refused"
	EarlyFlightBusy          = "flight_busy"
	EarlyContractExpired     = "contract_expired"
	EarlyFailed              = "failed"
)

// EarlyOutcomes is every early read outcome.
var EarlyOutcomes = []string{EarlyBeforeNext, EarlyNothingLate, EarlyRungFirst, EarlyMultiQuery, EarlyFirstReadIncomplete,
	EarlyFirstReadRefused, EarlyOwnerLost, EarlyAnchorUnknown, EarlyOvertaken, EarlyOlderSlotPending, EarlyYielded,
	EarlyPermitRefused, EarlyAnchorPassed, EarlyReadFailed, EarlyMemoryRefused, EarlyFlightBusy, EarlyContractExpired,
	EarlyFailed}

// EarlyOutsideMechanism are the outcomes the early read's working is not
// read by: a Slot it does not apply to, one it could not be anchored for,
// or one read with nothing late. The rest are its attempts, before_next the
// ones that worked.
var EarlyOutsideMechanism = map[string]bool{EarlyNothingLate: true, EarlyRungFirst: true, EarlyMultiQuery: true,
	EarlyFirstReadIncomplete: true, EarlyFirstReadRefused: true, EarlyOwnerLost: true, EarlyAnchorUnknown: true}

// readingsKept is how many of a Query Group's first reads and supplement
// holds its lead is the longest of: when readings can be exchanged, the next
// is longer than the longest of sixteen one time in seventeen.
const readingsKept = 16

// durations are the last readingsKept of a kind of reading.
type durations struct {
	values [readingsKept]time.Duration
	count  int
	next   int
}

func (readings *durations) add(value time.Duration) {
	readings.values[readings.next] = value
	readings.next = (readings.next + 1) % readingsKept
	readings.count = min(readings.count+1, readingsKept)
}

func (readings *durations) longest() time.Duration {
	longest := time.Duration(0)
	for index := 0; index < readings.count; index++ {
		longest = max(longest, readings.values[index])
	}
	return longest
}

// earlyRead is one directed Slot's early read.
type earlyRead struct {
	// next is when the next Slot reads, latest the latest the early read may
	// start to be done by then; retryAt is when a read refused a permit
	// tries again.
	next    time.Time
	latest  time.Time
	retryAt time.Time
	// tried is set once a permit was asked for, yielded once a read was
	// stopped for a formal query; running while a read is in flight, done
	// once the read has its outcome.
	tried, yielded, running, done bool
	queued                        bool
	outcome                       string
	// readAt is when the read was made, crossed whether its supplement found
	// series past the Slot, and late the series it found late, kept until
	// the Slot is filed.
	readAt  time.Time
	crossed bool
	late    *seriesSet
}

// leadLocked is how long before the next Slot reads a Query Group's early
// read starts at the latest: the longest of its last first reads and of its
// last supplement holds, or of every supplement hold this process has seen
// while the group has had none.
func (engine *Engine) leadLocked(state *group) time.Duration {
	hold := state.holds.longest()
	if state.holds.count == 0 {
		hold = engine.holdMax
	}
	return state.took.longest() + hold
}

// planEarlyLocked decides a directed Slot's early read once its first read
// has ended: none, by why, or one anchored before the next Slot reads.
func (engine *Engine) planEarlyLocked(state *group, slot *directedSlot, now time.Time) {
	if slot.early != nil {
		return
	}
	slot.early = &earlyRead{}
	outcome := ""
	switch {
	case len(slot.queries) != 1:
		outcome = EarlyMultiQuery
	case slot.queries[0].first.refused:
		outcome = EarlyFirstReadRefused
	case !slot.queries[0].complete:
		outcome = EarlyFirstReadIncomplete
	case slot.following <= 0 || slot.readyAt.IsZero():
		outcome = EarlyAnchorUnknown
	}
	if outcome != "" {
		engine.fileEarlyLocked(state, slot, outcome)
		return
	}
	slot.early.next = engine.nextReadLocked(slot)
	if !slot.early.next.Before(slot.readAt.Add(rungDelay(slot.rung, slot.step))) {
		engine.fileEarlyLocked(state, slot, EarlyRungFirst)
		return
	}
	slot.early.latest = slot.early.next.Add(-engine.leadLocked(state))
	if now.After(slot.early.latest) {
		outcome := EarlyAnchorPassed
		frozenNext := time.Unix(int64(slot.following), 0).Add(slot.readyAt.Sub(time.Unix(int64(slot.evaluation), 0)))
		if slot.early.next.Before(frozenNext) {
			outcome = EarlyOvertaken
		}
		engine.fileEarlyLocked(state, slot, outcome)
		return
	}
	engine.earlyPending = append(engine.earlyPending, slot)
}

// ReadyAt already contains h_T. The next Slot uses its own effective h,
// including a deterministic decrease transition, rather than adding h twice.
func (engine *Engine) nextReadLocked(slot *directedSlot) time.Time {
	frozen := time.Duration(slot.readHold) * time.Millisecond
	hold := frozen
	if engine.options.ReadHoldAt != nil {
		hold = engine.options.ReadHoldAt(slot.queryGroup, slot.following)
	} else if engine.options.CurrentReadHold != nil {
		hold = engine.options.CurrentReadHold(slot.queryGroup)
	}
	offset := slot.readyAt.Sub(time.Unix(int64(slot.evaluation), 0)) - frozen + hold
	return time.Unix(int64(slot.following), 0).Add(offset)
}

// fileEarlyLocked counts a directed Slot's early read's outcome.
func (engine *Engine) fileEarlyLocked(state *group, slot *directedSlot, outcome string) {
	if slot.early == nil {
		slot.early = &earlyRead{}
	}
	if slot.early.done {
		return
	}
	slot.early.done, slot.early.outcome, slot.early.running = true, outcome, false
	engine.counts.early[key2(slot.source, outcome)]++
	if state != nil && state.supplement != nil {
		state.supplement.early[outcome]++
	}
}

// earlyStart is when the early reads due at one second start: the latest of
// them less as many rounds of free permits as they need, a read's time each.
func earlyStart(slots []*directedSlot, took map[*directedSlot]time.Duration, free int) time.Time {
	latest, longest := time.Time{}, time.Duration(0)
	for _, slot := range slots {
		if latest.IsZero() || slot.early.latest.Before(latest) {
			latest = slot.early.latest
		}
		longest = max(longest, took[slot])
	}
	return latest.Add(-time.Duration(len(slots)) * longest / time.Duration(max(free, 1)))
}

// olderPendingLocked reports whether a Slot older than slot of the same
// Query Group is still to be filed: a supplement of a Slot runs after those
// of the Slots before it.
func olderPendingLocked(state *group, slot *directedSlot) bool {
	for _, other := range state.directed {
		if other != slot && other.evaluation < slot.evaluation && !other.filed {
			return true
		}
	}
	return false
}

// groupReadingLocked reports whether a directed read of the Query Group is
// in flight.
func groupReadingLocked(state *group) bool {
	for _, other := range state.directed {
		if other.running {
			return true
		}
	}
	return false
}

// dueEarlyLocked settles the early reads past their latest start, and says
// which may start now, by their latest start, with when the loop is next
// needed for them.
func (engine *Engine) dueEarlyLocked(now time.Time, free int) ([]*directedSlot, time.Time) {
	pending := engine.earlyPending[:0]
	bySecond := map[int64][]*directedSlot{}
	took := map[*directedSlot]time.Duration{}
	for _, slot := range engine.earlyPending {
		state := engine.groups[slot.queryGroup]
		if state != nil && !slot.early.done && !slot.early.running {
			next := engine.nextReadLocked(slot)
			previous := slot.early.next
			slot.early.next, slot.early.latest = next, next.Add(-engine.leadLocked(state))
			if next.Before(previous) && now.After(slot.early.latest) {
				engine.fileEarlyLocked(state, slot, EarlyOvertaken)
			} else if !next.Before(slot.readAt.Add(rungDelay(slot.rung, slot.step))) {
				engine.fileEarlyLocked(state, slot, EarlyRungFirst)
			}
		}
		switch {
		case slot.early.done:
			continue
		case slot.dropped || state == nil:
			engine.fileEarlyLocked(state, slot, EarlyOwnerLost)
			continue
		case slot.early.running || slot.early.queued:
			pending = append(pending, slot)
			continue
		case now.After(slot.early.latest):
			engine.fileEarlyLocked(state, slot, engine.missedLocked(state, slot))
			continue
		}
		pending = append(pending, slot)
		second := slot.early.next.Unix()
		bySecond[second] = append(bySecond[second], slot)
		took[slot] = state.took.longest()
	}
	engine.earlyPending = pending
	var due []*directedSlot
	wake := time.Time{}
	later := func(at time.Time) {
		if wake.IsZero() || at.Before(wake) {
			wake = at
		}
	}
	for _, slots := range bySecond {
		start := earlyStart(slots, took, free)
		for _, slot := range slots {
			state := engine.groups[slot.queryGroup]
			at := start
			if slot.early.retryAt.After(at) {
				at = slot.early.retryAt
			}
			switch {
			case now.Before(at):
				later(at)
			case olderPendingLocked(state, slot) || groupReadingLocked(state):
				// Waits for what goes before it, and is settled at its latest
				// start if that has not gone by then.
				later(slot.early.latest)
			default:
				due = append(due, slot)
			}
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].early.latest.Before(due[j].early.latest) })
	return due, wake
}

// missedLocked is why an early read that did not start by its latest start
// did not, the first that holds: the next Slot began, an older Slot was
// still to be filed, a read was stopped for a formal query, a permit was
// refused, or it was never tried.
func (engine *Engine) missedLocked(state *group, slot *directedSlot) string {
	switch {
	case state.lastSlot > slot.evaluation:
		return EarlyOvertaken
	case olderPendingLocked(state, slot):
		return EarlyOlderSlotPending
	case slot.early.yielded:
		return EarlyYielded
	case slot.early.tried:
		return EarlyPermitRefused
	default:
		return EarlyAnchorPassed
	}
}

// StepEarly starts the early reads due now as permits allow, and settles
// those past their latest start. It touches only the Slots waiting for an
// early read, so the loop can run it between its steps.
func (engine *Engine) StepEarly(ctx context.Context) {
	if engine == nil {
		return
	}
	free := 1
	if engine.options.FreePermits != nil {
		free = max(engine.options.FreePermits(), 1)
	}
	now := engine.options.Now()
	engine.mu.Lock()
	engine.earlyQueueLocked(now, free)
	engine.mu.Unlock()
	for {
		engine.mu.Lock()
		if engine.earlyWork.Len() == 0 || now.Before(engine.earlyWork[0].at) {
			engine.mu.Unlock()
			return
		}
		work := heap.Pop(&engine.earlyWork).(earlyWork)
		if work.trial != nil && (work.trial.done || work.trial.formal) || work.slot != nil && (work.slot.dropped || work.slot.early.done) {
			engine.mu.Unlock()
			continue
		}
		if work.slot != nil {
			work.slot.early.queued = false
		}
		engine.mu.Unlock()
		qg := execution.QueryGroupIdentity("")
		if work.slot != nil {
			qg = work.slot.queryGroup
		} else {
			qg = work.trial.query.Contract.Slot.QueryGroup
		}
		if !engine.options.Owns(qg) {
			engine.Forget(qg)
			continue
		}
		release, yield, refused := engine.options.Permit()
		if refused != "" {
			engine.mu.Lock()
			if _, named := engine.counts.refusals[refused]; !named {
				refused = RefusedOther
			}
			engine.counts.refusals[refused]++
			// None is free now: every read due tries again a read's time on,
			// short of its latest start.
			waiting := earlyHeap{work}
			for engine.earlyWork.Len() > 0 && !now.Before(engine.earlyWork[0].at) {
				waiting = append(waiting, heap.Pop(&engine.earlyWork).(earlyWork))
			}
			for _, item := range waiting {
				if item.trial != nil {
					if !item.trial.formal && !item.trial.done {
						item.trial.done, item.trial.outcome = true, EarlierPermitRefused
					}
					continue
				}
				slot := item.slot
				slot.early.queued = false
				state := engine.groups[slot.queryGroup]
				if state == nil {
					continue
				}
				slot.early.tried = true
				retry := now.Add(state.took.longest() / time.Duration(free))
				if retry.After(slot.early.latest) {
					retry = slot.early.latest
				}
				slot.early.retryAt = retry
			}
			engine.mu.Unlock()
			return
		}
		engine.mu.Lock()
		if trial := work.trial; trial != nil {
			if trial.done || trial.formal {
				engine.mu.Unlock()
				release()
				continue
			}
			trial.running = true
			engine.mu.Unlock()
			go engine.earlierRead(ctx, trial, release, yield)
			continue
		}
		slot := work.slot
		if slot.dropped || slot.early.done {
			engine.mu.Unlock()
			release()
			continue
		}
		slot.early.tried, slot.early.running, slot.running = true, true, true
		engine.mu.Unlock()
		go engine.earlyRead(ctx, slot, release, yield)
	}
}

// nextEarlyWake is when the loop is next needed for the early reads, zero
// when none waits.
func (engine *Engine) nextEarlyWake() time.Time {
	if engine == nil {
		return time.Time{}
	}
	free := 1
	if engine.options.FreePermits != nil {
		free = max(engine.options.FreePermits(), 1)
	}
	now := engine.options.Now()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	wake := engine.earlyQueueLocked(now, free)
	if !wake.IsZero() && !now.Before(wake) {
		return now
	}
	return wake
}

// earlyRead reads a directed Slot's query once before the next Slot reads,
// and supplements the series its first read did not have, guarded under the
// flight against a next Slot that has begun. The permit is given back
// before the supplement, as a directed read's is.
func (engine *Engine) earlyRead(ctx context.Context, slot *directedSlot, release func(), yield <-chan struct{}) {
	readCtx, cancel := context.WithTimeout(ctx, RecheckTimeout)
	back := make(chan struct{})
	if yield != nil {
		go func() {
			select {
			case <-yield:
				cancel()
			case <-back:
			}
		}()
	}
	captured := slot.queries[0]
	engine.mu.Lock()
	first := captured.first.set
	engine.mu.Unlock()
	sink := &keptSink{first: first, admit: engine.options.Memory}
	readAt := engine.options.Now()
	completion, err := engine.options.Recheck(readCtx, captured.spec, sink)
	close(back)
	release()
	cancel()
	engine.mu.Lock()
	engine.counts.directedBytes[slot.source] += sink.bytes
	engine.counts.earlyBytes[slot.source] += sink.bytes
	state := engine.groups[slot.queryGroup]
	slot.early.running, slot.running = false, false
	defer engine.poke()
	if slot.dropped || state == nil {
		engine.fileEarlyLocked(state, slot, EarlyOwnerLost)
		engine.mu.Unlock()
		return
	}
	if (err != nil || completion.Completeness != execution.CompletenessFull) && closed(yield) {
		// Stopped for a formal query: tried again until its latest start.
		slot.early.yielded, slot.early.retryAt = true, engine.options.Now()
		engine.mu.Unlock()
		return
	}
	switch {
	case err != nil || completion.Completeness != execution.CompletenessFull || sink.invalid != nil:
		engine.fileEarlyLocked(state, slot, EarlyReadFailed)
		engine.mu.Unlock()
		return
	case sink.refused:
		engine.fileEarlyLocked(state, slot, EarlyMemoryRefused)
		engine.mu.Unlock()
		return
	}
	read, series := sink.kept(captured.spec, completion)
	slot.early.readAt = readAt
	// The late series are kept until the Slot is filed, whatever becomes of
	// their supplement: the Slot's late count is both reads' together.
	slot.early.late = newSeriesSet(engine.options.Memory)
	for identity := range sink.series {
		slot.early.late.add(hashString(string(identity)))
	}
	if slot.early.late.refused {
		engine.fileEarlyLocked(state, slot, EarlyMemoryRefused)
		engine.mu.Unlock()
		return
	}
	if len(series) == 0 {
		engine.fileEarlyLocked(state, slot, EarlyNothingLate)
		engine.mu.Unlock()
		return
	}
	job := SupplementJob{QueryGroup: slot.queryGroup, EvaluationTime: slot.evaluation, ReadHoldMillis: slot.readHold,
		Series: series, Read: read,
		Deadline: slot.early.next, Guard: func() bool {
			engine.mu.Lock()
			defer engine.mu.Unlock()
			current := engine.groups[slot.queryGroup]
			return current != nil && current.lastSlot <= slot.evaluation && engine.options.Now().Before(engine.nextReadLocked(slot))
		}}
	slot.running = true
	engine.mu.Unlock()

	outcome := engine.options.Supplement(ctx, job)

	engine.mu.Lock()
	defer engine.mu.Unlock()
	slot.running = false
	state = engine.groups[slot.queryGroup]
	engine.noteHoldLocked(state, slot.source, outcome.Held)
	if state == nil || slot.dropped {
		engine.fileEarlyLocked(state, slot, EarlyOwnerLost)
		return
	}
	switch {
	case outcome.Ran:
		// What it supplemented is not the read at the rung's to supplement
		// again: the series join the first read's, admitted as it grows.
		for identity := range sink.series {
			captured.first.add(hashString(string(identity)))
		}
		slot.facts = addFacts(slot.facts, outcome.Facts)
		slot.supplemented = true
		slot.early.crossed = outcome.Facts.CrossedT > 0
		engine.counts.earlyUndecided[slot.source] += uint64(outcome.Facts.Withheld + outcome.Facts.InputIncomplete +
			outcome.Facts.ConfigDrift)
		engine.fileEarlyLocked(state, slot, EarlyBeforeNext)
	case outcome.Refused == SupplementOvertaken:
		engine.fileEarlyLocked(state, slot, EarlyOvertaken)
	case outcome.Refused == DirectedFlightBusy:
		engine.fileEarlyLocked(state, slot, EarlyFlightBusy)
	case outcome.Refused == DirectedContractExpired:
		engine.fileEarlyLocked(state, slot, EarlyContractExpired)
	default:
		engine.fileEarlyLocked(state, slot, EarlyFailed)
	}
}

// noteHoldLocked counts how long a supplement held its Query Group's
// flight, and keeps it among the group's readings.
func (engine *Engine) noteHoldLocked(state *group, source string, held time.Duration) {
	if held <= 0 {
		return
	}
	engine.counts.supplementHold[key2(source, holdBucket(held))]++
	engine.counts.supplementHoldMax[source] = max(engine.counts.supplementHoldMax[source], held)
	engine.holdMax = max(engine.holdMax, held)
	if state != nil {
		state.holds.add(held)
	}
}

// addFacts is two supplements' facts together.
func addFacts(sum, facts execution.SupplementFacts) execution.SupplementFacts {
	sum.Candidates += facts.Candidates
	sum.Admitted += facts.Admitted
	sum.Points += facts.Points
	sum.CrossedT += facts.CrossedT
	sum.NoDataFact += facts.NoDataFact
	sum.ConfigDrift += facts.ConfigDrift
	sum.InputIncomplete += facts.InputIncomplete
	sum.Withheld += facts.Withheld
	return sum
}

// poke wakes the loop, which may have early reads that were waiting on the
// read that just ended.
func (engine *Engine) poke() {
	select {
	case engine.woken <- struct{}{}:
	default:
	}
}
