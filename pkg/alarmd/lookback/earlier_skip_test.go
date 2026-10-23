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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// skippedBy is the held Slots counted skipped, by why, across the sources, and on qg's reading; only reasons
// counted at all.
func skippedBy(f *fixture) (sources, group map[string]uint64) {
	sources, group = map[string]uint64{}, map[string]uint64{}
	for _, source := range f.engine.Stats().Sources {
		for reason, count := range source.EarlierSkipped {
			if count > 0 {
				sources[reason] += count
			}
		}
	}
	if reading, ok := f.engine.GroupReading("qg"); ok {
		for reason, count := range reading.EarlierSkipped {
			if count > 0 {
				group[reason] = count
			}
		}
	}
	return sources, group
}

// withGroup runs change on qg's state under the engine's lock, the state made as Prepare makes it.
func withGroup(f *fixture, q Query, change func(*group)) {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	change(f.engine.groupLocked(q, f.clock.now()))
}

// Every held Slot whose first attempt reserves no earlier-read trial is counted, once however often its
// readiness is retried, by what stood in the way - by its source and on its group's reading - and the
// controller is told only of the one it gave no candidate for. A Slot that reserves its trial is counted
// nowhere. Before, each of these left Prepare without a word, and a group never tried said nothing of why.
func TestAHeldSlotWithoutATrialIsCountedOnceByWhy(t *testing.T) {
	for _, c := range []struct {
		reason string
		set    func(f *fixture, q *Query)
	}{
		{EarlierSkipNoHold, func(f *fixture, _ *Query) {
			f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return 0 }
		}},
		{EarlierSkipBackendFailing, func(_ *fixture, q *Query) { q.BackendFailing = true }},
		{EarlierSkipNoCandidate, func(f *fixture, _ *Query) {
			f.engine.options.CurrentCandidate = func(execution.QueryGroupIdentity) (time.Duration, bool) { return 0, false }
		}},
		{EarlierSkipTrialReserved, func(f *fixture, q *Query) {
			later := *q
			later.Contract.Slot.EvaluationTime += 60
			later.ReadyAt = q.ReadyAt.Add(time.Minute)
			withGroup(f, *q, func(state *group) {
				state.prepared = &earlierSample{query: later, source: state.source, group: state, ahead: true, aheadSlots: 1}
			})
		}},
		{EarlierSkipSampleInFlight, func(f *fixture, q *Query) { withGroup(f, *q, func(state *group) { state.capturing = true }) }},
		{EarlierSkipResting, func(f *fixture, q *Query) {
			withGroup(f, *q, func(state *group) { state.nextAt = q.ReadyAt.Add(time.Hour) })
		}},
		{"", func(*fixture, *Query) {}},
	} {
		name := c.reason
		if name == "" {
			name = "a trial reserved"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
			heard := 0
			f.engine.options.EarlierRefused = func(execution.QueryGroupIdentity) { heard++ }
			q := heldQuery(f)
			c.set(f, &q)
			f.engine.Prepare(q)
			f.engine.Prepare(q)
			sources, group := skippedBy(f)
			if c.reason == "" {
				if len(sources) != 0 || len(group) != 0 || preparedTrial(f) == nil {
					t.Fatalf("a Slot that reserved its trial: skipped %v, on its group %v, reserved %+v", sources, group, preparedTrial(f))
				}
				return
			}
			want := map[string]uint64{c.reason: 1}
			if len(sources) != 1 || sources[c.reason] != 1 || len(group) != 1 || group[c.reason] != 1 {
				t.Fatalf("a Slot prepared twice: skipped %v, on its group %v, want %v on both", sources, group, want)
			}
			if wantHeard := map[bool]int{true: 1}[c.reason == EarlierSkipNoCandidate]; heard != wantHeard {
				t.Fatalf("the controller heard %d refusals, want %d", heard, wantHeard)
			}
		})
	}
	// A group this process does not own is kept nothing of: counted by its source alone, at each attempt.
	t.Run(EarlierSkipNotOwned, func(t *testing.T) {
		f := newFixture(t)
		f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
		f.mu.Lock()
		f.owned["qg"] = false
		f.mu.Unlock()
		q := heldQuery(f)
		f.engine.Prepare(q)
		f.engine.Prepare(q)
		sources, _ := skippedBy(f)
		f.engine.mu.Lock()
		kept := f.engine.groups["qg"] != nil
		f.engine.mu.Unlock()
		if len(sources) != 1 || sources[EarlierSkipNotOwned] != 2 || kept {
			t.Fatalf("a group not owned: skipped %v, state kept %t; want not_owned twice and nothing kept", sources, kept)
		}
	})
}

// A Slot after the one counted is counted again: the once is per Slot, not per group.
func TestTheNextSlotWithoutATrialIsCountedToo(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	q.BackendFailing = true
	f.engine.Prepare(q)
	next := q
	next.Contract.Slot.EvaluationTime += 60
	next.ReadyAt = q.ReadyAt.Add(time.Minute)
	f.engine.Prepare(next)
	f.engine.Prepare(next)
	if sources, group := skippedBy(f); sources[EarlierSkipBackendFailing] != 2 || group[EarlierSkipBackendFailing] != 2 {
		t.Fatalf("two Slots, the second prepared twice: skipped %v, on its group %v, want two", sources, group)
	}
}

// A Slot that reserved its own trial and is refused a candidate on its readiness retry - its hold moved in
// between - had its trial: it is not counted skipped, and the controller hears nothing.
func TestASlotThatReservedItsTrialIsNotCountedWhenARetryIsRefused(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	tries := true
	f.engine.options.CurrentCandidate = func(execution.QueryGroupIdentity) (time.Duration, bool) { return time.Minute, tries }
	heard := 0
	f.engine.options.EarlierRefused = func(execution.QueryGroupIdentity) { heard++ }
	q := heldQuery(f)
	f.engine.Prepare(q)
	if preparedTrial(f) == nil {
		t.Fatal("fixture: no trial reserved")
	}
	tries = false
	f.engine.Prepare(q)
	if sources, group := skippedBy(f); len(sources) != 0 || len(group) != 0 || heard != 0 {
		t.Fatalf("a Slot with its trial, refused on its retry: skipped %v, on its group %v, heard %d", sources, group, heard)
	}
}

// Under a hold past the period a Slot's readiness retry comes after the next
// Slot's first attempt. Each Slot is still counted once.
func TestASlotRetriedAfterTheNextSlotsFirstAttemptIsNotCountedAgain(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	q.BackendFailing = true
	next := q
	next.Contract.Slot.EvaluationTime += 60
	next.ReadyAt = q.ReadyAt.Add(time.Minute)
	f.engine.Prepare(q)
	f.engine.Prepare(next)
	f.engine.Prepare(q)
	f.engine.Prepare(next)
	if sources, group := skippedBy(f); sources[EarlierSkipBackendFailing] != 2 || group[EarlierSkipBackendFailing] != 2 {
		t.Fatalf("two Slots, each retried after the other's first attempt: skipped %v, on its group %v, want two", sources, group)
	}
}

// A Slot whose retry comes late and reserves its trial does not take the
// group back to it: the next Slot, already counted, is not counted again.
func TestASlotThatReservesLateDoesNotUncountTheNextSlot(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	next := q
	next.Contract.Slot.EvaluationTime += 60
	next.ReadyAt = q.ReadyAt.Add(time.Minute)
	resting := func(until time.Time) { withGroup(f, q, func(state *group) { state.nextAt = until }) }
	resting(next.ReadyAt.Add(time.Hour))
	f.engine.Prepare(q)
	f.engine.Prepare(next)
	// The rest ends: the first Slot's late retry reserves its trial.
	resting(time.Time{})
	f.engine.Prepare(q)
	if preparedTrial(f) == nil {
		t.Fatal("fixture: the late retry reserved no trial")
	}
	// The group rests again by the next Slot's retry.
	resting(next.ReadyAt.Add(time.Hour))
	f.engine.Prepare(next)
	if sources, group := skippedBy(f); sources[EarlierSkipResting] != 2 || group[EarlierSkipResting] != 2 {
		t.Fatalf("skipped %v, on its group %v, want each Slot counted resting once", sources, group)
	}
}
