// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// Redis 只放业务 namespace 的扩展时，其他 namespace 与该业务 namespace 都应保留内置路径；
// 期望来自 static 模式下同一对资源的路径。
func TestRedisBusinessExtensionKeepsBuiltinPaths(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	write := func(kind, namespace string, definitions any) {
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+kind, namespace, data).Err())
	}
	write(relation.KindResourceDefinition, "bkcc__2", map[string]*relation.ResourceDefinition{
		"custom_app": {Namespace: "bkcc__2", Name: "custom_app", Fields: []relation.FieldDefinition{{Name: "app_id", Required: true}}},
	})
	write(relation.KindRelationDefinition, "bkcc__2", map[string]*relation.RelationDefinition{
		"custom_app_with_pod": {Namespace: "bkcc__2", Name: "custom_app_with_pod", FromResource: "custom_app", ToResource: "pod"},
	})
	p := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	defer p.Close()

	oldModels, oldProvider := models, provider
	oldResourceConfig := AllResources()
	t.Cleanup(func() {
		mtx.Lock()
		models, provider = oldModels, oldProvider
		mtx.Unlock()
		resourceConfigMu.Lock()
		resourceConfig = oldResourceConfig
		resourceConfigMu.Unlock()
	})
	staticModel, err := newModel(ctx, configData)
	require.NoError(t, err)
	wantPaths, err := staticModel.getPaths(ctx, "pod", "node", nil)
	require.NoError(t, err)
	require.NotEmpty(t, wantPaths)

	InitSchemaProvider(p)
	for _, namespace := range []string{relation.NamespaceAll, "bkcc__2", "bkcc__3"} {
		current, err := GetModel(ctx, namespace)
		require.NoError(t, err, namespace)
		paths, err := current.(*model).getPaths(ctx, "pod", "node", nil)
		require.NoError(t, err, namespace)
		require.Equal(t, wantPaths, paths, namespace)
	}
	business, err := GetModel(ctx, "bkcc__2")
	require.NoError(t, err)
	paths, err := business.(*model).getPaths(ctx, "custom_app", "pod", nil)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"custom_app", "pod"}}, paths)
	other, err := GetModel(ctx, "bkcc__3")
	require.NoError(t, err)
	_, err = other.(*model).getPaths(ctx, cmdb.Resource("custom_app"), "pod", nil)
	require.Error(t, err, "业务 namespace 的扩展不泄漏到其他 namespace")
}

// __all__ 在 Redis 里变了，已构建的业务 namespace model 也要跟着重建。
func TestRedisAllChangeRebuildsBusinessModel(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	write := func(kind, namespace string, definitions any) {
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+kind, namespace, data).Err())
	}
	write(relation.KindResourceDefinition, "bkcc__2", map[string]*relation.ResourceDefinition{
		"custom_app": {Namespace: "bkcc__2", Name: "custom_app", Fields: []relation.FieldDefinition{{Name: "app_id", Required: true}}},
	})
	p := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	defer p.Close()
	oldModels, oldProvider := models, provider
	oldResourceConfig := AllResources()
	t.Cleanup(func() {
		mtx.Lock()
		models, provider = oldModels, oldProvider
		mtx.Unlock()
		resourceConfigMu.Lock()
		resourceConfig = oldResourceConfig
		resourceConfigMu.Unlock()
	})
	InitSchemaProvider(p)
	before, err := GetModel(ctx, "bkcc__2")
	require.NoError(t, err)
	_, err = before.(*model).getPaths(ctx, "custom_app", "pod", nil)
	require.Error(t, err)

	channel := relation.RedisKeyPrefix + ":" + relation.KindRelationDefinition + relation.DefaultRedisPubSubChannelSuffix
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		return err == nil && counts[channel] == 1
	}, time.Second, 10*time.Millisecond)
	write(relation.KindRelationDefinition, relation.NamespaceAll, map[string]*relation.RelationDefinition{
		"custom_app_with_pod": {Namespace: relation.NamespaceAll, Name: "custom_app_with_pod", FromResource: "custom_app", ToResource: "pod"},
	})
	require.NoError(t, client.Publish(ctx, channel, `{"kind":"RelationDefinition","namespace":"__all__"}`).Err())
	require.Eventually(t, func() bool {
		after, err := GetModel(ctx, "bkcc__2")
		if err != nil {
			return false
		}
		paths, err := after.(*model).getPaths(ctx, "custom_app", "pod", nil)
		return err == nil && len(paths) == 1
	}, 2*time.Second, 10*time.Millisecond)
}
