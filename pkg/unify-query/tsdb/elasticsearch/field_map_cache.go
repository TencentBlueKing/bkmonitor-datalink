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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	ristretto "github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	fieldMapCache *FieldMapCache
	once          sync.Once
)

type FieldMapCache struct {
	cache *ristretto.Cache[string, metadata.FieldOption]
}

func (m *FieldMapCache) GetFieldsMap(ctx context.Context, alias []string, fetchFieldOptionCallback func(missingAlias []string) (metadata.FieldsMap, error)) (metadata.FieldsMap, error) {
	var (
		result       = make(metadata.FieldsMap)
		missingAlias []string
		hitAlias     []string
	)

	var (
		err  error
		span *trace.Span
	)
	ctx, span = trace.NewSpan(ctx, "get-alias-mapping")
	defer span.End(&err)

	for _, a := range alias {
		if mapping, ok := m.cache.Get(a); ok {
			hitAlias = append(hitAlias, a)
			result.Set(a, mapping)
		} else {
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

		for a, fieldOptions := range fetchedFieldMap {
			result.Set(a, fieldOptions)
			m.cache.SetWithTTL(a, fieldOptions, 1, viper.GetDuration(MappingCacheTTLPath))
		}
	}

	return result, nil
}

func GetMappingCache() *FieldMapCache {
	once.Do(func() {
		c, _ := ristretto.NewCache(&ristretto.Config[string, metadata.FieldOption]{
			MaxCost:     viper.GetInt64(MappingCacheMaxCostPath),
			NumCounters: viper.GetInt64(MappingCacheNumCountersPath),
			BufferItems: viper.GetInt64(MappingCacheBufferItemsPath),
		})

		fieldMapCache = &FieldMapCache{
			cache: c,
		}
	})

	return fieldMapCache
}
