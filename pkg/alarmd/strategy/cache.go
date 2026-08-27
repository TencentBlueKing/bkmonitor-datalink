// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"container/list"
	"sync"
	"time"
)

type CacheStats struct {
	Hits         uint64
	Misses       uint64
	NegativeHits uint64
	Evictions    uint64
	Entries      int
	Bytes        int
}

type compileCacheEntry struct {
	key       string
	result    CompileResult
	size      int
	negative  bool
	expiresAt time.Time
}

type compileCache struct {
	mu          sync.Mutex
	entries     map[string]*list.Element
	lru         *list.List
	maxEntries  int
	maxBytes    int
	negativeTTL time.Duration
	bytes       int
	stats       CacheStats
}

func newCompileCache(maxEntries, maxBytes int, negativeTTL time.Duration) *compileCache {
	return &compileCache{
		entries: make(map[string]*list.Element, maxEntries), lru: list.New(),
		maxEntries: maxEntries, maxBytes: maxBytes, negativeTTL: negativeTTL,
	}
}

func (c *compileCache) get(key string, now time.Time) (CompileResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		c.stats.Misses++
		return CompileResult{}, false
	}
	entry := element.Value.(*compileCacheEntry)
	if entry.negative && !entry.expiresAt.After(now) {
		c.remove(element, false)
		c.stats.Misses++
		return CompileResult{}, false
	}
	c.lru.MoveToFront(element)
	c.stats.Hits++
	if entry.negative {
		c.stats.NegativeHits++
	}
	return entry.result, true
}

func (c *compileCache) put(key string, result CompileResult, size int, negative bool, now time.Time) {
	if size <= 0 || size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		c.remove(existing, false)
	}
	entry := &compileCacheEntry{key: key, result: result, size: size, negative: negative}
	if negative {
		entry.expiresAt = now.Add(c.negativeTTL)
	}
	element := c.lru.PushFront(entry)
	c.entries[key] = element
	c.bytes += size
	for len(c.entries) > c.maxEntries || c.bytes > c.maxBytes {
		c.remove(c.lru.Back(), true)
	}
}

func (c *compileCache) remove(element *list.Element, eviction bool) {
	if element == nil {
		return
	}
	entry := element.Value.(*compileCacheEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.size
	c.lru.Remove(element)
	if eviction {
		c.stats.Evictions++
	}
}

func (c *compileCache) snapshot() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	stats.Entries = len(c.entries)
	stats.Bytes = c.bytes
	return stats
}
