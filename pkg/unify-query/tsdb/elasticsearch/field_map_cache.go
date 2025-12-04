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
	"sync"

	"github.com/patrickmn/go-cache"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	fieldMapCache *FieldMapCache
	once          sync.Once
)

type FieldMapCache struct {
	cache *cache.Cache
}

func (m *FieldMapCache) Close() error {
	if m.cache != nil {
		m.cache.Flush()
	}
	return nil
}

func (m *FieldMapCache) cacheAndMergeResult(ctx context.Context, aliases []string, fetchedFieldMap metadata.FieldsMap, result metadata.FieldsMap) {
	ttl := viper.GetDuration(MappingCacheTTLPath)

	for _, alias := range aliases {
		m.cache.Set(alias, fetchedFieldMap, ttl)
		log.Infof(ctx, `[fieldMap cache] set alias: %s fieldsMap to cache (fields count: %d)`, alias, len(fetchedFieldMap))
	}

	for fieldName, fieldOption := range fetchedFieldMap {
		result.Set(fieldName, fieldOption)
	}
}

func (m *FieldMapCache) GetFieldsMap(ctx context.Context, alias []string, fetchFieldOptionCallback func(missingAlias []string) (metadata.FieldsMap, error)) (metadata.FieldsMap, error) {
	var (
		err  error
		span *trace.Span
	)
	ctx, span = trace.NewSpan(ctx, "get-alias-mapping")
	defer span.End(&err)

	if m.cache == nil {
		return nil, metadata.NewMessage(
			metadata.MsgQueryES,
			"缓存未初始化",
		).Error(ctx, nil)
	}

	var (
		result       = make(metadata.FieldsMap)
		missingAlias []string
		hitAlias     []string
	)

	for _, a := range alias {
		if value, found := m.cache.Get(a); found {
			if aliasFieldsMap, ok := value.(metadata.FieldsMap); ok {
				log.Infof(ctx, `[fieldMap cache] got alias: %s from cache`, a)
				hitAlias = append(hitAlias, a)
				for fieldName, fieldOption := range aliasFieldsMap {
					result.Set(fieldName, fieldOption)
				}
			} else {
				log.Warnf(ctx, `[fieldMap cache] alias: %s type assertion failed`, a)
				missingAlias = append(missingAlias, a)
			}
		} else {
			log.Infof(ctx, `[fieldMap cache] alias: %s missing in cache`, a)
			missingAlias = append(missingAlias, a)
		}
	}

	span.Set("cache-alias", hitAlias)
	span.Set("missing-alias", missingAlias)

	if len(missingAlias) > 0 {
		fetchedFieldMap, err := fetchFieldOptionCallback(missingAlias)
		if err != nil {
			return nil, err
		}

		m.cacheAndMergeResult(ctx, missingAlias, fetchedFieldMap, result)
	}

	return result, nil
}

func GetMappingCache() *FieldMapCache {
	once.Do(func() {
		defaultExpiration := viper.GetDuration(MappingCacheTTLPath)
		cleanupInterval := viper.GetDuration(MappingCacheCleanupPath)

		fieldMapCache = &FieldMapCache{
			cache: cache.New(defaultExpiration, cleanupInterval),
		}
	})

	return fieldMapCache
}
