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
	"testing"
	"time"

	ristretto "github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestFieldMapCache(t *testing.T) {
	viper.Set(MappingCacheMaxCostPath, 1000)
	viper.Set(MappingCacheNumCountersPath, 10000)
	viper.Set(MappingCacheBufferItemsPath, 64)
	viper.Set(MappingCacheTTLPath, "1m")

	tests := []struct {
		name         string
		alias        []string
		preSetCache  map[string]metadata.FieldOption
		expectFetch  bool
		expectFields map[string]metadata.FieldOption
	}{
		{
			name:        "缓存未命中",
			alias:       []string{"test_alias"},
			expectFetch: true,
			expectFields: map[string]metadata.FieldOption{
				"test_alias": {
					FieldName: "field_test_alias",
					FieldType: "keyword",
					IsAgg:     false,
				},
			},
		},
		{
			name:  "缓存命中",
			alias: []string{"cached_alias"},
			preSetCache: map[string]metadata.FieldOption{
				"cached_alias": {
					FieldName: "cached_field",
					FieldType: "text",
					IsAgg:     true,
				},
			},
			expectFetch: false,
			expectFields: map[string]metadata.FieldOption{
				"cached_alias": {
					FieldName: "cached_field",
					FieldType: "text",
					IsAgg:     true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			cache := &FieldMapCache{}
			c, err := ristretto.NewCache(&ristretto.Config[string, metadata.FieldOption]{
				MaxCost:     1000,
				NumCounters: 10000,
				BufferItems: 64,
			})
			assert.NoError(t, err)
			cache.cache = c
			defer c.Close()

			fetchCount := 0
			fetchCallback := func(missingAlias []string) (metadata.FieldsMap, error) {
				fetchCount++
				result := make(metadata.FieldsMap)
				for _, alias := range missingAlias {
					result.Set(alias, metadata.FieldOption{
						FieldName: "field_" + alias,
						FieldType: "keyword",
						IsAgg:     false,
					})
				}
				return result, nil
			}

			for alias, fieldOption := range tt.preSetCache {
				cache.cache.SetWithTTL(alias, fieldOption, 1, time.Minute)
				cache.cache.Wait()
			}

			result, err := cache.GetFieldsMap(ctx, tt.alias, fetchCallback)
			assert.NoError(t, err)
			for expectedAlias, expectedOption := range tt.expectFields {
				actualOption, exists := result[expectedAlias]
				assert.True(t, exists)
				assert.Equal(t, expectedOption, actualOption)
			}
		})
	}
}
