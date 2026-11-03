// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import "time"

// OpenCounts is, for every strategy this copy tracks, how many open alerts its
// set holds as the gate reads it, calibration applied -- a loaded set with no
// member is there as zero -- and the strategies it cannot count: a set never
// loaded, stale past its bound, or refused for its size. Those are unknown and
// must never read as zero. Every tracked strategy is in exactly one of the
// two, so a strategy in neither is one this copy does not track.
func (cache *Cache) OpenCounts() (map[StrategyKey]int, []StrategyKey) {
	if cache == nil {
		return nil, nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	counts := map[StrategyKey]int{}
	var unknown []StrategyKey
	for _, key := range cache.index.order {
		entry := cache.index.entries[key]
		if entry == nil {
			continue
		}
		if entry.indexReadAt.IsZero() || entry.tooLarge != nil || cache.stale(entry, now) {
			unknown = append(unknown, key)
			continue
		}
		counts[key] = len(cache.indexMembers(key))
	}
	return counts, unknown
}

// The set's members as a stand-in for the alerts a calibration lists. When
// the link's reconcile gave no usable rows for a strategy - no current
// calibration, and no truncated one that names an alert - a policy close
// can still address each member: the link resolves a close to the active
// alert by tenant, event source and fingerprint, and a member is a
// fingerprint once the Console has confirmed the sets are keyed by our
// alert ids. They answer only under OpenCounts' rule - a set read, not
// stale, not refused for its size - and only for a trusted copy; anywhere
// else ok is false and nothing is to be decided from the answer. Where the
// reconcile gave rows, ok is false too: the rows answer, not the members.

// UnlistedMembers is the strategy's members when its alerts are not listed
// and its set stands in for them.
func (cache *Cache) UnlistedMembers(key StrategyKey) (members []string, ok bool) {
	if cache == nil {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.membersStandIn(cache.index.entries[key], cache.now()) {
		return nil, false
	}
	return cache.indexMembers(key), true
}

// UnlistedMemberCount is how many members UnlistedMembers answers.
func (cache *Cache) UnlistedMemberCount(key StrategyKey) (count int, ok bool) {
	members, ok := cache.UnlistedMembers(key)
	return len(members), ok
}

// UnlistedHolds is whether the fingerprint is one of UnlistedMembers.
func (cache *Cache) UnlistedHolds(key StrategyKey, fingerprint string) (held, ok bool) {
	if cache == nil {
		return false, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	if !cache.membersStandIn(cache.index.entries[key], now) {
		return false, false
	}
	return cache.indexContains(member{key: key, fingerprint: fingerprint}, now, false), true
}

// membersStandIn is whether the entry's members stand in for its alerts.
// Called with the lock held.
func (cache *Cache) membersStandIn(entry *indexEntry, now time.Time) bool {
	if entry == nil || !cache.trusted() || cache.calibrated(entry, now) || (cache.partial(entry, now) && len(entry.alerts) > 0) {
		return false
	}
	return !entry.indexReadAt.IsZero() && entry.tooLarge == nil && !cache.stale(entry, now)
}
