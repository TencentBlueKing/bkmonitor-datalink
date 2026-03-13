// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProvider 启动 miniredis，预置数据，返回 RedisSchemaProvider
func newTestProvider(t *testing.T, resourceData map[string]map[string]ResourceDefinition, relationData map[string]map[string]RelationDefinition) (*RedisSchemaProvider, *miniredis.Miniredis) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{mr.Addr()},
	})

	// 写入 ResourceDefinition
	if len(resourceData) > 0 {
		rdKey := RedisKeyPrefix + ":" + KindResourceDefinition
		for ns, entities := range resourceData {
			b, err := json.Marshal(entities)
			require.NoError(t, err)
			mr.HSet(rdKey, ns, string(b))
		}
	}

	// 写入 RelationDefinition
	if len(relationData) > 0 {
		rlKey := RedisKeyPrefix + ":" + KindRelationDefinition
		for ns, entities := range relationData {
			b, err := json.Marshal(entities)
			require.NoError(t, err)
			mr.HSet(rlKey, ns, string(b))
		}
	}

	provider, err := NewRedisSchemaProvider(context.Background(), client)
	require.NoError(t, err)

	return provider, mr
}

func podResourceDef(ns string) ResourceDefinition {
	return ResourceDefinition{
		Namespace: ns,
		Name:      "pod",
		Fields: []FieldDefinition{
			{Namespace: "k8s", Name: "bcs_cluster_id", Required: true},
			{Namespace: "k8s", Name: "namespace", Required: true},
			{Namespace: "k8s", Name: "pod", Required: true},
		},
	}
}

func nodeResourceDef(ns string) ResourceDefinition {
	return ResourceDefinition{
		Namespace: ns,
		Name:      "node",
		Fields: []FieldDefinition{
			{Namespace: "k8s", Name: "bcs_cluster_id", Required: true},
			{Namespace: "k8s", Name: "node", Required: true},
		},
	}
}

// TestGetResourceDefinition 验证按 namespace + 资源类型查找资源定义
func TestGetResourceDefinition(t *testing.T) {
	ns := "bkcc__2"
	provider, mr := newTestProvider(t,
		map[string]map[string]ResourceDefinition{
			ns: {
				"pod":  podResourceDef(ns),
				"node": nodeResourceDef(ns),
			},
		},
		nil,
	)
	defer mr.Close()
	defer provider.Close()

	t.Run("找到指定 namespace 下的资源", func(t *testing.T) {
		def, err := provider.GetResourceDefinition(ns, "pod")
		require.NoError(t, err)
		assert.Equal(t, "pod", def.Name)
		assert.Equal(t, ns, def.Namespace)
		assert.Len(t, def.Fields, 3)
	})

	t.Run("资源不存在时返回 error", func(t *testing.T) {
		_, err := provider.GetResourceDefinition(ns, "nonexistent")
		assert.Error(t, err)
	})

	t.Run("空 namespace 映射到 __all__", func(t *testing.T) {
		providerAll, mrAll := newTestProvider(t,
			map[string]map[string]ResourceDefinition{
				NamespaceAll: {
					"global": {Namespace: "", Name: "global", Fields: []FieldDefinition{{Name: "id", Required: true}}},
				},
			},
			nil,
		)
		defer mrAll.Close()
		defer providerAll.Close()

		def, err := providerAll.GetResourceDefinition("", "global")
		require.NoError(t, err)
		assert.Equal(t, "global", def.Name)
	})

	t.Run("指定 namespace 不存在时从 __all__ 降级查找", func(t *testing.T) {
		providerFallback, mrFallback := newTestProvider(t,
			map[string]map[string]ResourceDefinition{
				NamespaceAll: {
					"global": {Namespace: "", Name: "global", Fields: []FieldDefinition{{Name: "id", Required: true}}},
				},
			},
			nil,
		)
		defer mrFallback.Close()
		defer providerFallback.Close()

		def, err := providerFallback.GetResourceDefinition("bkcc__99", "global")
		require.NoError(t, err)
		assert.Equal(t, "global", def.Name)
	})
}

// TestGetRelationDefinition 验证关联定义查找及 cache key 一致性
func TestGetRelationDefinition(t *testing.T) {
	ns := "bkcc__2"

	provider, mr := newTestProvider(t,
		nil,
		map[string]map[string]RelationDefinition{
			ns: {
				// 双向关联：名称使用 _with_，is_directional=false
				"pod_with_node": {
					Namespace:     ns,
					Name:          "pod_with_node",
					FromResource:  "pod",
					ToResource:    "node",
					IsDirectional: false,
				},
				// 单向关联：名称使用 _to_，is_directional=true
				"pod_to_biz": {
					Namespace:     ns,
					Name:          "pod_to_biz",
					FromResource:  "pod",
					ToResource:    "biz",
					IsDirectional: true,
				},
			},
		},
	)
	defer mr.Close()
	defer provider.Close()

	t.Run("查找双向关联", func(t *testing.T) {
		def, found := provider.GetRelationDefinition(ns, "pod", "node", RelationTypeBidirectional)
		assert.True(t, found)
		assert.Equal(t, "pod_with_node", def.Name)
		assert.False(t, def.IsDirectional)
	})

	t.Run("查找双向关联（反向 from/to 也能命中，因为 key 按字母序排序）", func(t *testing.T) {
		def, found := provider.GetRelationDefinition(ns, "node", "pod", RelationTypeBidirectional)
		assert.True(t, found)
		assert.Equal(t, "pod_with_node", def.Name)
	})

	t.Run("查找单向关联", func(t *testing.T) {
		def, found := provider.GetRelationDefinition(ns, "pod", "biz", RelationTypeDirectional)
		assert.True(t, found)
		assert.Equal(t, "pod_to_biz", def.Name)
		assert.True(t, def.IsDirectional)
	})

	t.Run("单向关联不能用双向类型查找到", func(t *testing.T) {
		_, found := provider.GetRelationDefinition(ns, "pod", "biz", RelationTypeBidirectional)
		assert.False(t, found)
	})

	t.Run("双向关联不能用单向类型查找到", func(t *testing.T) {
		_, found := provider.GetRelationDefinition(ns, "pod", "node", RelationTypeDirectional)
		assert.False(t, found)
	})

	t.Run("不存在的关联返回 false", func(t *testing.T) {
		_, found := provider.GetRelationDefinition(ns, "pod", "nonexistent", RelationTypeBidirectional)
		assert.False(t, found)
	})
}

// TestLoadEntityByKind_CacheKeyConsistency 验证 loadEntityByKind 使用 buildXxxRelationKey 构建 cache key，
// 与 GetRelationDefinition 的查找逻辑一致（P0 issue 修复验证）
func TestLoadEntityByKind_CacheKeyConsistency(t *testing.T) {
	ns := "bkcc__2"

	// 写入 Redis 时使用符合命名规范的 name
	provider, mr := newTestProvider(t,
		nil,
		map[string]map[string]RelationDefinition{
			ns: {
				"pod_with_node": {
					Namespace: ns, Name: "pod_with_node",
					FromResource: "pod", ToResource: "node", IsDirectional: false,
				},
				"pod_to_biz": {
					Namespace: ns, Name: "pod_to_biz",
					FromResource: "pod", ToResource: "biz", IsDirectional: true,
				},
			},
		},
	)
	defer mr.Close()
	defer provider.Close()

	// 初始加载后（不经过 Pub/Sub reload），GetRelationDefinition 应能直接命中
	t.Run("初始加载后双向关联可命中", func(t *testing.T) {
		def, found := provider.GetRelationDefinition(ns, "pod", "node", RelationTypeBidirectional)
		assert.True(t, found, "初始加载后应能通过 buildBidirectionalRelationKey 命中")
		assert.NotNil(t, def)
	})

	t.Run("初始加载后单向关联可命中", func(t *testing.T) {
		def, found := provider.GetRelationDefinition(ns, "pod", "biz", RelationTypeDirectional)
		assert.True(t, found, "初始加载后应能通过 buildDirectionalRelationKey 命中")
		assert.NotNil(t, def)
	})
}

// TestListRelationDefinitions 验证列出关联定义
func TestListRelationDefinitions(t *testing.T) {
	ns := "bkcc__2"

	provider, mr := newTestProvider(t,
		nil,
		map[string]map[string]RelationDefinition{
			ns: {
				"pod_with_node": {Namespace: ns, Name: "pod_with_node", FromResource: "pod", ToResource: "node", IsDirectional: false},
				"pod_to_biz":   {Namespace: ns, Name: "pod_to_biz", FromResource: "pod", ToResource: "biz", IsDirectional: true},
			},
			NamespaceAll: {
				"global_with_region": {Namespace: "", Name: "global_with_region", FromResource: "global", ToResource: "region", IsDirectional: false},
			},
		},
	)
	defer mr.Close()
	defer provider.Close()

	t.Run("列出指定 namespace 含 __all__ 合并结果", func(t *testing.T) {
		defs, err := provider.ListRelationDefinitions(ns)
		require.NoError(t, err)
		assert.Len(t, defs, 3) // 2 条 bkcc__2 + 1 条 __all__
	})

	t.Run("列出不存在的 namespace 只返回 __all__", func(t *testing.T) {
		defs, err := provider.ListRelationDefinitions("bkcc__99")
		require.NoError(t, err)
		assert.Len(t, defs, 1)
	})
}

// TestGetPrimaryKeys 验证资源主键提取
func TestGetPrimaryKeys(t *testing.T) {
	rd := ResourceDefinition{
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: false},
			{Name: "pod", Required: true},
		},
	}

	keys := rd.GetPrimaryKeys()
	assert.Equal(t, []string{"bcs_cluster_id", "pod"}, keys)
}

// TestGetRelationName 验证关联指标名称生成规则
func TestGetRelationName(t *testing.T) {
	t.Run("双向关联按字母序加 _with_ 和 _relation 后缀", func(t *testing.T) {
		def := RelationDefinition{FromResource: "pod", ToResource: "node", IsDirectional: false}
		assert.Equal(t, "node_with_pod_relation", def.GetRelationName())
	})

	t.Run("单向关联保持 from_to_to 方向加 _flow 后缀", func(t *testing.T) {
		def := RelationDefinition{FromResource: "pod", ToResource: "biz", IsDirectional: true}
		assert.Equal(t, "pod_to_biz_flow", def.GetRelationName())
	})
}
