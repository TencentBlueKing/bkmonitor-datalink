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

	"github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"
)

var (
	fieldTypesCache *MappingCache
	once            sync.Once
)

type MappingCache struct {
	fieldTypesCache *ristretto.Cache[string, map[string]any]
}

func (m *MappingCache) ClearFieldTypesCache() {
	m.fieldTypesCache.Clear()
}

func NewMappingCache() (cache *MappingCache) {
	c, _ := ristretto.NewCache(&ristretto.Config[string, []map[string]any]{
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

func (m *MappingCache) GetAliasMappings(alias []string, fetchAliasMapping func(alias string) (map[string]any, error)) ([]map[string]any, error) {
	var res []map[string]any
	for _, a := range alias {
		if mapping, ok := m.fieldTypesCache.Get(a); ok {
			res = append(res, mapping)
		} else {
			fetchedMapping, err := fetchAliasMapping(a)
			if err != nil {
				return nil, err
			}
			ttl := viper.GetDuration(MappingCacheTTLPath)
			m.fieldTypesCache.SetWithTTL(a, fetchedMapping, int64(len(mapping)), ttl)
			res = append(res, fetchedMapping)
		}
	}
	return res, nil
}

func GetMappingCache() *MappingCache {
	once.Do(func() {
		fieldTypesCache = NewMappingCache()
	})

	return fieldTypesCache
}
