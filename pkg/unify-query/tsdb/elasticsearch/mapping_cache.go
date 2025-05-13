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
	"github.com/dgraph-io/ristretto/v2"
	"time"
)

var (
	fieldTypesCache *MappingCache
)

var (
	DefaultMappingCacheTTL       = 5 * time.Minute
	DefaultNumCounters     int64 = 1e6
	DefaultMaxCost         int64 = 1e8 // 100MB
	DefaultBufferItems     int64 = 64
)

func init() {
	fieldTypesCache = NewMappingCache(DefaultMappingCacheTTL)
}

// MappingCache 用于缓存字段类型
type MappingCache struct {
	cache *ristretto.Cache[string, string]
	ttl   time.Duration
}

// NewMappingCache 创建MappingCache
func NewMappingCache(ttl time.Duration) *MappingCache {
	cache, err := ristretto.NewCache(&ristretto.Config[string, string]{
		NumCounters: DefaultNumCounters,
		MaxCost:     DefaultMaxCost,
		BufferItems: DefaultBufferItems,
		Metrics:     true,
	})
	if err != nil {
		panic(fmt.Errorf("NewMappingCache error: %v", err))
	}

	return &MappingCache{
		cache: cache,
		ttl:   ttl,
	}
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
	for field, fieldType := range mapping {
		key := createCacheKey(tableID, field)
		m.cache.SetWithTTL(key, fieldType, 1, m.ttl)
	}

	m.cache.Wait()
}

// GetFieldType 从缓存获取字段类型
func (m *MappingCache) GetFieldType(tableID string, fieldsStr string) (string, bool) {
	key := createCacheKey(tableID, fieldsStr)

	fieldType, found := m.cache.Get(key)
	if !found {
		return "", false
	}

	return fieldType, found
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(tableID string, field string) {
	key := createCacheKey(tableID, field)
	m.cache.Del(key)
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	m.cache.Clear()
}

// createCacheKey 创建缓存键
func createCacheKey(tableID string, field string) string {
	return fmt.Sprintf("%s:%s", tableID, field)
}
