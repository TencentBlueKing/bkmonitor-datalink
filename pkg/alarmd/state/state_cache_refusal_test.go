// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group kept out of the State cache says which bound kept it out:
// past the budget it leaves and is read back under over_budget, within the
// budget but past what the Slots leave of the retained pool under yielded,
// and never under the other word.
func TestAKeptOutQueryGroupSaysWhichBoundKeptItOut(t *testing.T) {
	for name, test := range map[string]struct {
		budget                int64
		noRoom                bool
		departure, other      string
		missWord, otherMissed string
	}{
		"the budget":      {1, false, StateCacheDepartOverBudget, StateCacheDepartYielded, StateCacheMissOverBudget, StateCacheMissYielded},
		"the pool's room": {1 << 20, true, StateCacheDepartYielded, StateCacheDepartOverBudget, StateCacheMissYielded, StateCacheMissOverBudget},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newCacheRig(t, test.budget)
			if test.noRoom {
				rig.store.StateCacheBorrower().BorrowRetainedWithin(func() uint64 { return 0 })
			}
			rig.round(first, 2, 0)
			rig.now = rig.now.Add(time.Minute)
			if reads, _ := rig.round(second, 2, 1); reads == 0 {
				t.Fatal("the group kept out was not read back")
			}
			stats := rig.store.StateCacheStats()
			if stats.Departures[test.departure] == 0 || stats.Departures[test.other] != 0 ||
				rig.lookups(test.missWord) != 2 || rig.lookups(test.otherMissed) != 0 {
				t.Fatalf("departures %v, %s %d, %s %d; want it out under %s and read back under %s only",
					stats.Departures, test.missWord, rig.lookups(test.missWord), test.otherMissed, rig.lookups(test.otherMissed),
					test.departure, test.missWord)
			}
		})
	}
}

// The word follows the bound as of the group's latest round: kept out by the
// pool's room, a group that has since grown past the budget is read back
// under over_budget once the Slots leave the room again.
func TestTheBoundAKeptOutQueryGroupSaysIsItsLatestRounds(t *testing.T) {
	cache := bareCache(20)
	room := uint64(cache.budget / 2)
	cache.BorrowRetainedWithin(func() uint64 { return room })
	writeSeries(cache, "big", 60, 0, 25)
	if word := missOf(cache, "big"); word != StateCacheMissYielded {
		t.Fatalf("kept out by the pool's room, read under %q; want %q", word, StateCacheMissYielded)
	}
	room = uint64(cache.budget)
	writeSeries(cache, "big", 120, 0, 25)
	if word := missOf(cache, "big"); word != StateCacheMissOverBudget {
		t.Fatalf("a round of 25 past a budget of 20 read under %q; want %q", word, StateCacheMissOverBudget)
	}
}

// bareCache is a State cache of room for entries entries of the tests below:
// keys of one length and values of one size, so each costs the same.
func bareCache(entries int64) *stateCache {
	now := time.Unix(1_700_000_000, 0)
	return newStateCache(entries*entryCost(cacheKey("big", 0), make([]byte, 1000)), time.Minute, func() time.Time { return now })
}

func cacheKey(group execution.QueryGroupIdentity, index int) string {
	return fmt.Sprintf("%s/%05d", group, index)
}

// writeSeries records as applied the writes of series [from, to) of group for
// the Slot at.
func writeSeries(cache *stateCache, group execution.QueryGroupIdentity, at execution.EvaluationTime, from, to int) {
	for index := from; index < to; index++ {
		cache.applied(stateCacheEntry{key: cacheKey(group, index), group: group, raw: make([]byte, 1000), slot: at,
			usableUntil: cache.now().Add(time.Hour)})
	}
}

// heldOf is how many entries of group the cache holds.
func heldOf(cache *stateCache, group execution.QueryGroupIdentity) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if held := cache.groups[group]; held != nil {
		return len(held.entries)
	}
	return 0
}

// missOf is the word a lookup of group's first key gets.
func missOf(cache *stateCache, group execution.QueryGroupIdentity) string {
	_, word := cache.lookup(cacheKey(group, 0), execution.StatePreflightRequest{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: group}}}, execution.StatePreflightItem{})
	return word
}

// A Query Group of 15 entries a round, in a cache with room for 10, fills it
// once and leaves once: it is not taken back in write by write and given back
// whole every round after, and each later round reads it back under
// over_budget.
func TestAQueryGroupLargerThanTheCacheStaysOutRatherThanComingInEachRound(t *testing.T) {
	cache := bareCache(10)
	for round := 1; round <= 5; round++ {
		if round > 1 {
			if word := missOf(cache, "big"); word != StateCacheMissOverBudget {
				t.Fatalf("round %d read the group under %q, want %q", round, word, StateCacheMissOverBudget)
			}
		}
		writeSeries(cache, "big", execution.EvaluationTime(60*round), 0, 15)
	}
	stats := cache.stats()
	if stats.Departures[StateCacheDepartOverBudget] != 10 || stats.Bytes != 0 || stats.QueryGroups != 0 {
		t.Fatalf("after five rounds: over_budget %d, charge %d, %d groups; want the 10 it filled the cache with "+
			"to have left once, and nothing held", stats.Departures[StateCacheDepartOverBudget], stats.Bytes, stats.QueryGroups)
	}
}

// A replay's write for an older Slot, while the group is kept out, neither
// counts toward the round being measured nor closes it: the next round is
// still judged by a whole round of the group.
func TestAReplayWhileKeptOutDoesNotCutTheRoundShort(t *testing.T) {
	cache := bareCache(10)
	writeSeries(cache, "big", 60, 0, 15)
	writeSeries(cache, "big", 120, 0, 15)
	writeSeries(cache, "big", 60, 0, 1)
	writeSeries(cache, "big", 180, 0, 15)
	if held, left := heldOf(cache, "big"), cache.stats().Departures[StateCacheDepartOverBudget]; held != 0 || left != 10 {
		t.Fatalf("held %d entries, over_budget %d after a replay between two rounds of 15; want it still out, left once", held, left)
	}
}

// A Query Group given back for a Slot is measured by all it held for its
// latest Slot: beside another group's entry it no longer fits, and stays out
// rather than coming back to be given back again.
func TestAGroupGivenBackComesBackOnlyWhenWhatItHeldFits(t *testing.T) {
	cache := bareCache(10)
	writeSeries(cache, "big", 60, 0, 10)
	cache.YieldRetainedBytes(1)
	writeSeries(cache, "own", 60, 0, 1)
	writeSeries(cache, "big", 120, 0, 10)
	stats := cache.stats()
	if held := heldOf(cache, "big"); held != 0 || stats.Departures[StateCacheDepartOverBudget] != 0 || heldOf(cache, "own") != 1 {
		t.Fatalf("held big %d, own %d, departures %v; want the 10 given back kept out beside the other group's entry, "+
			"and nothing else leaving", held, heldOf(cache, "own"), stats.Departures)
	}
}

// A Query Group kept out comes back at the first round after one whole round
// of it fits: when it shrinks, once a round of the smaller size has been
// measured, and when the Slots leave the pool room for it, at the next round.
func TestAKeptOutQueryGroupComesBackOnceAWholeRoundOfItFits(t *testing.T) {
	t.Run("it shrinks", func(t *testing.T) {
		cache := bareCache(10)
		writeSeries(cache, "big", 60, 0, 15)
		writeSeries(cache, "big", 120, 0, 15)
		writeSeries(cache, "big", 180, 0, 8)
		if held := heldOf(cache, "big"); held != 0 {
			t.Fatalf("held %d entries in the round that measured 8, after a round of 15; want it still out", held)
		}
		writeSeries(cache, "big", 240, 0, 8)
		if held, left := heldOf(cache, "big"), cache.stats().Departures[StateCacheDepartOverBudget]; held != 8 || left != 10 {
			t.Fatalf("held %d entries, over_budget %d after a round of 8; want all 8 taken back and no further departure", held, left)
		}
	})
	t.Run("the pool has room again", func(t *testing.T) {
		cache := bareCache(20)
		room := uint64(cache.budget / 2)
		cache.BorrowRetainedWithin(func() uint64 { return room })
		writeSeries(cache, "big", 60, 0, 15)
		room = uint64(cache.budget)
		if word := missOf(cache, "big"); word != StateCacheMissYielded {
			t.Fatalf("the group kept out by the pool's room read under %q, want %q", word, StateCacheMissYielded)
		}
		writeSeries(cache, "big", 120, 0, 15)
		stats := cache.stats()
		if held := heldOf(cache, "big"); held != 15 || stats.Departures[StateCacheDepartYielded] != 10 ||
			stats.Departures[StateCacheDepartOverBudget] != 0 {
			t.Fatalf("held %d entries, departures %v with the room back; want all 15 taken back and the 10 that left once under yielded",
				held, stats.Departures)
		}
	})
}

// A Query Group kept out takes nothing from the ones that fit: with a group of
// 15 kept out of a cache with room for 10, a resident group of 4 keeps every
// entry, and a group of 2 that first writes mid-round, in the middle of the
// large group's writes, is taken in.
func TestAQueryGroupKeptOutLeavesTheRoomToTheGroupsThatFit(t *testing.T) {
	cache := bareCache(10)
	writeSeries(cache, "own", 60, 0, 4)
	writeSeries(cache, "big", 60, 0, 15)
	writeSeries(cache, "own", 120, 0, 4)
	writeSeries(cache, "big", 120, 0, 5)
	writeSeries(cache, "add", 120, 0, 2)
	writeSeries(cache, "big", 120, 5, 15)
	if own, added, big := heldOf(cache, "own"), heldOf(cache, "add"), heldOf(cache, "big"); own != 4 || added != 2 || big != 0 {
		t.Fatalf("held own %d, add %d, big %d; want the two groups that fit whole and the large one out", own, added, big)
	}
	if left := cache.stats().Departures[StateCacheDepartOverBudget]; left != 6 {
		t.Fatalf("over_budget %d; want only the 6 the large group filled the cache with in its first round", left)
	}
}
