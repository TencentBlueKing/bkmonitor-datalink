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

var (
	fieldTypesCache MappingCache
)

var (
	DefaultMappingCacheTTL = 5 * time.Minute
)

func init() {
	fieldTypesCache = NewMappingCache(DefaultMappingCacheTTL)
}

// MappingEntry 保存缓存映射类型和最后更新时间
type MappingEntry struct {
	fieldType   string
	lastUpdated time.Time
}

// IsExpired 判断缓存是否过期
func (m MappingEntry) IsExpired(ttl time.Duration) bool {
	return time.Now().After(m.lastUpdated.Add(ttl))
}

// MappingEntryKey 保存缓存映射的键
type MappingEntryKey struct {
	tableID   string
	fieldsStr string
}

// MappingCache 保存缓存映射
// 结构: map[MappingEntryKey]MappingEntry
type MappingCache struct {
	data map[MappingEntryKey]MappingEntry
	lock sync.RWMutex
	ttl  time.Duration
}

// NewMappingCache 创建一个新的映射缓存
func NewMappingCache(ttl time.Duration) MappingCache {
	return MappingCache{
		data: make(map[MappingEntryKey]MappingEntry),
		ttl:  ttl,
		lock: sync.RWMutex{},
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

// AppendFieldTypesCache 将字段类型添加到缓存
func (m *MappingCache) AppendFieldTypesCache(tableID string, mapping map[string]string) {
	m.withWriteLock(func() {
		if m.data == nil {
			m.data = make(map[MappingEntryKey]MappingEntry)
		}

		for field, fieldType := range mapping {
			m.data[MappingEntryKey{
				tableID:   tableID,
				fieldsStr: field,
			}] = MappingEntry{
				fieldType:   fieldType,
				lastUpdated: time.Now(),
			}
		}
	})
}

// GetFieldType 从缓存获取字段类型，自动处理过期条目
func (m *MappingCache) GetFieldType(tableID string, fieldsStr string) (string, bool) {
	entry, ok := m.get(tableID, fieldsStr)
	return entry.fieldType, ok
}

func (m *MappingCache) get(tableID string, fieldsStr string) (MappingEntry, bool) {
	return m.cleanupOrGetEntry(tableID, fieldsStr)
}

// 辅助方法：使用写锁清理过期条目或获取有效条目
func (m *MappingCache) cleanupOrGetEntry(tableID string, fieldsStr string) (MappingEntry, bool) {
	var result MappingEntry
	var found bool

	m.withWriteLock(func() {
		k := MappingEntryKey{
			tableID:   tableID,
			fieldsStr: fieldsStr,
		}

		entry, ok := m.data[k]
		if !ok {
			return
		}

		if entry.IsExpired(m.ttl) {
			delete(m.data, k)
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
		k := MappingEntryKey{
			tableID:   tableID,
			fieldsStr: field,
		}

		delete(m.data, k)
	})
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	m.withWriteLock(func() {
		m.data = make(map[MappingEntryKey]MappingEntry)
	})
}
