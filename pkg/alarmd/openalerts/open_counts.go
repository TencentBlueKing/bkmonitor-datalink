// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

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
