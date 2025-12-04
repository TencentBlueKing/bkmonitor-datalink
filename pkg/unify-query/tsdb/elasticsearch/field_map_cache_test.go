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

	"github.com/patrickmn/go-cache"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestFieldMapCache(t *testing.T) {
	viper.Set(MappingCacheTTLPath, "1m")
	viper.Set(MappingCacheCleanupPath, "30s")

	tests := []struct {
		name         string
		alias        []string
		preSetCache  metadata.FieldsMap
		expectFetch  bool
		expectFields metadata.FieldsMap
	}{
		{
			name:        "缓存未命中",
			alias:       []string{"test_alias"},
			expectFetch: true,
			expectFields: metadata.FieldsMap{
				"field1": {
					FieldName: "field_test_alias",
					FieldType: "keyword",
					IsAgg:     false,
				},
			},
		},
		{
			name:  "缓存命中",
			alias: []string{"cached_alias"},
			preSetCache: metadata.FieldsMap{
				"field1": {
					FieldName: "cached_field",
					FieldType: "text",
					IsAgg:     true,
				},
			},
			expectFetch: false,
			expectFields: metadata.FieldsMap{
				"field1": {
					FieldName: "cached_field",
					FieldType: "text",
					IsAgg:     true,
				},
			},
		},
		{
			name:        "多alias部分缓存命中",
			alias:       []string{"cached_alias", "uncached_alias"},
			expectFetch: true,
			expectFields: metadata.FieldsMap{
				"field1": {
					FieldName: "field_cached_alias",
					FieldType: "keyword",
					IsAgg:     false,
				},
				"field2": {
					FieldName: "field_uncached_alias",
					FieldType: "keyword",
					IsAgg:     false,
				},
			},
		},
		{
			name:  "test",
			alias: []string{"bkmonitor_event_20251204"},
			preSetCache: metadata.FieldsMap{
				"event_field": {
					FieldName: "event_field",
					FieldType: "keyword",
					IsAgg:     true,
				},
			},
			expectFetch: false,
			expectFields: metadata.FieldsMap{
				"event_field": {
					FieldName: "event_field",
					FieldType: "keyword",
					IsAgg:     true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			fieldMapCache := &FieldMapCache{}
			fieldMapCache.cache = cache.New(time.Minute, 30*time.Second)

			fetchCount := 0
			fetchCallback := func(missingAlias []string) (metadata.FieldsMap, error) {
				fetchCount++
				result := make(metadata.FieldsMap)
				for fieldName, fieldOption := range tt.expectFields {
					result.Set(fieldName, fieldOption)
				}
				return result, nil
			}

			for _, alias := range tt.alias {
				if tt.preSetCache != nil {
					fieldMapCache.cache.Set(alias, tt.preSetCache, time.Minute)
				}
			}

			result, err := fieldMapCache.GetFieldsMap(ctx, tt.alias, fetchCallback)
			assert.NoError(t, err)

			if tt.expectFetch {
				assert.Greater(t, fetchCount, 0, "期望fetch回调被调用")
			} else {
				assert.Equal(t, 0, fetchCount, "期望fetch回调不被调用")
			}

			for expectedFieldName, expectedOption := range tt.expectFields {
				actualOption, exists := result[expectedFieldName]
				assert.True(t, exists, "期望字段 %s 存在", expectedFieldName)
				assert.Equal(t, expectedOption, actualOption, "字段 %s 的配置不匹配", expectedFieldName)
			}
		})
	}
}
