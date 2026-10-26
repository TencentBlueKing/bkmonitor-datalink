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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestRolloutReadinessChangePreservesLegacyBusinessPaths(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	write := func(kind, namespace string, definitions any) {
		data, err := json.Marshal(definitions)
		require.NoError(t, err)
		require.NoError(t, client.HSet(ctx, relation.RedisKeyPrefix+":"+kind, namespace, data).Err())
	}
	write(relation.KindResourceDefinition, relation.NamespaceAll, map[string]*relation.ResourceDefinition{
		"shared": {Name: "shared", Fields: []relation.FieldDefinition{{Name: "shared_id", Required: true}}},
	})
	write(relation.KindResourceDefinition, "bkcc__2", map[string]*relation.ResourceDefinition{
		"custom_a": {Name: "custom_a", Fields: []relation.FieldDefinition{{Name: "a_id", Required: true}}},
		"custom_b": {Name: "custom_b", Fields: []relation.FieldDefinition{{Name: "b_id", Required: true}}},
	})
	write(relation.KindRelationDefinition, "bkcc__2", map[string]*relation.RelationDefinition{
		"custom_edge": {Name: "custom_edge", FromResource: "custom_a", ToResource: "custom_b", Category: "static"},
	})
	p, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(10*time.Millisecond))
	require.NoError(t, err)
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
	paths, err := before.(*model).getPaths(ctx, "custom_a", "custom_b", nil)
	require.NoError(t, err)
	t.Logf("legacy path before rollout rollback: %v", paths)

	// Query rollback changes readiness, while the shared definition hashes
	// retain the business schema. Simulate the publisher's reload notification.
	channel := relation.RedisKeyPrefix + ":" + relation.KindRelationDefinition + relation.DefaultRedisPubSubChannelSuffix
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		return err == nil && counts[channel] == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, client.Set(ctx, relation.RelationSchemaReadyKey, `{"biz_ids":[],"version":"rollback"}`, 0).Err())
	require.NoError(t, client.Publish(ctx, channel, `{"kind":"RelationDefinition","namespace":"bkcc__2"}`).Err())
	require.Eventually(t, func() bool {
		after, getErr := GetModel(ctx, "bkcc__2")
		return getErr == nil && after != before
	}, time.Second, 10*time.Millisecond)
	after, err := GetModel(ctx, "bkcc__2")
	require.NoError(t, err)
	paths, err = after.(*model).getPaths(ctx, "custom_a", "custom_b", nil)
	t.Logf("legacy path after rollout rollback: paths=%v error=%v", paths, err)
	require.NoError(t, err, "Turning off v1beta3 must preserve the legacy business path")
}
