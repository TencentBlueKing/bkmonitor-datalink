// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package absentalerts

import (
	"sort"
	"sync"
	"time"
)

// Tracker is the loop's memory between rounds: when each candidate was first
// found missing from the snapshot, and under which observation. It holds
// nothing else.
//
// The memory is bounded, and what the bound refuses is counted: a candidate
// not remembered restarts its grace every round and is never closed.
type Tracker struct {
	mu          sync.Mutex
	firstAbsent map[Key]Absence
	maxTracked  int
	// dropped counts the candidates the bound refused to remember. A
	// candidate not remembered is never closed - it restarts its grace on
	// every round - so this is reported rather than left to be read as an
	// empty difference.
	dropped uint64
}

func NewTracker(maxTracked int) *Tracker {
	if maxTracked <= 0 {
		maxTracked = 1
	}
	return &Tracker{firstAbsent: make(map[Key]Absence), maxTracked: maxTracked}
}

// Note records this round's candidates and forgets every key that is no
// longer one: a strategy the snapshot lists again, and one the link no
// longer lists, both stop being candidates, and neither should keep an
// absence that a later round would read as long-standing. A strategy an
// incomplete walk did not reach is forgotten the same way, which only
// delays it.
func (tracker *Tracker) Note(candidates []Key, observation string, now time.Time) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	current := make(map[Key]struct{}, len(candidates))
	for _, key := range candidates {
		current[key] = struct{}{}
	}
	for key := range tracker.firstAbsent {
		if _, still := current[key]; !still {
			delete(tracker.firstAbsent, key)
		}
	}
	// Admit in a fixed order so that which candidates a full memory holds is
	// the same on every replica and every round, rather than map order.
	ordered := append([]Key(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].TenantID != ordered[j].TenantID {
			return ordered[i].TenantID < ordered[j].TenantID
		}
		return ordered[i].StrategyID < ordered[j].StrategyID
	})
	for _, key := range ordered {
		if _, known := tracker.firstAbsent[key]; known {
			continue
		}
		if len(tracker.firstAbsent) >= tracker.maxTracked {
			tracker.dropped++
			continue
		}
		tracker.firstAbsent[key] = Absence{Since: now, Observation: observation}
	}
}

// Absences is the memory as a round reads it.
func (tracker *Tracker) Absences() map[Key]Absence {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	absences := make(map[Key]Absence, len(tracker.firstAbsent))
	for key, absence := range tracker.firstAbsent {
		absences[key] = absence
	}
	return absences
}

// Tracked and Dropped are the memory's own readings: how many candidates it
// holds, and how many it has refused to hold.
func (tracker *Tracker) Tracked() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.firstAbsent)
}

func (tracker *Tracker) Dropped() uint64 {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.dropped
}

// Forget drops every candidate clock. A replica that loses the control
// term calls it, so an absence it observed while it was leader cannot
// mature into a close after a term it did not hold.
func (tracker *Tracker) Forget() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.firstAbsent = make(map[Key]Absence)
}

// Round is the whole decision with the tracker's memory in it: take the
// difference, start the clock on candidates that are new, then decide with
// the memory as it now stands. A candidate first seen this round is within
// grace by construction, which is what makes a newly elected leader unable
// to close anything on its first round.
func (tracker *Tracker) Round(round Round, bounds Bounds) Result {
	candidates, _, refusal := Candidates(round, bounds)
	if refusal != RefusalNone {
		// A refused round decides nothing, and must not age anything either:
		// the memory is left exactly as it was, so a run of refused rounds
		// cannot mature a candidate into a close.
		return Compute(round, bounds)
	}
	tracker.Note(candidates, round.SnapshotObservation, round.Now)
	round.FirstAbsent = tracker.Absences()
	return Compute(round, bounds)
}
