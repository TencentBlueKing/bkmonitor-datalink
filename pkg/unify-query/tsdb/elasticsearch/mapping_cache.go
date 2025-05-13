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
	"context"
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var (
	fieldTypesCache *MappingCache
)

var (
	DefaultMappingCacheTTL = 5 * time.Minute
	DefaultCleanupInterval = 10 * time.Minute
)

func init() {
	fieldTypesCache = NewMappingCache(DefaultMappingCacheTTL)
}

// MappingCache 用于缓存字段类型
type MappingCache struct {
	cache *cache.Cache
	ttl   time.Duration
}

// NewMappingCache 创建MappingCache
func NewMappingCache(ttl time.Duration) *MappingCache {
	return &MappingCache{
		cache: cache.New(ttl, DefaultCleanupInterval),
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
		m.cache.Set(key, fieldType, m.ttl)
	}
}

// GetFieldType 从缓存获取字段类型
func (m *MappingCache) GetFieldType(tableID string, fieldsStr string) (string, bool) {
	key := createCacheKey(tableID, fieldsStr)
	value, found := m.cache.Get(key)
	if !found {
		log.Infof(context.TODO(), "GetFieldType tableID: %s, fieldsStr: %s, fieldType: not found", tableID, fieldsStr)
		return "", false
	}

	fieldType, ok := value.(string)
	return fieldType, ok
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(tableID string, field string) {
	key := createCacheKey(tableID, field)
	m.cache.Delete(key)
}

// Clear 清空缓存
func (m *MappingCache) Clear() {
	m.cache.Flush()
}

// createCacheKey 创建缓存键
func createCacheKey(tableID string, field string) string {
	return fmt.Sprintf("%s:%s", tableID, field)
}
