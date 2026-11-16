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
	fields      map[string]map[string]string
	relationErr error
}

func (client *snapshotRedisClient) HGetAll(ctx context.Context, key string) *redis.StringStringMapCmd {
	if key == RedisKeyPrefix+":"+KindRelationDefinition && client.relationErr != nil {
		return redis.NewStringStringMapResult(nil, client.relationErr)
	}
	return redis.NewStringStringMapResult(client.fields[key], nil)
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
