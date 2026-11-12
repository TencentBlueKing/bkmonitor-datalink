//go:build integration

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 期望来自 static 模式：Redis 为空时，redis 模式的生效定义应与 static 模式逐条一致。
func TestRedisProviderEmptyRedisServesBuiltinSchemaIntegration(t *testing.T) {
	client := startSchemaRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.FlushDB(ctx).Err())
	provider, err := NewRedisProvider(ctx, client, WithReconcileInterval(0))
	require.NoError(t, err)
	defer provider.Close()

	static := NewDefaultStaticSchemaProvider()
	for _, namespace := range []string{"", NamespaceAll, "bkcc__2"} {
		resources, err := provider.ListResourceDefinitions(namespace)
		require.NoError(t, err)
		wantResources, err := static.ListResourceDefinitions(NamespaceAll)
		require.NoError(t, err)
		require.ElementsMatch(t, resourceNames(wantResources), resourceNames(resources), namespace)
		for _, want := range wantResources {
			got, err := provider.GetResourceDefinition(namespace, want.Name)
			require.NoError(t, err, want.Name)
			require.Equal(t, want.Fields, got.Fields, want.Name)
		}

		relations, err := provider.ListRelationDefinitions(namespace)
		require.NoError(t, err)
		wantRelations, err := static.ListRelationDefinitions(NamespaceAll)
		require.NoError(t, err)
		require.ElementsMatch(t, relationNames(wantRelations), relationNames(relations), namespace)
		for _, want := range wantRelations {
			got, err := provider.GetRelationDefinition(namespace, want.Name)
			require.NoError(t, err, want.Name)
			require.Equal(t, ToRelationSchema(want), ToRelationSchema(got), want.Name)
		}
	}
	require.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"}, provider.GetResourcePrimaryKeys("pod"))
	found, ok := provider.FindRelationByResourceTypes("bkcc__2", "system", "host", DirectionTypeBidirectional)
	require.True(t, ok)
	require.Equal(t, "host_with_system", found.Name)
}

// 覆盖、业务 namespace 与删除恢复都经公开接口与真实 Pub/Sub 验证。
func TestRedisProviderOverlayOverridesAndRestoresBuiltinIntegration(t *testing.T) {
	client := startSchemaRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.FlushDB(ctx).Err())
	write := func(kind, namespace string, definitions any) {
		t.Helper()
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":"+kind, namespace, data).Err())
	}
	// __all__ 用两个字段覆盖内置 pod（内置是三个主键），并新增一个内置没有的资源。
	write(KindResourceDefinition, NamespaceAll, map[string]*ResourceDefinition{
		"pod": {Namespace: NamespaceAll, Name: "pod", Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true}, {Name: "pod", Required: true},
		}},
		"custom_app": {Namespace: NamespaceAll, Name: "custom_app", Fields: []FieldDefinition{{Name: "app_id", Required: true}}},
	})
	write(KindRelationDefinition, NamespaceAll, map[string]*RelationDefinition{
		"node_with_pod": {Namespace: NamespaceAll, Name: "node_with_pod", FromResource: "pod", ToResource: "node", IsBelongsTo: true},
	})
	// 业务 namespace 再覆盖 pod，并只在本 namespace 定义一个关系。
	write(KindResourceDefinition, "bkcc__2", map[string]*ResourceDefinition{
		"pod": {Namespace: "bkcc__2", Name: "pod", Fields: []FieldDefinition{{Name: "pod_uid", Required: true}}},
	})
	write(KindRelationDefinition, "bkcc__2", map[string]*RelationDefinition{
		"custom_app_with_pod": {Namespace: "bkcc__2", Name: "custom_app_with_pod", FromResource: "custom_app", ToResource: "pod"},
	})
	provider, err := NewRedisProvider(ctx, client, WithReconcileInterval(0))
	require.NoError(t, err)
	defer provider.Close()

	primaryKeys := func(namespace, name string) []string {
		t.Helper()
		definition, err := provider.GetResourceDefinition(namespace, name)
		require.NoError(t, err, "%s/%s", namespace, name)
		return definition.GetPrimaryKeys()
	}
	require.Equal(t, []string{"bcs_cluster_id", "pod"}, primaryKeys(NamespaceAll, "pod"))
	require.Equal(t, []string{"bcs_cluster_id", "pod"}, primaryKeys("bkcc__3", "pod"))
	require.Equal(t, []string{"pod_uid"}, primaryKeys("bkcc__2", "pod"))
	require.Equal(t, []string{"app_id"}, primaryKeys("bkcc__3", "custom_app"))
	// 没有被覆盖的内置资源照常可用。
	require.Equal(t, []string{"bcs_cluster_id", "node"}, primaryKeys("bkcc__2", "node"))

	builtinRelation, err := NewDefaultStaticSchemaProvider().ListRelationDefinitions(NamespaceAll)
	require.NoError(t, err)
	relations, err := provider.ListRelationDefinitions("bkcc__2")
	require.NoError(t, err)
	require.ElementsMatch(t, append(relationNames(builtinRelation), "custom_app_with_pod"), relationNames(relations),
		"同名定义只出现一次，业务 namespace 的关系追加在内置之上")
	overridden, err := provider.GetRelationDefinition("bkcc__2", "node_with_pod")
	require.NoError(t, err)
	require.Equal(t, "pod", overridden.FromResource, "__all__ 的同名关系整条覆盖内置")
	require.True(t, overridden.IsBelongsTo)
	_, err = provider.GetRelationDefinition("bkcc__3", "custom_app_with_pod")
	require.ErrorIs(t, err, ErrRelationDefinitionNotFound, "业务 namespace 的定义不泄漏到其他 namespace")

	changes := make(chan string, 8)
	require.NoError(t, provider.Subscribe(func(kind, namespace string) { changes <- kind + ":" + namespace }))
	for _, kind := range []string{KindResourceDefinition, KindRelationDefinition} {
		channel := RedisKeyPrefix + ":" + kind + DefaultRedisPubSubChannelSuffix
		require.Eventually(t, func() bool {
			counts, err := client.PubSubNumSub(ctx, channel).Result()
			return err == nil && counts[channel] == 1
		}, time.Second, time.Millisecond)
	}
	remove := func(kind, namespace string) {
		t.Helper()
		require.NoError(t, client.HDel(ctx, RedisKeyPrefix+":"+kind, namespace).Err())
		payload, err := json.Marshal(MsgPayload{Namespace: namespace, Kind: kind})
		require.NoError(t, err)
		require.NoError(t, client.Publish(ctx, RedisKeyPrefix+":"+kind+DefaultRedisPubSubChannelSuffix, payload).Err())
		// 回调在本次重载完成后触发；__all__ 变更还会通知业务 namespace，只等本次消息对应的那一条。
		for {
			select {
			case change := <-changes:
				if change == kind+":"+namespace {
					return
				}
			case <-ctx.Done():
				t.Fatal("namespace reload did not finish:", ctx.Err())
			}
		}
	}

	remove(KindResourceDefinition, "bkcc__2")
	require.Equal(t, []string{"bcs_cluster_id", "pod"}, primaryKeys("bkcc__2", "pod"), "删掉业务覆盖后回到 __all__")
	remove(KindResourceDefinition, NamespaceAll)
	require.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"}, primaryKeys("bkcc__2", "pod"), "删掉 __all__ 覆盖后内置恢复")
	_, err = provider.GetResourceDefinition("bkcc__2", "custom_app")
	require.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	remove(KindRelationDefinition, NamespaceAll)
	restored, err := provider.GetRelationDefinition("bkcc__2", "node_with_pod")
	require.NoError(t, err)
	require.Equal(t, "node", restored.FromResource)
	require.False(t, restored.IsBelongsTo)
}
