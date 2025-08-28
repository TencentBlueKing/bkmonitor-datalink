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
	"sync"

	ristretto "github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"
)

var (
	fieldTypesCache *MappingCache
	once            sync.Once
)

type MappingCache struct {
	fieldTypesCache *ristretto.Cache[string, map[string]any]
}

func NewMappingCache() (cache *MappingCache) {
	c, _ := ristretto.NewCache(&ristretto.Config[string, map[string]any]{
		MaxCost:     viper.GetInt64(MappingCacheMaxCostPath),
		NumCounters: viper.GetInt64(MappingCacheNumCountersPath),
		BufferItems: viper.GetInt64(MappingCacheBufferItemsPath),
		Cost: func(value map[string]any) int64 {
			return int64(len(value))
		},
		IgnoreInternalCost: false,
	})

	return &MappingCache{
		fieldTypesCache: c,
	}
}

func (m *MappingCache) GetAliasMappings(alias []string, fetchAliasMapping func(alias []string) (map[string]any, error)) ([]map[string]any, error) {
	var res []map[string]any
	var missingAlias []string

	for _, a := range alias {
		// 优先从缓存获取，如果缓存没有，则加入到 missingAlias 列表中
		if mapping, ok := m.fieldTypesCache.Get(a); ok {
			res = append(res, mapping)
		} else {
			missingAlias = append(missingAlias, a)
		}
	}

	if len(missingAlias) > 0 {
		mappings, err := fetchAliasMapping(missingAlias)
		if err != nil {
			return nil, err
		}

		for indexName, value := range mappings {
			mappingData, ok := fetchMappingData(value)
			if !ok {
				continue
			}
			res = append(res, mappingData)
			cost := int64(len(mappingData))
			m.fieldTypesCache.SetWithTTL(indexName, mappingData, cost, viper.GetDuration(MappingCacheTTLPath))
		}
	}
	return res, nil
}

func fetchMappingData(value interface{}) (map[string]any, bool) {
	if mappingData, ok := value.(map[string]any)["mappings"].(map[string]any); ok {
		return mappingData, true
	}
	return nil, false
}

func GetMappingCache() *MappingCache {
	once.Do(func() {
		fieldTypesCache = NewMappingCache()
	})

	return fieldTypesCache
}
