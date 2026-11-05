// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"maps"
	"sync"
	"time"
)

type pendingEntry struct {
	Value  Value
	Digest fingerprint
	Force  bool
}

type snapshot struct {
	items      map[Key]pendingEntry
	generation uint64
}

// Stats 不携带 DataID、指标名等高基数标签，返回值为独立快照。
type Stats struct {
	Pending, BloomVersions       int
	Exported, Suppressed, Forced uint64
	Rotations, Cleans, Failures  uint64
	Dropped                      map[string]uint64
	FlushCount                   uint64
	FlushSeconds                 float64
	FlushBuckets                 map[float64]uint64
}

// Store 以有界待发集合和双 Bloom 实现并发观察、同键合并和去重。
type Store struct {
	mu            sync.Mutex
	config        Config
	pending       map[Key]pendingEntry
	filters       [2]bloomFilter
	current       int
	lastRotation  time.Time
	generation    uint64
	forceNext     bool
	closed        bool
	windowStart   time.Time
	updates       int
	dataIDUpdates map[int32]int
	stats         Stats
	now           func() time.Time
}

func newStore(config Config) *Store {
	return &Store{
		config:        config,
		pending:       make(map[Key]pendingEntry),
		filters:       newBloomPair(bloomDesignLoad),
		dataIDUpdates: make(map[int32]int),
		stats: Stats{
			Dropped:      make(map[string]uint64),
			FlushBuckets: map[float64]uint64{0.01: 0, 0.05: 0, 0.1: 0, 0.5: 0, 1: 0, 5: 0, 10: 0},
		},
		now:       time.Now,
		forceNext: true,
	}
}

func (s *Store) drop(reason string) bool {
	s.stats.Dropped[reason]++
	return false
}

func (s *Store) rotate(now time.Time) {
	if s.lastRotation.IsZero() {
		s.lastRotation = now
		return
	}
	interval := s.config.Dedup.MaxAge / 2
	steps := now.Sub(s.lastRotation) / interval
	if steps <= 0 {
		return
	}
	if steps >= 2 {
		s.filters[0].reset()
		s.filters[1].reset()
	} else {
		s.current = 1 - s.current
		s.filters[s.current].reset()
	}
	s.lastRotation = s.lastRotation.Add(steps * interval)
	s.stats.Rotations += uint64(steps)
}

func (s *Store) contains(d fingerprint) bool {
	return s.filters[0].contains(d) || s.filters[1].contains(d)
}

func (s *Store) allowUpdate(dataID int32, now time.Time) string {
	if s.windowStart.IsZero() || now.Sub(s.windowStart) >= time.Minute {
		s.windowStart, s.updates = now, 0
		clear(s.dataIDUpdates)
	}
	if s.updates >= s.config.Limits.Instance {
		return "global_circuit"
	}
	limit := s.config.Limits.DefaultSource
	if configured, ok := s.config.Limits.ByDataID[dataID]; ok {
		limit = configured
	}
	if s.dataIDUpdates[dataID] >= limit {
		return "dataid_circuit"
	}
	s.updates++
	s.dataIDUpdates[dataID]++
	return ""
}

// Observe 合并主键的最新值，拒绝原因可从 Stats 的 Dropped 查询。
func (s *Store) Observe(key Key, value Value) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.drop("closed")
	}
	if n, ok := s.config.Limits.ByDataID[key.DataID]; ok && n == 0 {
		return s.drop("blacklist")
	}
	old, exists := s.pending[key]
	if exists && old.Value == value {
		return true
	}
	entry := Entry{key, value}
	now := s.now()
	s.rotate(now)
	if !entry.valid() {
		return s.drop("invalid")
	}
	d := digest(entry)
	if !exists && s.contains(d) {
		s.stats.Suppressed++
		return true
	}
	if !exists && len(s.pending) >= s.config.Exporter.QueueSize {
		return s.drop("pending_capacity")
	}
	if reason := s.allowUpdate(key.DataID, now); reason != "" {
		delete(s.pending, key)
		return s.drop(reason)
	}
	entry = entry.clone()
	s.pending[entry.Key] = pendingEntry{Value: entry.Value, Digest: d, Force: old.Force}
	return true
}

// Clean 保留待发值；已在途的旧快照成功后不得重新填充被清理的 Bloom。
func (s *Store) Clean() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.filters[0].reset()
	s.filters[1].reset()
	s.generation++
	s.forceNext = true
	s.stats.Cleans++
	return nil
}

func (s *Store) take() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotate(s.now())
	if len(s.pending) == 0 {
		return snapshot{}
	}
	items := s.pending
	s.pending = make(map[Key]pendingEntry)
	if s.forceNext {
		for key, item := range items {
			item.Force = true
			items[key] = item
		}
	}
	s.forceNext = false
	return snapshot{items, s.generation}
}

func (s *Store) suppressed(item pendingEntry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !item.Force && s.contains(item.Digest) {
		s.stats.Suppressed++
		return true
	}
	return false
}

func (s *Store) ack(key Key, item pendingEntry, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 时间轮换期间完成的请求按确认时刻登记；Clean 则让旧快照失效。
	s.rotate(s.now())
	filter := &s.filters[s.current]
	if generation == s.generation && !filter.contains(item.Digest) {
		filter.add(item.Digest)
	}
	s.stats.Exported++
	if item.Force {
		s.stats.Forced++
		if newer, ok := s.pending[key]; ok && newer.Value != item.Value {
			newer.Force = true
			s.pending[key] = newer
		}
	}
}

func (s *Store) restore(items map[Key]pendingEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range items {
		if newer, ok := s.pending[key]; ok {
			newer.Force = newer.Force || item.Force
			s.pending[key] = newer
		} else if len(s.pending) < s.config.Exporter.QueueSize {
			s.pending[key] = item
		} else {
			s.drop("restore_capacity")
		}
	}
}

// Stats 返回不共享内部 map 的统计快照。
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.stats
	result.Pending = len(s.pending)
	result.BloomVersions = s.filters[0].count + s.filters[1].count
	result.Dropped = maps.Clone(s.stats.Dropped)
	result.FlushBuckets = maps.Clone(s.stats.FlushBuckets)
	return result
}

func (s *Store) observeFlush(duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := duration.Seconds()
	s.stats.FlushCount++
	s.stats.FlushSeconds += seconds
	for bound := range s.stats.FlushBuckets {
		if seconds <= bound {
			s.stats.FlushBuckets[bound]++
		}
	}
}

func (s *Store) seal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *Store) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Dropped["shutdown"] += uint64(len(s.pending))
	s.pending = nil
	s.filters = [2]bloomFilter{}
	s.dataIDUpdates = nil
}
