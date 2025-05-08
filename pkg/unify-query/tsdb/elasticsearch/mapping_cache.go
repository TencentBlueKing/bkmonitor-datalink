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
	"sync"
	"time"
)

// MappingEntry 保存缓存映射类型和最后更新时间
type MappingEntry struct {
	fieldType   string
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
	m.ttl = ttl
}

// GetTTL 获取缓存的TTL
func (m *MappingCache) GetTTL() time.Duration {
	return m.ttl
}

func (m *MappingCache) AppendFieldTypesCache(tableID string, mapping map[string]string) {
	m.withWriteLock(func() {
		if m.data == nil {
			m.data = make(map[string]map[string]MappingEntry)
		}

		if _, ok := m.data[tableID]; !ok {
			m.data[tableID] = make(map[string]MappingEntry)
		}

		for field, fieldType := range mapping {
			m.data[tableID][field] = MappingEntry{
				fieldType:   fieldType,
				lastUpdated: time.Now(),
			}
		}
	})
}

func (m *MappingCache) GetFieldType(tableID string, fieldsStr string) (string, bool) {
	entry, ok := m.get(tableID, fieldsStr)
	return entry.fieldType, ok
}

func (m *MappingCache) get(tableID string, fieldsStr string) (MappingEntry, bool) {
	if m.data == nil {
		return MappingEntry{}, false
	}

	readResult, readOK := m.withReadLock(func() (interface{}, bool) {
		if tableMap, ok := m.data[tableID]; ok {
			if entry, ok := tableMap[fieldsStr]; ok {
				if !entry.IsExpired(m.ttl) {
					return entry, true
				}
			}
		}
		return nil, false
	})

	if readOK && readResult != nil {
		return readResult.(MappingEntry), true
	}

	return m.cleanupOrGetEntry(tableID, fieldsStr)
}

// 辅助方法：使用写锁清理过期条目或获取有效条目
func (m *MappingCache) cleanupOrGetEntry(tableID string, fieldsStr string) (MappingEntry, bool) {
	var result MappingEntry
	var found bool

	m.withWriteLock(func() {
		// 双重检查
		tableMap, ok := m.data[tableID]
		if !ok {
			return
		}

		entry, ok := tableMap[fieldsStr]
		if !ok {
			return
		}

		if entry.IsExpired(m.ttl) {
			// 清理过期条目
			delete(tableMap, fieldsStr)
			if len(tableMap) == 0 {
				delete(m.data, tableID)
			}
			return
		}

		result = entry
		found = true
	})

	return result, found
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(tableID string, field string) {
	if m.data == nil {
		return
	}

	m.withWriteLock(func() {
		tableMap, ok := m.data[tableID]
		if !ok {
			return
		}

		if field == "" {
			delete(m.data, tableID)
		} else {
			delete(tableMap, field)
			if len(tableMap) == 0 {
				delete(m.data, tableID)
			}
		}
	})
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	m.withWriteLock(func() {
		m.data = make(map[string]map[string]MappingEntry)
	})
}
