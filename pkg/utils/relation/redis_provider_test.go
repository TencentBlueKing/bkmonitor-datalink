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
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

type snapshotRedisClient struct {
	redis.UniversalClient
	fields       map[string]map[string]string
	relationErr  error
	ready        string
	hgetAllCalls int
}

func (client *snapshotRedisClient) HGetAll(ctx context.Context, key string) *redis.StringStringMapCmd {
	client.hgetAllCalls++
	if key == RedisKeyPrefix+":"+KindRelationDefinition && client.relationErr != nil {
		return redis.NewStringStringMapResult(nil, client.relationErr)
	}
	return redis.NewStringStringMapResult(client.fields[key], nil)
}

func (client *snapshotRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	if key != RelationSchemaReadyKey || client.ready == "" {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(client.ready, nil)
}

func TestRedisProviderFullReloadRetainsSnapshotOnReadFailure(t *testing.T) {
	ctx := context.Background()
	resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
	relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
	encode := func(definitions any) string {
		t.Helper()
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		return string(data)
	}
	client := &snapshotRedisClient{fields: map[string]map[string]string{
		resourceKey: {"bkcc__2": encode(map[string]*ResourceDefinition{
			"service": {Namespace: "bkcc__2", Name: "service"},
		})},
		relationKey: {"bkcc__2": encode(map[string]*RelationDefinition{
			"calls": {Namespace: "bkcc__2", Name: "calls", FromResource: "service", ToResource: "service"},
		})},
	}}
	provider := &RedisProvider{
		client: client, config: DefaultRedisProviderConfig(), ctx: ctx,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
	}
	require.NoError(t, provider.loadAllEntities())

	delete(client.fields[resourceKey], "bkcc__2")
	delete(client.fields[relationKey], "bkcc__2")
	client.relationErr = errors.New("relation Redis read failed")
	require.ErrorContains(t, provider.loadAllEntities(), "relation Redis read failed")
	_, err := provider.GetResourceDefinition("bkcc__2", "service")
	require.NoError(t, err)
	_, err = provider.GetRelationDefinition("bkcc__2", "calls")
	require.NoError(t, err)

	client.relationErr = nil
	require.NoError(t, provider.loadAllEntities())
	_, err = provider.GetResourceDefinition("bkcc__2", "service")
	require.ErrorIs(t, err, ErrResourceDefinitionNotFound)
	_, err = provider.GetRelationDefinition("bkcc__2", "calls")
	require.ErrorIs(t, err, ErrRelationDefinitionNotFound)
}

func TestRedisProviderEnsureVersionLoadsPublishedSchemaBeforeQuery(t *testing.T) {
	ctx := context.Background()
	resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
	relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
	encode := func(definitions any) string {
		t.Helper()
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		return string(data)
	}
	client := &snapshotRedisClient{fields: map[string]map[string]string{
		resourceKey: {NamespaceAll: encode(map[string]*ResourceDefinition{
			"service": {Namespace: NamespaceAll, Name: "service", Fields: []FieldDefinition{{Name: "shared_id", Required: true}}},
		})},
		relationKey: {NamespaceAll: encode(map[string]*RelationDefinition{
			"calls": {Namespace: NamespaceAll, Name: "calls", FromResource: "service", ToResource: "service"},
		})},
	}}
	provider := &RedisProvider{
		client: client, config: DefaultRedisProviderConfig(), ctx: ctx,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
	}
	require.NoError(t, provider.loadAllEntities())

	client.fields[resourceKey]["bkcc__2"] = encode(map[string]*ResourceDefinition{
		"service": {Namespace: "bkcc__2", Name: "service", Fields: []FieldDefinition{{Name: "business_id", Required: true}}},
	})
	client.fields[relationKey]["bkcc__2"] = encode(map[string]*RelationDefinition{
		"business_calls": {Namespace: "bkcc__2", Name: "business_calls", FromResource: "service", ToResource: "service"},
	})
	client.ready = `{"biz_ids":[2],"version":"version-1"}`
	require.ErrorContains(t, provider.EnsureVersion(ctx, "old-version"), "version changed")
	require.NoError(t, provider.EnsureVersion(ctx, "version-1"))
	resource, err := provider.GetResourceDefinition("bkcc__2", "service")
	require.NoError(t, err)
	require.Equal(t, "business_id", resource.Fields[0].Name)
	relation, err := provider.GetRelationDefinition("bkcc__2", "business_calls")
	require.NoError(t, err)
	require.Equal(t, "business_calls", relation.Name)

	client.fields[resourceKey]["bkcc__2"] = `{"service":true}`
	client.ready = `{"biz_ids":[2],"version":"version-2"}`
	require.ErrorContains(t, provider.EnsureVersion(ctx, "version-2"), "invalid relation schema snapshot")
	resource, err = provider.GetResourceDefinition("bkcc__2", "service")
	require.NoError(t, err)
	require.Equal(t, "business_id", resource.Fields[0].Name)

	client.ready = ""
	require.ErrorContains(t, provider.EnsureVersion(ctx, "version-1"), "read relation schema readiness")
}

func TestRedisProviderBackgroundReloadInvalidatesVerifiedVersion(t *testing.T) {
	ctx := context.Background()
	client := &snapshotRedisClient{ready: `{"version":"v1"}`, fields: map[string]map[string]string{
		RedisKeyPrefix + ":" + KindResourceDefinition: {NamespaceAll: `{"service":{"name":"service"}}`},
		RedisKeyPrefix + ":" + KindRelationDefinition: {NamespaceAll: `{"calls":{"name":"calls","from_resource":"service","to_resource":"service"}}`},
	}}
	provider := &RedisProvider{
		client: client, config: DefaultRedisProviderConfig(), ctx: ctx,
		resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]map[string]*RelationDefinition),
	}
	require.NoError(t, provider.EnsureVersion(ctx, "v1"))
	// A background reconcile observes lost hashes while the ready key survives.
	// The previous request barrier must not certify this now-empty cache.
	client.fields = map[string]map[string]string{}
	require.NoError(t, provider.loadAllEntities())
	require.ErrorContains(t, provider.EnsureVersion(ctx, "v1"), "incomplete")
	require.Empty(t, provider.ensuredVersion)
}

func TestRedisProviderUnchangedReloadKeepsVerifiedVersion(t *testing.T) {
	ctx := context.Background()
	for _, mutation := range []string{"unchanged", "new version", "invalid resources", "invalid relations"} {
		t.Run(mutation, func(t *testing.T) {
			resourceKey := RedisKeyPrefix + ":" + KindResourceDefinition
			relationKey := RedisKeyPrefix + ":" + KindRelationDefinition
			client := &snapshotRedisClient{ready: `{"version":"v1"}`, fields: map[string]map[string]string{
				resourceKey: {NamespaceAll: `{"service":{"name":"service"}}`},
				relationKey: {NamespaceAll: `{"calls":{"name":"calls","from_resource":"service","to_resource":"service"}}`},
			}}
			provider := &RedisProvider{
				client: client, config: DefaultRedisProviderConfig(), ctx: ctx,
				resourceDefinitions: make(map[string]map[string]*ResourceDefinition),
				relationDefinitions: make(map[string]map[string]*RelationDefinition),
			}
			require.NoError(t, provider.EnsureVersion(ctx, "v1"))
			version := "v1"
			switch mutation {
			case "new version":
				version = "v2"
				client.ready = `{"version":"v2"}`
			case "invalid resources":
				client.fields[resourceKey][NamespaceAll] = `{"service":true}`
			case "invalid relations":
				client.fields[relationKey][NamespaceAll] = `{"calls":true}`
			}
			require.NoError(t, provider.loadAllEntities())
			calls := client.hgetAllCalls
			err := provider.EnsureVersion(ctx, version)
			switch mutation {
			case "unchanged":
				require.NoError(t, err)
				require.Equal(t, calls, client.hgetAllCalls, "unchanged reconciliation must not cause another full snapshot read")
			case "new version":
				require.NoError(t, err)
				require.Equal(t, calls+2, client.hgetAllCalls)
			default:
				require.ErrorContains(t, err, "invalid relation schema snapshot")
				require.Empty(t, provider.ensuredVersion)
			}
		})
	}
}
