// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"sort"
	"strconv"
	"sync"
	"time"
)

// DepartedStrategy is what the catalog remembers about a strategy after it
// let it go: which business it belonged to and which snapshot revision its
// Plan last carried.
//
// It is remembered here because here is the only place it is ever known. A
// deleted strategy has no document to read and no Plan to look at, and the
// alert link's index carries its tenant and id and nothing else. Without
// this memory a close for such a strategy cannot be addressed at all, and
// the alerts it left open stay open forever.
type DepartedStrategy struct {
	TenantID   string
	StrategyID string
	BusinessID int64
	Revision   int64
	// At is when the catalog stopped publishing the strategy - which is
	// already past the removal grace and two observations of the source, so
	// it is a confirmed departure rather than a first sighting.
	At time.Time
}

// DepartedStrategyRetention is how long a departure is remembered. Long
// enough that a backlog of alerts on a deleted strategy is still closable
// after a weekend, short enough that the memory is not a second catalog.
const DepartedStrategyRetention = 7 * 24 * time.Hour

// MaxDepartedStrategies bounds the memory. A deployment that deletes more
// strategies than this inside the retention window is not the case this
// serves; what it must not do is grow the leader's memory without a bound.
const MaxDepartedStrategies = 20000

// departedMemory is the leader's memory of departures, kept in process. It
// is not persisted: on election the reconciler loads the publication that
// was current before this process started, so the first round after an
// election compares against it and records whatever left while nobody was
// looking.
type departedMemory struct {
	mu      sync.Mutex
	entries map[string]DepartedStrategy
	// refused counts departures the bound would not admit, so a full memory
	// is a reading rather than a silence.
	refused uint64
}

func newDepartedMemory() *departedMemory {
	return &departedMemory{entries: make(map[string]DepartedStrategy)}
}

func departedKey(tenantID, strategyID string) string {
	return tenantID + "\x00" + strategyID
}

// record takes the strategies of two consecutive publications and remembers
// the ones the new publication no longer has. A strategy that comes back is
// forgotten, so a strategy deleted, restored, and deleted again is
// remembered from its second departure and not its first.
func (memory *departedMemory) record(before, after []QueryGroup, at time.Time) {
	if memory == nil {
		return
	}
	previous := publishedIdentities(before)
	current := publishedIdentities(after)
	memory.mu.Lock()
	defer memory.mu.Unlock()
	memory.expireLocked(at)
	for key := range current {
		delete(memory.entries, key)
	}
	departed := make([]string, 0)
	for key := range previous {
		if _, still := current[key]; still {
			continue
		}
		if _, known := memory.entries[key]; known {
			continue
		}
		departed = append(departed, key)
	}
	// A fixed order so that which departures a full memory holds does not
	// depend on map order.
	sort.Strings(departed)
	for _, key := range departed {
		if len(memory.entries) >= MaxDepartedStrategies {
			memory.refused++
			continue
		}
		entry := previous[key]
		entry.At = at
		memory.entries[key] = entry
	}
}

func (memory *departedMemory) expireLocked(at time.Time) {
	for key, entry := range memory.entries {
		if at.Sub(entry.At) > DepartedStrategyRetention {
			delete(memory.entries, key)
		}
	}
}

// Departed is the memory as a reader sees it.
func (memory *departedMemory) departed() []DepartedStrategy {
	if memory == nil {
		return nil
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	entries := make([]DepartedStrategy, 0, len(memory.entries))
	for _, entry := range memory.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].TenantID != entries[j].TenantID {
			return entries[i].TenantID < entries[j].TenantID
		}
		return entries[i].StrategyID < entries[j].StrategyID
	})
	return entries
}

func (memory *departedMemory) refusedCount() uint64 {
	if memory == nil {
		return 0
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	return memory.refused
}

// publishedIdentities is every strategy with a Plan in a publication, with
// what a close needs to name it. A strategy whose business or revision does
// not parse is still recorded, with the part that parsed: the difference
// reports "the revision is unknown" as its own answer, and a strategy left
// out here would be reported as having no identity at all, which is a
// different and wrong sentence.
func publishedIdentities(groups []QueryGroup) map[string]DepartedStrategy {
	identities := make(map[string]DepartedStrategy)
	for _, group := range groups {
		for _, plan := range group.Plans {
			key := departedKey(plan.Identity.TenantID, plan.Identity.StrategyID)
			if _, known := identities[key]; known {
				continue
			}
			business, err := strconv.ParseInt(plan.Identity.BusinessID, 10, 64)
			if err != nil {
				business = 0
			}
			identities[key] = DepartedStrategy{TenantID: plan.Identity.TenantID, StrategyID: plan.Identity.StrategyID,
				BusinessID: business, Revision: plan.Plan.StrategyRef.SnapshotRevision}
		}
	}
	return identities
}

// PublishedStrategies is every strategy the publication this process last
// published or read runs a Plan of. A strategy here is executing whatever
// any other list says about it, which is what the difference needs to know
// before it closes anything.
func (reconciler *SourceReconciler) PublishedStrategies() []DepartedStrategy {
	if reconciler == nil || reconciler.lastGood == nil {
		return nil
	}
	identities := publishedIdentities(reconciler.lastGood.QueryGroups)
	published := make([]DepartedStrategy, 0, len(identities))
	for _, entry := range identities {
		published = append(published, entry)
	}
	sort.Slice(published, func(i, j int) bool {
		if published[i].TenantID != published[j].TenantID {
			return published[i].TenantID < published[j].TenantID
		}
		return published[i].StrategyID < published[j].StrategyID
	})
	return published
}

// DepartedStrategies is what the catalog remembers about the strategies it
// let go, and how many departures the memory's bound refused to hold.
func (reconciler *SourceReconciler) DepartedStrategies() ([]DepartedStrategy, uint64) {
	if reconciler == nil {
		return nil, 0
	}
	return reconciler.departed.departed(), reconciler.departed.refusedCount()
}

// ObservedSnapshot is the source's own list of strategies as this
// reconciler last read it: which strategies exist, which observation said
// so, and when it was read.
//
// The observation is identified by the moment of the read rather than by a
// digest of its content, because what a caller needs from it is exactly
// "is this the same read as last time": a round that reuses the previous
// round's observation has to be unable to confirm anything the previous
// round saw. Two reads never share a moment; two rounds reusing one read
// always do.
type ObservedSnapshot struct {
	Strategies  []DepartedStrategy
	Observation string
	ReadAt      time.Time
}

// ObservedSnapshot returns the last observation, and whether there is one.
// A follower and a leader before its first read have none, which is not an
// empty source.
func (reconciler *SourceReconciler) ObservedSnapshot() (ObservedSnapshot, bool) {
	if reconciler == nil || reconciler.memory == nil || len(reconciler.memory.cycle.strategies) == 0 {
		return ObservedSnapshot{}, false
	}
	observed := ObservedSnapshot{Observation: reconciler.memory.readAt.UTC().Format(time.RFC3339Nano), ReadAt: reconciler.memory.readAt}
	for _, strategy := range reconciler.memory.cycle.strategies {
		business, err := strconv.ParseInt(strategy.Identity.BusinessID, 10, 64)
		if err != nil {
			business = 0
		}
		observed.Strategies = append(observed.Strategies, DepartedStrategy{
			TenantID: strategy.Identity.TenantID, StrategyID: strategy.SourceID, BusinessID: business})
	}
	return observed, true
}
