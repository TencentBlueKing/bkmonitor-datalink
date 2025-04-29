// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"sync"
	"time"
)

// MappingEntry 保存缓存映射数据和最后更新时间
type MappingEntry struct {
	mappings    []map[string]any
	lastUpdated time.Time
}

func (m MappingEntry) IsExpired(ttl time.Duration) bool {
	return time.Now().After(m.lastUpdated.Add(ttl))
}

// MappingCache 保存缓存映射
// 结构: map[tableID]map[fieldsStr]MappingEntry
type MappingCache struct {
	data map[string]map[string]MappingEntry
	lock sync.RWMutex
	ttl  time.Duration
}

// NewMappingCache 创建一个新的映射缓存
func NewMappingCache(ttl time.Duration) *MappingCache {
	return &MappingCache{
		data: make(map[string]map[string]MappingEntry),
		ttl:  ttl,
	}
}

// withReadLock 使用读锁执行函数
func (m *MappingCache) withReadLock(fn func() (interface{}, bool)) (interface{}, bool) {
	if m == nil {
		return nil, false
	}

	m.lock.RLock()
	defer m.lock.RUnlock()

	return fn()
}

// withWriteLock 使用写锁执行函数
func (m *MappingCache) withWriteLock(fn func()) {
	m.lock.Lock()
	defer m.lock.Unlock()

	fn()
}

// SetTTL 设置缓存的TTL
func (m *MappingCache) SetTTL(ttl time.Duration) {
	if m == nil {
		return
	}
	m.ttl = ttl
}

// GetTTL 获取缓存的TTL
func (m *MappingCache) GetTTL() time.Duration {
	if m == nil {
		return 0
	}

	return m.ttl
}

// Put 添加映射到缓存
func (m *MappingCache) Put(tableID string, fieldsStr string, mappings []map[string]any) {
	m.withWriteLock(func() {
		if m.data == nil {
			m.data = make(map[string]map[string]MappingEntry)
		}

		if _, ok := m.data[tableID]; !ok {
			m.data[tableID] = make(map[string]MappingEntry)
		}

		m.data[tableID][fieldsStr] = MappingEntry{
			mappings:    mappings,
			lastUpdated: time.Now(),
		}
	})
}

// Get 从缓存获取映射条目，自动处理过期条目
func (m *MappingCache) Get(tableID string, fieldsStr string) (MappingEntry, bool) {
	if m == nil || m.data == nil {
		return MappingEntry{}, false
	}

	// 直接使用一次读锁检查是否有效（非过期），如果有效则返回
	var result MappingEntry
	var found bool

	m.lock.RLock()
	if tableMap, ok := m.data[tableID]; ok {
		if entry, ok := tableMap[fieldsStr]; ok {
			if !entry.IsExpired(m.ttl) { // 直接使用m.ttl而不是调用GetTTL()避免嵌套锁
				result = entry
				found = true
			}
		}
	}
	m.lock.RUnlock()

	if found {
		return result, true
	}

	// 如果读锁检查没有找到有效条目，使用写锁检查并清理过期条目
	return m.cleanupOrGetEntry(tableID, fieldsStr)
}

// 辅助方法：使用写锁清理过期条目或获取有效条目
func (m *MappingCache) cleanupOrGetEntry(tableID string, fieldsStr string) (MappingEntry, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	// 双重检查
	tableMap, ok := m.data[tableID]
	if !ok {
		return MappingEntry{}, false
	}

	entry, ok := tableMap[fieldsStr]
	if !ok {
		return MappingEntry{}, false
	}

	if entry.IsExpired(m.ttl) { // 直接使用m.ttl而不是调用GetTTL()
		// 清理过期条目
		delete(tableMap, fieldsStr)
		if len(tableMap) == 0 {
			delete(m.data, tableID)
		}
		return MappingEntry{}, false
	}

	return entry, true
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(tableID string, fieldsStr string) {
	if m == nil || m.data == nil {
		return
	}

	m.withWriteLock(func() {
		tableMap, ok := m.data[tableID]
		if !ok {
			return
		}

		if fieldsStr == "" {
			delete(m.data, tableID)
		} else {
			delete(tableMap, fieldsStr)
			if len(tableMap) == 0 {
				delete(m.data, tableID)
			}
		}
	})
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	if m == nil {
		return
	}

	m.withWriteLock(func() {
		m.data = make(map[string]map[string]MappingEntry)
	})
}

// checkMappingCache 检查映射是否已缓存
func (i *Instance) checkMappingCache(tableID string, fieldsStr string) ([]map[string]any, bool) {
	entry, exist := i.mappingCache.Get(tableID, fieldsStr)
	if !exist {
		return nil, false
	}

	return entry.mappings, true
}

// writeMappingCache 写入映射到缓存
func (i *Instance) writeMappingCache(mappings []map[string]any, tableID string, fieldsStr string) error {
	if len(mappings) == 0 {
		return fmt.Errorf("cannot cache empty mappings")
	}

	i.mappingCache.Put(tableID, fieldsStr, mappings)
	return nil
}
