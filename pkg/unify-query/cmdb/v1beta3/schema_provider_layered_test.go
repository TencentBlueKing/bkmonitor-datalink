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

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// 期望来自 static 模式：Redis 为空时 redis 模式应给出与 static 模式完全相同的 schema。
func TestRedisSchemaEmptyRedisMatchesStatic(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	provider, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	static := NewStaticSchemaProvider()
	redisSchema := NewSchemaProviderFromRelation(provider)
	for _, namespace := range []string{"", relation.NamespaceAll, "bkcc__2"} {
		resourceTypes := static.ListResourceTypes(namespace)
		require.NotEmpty(t, resourceTypes)
		require.Equal(t, resourceTypes, redisSchema.ListResourceTypes(namespace), namespace)
		for _, resourceType := range resourceTypes {
			require.Equal(t, static.GetResourcePrimaryKeys(namespace, resourceType), redisSchema.GetResourcePrimaryKeys(namespace, resourceType), resourceType)
			require.Equal(t, static.GetResourceFields(namespace, resourceType), redisSchema.GetResourceFields(namespace, resourceType), resourceType)
		}
		require.Equal(t, static.ListRelationSchemas(namespace), redisSchema.ListRelationSchemas(namespace), namespace)
	}
	require.NoError(t, redisSchema.(SchemaProviderValidator).ValidateSchema("bkcc__2", "pod", "node"))
}

// 生效定义与来源计数读的是当前注入的 Provider。
func TestEffectiveSchemaReportsCurrentProviderSources(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	data, err := json.Marshal(map[string]*relation.ResourceDefinition{
		"pod": {Namespace: relation.NamespaceAll, Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
	})
	require.NoError(t, err)
	require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+relation.KindResourceDefinition, relation.NamespaceAll, data).Err())
	provider, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	previous := GetUnderlyingProvider(GetSchemaProvider())
	t.Cleanup(func() { InitSchemaProvider(previous) })
	builtinResources := len(relation.DefaultResourceDefinitions())
	count := func(kind, source string) int {
		for _, item := range relationSchemaSourceCounts() {
			if item.Kind == kind && item.Source == source {
				return item.Count
			}
		}
		return -1
	}

	InitSchemaProvider(nil)
	require.Equal(t, builtinResources, count(relation.KindResourceDefinition, "builtin"))
	require.Zero(t, count(relation.KindResourceDefinition, "override"))
	schema, ok := EffectiveSchema("bkcc__2")
	require.True(t, ok)
	require.Equal(t, "static", schema.Provider)

	InitSchemaProvider(provider)
	require.Equal(t, builtinResources-1, count(relation.KindResourceDefinition, "builtin"))
	require.Equal(t, 1, count(relation.KindResourceDefinition, "override"))
	schema, ok = EffectiveSchema("bkcc__2")
	require.True(t, ok)
	require.Equal(t, "redis", schema.Provider)
	for _, item := range schema.Resources {
		if item.Name == "pod" {
			require.Equal(t, relation.DefinitionSourceOverride, item.Source)
			require.False(t, item.MatchesBuiltin)
		}
	}
}
