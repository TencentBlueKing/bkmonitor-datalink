// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompositeSchemaProvider_Priority 测试优先级查询
func TestCompositeSchemaProvider_Priority(t *testing.T) {
	// 创建 StaticSchemaProvider（低优先级）
	staticProvider := NewStaticSchemaProvider()

	// 创建 RedisSchemaProvider（高优先级）
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 在 Redis 中添加一个自定义资源（与静态资源重名）
	customRd := &ResourceDefinition{
		Namespace: "",
		Name:      "pod", // 与 StaticSchemaProvider 中的 pod 重名（注意是小写）
		Fields: []FieldDefinition{
			{Name: "custom_field", Required: true}, // 不同的字段
		},
		Labels: map[string]string{"source": "redis"},
	}
	rdData, _ := json.Marshal(customRd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+":pod", string(rdData))

	redisProvider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer redisProvider.Close()
	time.Sleep(100 * time.Millisecond)

	// 创建 CompositeSchemaProvider：Redis 优先级高于 Static
	composite := NewCompositeSchemaProvider(redisProvider, staticProvider)

	// 测试查询 pod 资源，应该返回 Redis 中的版本
	t.Run("HighPriorityWins", func(t *testing.T) {
		rd, err := composite.GetResourceDefinition("", "pod")
		require.NoError(t, err)
		assert.Equal(t, "pod", rd.Name)
		assert.Len(t, rd.Fields, 1)
		assert.Equal(t, "custom_field", rd.Fields[0].Name)
		assert.Equal(t, "redis", rd.Labels["source"])
	})

	// 测试查询只存在于 Static 中的资源
	t.Run("FallbackToLowPriority", func(t *testing.T) {
		rd, err := composite.GetResourceDefinition("", "node")
		require.NoError(t, err)
		assert.Equal(t, "node", rd.Name)
	})

	// 测试不存在的资源
	t.Run("NotFound", func(t *testing.T) {
		_, err := composite.GetResourceDefinition("", "NonExistent")
		assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	})
}

// TestCompositeSchemaProvider_ListMerge 测试列表合并
func TestCompositeSchemaProvider_ListMerge(t *testing.T) {
	// 创建 StaticSchemaProvider
	staticProvider := NewStaticSchemaProvider()

	// 创建 RedisSchemaProvider 并添加自定义资源
	client, mr := setupTestRedis(t)
	defer mr.Close()

	customRd1 := &ResourceDefinition{
		Namespace: "custom",
		Name:      "app_instance",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}
	customRd2 := &ResourceDefinition{
		Namespace: "custom",
		Name:      "git_commit",
		Fields:    []FieldDefinition{{Name: "commit_sha", Required: true}},
	}

	rd1Data, _ := json.Marshal(customRd1)
	rd2Data, _ := json.Marshal(customRd2)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"custom:app_instance", string(rd1Data))
	mr.Set(DefaultRedisKeyPrefixResourceDef+"custom:git_commit", string(rd2Data))

	redisProvider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer redisProvider.Close()
	time.Sleep(100 * time.Millisecond)

	// 创建 CompositeSchemaProvider
	composite := NewCompositeSchemaProvider(redisProvider, staticProvider)

	// 测试列出全局资源（包含 Static 的所有资源）
	t.Run("ListGlobalResources", func(t *testing.T) {
		list, err := composite.ListResourceDefinitions("")
		require.NoError(t, err)
		assert.Greater(t, len(list), 0) // 应该包含 Static 中的资源
	})

	// 测试列出自定义命名空间的资源（只来自 Redis）
	t.Run("ListCustomNamespace", func(t *testing.T) {
		list, err := composite.ListResourceDefinitions("custom")
		require.NoError(t, err)
		assert.Len(t, list, 2)

		names := make(map[string]bool)
		for _, rd := range list {
			names[rd.Name] = true
		}
		assert.True(t, names["app_instance"])
		assert.True(t, names["git_commit"])
	})
}

// TestCompositeSchemaProvider_RelationMerge 测试关联定义合并
func TestCompositeSchemaProvider_RelationMerge(t *testing.T) {
	// 创建 StaticSchemaProvider
	staticProvider := NewStaticSchemaProvider()

	// 创建 RedisSchemaProvider 并添加自定义关联
	client, mr := setupTestRedis(t)
	defer mr.Close()

	customRel := &RelationDefinition{
		Namespace:    "custom",
		Name:         "app_to_commit",
		FromResource: "app_instance",
		ToResource:   "git_commit",
		Category:     "dynamic",
		IsBelongsTo:  false,
	}

	relData, _ := json.Marshal(customRel)
	mr.Set(DefaultRedisKeyPrefixRelationDef+"custom:app_to_commit", string(relData))

	redisProvider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer redisProvider.Close()
	time.Sleep(100 * time.Millisecond)

	// 创建 CompositeSchemaProvider
	composite := NewCompositeSchemaProvider(redisProvider, staticProvider)

	// 测试获取自定义关联
	t.Run("GetCustomRelation", func(t *testing.T) {
		rd, err := composite.GetRelationDefinition("custom", "app_to_commit")
		require.NoError(t, err)
		assert.Equal(t, "app_to_commit", rd.Name)
		assert.Equal(t, "dynamic", rd.Category)
	})

	// 测试获取静态关联
	t.Run("GetStaticRelation", func(t *testing.T) {
		list, _ := staticProvider.ListRelationDefinitions("")
		if len(list) > 0 {
			firstRelName := list[0].Name
			rd, err := composite.GetRelationDefinition("", firstRelName)
			require.NoError(t, err)
			assert.Equal(t, firstRelName, rd.Name)
		}
	})

	// 测试 ListRelationSchemas 合并
	t.Run("ListRelationSchemas", func(t *testing.T) {
		schemas := composite.ListRelationSchemas()
		// 应该包含 Static 的所有 Schema + Redis 的自定义 Schema
		assert.Greater(t, len(schemas), 0)

		// 验证自定义关联存在
		found := false
		for _, schema := range schemas {
			if schema.RelationType == "custom:app_to_commit" {
				found = true
				assert.Equal(t, RelationCategoryDynamic, schema.Category)
				break
			}
		}
		assert.True(t, found, "custom relation should be in the merged list")
	})
}

// TestCompositeSchemaProvider_GetResourcePrimaryKeys 测试主键查询
func TestCompositeSchemaProvider_GetResourcePrimaryKeys(t *testing.T) {
	// 创建 StaticSchemaProvider
	staticProvider := NewStaticSchemaProvider()

	// 创建 RedisSchemaProvider 并添加自定义资源
	client, mr := setupTestRedis(t)
	defer mr.Close()

	customRd := &ResourceDefinition{
		Namespace: "custom",
		Name:      "app",
		Fields: []FieldDefinition{
			{Name: "app_id", Required: true},
			{Name: "env", Required: true},
		},
	}

	rdData, _ := json.Marshal(customRd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"custom:app", string(rdData))

	redisProvider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer redisProvider.Close()
	time.Sleep(100 * time.Millisecond)

	// 创建 CompositeSchemaProvider
	composite := NewCompositeSchemaProvider(redisProvider, staticProvider)

	// 测试获取自定义资源的主键
	t.Run("CustomResourcePrimaryKeys", func(t *testing.T) {
		keys := composite.GetResourcePrimaryKeys("app")
		assert.Equal(t, []string{"app_id", "env"}, keys)
	})

	// 测试获取静态资源的主键
	t.Run("StaticResourcePrimaryKeys", func(t *testing.T) {
		keys := composite.GetResourcePrimaryKeys("pod")
		assert.Greater(t, len(keys), 0)
	})

	// 测试不存在的资源
	t.Run("NonExistentResource", func(t *testing.T) {
		keys := composite.GetResourcePrimaryKeys("NonExistent")
		assert.Empty(t, keys)
	})
}

// TestCompositeSchemaProvider_EmptyProviders 测试空提供器列表
func TestCompositeSchemaProvider_EmptyProviders(t *testing.T) {
	composite := NewCompositeSchemaProvider()

	t.Run("GetResourceDefinition", func(t *testing.T) {
		_, err := composite.GetResourceDefinition("", "Pod")
		assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	})

	t.Run("ListResourceDefinitions", func(t *testing.T) {
		list, err := composite.ListResourceDefinitions("")
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("GetRelationSchema", func(t *testing.T) {
		_, err := composite.GetRelationSchema("some_relation")
		assert.ErrorIs(t, err, ErrRelationDefinitionNotFound)
	})

	t.Run("ListRelationSchemas", func(t *testing.T) {
		schemas := composite.ListRelationSchemas()
		assert.Empty(t, schemas)
	})
}

// TestCompositeSchemaProvider_AddProvider 测试动态添加提供器
func TestCompositeSchemaProvider_AddProvider(t *testing.T) {
	composite := NewCompositeSchemaProvider()

	// 初始状态应该为空
	_, err := composite.GetResourceDefinition("", "pod")
	assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)

	// 添加 StaticSchemaProvider
	staticProvider := NewStaticSchemaProvider()
	composite.AddProvider(staticProvider)

	// 现在应该能找到资源了
	rd, err := composite.GetResourceDefinition("", "pod")
	require.NoError(t, err)
	assert.Equal(t, "pod", rd.Name)
}

// TestCompositeSchemaProvider_Deduplication 测试去重逻辑
func TestCompositeSchemaProvider_Deduplication(t *testing.T) {
	// 创建两个 StaticSchemaProvider（包含相同的资源）
	provider1 := NewStaticSchemaProvider()
	provider2 := NewStaticSchemaProvider()

	// 创建 CompositeSchemaProvider
	composite := NewCompositeSchemaProvider(provider1, provider2)

	// 列出所有资源
	list, err := composite.ListResourceDefinitions("")
	require.NoError(t, err)

	// 检查是否有重复
	seen := make(map[string]bool)
	for _, rd := range list {
		key := makeResourceCacheKey(rd.Namespace, rd.Name)
		assert.False(t, seen[key], "duplicate resource found: %s", key)
		seen[key] = true
	}
}
