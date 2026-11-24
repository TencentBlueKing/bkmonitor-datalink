// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package absentalerts

import (
	"sort"
	"sync"
	"time"
)

// Tracker is the loop's memory between rounds: when each candidate was first
// found missing from the snapshot, under which observation, how many rounds
// have found it missing, and under which of the writer's marks. It holds
// nothing else. The loop keeps a copy in the runtime Redis (Restore on a new
// term, Absences after each round); the tracker itself only remembers.
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
// delays it. Each candidate already held counts one more read, confirmed
// when this observation is not the one that first found it; marks is this
// round's writer's marks by strategy id (markFor).
func (tracker *Tracker) Note(candidates []Key, observation string, marks map[string]Mark, now time.Time) {
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
	round := Round{Marks: marks}
	for _, key := range ordered {
		mark, _ := markFor(round, key)
		if absence, known := tracker.firstAbsent[key]; known {
			absence.Reads++
			if absence.TermReads == 0 {
				absence.TermObservation = observation
			} else if observation != absence.TermObservation {
				absence.Confirmed = true
			}
			absence.TermReads++
			if mark == absence.Mark && mark != (Mark{}) {
				absence.MarkReads++
			} else {
				absence.Mark, absence.MarkReads = mark, markReads(mark)
			}
			tracker.firstAbsent[key] = absence
			continue
		}
		if len(tracker.firstAbsent) >= tracker.maxTracked {
			tracker.dropped++
			continue
		}
		tracker.firstAbsent[key] = Absence{Since: now, Observation: observation, Reads: 1, TermReads: 1, TermObservation: observation,
			Mark: mark, MarkReads: markReads(mark)}
	}
}

func markReads(mark Mark) int {
	if mark == (Mark{}) {
		return 0
	}
	return 1
}

// Restore replaces the memory with what an earlier term kept, for a new
// term's first round. Nothing restored counts as read in this term, so a
// candidate is decided only after this term has itself read it missing on
// two rounds, and none keeps its confirmation: this term confirms it again
// under two observations of its own. That costs a new leader up to one
// source re-read after it takes over, and keeps it from closing on a
// confirmation made before the strategy came back and left again while no
// leader watched. Past the bound, the rest are counted dropped.
func (tracker *Tracker) Restore(absences map[Key]Absence) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.firstAbsent = make(map[Key]Absence, len(absences))
	ordered := make([]Key, 0, len(absences))
	for key := range absences {
		ordered = append(ordered, key)
	}
	sortKeys(ordered)
	for _, key := range ordered {
		if len(tracker.firstAbsent) >= tracker.maxTracked {
			tracker.dropped++
			continue
		}
		absence := absences[key]
		absence.TermReads, absence.TermObservation, absence.Confirmed = 0, "", false
		tracker.firstAbsent[key] = absence
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
// difference, count the round's read of every candidate, then decide with
// the memory as it now stands. A candidate is decided only once this term
// has read it missing on two rounds, which is what makes a newly elected
// leader unable to close anything on its first round, whatever an earlier
// term kept.
func (tracker *Tracker) Round(round Round, bounds Bounds) Result {
	candidates, _, refusal := Candidates(round, bounds)
	if refusal != RefusalNone {
		// A refused round decides nothing, and must not age anything either:
		// the memory is left exactly as it was, so a run of refused rounds
		// cannot mature a candidate into a close.
		return Compute(round, bounds)
	}
	tracker.Note(candidates, round.SnapshotObservation, round.Marks, round.Now)
	round.FirstAbsent = tracker.Absences()
	return Compute(round, bounds)
}
