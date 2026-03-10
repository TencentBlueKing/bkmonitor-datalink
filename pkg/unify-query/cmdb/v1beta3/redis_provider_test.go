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

// setResourceDefinitions 向 miniredis 写入资源定义
// Redis 结构: bkmonitorv3:entity:ResourceDefinition -> namespace -> {name: JSON, ...}
func setResourceDefinitions(t *testing.T, mr *miniredis.Miniredis, namespace string, defs ...*ResourceDefinition) {
	t.Helper()
	entities := make(map[string]json.RawMessage, len(defs))
	for _, def := range defs {
		b, err := json.Marshal(def)
		require.NoError(t, err)
		entities[def.Name] = b
	}
	b, err := json.Marshal(entities)
	require.NoError(t, err)
	mr.HSet(DefaultRedisKeyPrefixResourceDef, namespace, string(b))
}

// setRelationDefinitions 向 miniredis 写入关联定义
// Redis 结构: bkmonitorv3:entity:RelationDefinition -> namespace -> {name: JSON, ...}
func setRelationDefinitions(t *testing.T, mr *miniredis.Miniredis, namespace string, defs ...*RelationDefinition) {
	t.Helper()
	entities := make(map[string]json.RawMessage, len(defs))
	for _, def := range defs {
		b, err := json.Marshal(def)
		require.NoError(t, err)
		entities[def.Name] = b
	}
	b, err := json.Marshal(entities)
	require.NoError(t, err)
	mr.HSet(DefaultRedisKeyPrefixRelationDef, namespace, string(b))
}

// TestRedisSchemaProvider_LoadResourceDefinitions 测试加载资源定义
func TestRedisSchemaProvider_LoadResourceDefinitions(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd1 := &ResourceDefinition{
		Namespace: "test",
		Name:      "app_instance",
		Fields: []FieldDefinition{
			{Name: "app_id", Required: true},
			{Name: "instance_id", Required: true},
			{Name: "version", Required: false},
		},
		Labels: map[string]string{"env": "test"},
	}
	rd2 := &ResourceDefinition{
		Namespace: "test",
		Name:      "git_commit",
		Fields: []FieldDefinition{
			{Name: "repo", Required: true},
			{Name: "commit_sha", Required: true},
		},
	}
	setResourceDefinitions(t, mr, "test", rd1, rd2)

	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	t.Run("GetResourceDefinition", func(t *testing.T) {
		rd, err := provider.GetResourceDefinition("test", "app_instance")
		require.NoError(t, err)
		assert.Equal(t, "test", rd.Namespace)
		assert.Equal(t, "app_instance", rd.Name)
		assert.Len(t, rd.Fields, 3)
		assert.Equal(t, []string{"app_id", "instance_id"}, rd.GetPrimaryKeys())
	})

	t.Run("ListResourceDefinitions", func(t *testing.T) {
		list, err := provider.ListResourceDefinitions("test")
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("GetResourcePrimaryKeys", func(t *testing.T) {
		keys := provider.GetResourcePrimaryKeys("app_instance")
		assert.Equal(t, []string{"app_id", "instance_id"}, keys)
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := provider.GetResourceDefinition("test", "nonexistent")
		assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	})
}

// TestRedisSchemaProvider_LoadRelationDefinitions 测试加载关联定义
func TestRedisSchemaProvider_LoadRelationDefinitions(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd1 := &RelationDefinition{
		Namespace:    "test",
		Name:         "app_to_commit",
		FromResource: "app_instance",
		ToResource:   "git_commit",
		Category:     "dynamic",
	}
	rd2 := &RelationDefinition{
		Namespace:    "test",
		Name:         "commit_to_author",
		FromResource: "git_commit",
		ToResource:   "developer",
		Category:     "static",
		IsBelongsTo:  true,
	}
	setRelationDefinitions(t, mr, "test", rd1, rd2)

	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	t.Run("GetRelationDefinition", func(t *testing.T) {
		rd, err := provider.GetRelationDefinition("test", "app_to_commit")
		require.NoError(t, err)
		assert.Equal(t, "test", rd.Namespace)
		assert.Equal(t, "app_to_commit", rd.Name)
		assert.Equal(t, "app_instance", rd.FromResource)
		assert.Equal(t, "git_commit", rd.ToResource)
		assert.Equal(t, "dynamic", rd.Category)
	})

	t.Run("ListRelationDefinitions", func(t *testing.T) {
		list, err := provider.ListRelationDefinitions("test")
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("GetRelationSchema", func(t *testing.T) {
		schema, err := provider.GetRelationSchema("test:app_to_commit")
		require.NoError(t, err)
		assert.Equal(t, RelationType("test:app_to_commit"), schema.RelationType)
		assert.Equal(t, RelationCategoryDynamic, schema.Category)
		assert.Equal(t, ResourceType("app_instance"), schema.FromType)
		assert.Equal(t, ResourceType("git_commit"), schema.ToType)
		assert.False(t, schema.IsBelongsTo)
	})

	t.Run("ListRelationSchemas", func(t *testing.T) {
		schemas := provider.ListRelationSchemas()
		assert.Len(t, schemas, 2)
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := provider.GetRelationDefinition("test", "nonexistent")
		assert.ErrorIs(t, err, ErrRelationDefinitionNotFound)
	})
}

// TestRedisSchemaProvider_HotReload 测试热更新（Pub/Sub）
func TestRedisSchemaProvider_HotReload(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}
	setResourceDefinitions(t, mr, "test", rd)

	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 验证初始数据
	result, err := provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)
	assert.Len(t, result.Fields, 1)

	// 等待 Pub/Sub 订阅建立
	time.Sleep(100 * time.Millisecond)

	// 更新 Redis 数据
	rd.Fields = append(rd.Fields, FieldDefinition{Name: "version", Required: false})
	setResourceDefinitions(t, mr, "test", rd)

	// 发送 Pub/Sub 通知（JSON 格式，与 bk-monitor-worker 保持一致）
	ctx := context.Background()
	payload, _ := json.Marshal(MsgPayload{Namespace: "test", Kind: KindResourceDef})
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, string(payload))

	// 等待热更新完成（轮询，最多 1 秒）
	var updated *ResourceDefinition
	for i := 0; i < 20; i++ {
		updated, err = provider.GetResourceDefinition("test", "app")
		if err == nil && len(updated.Fields) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	require.NoError(t, err)
	require.Len(t, updated.Fields, 2)
	assert.Equal(t, "version", updated.Fields[1].Name)
}

// TestRedisSchemaProvider_HotDelete 测试热删除（namespace 不存在时清空缓存）
func TestRedisSchemaProvider_HotDelete(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}
	setResourceDefinitions(t, mr, "test", rd)

	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

	// 验证初始数据
	_, err = provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)

	// 等待 Pub/Sub 订阅建立
	time.Sleep(100 * time.Millisecond)

	// 删除 Redis Hash field（整个 namespace）
	mr.HDel(DefaultRedisKeyPrefixResourceDef, "test")

	// 发送 Pub/Sub 通知
	ctx := context.Background()
	payload, _ := json.Marshal(MsgPayload{Namespace: "test", Kind: KindResourceDef})
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, string(payload))

	// 等待删除完成
	for i := 0; i < 20; i++ {
		_, err = provider.GetResourceDefinition("test", "app")
		if err != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.ErrorIs(t, err, ErrResourceDefinitionNotFound)
}

// TestRedisSchemaProvider_WithoutReloadOnStart 测试不预加载
func TestRedisSchemaProvider_WithoutReloadOnStart(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}
	setResourceDefinitions(t, mr, "test", rd)

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
	payload, _ := json.Marshal(MsgPayload{Namespace: "test", Kind: KindResourceDef})
	client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, string(payload))

	// 等待加载完成
	var result *ResourceDefinition
	for i := 0; i < 20; i++ {
		result, err = provider.GetResourceDefinition("test", "app")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	require.NoError(t, err)
	assert.Equal(t, "app", result.Name)
}

// TestRedisSchemaProvider_ConcurrentAccess 测试并发访问
func TestRedisSchemaProvider_ConcurrentAccess(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()

	rd := &ResourceDefinition{
		Namespace: "test",
		Name:      "app",
		Fields:    []FieldDefinition{{Name: "app_id", Required: true}},
	}
	setResourceDefinitions(t, mr, "test", rd)

	provider, err := NewRedisSchemaProvider(client)
	require.NoError(t, err)
	defer provider.Close()

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
		payload, _ := json.Marshal(MsgPayload{Namespace: "test", Kind: KindResourceDef})
		for i := 0; i < 50; i++ {
			client.Publish(ctx, DefaultRedisPubSubChannelResourceDef, string(payload))
			time.Sleep(10 * time.Millisecond)
		}
	}()

	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证数据一致性
	result, err := provider.GetResourceDefinition("test", "app")
	require.NoError(t, err)
	assert.Equal(t, "app", result.Name)
}
