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

	"github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	fieldTypesCache *MappingCache
)

var (
	DefaultMappingCacheTTL = 5 * time.Minute
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
		NumCounters: viper.GetInt64(memcache.RistrettoNumCountersPath),
		MaxCost:     viper.GetInt64(memcache.RistrettoMaxCostPath),
		BufferItems: viper.GetInt64(memcache.RistrettoBufferItemsPath),
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
func (m *MappingCache) AppendFieldTypesCache(ctx context.Context, tableID string, mapping map[string]string) {
	var err error

	_, span := trace.NewSpan(ctx, "mapping-cache-append-field-types")
	defer span.End(&err)

	span.Set("table-id", tableID)
	span.Set("mapping-size", len(mapping))

	fields := make([]string, 0, len(mapping))
	for field, fieldType := range mapping {
		key := createCacheKey(tableID, field)
		fields = append(fields, key)
		m.cache.SetWithTTL(key, fieldType, 1, m.ttl)
	}

	span.Set("mapping-keys", fields)

	m.cache.Wait()
}

// GetFieldType 从缓存获取字段类型
func (m *MappingCache) GetFieldType(ctx context.Context, tableID string, fieldsStr string) (string, bool) {
	var (
		result string
		ok     bool
		err    error
	)

	_, span := trace.NewSpan(ctx, "mapping-cache-get-field-type")
	defer span.End(&err)

	key := createCacheKey(tableID, fieldsStr)
	span.Set("table-id", tableID)
	span.Set("field", fieldsStr)
	span.Set("cache-key", key)

	result, ok = m.cache.Get(key)
	if !ok {
		span.Set("cache-hit", "miss")
	} else {
		span.Set("cache-hit", "hit")
	}

	return result, ok
}

// Delete 从缓存删除映射
func (m *MappingCache) Delete(ctx context.Context, tableID string, field string) {
	var err error

	_, span := trace.NewSpan(ctx, "mapping-cache-delete")
	defer span.End(&err)

	key := createCacheKey(tableID, field)
	span.Set("table-id", tableID)
	span.Set("field", field)
	span.Set("cache-key", key)

	m.cache.Del(key)
}

// Clear 清空缓存
func (m *MappingCache) Clear(ctx context.Context) {
	var err error

	_, span := trace.NewSpan(ctx, "mapping-cache-clear")
	defer span.End(&err)

	m.cache.Clear()
}

// createCacheKey 创建缓存键
func createCacheKey(tableID string, field string) string {
	return fmt.Sprintf("%s:%s", tableID, field)
}
