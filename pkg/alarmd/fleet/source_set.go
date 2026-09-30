// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"sync"
	"time"
)

// The source's active set is the list of strategies the platform says exist.
// A strategy that leaves it is given one grace cycle under PENDING_REMOVAL
// and then REMOVED: its Plan leaves the Catalog, its Query Groups are swept,
// and when the strategy is listed again they are placed anew. Read one round
// at a time that is a strategy being deleted and later re-created. Read
// across rounds it was the platform's list losing 99 entries for six minutes
// at the top of every hour since a Friday: 22 strategies REMOVED and
// re-acquired every hour, four Slots of each lost to the gap, and the only
// word on the page for any of it was REMOVED, which says the strategy was
// deleted. An absence read as a fact is a silent stop.
//
// SourceSetLedger keeps what one round cannot say: when a strategy first went
// absent, whether it came back, and how often that happens by the hour. It
// is fed by the control leader from each round's composition and read into
// the source facts the fleet publishes; it holds strategy identifiers and
// nothing from the documents, and it is this process's account -- a new
// leader starts a new one and says since when.
type SourceSetLedger struct {
	mu      sync.Mutex
	now     func() time.Time
	started time.Time
	// absent is each strategy the set dropped and has not listed again,
	// with the round it first went absent at; removed marks the ones whose
	// Plan has since left the Catalog.
	absent  map[string]time.Time
	removed map[string]bool
	hours   map[time.Time]*SourceSetHour
	// returnedAfterRemoval counts, since started, the strategies listed
	// again after their Plan had left the Catalog.
	returnedAfterRemoval int
}

// SourceSetRound is one round's word on the set, as the composition has it:
// the strategies the source listed -- whatever became of them this round,
// accepted or refused, a strategy in the list is in the list -- the ones
// under grace, and the ones removed, each with the catalog's own word on
// when it was first found absent.
type SourceSetRound struct {
	At             time.Time
	Listed         []string
	PendingRemoval []AbsentStrategy
	Removed        []AbsentStrategy
}

// AbsentStrategy is one strategy the source did not list this round, with
// when the catalog first found it absent (zero when the catalog did not
// say: a disposition from before the field, or one this build cannot read).
// The catalog's moment is the true start of the absence and survives a
// leader restart on the published audit; this process's first sight of the
// strategy under grace is only a lower bound on it.
type AbsentStrategy struct {
	StrategyID  string
	AbsentSince time.Time
}

// SourceSetHour is one hour of the account.
type SourceSetHour struct {
	Hour time.Time `json:"hour"`
	// Dropped is the strategies that went absent in this hour, Removed the
	// ones whose Plan left the Catalog in it, Reactivated the ones the set
	// listed again in it after an absence.
	Dropped     int `json:"dropped"`
	Removed     int `json:"removed"`
	Reactivated int `json:"reactivated"`
	// LongestAbsentSeconds is the longest absence among the reactivations,
	// first absent round to the round that listed the strategy again.
	LongestAbsentSeconds float64 `json:"longest_absent_seconds,omitempty"`
	// Samples names up to SourceSetSampleLimit of the reactivated
	// strategies, smallest identifier first.
	Samples []string `json:"samples,omitempty"`
	// ReturnedAfterRemoval is the reactivations of this hour whose Plan had
	// already left the Catalog -- absent past the grace, removed, then listed
	// again -- and ReturnedAfterRemovalSamples names up to
	// SourceSetSampleLimit of them. A return inside the grace is not one.
	ReturnedAfterRemoval        int      `json:"returned_after_removal"`
	ReturnedAfterRemovalSamples []string `json:"returned_after_removal_samples,omitempty"`
}

// SourceSetFacts is the ledger as the fleet publishes it.
type SourceSetFacts struct {
	// Since is when this account began: the leader process that keeps it.
	Since time.Time `json:"since"`
	// PendingRemoval is the strategies under grace now, with when each went
	// absent; Removed the ones whose Plan has left and that have not been
	// listed again within SourceSetReturnWindow.
	PendingRemoval        int            `json:"pending_removal"`
	PendingRemovalSamples []AbsentSample `json:"pending_removal_samples,omitempty"`
	Removed               int            `json:"removed"`
	// ReactivatedThisHour is the strategies listed again after an absence
	// in the current clock hour -- the first screen's "this hour"; Hours is
	// the account by hour, newest first, at most SourceSetHours of them.
	ReactivatedThisHour int             `json:"reactivated_this_hour"`
	Hours               []SourceSetHour `json:"hours"`
	// ReturnedAfterRemovalTotal is every return after removal since Since,
	// whatever the hours kept: the count a Catalog that withdraws a Plan
	// only to receive it back is read by.
	ReturnedAfterRemovalTotal int `json:"returned_after_removal_total"`
}

// AbsentSample is one strategy the set dropped and when.
type AbsentSample struct {
	StrategyID  string    `json:"strategy_id"`
	AbsentSince time.Time `json:"absent_since"`
}

const (
	// SourceSetReturnWindow is how long after going absent a strategy's
	// return still counts as the set flapping rather than as a strategy
	// re-created: past it the strategy is forgotten by the ledger.
	SourceSetReturnWindow = 6 * time.Hour
	// SourceSetHours bounds the account: three days of hours.
	SourceSetHours = 72
	// SourceSetSampleLimit bounds the strategies an hour or the pending list
	// names.
	SourceSetSampleLimit = 8
)

func NewSourceSetLedger(now func() time.Time) *SourceSetLedger {
	if now == nil {
		now = time.Now
	}
	return &SourceSetLedger{now: now, started: now(), absent: map[string]time.Time{}, removed: map[string]bool{}, hours: map[time.Time]*SourceSetHour{}}
}

func (ledger *SourceSetLedger) hour(at time.Time) *SourceSetHour {
	key := at.UTC().Truncate(time.Hour)
	entry := ledger.hours[key]
	if entry == nil {
		entry = &SourceSetHour{Hour: key}
		ledger.hours[key] = entry
	}
	return entry
}

// NoteRound folds one round in, and says how many strategies it saw come
// back after removal. A strategy under grace or removed that the
// ledger did not know goes absent at this round; one the source listed again
// that the ledger knew as absent is a reactivation, whether or not it
// compiled a Plan this round -- a strategy back in the list under
// STALE_CONFIG is back, and counting it as still absent put a running
// strategy on the first screen as one waiting to be removed; one absent
// longer than the return window is forgotten, as a strategy that was
// deleted.
func (ledger *SourceSetLedger) NoteRound(round SourceSetRound) int {
	if ledger == nil || round.At.IsZero() {
		return 0
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for _, strategy := range round.PendingRemoval {
		ledger.noteAbsent(strategy, round.At)
	}
	for _, strategy := range round.Removed {
		ledger.noteAbsent(strategy, round.At)
		if !ledger.removed[strategy.StrategyID] {
			ledger.removed[strategy.StrategyID] = true
			ledger.hour(round.At).Removed++
		}
	}
	returned, afterRemoval := []string{}, []string{}
	for _, strategyID := range round.Listed {
		since, known := ledger.absent[strategyID]
		if !known {
			continue
		}
		entry := ledger.hour(round.At)
		entry.Reactivated++
		if gap := round.At.Sub(since).Seconds(); gap > entry.LongestAbsentSeconds {
			entry.LongestAbsentSeconds = gap
		}
		returned = append(returned, strategyID)
		if ledger.removed[strategyID] {
			entry.ReturnedAfterRemoval++
			afterRemoval = append(afterRemoval, strategyID)
		}
		delete(ledger.absent, strategyID)
		delete(ledger.removed, strategyID)
	}
	if len(returned) > 0 {
		entry := ledger.hour(round.At)
		entry.Samples = boundedSortedSample(append(entry.Samples, returned...), SourceSetSampleLimit)
	}
	if len(afterRemoval) > 0 {
		entry := ledger.hour(round.At)
		entry.ReturnedAfterRemovalSamples = boundedSortedSample(append(entry.ReturnedAfterRemovalSamples, afterRemoval...), SourceSetSampleLimit)
		ledger.returnedAfterRemoval += len(afterRemoval)
	}
	for strategyID, since := range ledger.absent {
		if round.At.Sub(since) > SourceSetReturnWindow {
			delete(ledger.absent, strategyID)
			delete(ledger.removed, strategyID)
		}
	}
	for key := range ledger.hours {
		if round.At.Sub(key) > time.Duration(SourceSetHours)*time.Hour {
			delete(ledger.hours, key)
		}
	}
	return len(afterRemoval)
}

// noteAbsent records a strategy the round did not list. The start of the
// absence is the catalog's word when it has one -- the moment the strategy
// was first found absent, carried on the disposition and on the published
// audit across leader restarts -- and this round otherwise, which is the
// first this process saw of it and a lower bound on the truth. An absence
// already known keeps the earliest start it has been given: a ledger that
// began after the strategy went absent learns the true start from the
// catalog's word on a later round, and never moves it later.
func (ledger *SourceSetLedger) noteAbsent(strategy AbsentStrategy, at time.Time) {
	since := at
	if !strategy.AbsentSince.IsZero() && strategy.AbsentSince.Before(at) {
		since = strategy.AbsentSince
	}
	known, seen := ledger.absent[strategy.StrategyID]
	if !seen {
		ledger.absent[strategy.StrategyID] = since
		// Dropped is counted in the hour the absence began, which for a
		// leader that took over mid-grace is an hour before its account did:
		// the hourly fold then says when the list lost the strategy, not when
		// this process heard of it, and "every hour at :01" reads as such
		// across a leader change.
		ledger.hour(since).Dropped++
		return
	}
	if since.Before(known) {
		ledger.absent[strategy.StrategyID] = since
	}
}

// boundedSortedSample is the smallest limit identifiers of names, once each.
func boundedSortedSample(names []string, limit int) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			unique = append(unique, name)
		}
	}
	sort.Strings(unique)
	if len(unique) > limit {
		unique = unique[:limit]
	}
	return unique
}

// Facts is the account as of now.
func (ledger *SourceSetLedger) Facts(now time.Time) *SourceSetFacts {
	if ledger == nil {
		return nil
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	facts := &SourceSetFacts{Since: ledger.started, Hours: []SourceSetHour{}, ReturnedAfterRemovalTotal: ledger.returnedAfterRemoval}
	pending := make([]AbsentSample, 0, len(ledger.absent))
	for strategyID, since := range ledger.absent {
		if ledger.removed[strategyID] {
			facts.Removed++
			continue
		}
		facts.PendingRemoval++
		pending = append(pending, AbsentSample{StrategyID: strategyID, AbsentSince: since})
	}
	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].AbsentSince.Equal(pending[j].AbsentSince) {
			return pending[i].AbsentSince.Before(pending[j].AbsentSince)
		}
		return pending[i].StrategyID < pending[j].StrategyID
	})
	if len(pending) > SourceSetSampleLimit {
		pending = pending[:SourceSetSampleLimit]
	}
	facts.PendingRemovalSamples = pending
	for _, entry := range ledger.hours {
		copied := *entry
		copied.Samples = append([]string(nil), entry.Samples...)
		copied.ReturnedAfterRemovalSamples = append([]string(nil), entry.ReturnedAfterRemovalSamples...)
		facts.Hours = append(facts.Hours, copied)
		if entry.Hour.Equal(now.UTC().Truncate(time.Hour)) {
			facts.ReactivatedThisHour = entry.Reactivated
		}
	}
	sort.Slice(facts.Hours, func(i, j int) bool { return facts.Hours[i].Hour.After(facts.Hours[j].Hour) })
	if len(facts.Hours) > SourceSetHours {
		facts.Hours = facts.Hours[:SourceSetHours]
	}
	return facts
}
