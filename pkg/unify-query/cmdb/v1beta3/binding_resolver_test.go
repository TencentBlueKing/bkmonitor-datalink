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
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestBindingResolverFetchesRedisRouteWithTenant(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	restoreBindingRedisKey := setBindingRedisKeyForTest("test:surrealdb_binding")
	defer restoreBindingRedisKey()

	var requestedFields []string
	resolver := &BindingResolver{
		redisLookup: func(ctx context.Context, key, field string) (string, error) {
			require.Equal(t, "test:surrealdb_binding", key)
			requestedFields = append(requestedFields, field)
			if field == "bkcc__2|tenant-a" {
				return `{"name":"binding-a","bk_biz_id":"2","database":"2_graph_rt","namespace":"mapleleaf_2","cluster_name":"surrealdb-main","phase":"Ok"}`, nil
			}
			return "", goRedis.Nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	info, err := resolver.Resolve(ctx, "bkcc__2")

	require.NoError(t, err)
	assert.Equal(t, "binding-a", info.Name)
	assert.Equal(t, "2_graph_rt", info.Database)
	assert.Equal(t, "mapleleaf_2", info.Namespace)
	assert.Equal(t, "surrealdb-main", info.ClusterName)
	assert.Equal(t, []string{"bkcc__2|tenant-a"}, requestedFields)
}

func TestBindingResolverFallsBackToPlainRedisField(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("")
	restoreBindingRedisKey := setBindingRedisKeyForTest("test:surrealdb_binding")
	defer restoreBindingRedisKey()

	var requestedFields []string
	resolver := &BindingResolver{
		redisLookup: func(ctx context.Context, key, field string) (string, error) {
			requestedFields = append(requestedFields, field)
			if field == "bkcc__2" {
				return `{"name":"binding-a","bk_biz_id":"2","database":"2_graph_rt","namespace":"mapleleaf_2","phase":"Ok"}`, nil
			}
			return "", goRedis.Nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	info, err := resolver.Resolve(ctx, "bkcc__2")

	require.NoError(t, err)
	assert.Equal(t, "binding-a", info.Name)
	assert.Equal(t, []string{"bkcc__2"}, requestedFields)
}

func TestBindingResolverDoesNotFallbackToPlainRedisFieldForTenant(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	restoreBindingRedisKey := setBindingRedisKeyForTest("test:surrealdb_binding")
	defer restoreBindingRedisKey()

	var requestedFields []string
	resolver := &BindingResolver{
		redisLookup: func(ctx context.Context, key, field string) (string, error) {
			requestedFields = append(requestedFields, field)
			if field == "bkcc__2" {
				return `{"name":"binding-other","bk_biz_id":"2","database":"other_graph_rt","namespace":"mapleleaf_other","phase":"Ok"}`, nil
			}
			return "", goRedis.Nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(ctx, "bkcc__2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no usable SurrealDBBinding")
	assert.Equal(t, []string{"bkcc__2|tenant-a"}, requestedFields)
}

func TestBindingResolverRejectsNotReadyRedisRoute(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	restoreBindingRedisKey := setBindingRedisKeyForTest("test:surrealdb_binding")
	defer restoreBindingRedisKey()

	resolver := &BindingResolver{
		redisLookup: func(ctx context.Context, key, field string) (string, error) {
			return `{"name":"binding-a","bk_biz_id":"2","database":"2_graph_rt","namespace":"mapleleaf_2","phase":"Pending"}`, nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(ctx, "bkcc__2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestBindingResolverRejectsRedisRouteForDifferentBiz(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	restoreBindingRedisKey := setBindingRedisKeyForTest("test:surrealdb_binding")
	defer restoreBindingRedisKey()

	resolver := &BindingResolver{
		redisLookup: func(ctx context.Context, key, field string) (string, error) {
			return `{"name":"binding-a","bk_biz_id":"3","database":"3_graph_rt","namespace":"mapleleaf_3","phase":"Ok"}`, nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(ctx, "bkcc__2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "biz mismatch")
	assert.Contains(t, err.Error(), "binding_bk_biz_id=3")
	assert.Contains(t, err.Error(), "request_bk_biz_id=2")
}

func TestBindingResolverDeletesExpiredCacheEntry(t *testing.T) {
	resolver := &BindingResolver{cache: make(map[string]*bindingCacheEntry)}
	cacheKey := bindingCacheKey("tenant-a", "2")
	expired := &bindingCacheEntry{
		info:   &BindingInfo{Name: "expired"},
		expiry: time.Now().Add(-time.Second),
	}
	resolver.cache[cacheKey] = expired

	assert.Nil(t, resolver.lookupCache(cacheKey))
	assert.Equal(t, 0, resolver.cacheSize())
}

func contextWithTenantForBindingResolverTest(tenantID string) context.Context {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{TenantID: tenantID})
	return ctx
}

func setBindingRedisKeyForTest(key string) func() {
	old := BindingRedisKey
	BindingRedisKey = key
	return func() {
		BindingRedisKey = old
	}
}
