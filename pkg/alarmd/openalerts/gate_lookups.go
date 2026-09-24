// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"sort"
	"time"
)

// The recovery gate's lookups, kept for reading. The answer counts say how
// often the gate said "not open"; they cannot say whether that was right.
// Most "not open" answers are: every round of a series that never alerted
// asks, and has no alert to resolve. The wrong ones are the lookups of an
// alert this process itself opened -- the gate answering "not open" for it
// holds a RECOVERY whose alert stays open. So each lookup is also split by
// whether it asked about one of this process's own open alerts, and a few
// are kept whole: the key the gate was asked, its answer, and -- for a
// "not open" -- which other strategies' sets carry the same fingerprint,
// which is what a lookup keyed differently from the sets looks like.
const (
	// RecentGateLookups bounds each kept sample.
	RecentGateLookups = 16
	// gateOtherSetsShown bounds the other strategies named on one lookup.
	gateOtherSetsShown = 4
)

// GateLookup is one recovery-gate lookup.
type GateLookup struct {
	At          time.Time `json:"at"`
	TenantID    string    `json:"tenant_id"`
	StrategyID  string    `json:"strategy_id"`
	Fingerprint string    `json:"fingerprint"`
	Answer      Answer    `json:"answer"`
	// Open is what the gate decided: the RECOVERY went when true.
	Open bool `json:"open"`
	// Own says this exact strategy and fingerprint is an alert this process
	// sent the ABNORMAL for and has not sent the RECOVERY for.
	Own bool `json:"own"`
	// InOtherSets names strategies whose set carries this fingerprint, on a
	// lookup the gate answered "not open", as of when it was read. Empty is
	// the ordinary case.
	InOtherSets []string `json:"in_other_sets,omitempty"`
}

// countLookup counts an answer and remembers it as this lookup's, for
// recordGate. Called with the lock held.
func (cache *Cache) countLookup(answer Answer) {
	cache.lookups[answer]++
	cache.lastAnswer = answer
}

// recordGate files the lookup Contains just answered. Called with the lock
// held.
func (cache *Cache) recordGate(m member, now time.Time, open bool) {
	own := false
	if cache.index != nil {
		_, own = cache.index.opened[m]
	}
	if !own {
		_, own = cache.added[m]
	}
	lookup := GateLookup{At: now, TenantID: m.key.TenantID, StrategyID: m.key.StrategyID, Fingerprint: m.fingerprint,
		Answer: cache.lastAnswer, Open: open, Own: own}
	if own {
		cache.ownLookups[cache.lastAnswer]++
		if !open {
			// The one branch the reading is for, and a rare one: only here
			// is the walk over every set paid on the gate's path.
			lookup.InOtherSets = cache.setsCarrying(m)
			cache.ownHeld++
			cache.recentOwnHeld.keep(lookup)
		}
	}
	cache.recentLookups.keep(lookup)
}

// setsCarrying is the other strategies whose last read carries the
// fingerprint, bounded. Called with the lock held.
func (cache *Cache) setsCarrying(m member) []string {
	var found []string
	if cache.index != nil {
		for key, entry := range cache.index.entries {
			if key == m.key || entry == nil {
				continue
			}
			if _, ok := entry.index[m.fingerprint]; ok {
				found = append(found, key.StrategyID)
			}
		}
	} else {
		for key, set := range cache.sets {
			if key == m.key {
				continue
			}
			if _, ok := set[m.fingerprint]; ok {
				found = append(found, key.StrategyID)
			}
		}
	}
	sort.Strings(found)
	if len(found) > gateOtherSetsShown {
		found = found[:gateOtherSetsShown]
	}
	return found
}

// gateRing keeps the last RecentGateLookups lookups in place: keeping one
// is a copy into a slot, with no allocation on the gate's path.
type gateRing struct {
	slots [RecentGateLookups]GateLookup
	next  int
	full  bool
}

func (ring *gateRing) keep(lookup GateLookup) {
	ring.slots[ring.next] = lookup
	ring.next = (ring.next + 1) % RecentGateLookups
	if ring.next == 0 {
		ring.full = true
	}
}

// ordered is the kept lookups, oldest first.
func (ring *gateRing) ordered() []GateLookup {
	if !ring.full {
		return append([]GateLookup(nil), ring.slots[:ring.next]...)
	}
	return append(append([]GateLookup(nil), ring.slots[ring.next:]...), ring.slots[:ring.next]...)
}

// gateStats copies the gate's own-alert split and samples into stats.
// Called with the lock held.
func (cache *Cache) gateStats(stats *Stats) {
	stats.OwnLookups = make(map[Answer]uint64, len(cache.ownLookups))
	for answer, n := range cache.ownLookups {
		stats.OwnLookups[answer] = n
	}
	stats.OwnHeld = cache.ownHeld
	stats.GateSince = cache.gateSince
	stats.RecentLookups = cache.recentLookups.ordered()
	// The other sets of a lookup that was not the replica's own are worked
	// out here, on the few kept, rather than on the gate's path for every
	// held lookup.
	for i := range stats.RecentLookups {
		if lookup := &stats.RecentLookups[i]; !lookup.Open && !lookup.Own {
			lookup.InOtherSets = cache.setsCarrying(member{key: StrategyKey{TenantID: lookup.TenantID, StrategyID: lookup.StrategyID}, fingerprint: lookup.Fingerprint})
		}
	}
	stats.RecentOwnHeld = cache.recentOwnHeld.ordered()
}
