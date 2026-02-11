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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRedis 创建测试用的 Redis 实例
func setupTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	return client, mr
}

// TestRedisSchemaProvider_LoadResourceDefinitions 测试加载资源定义
func TestRedisSchemaProvider_LoadResourceDefinitions(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 准备测试数据
	rd1 := &ResourceDefinition{
		Namespace: "test",
		Name:      "app_instance",
		Fields: []FieldDefinition{
			{Name: "app_id", Required: true},
			{Name: "instance_id", Required: true},
			{Name: "version", Required: false},
		},
		Labels: map[string]string{"env": "test"},
		Spec:   map[string]interface{}{"type": "application"},
	}

	rd2 := &ResourceDefinition{
		Namespace: "test",
		Name:      "git_commit",
		Fields: []FieldDefinition{
			{Name: "repo", Required: true},
			{Name: "commit_sha", Required: true},
		},
		Labels: map[string]string{"env": "test"},
		Spec:   map[string]interface{}{"type": "git"},
	}

	// 写入 Redis
	rd1Data, _ := json.Marshal(rd1)
	rd2Data, _ := json.Marshal(rd2)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app_instance", string(rd1Data))
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:git_commit", string(rd2Data))

	// 创建提供器
	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 等待加载完成
	time.Sleep(100 * time.Millisecond)

	// 测试 GetResourceDefinition
	t.Run("GetResourceDefinition", func(t *testing.T) {
		rd, err := provider.GetResourceDefinition("test", "app_instance")
		require.NoError(t, err)
		assert.Equal(t, "test", rd.Namespace)
		assert.Equal(t, "app_instance", rd.Name)
		assert.Len(t, rd.Fields, 3)
		assert.Equal(t, []string{"app_id", "instance_id"}, rd.GetPrimaryKeys())
	})

	// 测试 ListResourceDefinitions
	t.Run("ListResourceDefinitions", func(t *testing.T) {
		list, err := provider.ListResourceDefinitions("test")
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	// 测试 GetResourcePrimaryKeys
	t.Run("GetResourcePrimaryKeys", func(t *testing.T) {
		keys := provider.GetResourcePrimaryKeys("app_instance")
		assert.Equal(t, []string{"app_id", "instance_id"}, keys)
	})

	// 测试不存在的资源
	t.Run("NotFound", func(t *testing.T) {
		_, err := provider.GetResourceDefinition("test", "nonexistent")
		assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	})
}

// TestRedisSchemaProvider_LoadRelationDefinitions 测试加载关联定义
func TestRedisSchemaProvider_LoadRelationDefinitions(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 准备测试数据
	rd1 := &RelationDefinition{
		Namespace:    "test",
		Name:         "app_to_commit",
		FromResource: "app_instance",
		ToResource:   "git_commit",
		Category:     "dynamic",
		IsBelongsTo:  false,
		Labels:       map[string]string{"env": "test"},
		Spec:         map[string]interface{}{"type": "deployment"},
	}

	rd2 := &RelationDefinition{
		Namespace:    "test",
		Name:         "commit_to_author",
		FromResource: "git_commit",
		ToResource:   "developer",
		Category:     "static",
		IsBelongsTo:  true,
		Labels:       map[string]string{"env": "test"},
		Spec:         map[string]interface{}{"type": "ownership"},
	}

	// 写入 Redis
	rd1Data, _ := json.Marshal(rd1)
	rd2Data, _ := json.Marshal(rd2)
	mr.Set(DefaultRedisKeyPrefixRelationDef+"test:app_to_commit", string(rd1Data))
	mr.Set(DefaultRedisKeyPrefixRelationDef+"test:commit_to_author", string(rd2Data))

	// 创建提供器
	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 等待加载完成
	time.Sleep(100 * time.Millisecond)

	// 测试 GetRelationDefinition
	t.Run("GetRelationDefinition", func(t *testing.T) {
		rd, err := provider.GetRelationDefinition("test", "app_to_commit")
		require.NoError(t, err)
		assert.Equal(t, "test", rd.Namespace)
		assert.Equal(t, "app_to_commit", rd.Name)
		assert.Equal(t, "app_instance", rd.FromResource)
		assert.Equal(t, "git_commit", rd.ToResource)
		assert.Equal(t, "dynamic", rd.Category)
	})

	// 测试 ListRelationDefinitions
	t.Run("ListRelationDefinitions", func(t *testing.T) {
		list, err := provider.ListRelationDefinitions("test")
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	// 测试 GetRelationSchema
	t.Run("GetRelationSchema", func(t *testing.T) {
		schema, err := provider.GetRelationSchema("test:app_to_commit")
		require.NoError(t, err)
		assert.Equal(t, RelationType("test:app_to_commit"), schema.RelationType)
		assert.Equal(t, RelationCategoryDynamic, schema.Category)
		assert.Equal(t, ResourceType("app_instance"), schema.FromType)
		assert.Equal(t, ResourceType("git_commit"), schema.ToType)
		assert.False(t, schema.IsBelongsTo)
	})

	// 测试 ListRelationSchemas
	t.Run("ListRelationSchemas", func(t *testing.T) {
		schemas := provider.ListRelationSchemas()
		assert.Len(t, schemas, 2)
	})

	// 测试不存在的关联
	t.Run("NotFound", func(t *testing.T) {
		_, err := provider.GetRelationDefinition("test", "nonexistent")
		assert.ErrorIs(t, err, ErrRelationDefinitionNotFound)
	})
}

// TestRedisSchemaProvider_HotReload 测试热更新
func TestRedisSchemaProvider_HotReload(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 初始数据
	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields: []FieldDefinition{
			{Name: "app_id", Required: true},
		},
	}

	rdData, _ := json.Marshal(rd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app", string(rdData))

	// 创建提供器
	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 等待加载完成
	time.Sleep(100 * time.Millisecond)

	// 验证初始数据
	result, err := provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)
	assert.Len(t, result.Fields, 1)

	// 更新数据
	rd.Fields = append(rd.Fields, FieldDefinition{Name: "version", Required: false})
	rdData, _ = json.Marshal(rd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app", string(rdData))

	// 发送 Pub/Sub 通知
	ctx := context.Background()
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, "test:app")

	// 等待更新完成
	time.Sleep(200 * time.Millisecond)

	// 验证更新后的数据
	result, err = provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)
	assert.Len(t, result.Fields, 2)
	assert.Equal(t, "version", result.Fields[1].Name)
}

// TestRedisSchemaProvider_DeleteDefinition 测试删除定义
func TestRedisSchemaProvider_HotDelete(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 初始数据
	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields: []FieldDefinition{
			{Name: "app_id", Required: true},
		},
	}

	rdData, _ := json.Marshal(rd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app", string(rdData))

	// 创建提供器
	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 等待加载完成
	time.Sleep(100 * time.Millisecond)

	// 验证初始数据
	_, err = provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)

	// 删除 Redis key
	mr.Del(DefaultRedisKeyPrefixResourceDef + "test:app")

	// 发送 Pub/Sub 通知
	ctx := context.Background()
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, "test:app")

	// 等待删除完成
	time.Sleep(200 * time.Millisecond)

	// 验证已删除
	_, err = provider.GetResourceDefinition("test", "app")
	assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
}

// TestRedisSchemaProvider_WithoutReloadOnStart 测试不预加载
func TestRedisSchemaProvider_WithoutReloadOnStart(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 准备测试数据
	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}

	rdData, _ := json.Marshal(rd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app", string(rdData))

	// 创建提供器，不预加载
	provider, err := NewRedisSchemaProvider(client, WithReloadOnStart(false))
	require.NoError(t, err)
	defer provider.Close()

	// 等待 Pub/Sub 订阅建立
	time.Sleep(100 * time.Millisecond)

	// 立即查询，应该找不到（因为没有预加载）
	_, err = provider.GetResourceDefinition("test", "app")
	assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)

	// 发送 Pub/Sub 通知触发加载
	ctx := context.Background()
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, "test:app")

	// 等待加载完成，使用重试机制
	var result *ResourceDefinition
	for i := 0; i < 10; i++ {
		result, err = provider.GetResourceDefinition("test", "app")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 现在应该能找到了
	require.NoError(t, err)
	assert.Equal(t, "app", result.Name)
}

// TestRedisSchemaProvider_ConcurrentAccess 测试并发访问
func TestRedisSchemaProvider_ConcurrentAccess(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	// 准备测试数据
	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}

	rdData, _ := json.Marshal(rd)
	mr.Set(DefaultRedisKeyPrefixResourceDef+"test:app", string(rdData))

	// 创建提供器
	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 等待加载完成
	time.Sleep(100 * time.Millisecond)

	// 并发读写测试
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = provider.GetResourceDefinition("test", "app")
				_ = provider.ListRelationSchemas()
			}
			done <- true
		}()
	}

	// 同时进行更新
	go func() {
		ctx := context.Background()
		for i := 0; i < 50; i++ {
			client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, "test:app")
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证数据一致性
	result, err := provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)
	assert.Equal(t, "app", result.Name)
}
