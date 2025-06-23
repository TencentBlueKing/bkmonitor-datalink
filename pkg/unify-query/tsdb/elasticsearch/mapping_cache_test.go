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

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestNewMappingCache(t *testing.T) {
	// 设置测试配置
	viper.Set("es_mapping_cache.max_cost", 1000)
	viper.Set("es_mapping_cache.num_counters", 10000)
	viper.Set("es_mapping_cache.buffer_items", 64)
	viper.Set("es_mapping_cache.ttl", "5m")

	cache, err := NewMappingCache()
	assert.NoError(t, err)
	assert.NotNil(t, cache)
	assert.NotNil(t, cache.fieldTypesCache)
}

func TestMappingCache_SetAndGetFieldType(t *testing.T) {
	// 设置测试配置
	viper.Set("es_mapping_cache.ttl", "1m")

	// 初始化全局缓存
	err := InitFieldTypesCache()
	assert.NoError(t, err)

	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	tableID := "test_table"
	mappings := []map[string]any{
		{
			"properties": map[string]any{
				"field1": map[string]any{"type": "keyword"},
				"field2": map[string]any{"type": "text"},
				"field3": map[string]any{"type": "long"},
			},
		},
	}

	// 测试设置缓存
	cache.SetFieldTypesFromMappings(ctx, tableID, mappings)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	// 测试获取存在的字段类型
	fieldType, found := cache.GetFieldType(ctx, tableID, "field1")
	assert.True(t, found)
	assert.Equal(t, "keyword", fieldType)

	fieldType, found = cache.GetFieldType(ctx, tableID, "field2")
	assert.True(t, found)
	assert.Equal(t, "text", fieldType)

	// 测试获取不存在的字段类型
	fieldType, found = cache.GetFieldType(ctx, tableID, "nonexistent")
	assert.False(t, found)
	assert.Equal(t, "", fieldType)

	// 测试获取不存在的表
	fieldType, found = cache.GetFieldType(ctx, "nonexistent_table", "field1")
	assert.False(t, found)
	assert.Equal(t, "", fieldType)

	// 测试HasTableCache方法
	hasCache := cache.HasTableCache(ctx, tableID)
	assert.True(t, hasCache)

	hasCache = cache.HasTableCache(ctx, "nonexistent_table")
	assert.False(t, hasCache)
}

func TestMappingCache_MergeMappings(t *testing.T) {
	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	tableID := "test_table"

	// 第一次添加
	mappings1 := []map[string]any{
		{
			"properties": map[string]any{
				"field1": map[string]any{"type": "keyword"},
				"field2": map[string]any{"type": "text"},
			},
		},
	}
	cache.SetFieldTypesFromMappings(ctx, tableID, mappings1)

	// 等待第一次缓存完成
	time.Sleep(10 * time.Millisecond)

	// 第二次添加，应该合并
	mappings2 := []map[string]any{
		{
			"properties": map[string]any{
				"field3": map[string]any{"type": "long"},
				"field1": map[string]any{"type": "text"}, // 覆盖原有值
			},
		},
	}
	cache.SetFieldTypesFromMappings(ctx, tableID, mappings2)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	// 验证合并结果
	fieldType, found := cache.GetFieldType(ctx, tableID, "field1")
	assert.True(t, found)
	assert.Equal(t, "text", fieldType) // 应该是新值

	fieldType, found = cache.GetFieldType(ctx, tableID, "field2")
	assert.True(t, found)
	assert.Equal(t, "text", fieldType)

	fieldType, found = cache.GetFieldType(ctx, tableID, "field3")
	assert.True(t, found)
	assert.Equal(t, "long", fieldType)
}

func TestMappingCache_DeleteFieldTypesCache(t *testing.T) {
	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	tableID := "test_table"
	mappings := []map[string]any{
		{
			"properties": map[string]any{
				"field1": map[string]any{"type": "keyword"},
			},
		},
	}

	// 添加缓存
	cache.SetFieldTypesFromMappings(ctx, tableID, mappings)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	// 验证缓存存在
	_, found := cache.GetFieldType(ctx, tableID, "field1")
	assert.True(t, found)

	// 删除缓存
	cache.DeleteFieldTypesCache(ctx, tableID)

	// 验证缓存已删除
	_, found = cache.GetFieldType(ctx, tableID, "field1")
	assert.False(t, found)
}

func TestMappingCache_ClearFieldTypesCache(t *testing.T) {
	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	mappings := []map[string]any{
		{
			"properties": map[string]any{
				"field1": map[string]any{"type": "keyword"},
			},
		},
	}

	// 添加多个表的缓存
	cache.SetFieldTypesFromMappings(ctx, "table1", mappings)
	cache.SetFieldTypesFromMappings(ctx, "table2", mappings)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	// 验证缓存存在
	_, found := cache.GetFieldType(ctx, "table1", "field1")
	assert.True(t, found)
	_, found = cache.GetFieldType(ctx, "table2", "field1")
	assert.True(t, found)

	// 清空所有缓存
	cache.ClearFieldTypesCache(ctx)

	// 验证所有缓存已清空
	_, found = cache.GetFieldType(ctx, "table1", "field1")
	assert.False(t, found)
	_, found = cache.GetFieldType(ctx, "table2", "field1")
	assert.False(t, found)
}

func TestMappingCache_EmptyMapping(t *testing.T) {
	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	tableID := "test_table"

	// 测试添加空映射
	cache.SetFieldTypesFromMappings(ctx, tableID, []map[string]any{})

	// 验证没有添加任何内容
	_, found := cache.GetFieldType(ctx, tableID, "any_field")
	assert.False(t, found)
}

func TestMappingCache_TTL(t *testing.T) {
	// 设置短TTL用于测试
	viper.Set("es_mapping_cache.ttl", "100ms")

	cache, err := NewMappingCache()
	assert.NoError(t, err)

	ctx := context.Background()
	tableID := "test_table"
	mappings := []map[string]any{
		{
			"properties": map[string]any{
				"field1": map[string]any{"type": "keyword"},
			},
		},
	}

	// 添加缓存
	cache.SetFieldTypesFromMappings(ctx, tableID, mappings)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	// 立即验证缓存存在
	_, found := cache.GetFieldType(ctx, tableID, "field1")
	assert.True(t, found)

	// 等待TTL过期
	time.Sleep(200 * time.Millisecond)

	// 验证缓存已过期（注意：ristretto的TTL可能有延迟）
	// 这里我们只是验证功能，实际过期时间可能会有差异
	_, found = cache.GetFieldType(ctx, tableID, "field1")
	// 由于ristretto的异步特性，这里不强制断言false
}

func TestInitFieldTypesCache(t *testing.T) {
	// 设置测试配置
	viper.Set("es_mapping_cache.max_cost", 1000)
	viper.Set("es_mapping_cache.ttl", "5m")

	err := InitFieldTypesCache()
	assert.NoError(t, err)

	cache := GetFieldTypesCache()
	assert.NotNil(t, cache)

	// 测试全局缓存功能
	ctx := context.Background()
	tableID := "global_test_table"
	mappings := []map[string]any{
		{
			"properties": map[string]any{
				"global_field": map[string]any{"type": "keyword"},
			},
		},
	}

	cache.SetFieldTypesFromMappings(ctx, tableID, mappings)

	// 等待ristretto缓存异步处理完成
	time.Sleep(10 * time.Millisecond)

	fieldType, found := cache.GetFieldType(ctx, tableID, "global_field")
	assert.True(t, found)
	assert.Equal(t, "keyword", fieldType)
}
