// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
// http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under
// the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions and
// limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"

	ristretto "github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type FieldTypesCache interface {
	GetFieldType(ctx context.Context, tableID string, field string) (string, bool)
	HasTableCache(ctx context.Context, tableID string) bool
	SetFieldTypesFromMappings(ctx context.Context, tableID string, mappings []map[string]any)
	DeleteFieldTypesCache(ctx context.Context, tableID string)
	ClearFieldTypesCache(ctx context.Context)
}

type MappingCache struct {
	fieldTypesCache *ristretto.Cache[string, map[string]string]
}

func NewMappingCache() (*MappingCache, error) {
	maxCost := viper.GetInt64(MappingCacheMaxCostPath)
	numCounters := viper.GetInt64(MappingCacheNumCountersPath)
	bufferItems := viper.GetInt64(MappingCacheBufferItemsPath)

	fieldTypesCache, err := ristretto.NewCache(&ristretto.Config[string, map[string]string]{
		MaxCost:     maxCost,
		NumCounters: numCounters,
		BufferItems: bufferItems,
		Cost: func(value map[string]string) int64 {
			return int64(len(value))
		},
		IgnoreInternalCost: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create field types cache: %w", err)
	}

	return &MappingCache{
		fieldTypesCache: fieldTypesCache,
	}, nil
}

func (m *MappingCache) GetFieldType(ctx context.Context, tableID string, field string) (string, bool) {
	var err error
	ctx, span := trace.NewSpan(ctx, "mapping-cache-get-field-type")
	defer span.End(&err)
	span.Set("field", field)
	mapping, found := m.fieldTypesCache.Get(tableID)
	if !found {
		span.Set("cache_hit", "false")
		return "", false
	}
	span.Set("cache_hit", "true")

	fieldType, exists := mapping[field]
	if !exists {
		span.Set("field_exists", "false")
		return "", false
	}
	span.Set("field_exists", "true")
	span.Set("field_type", fieldType)
	return fieldType, true
}

func (m *MappingCache) HasTableCache(ctx context.Context, tableID string) bool {
	var err error
	_, found := m.fieldTypesCache.Get(tableID)
	ctx, span := trace.NewSpan(ctx, "mapping-cache-has-table-cache")
	defer span.End(&err)
	span.Set("table_id", tableID)
	span.Set("cache_found", fmt.Sprintf("%t", found))
	return found
}

func (m *MappingCache) SetFieldTypesFromMappings(ctx context.Context, tableID string, mappings []map[string]any) {
	var err error
	ctx, span := trace.NewSpan(ctx, "mapping-cache-set-field-types")
	defer span.End(&err)

	log.Debugf(ctx, "SetFieldTypesFromMappings called with tableID: %s, mappings count: %d", tableID, len(mappings))

	if len(mappings) == 0 {
		span.Set("mappings_count", "0")
		return
	}
	span.Set("table_id", tableID)

	fieldTypes := make(map[string]string)
	for _, mapping := range mappings {
		mapProperties("", mapping, fieldTypes)
	}

	if len(fieldTypes) == 0 {
		return
	}

	ttl := viper.GetDuration(MappingCacheTTLPath)

	existingMapping, found := m.fieldTypesCache.Get(tableID)
	if found {
		for k, v := range fieldTypes {
			existingMapping[k] = v
		}
		fieldTypes = existingMapping
	}

	success := m.fieldTypesCache.SetWithTTL(tableID, fieldTypes, int64(len(fieldTypes)), ttl)
	if !success {
		return
	}
	m.fieldTypesCache.Wait()
}

func (m *MappingCache) DeleteFieldTypesCache(ctx context.Context, tableID string) {
	m.fieldTypesCache.Del(tableID)
}

func (m *MappingCache) ClearFieldTypesCache(ctx context.Context) {
	m.fieldTypesCache.Clear()
}

var fieldTypesCache FieldTypesCache

func InitFieldTypesCache() error {
	cache, err := NewMappingCache()
	if err != nil {
		return fmt.Errorf("failed to initialize field types cache: %w", err)
	}
	fieldTypesCache = cache
	return nil
}

func GetFieldTypesCache() FieldTypesCache {
	return fieldTypesCache
}
