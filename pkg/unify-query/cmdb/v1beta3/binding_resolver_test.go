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
	"fmt"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestBindingResolverFetchesSurrealDBSubRouteFromDualWriteResultTable(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	var requests []string
	resolver := &BindingResolver{
		redisLookup: routeRedisLookupForTest(map[string]string{
			routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a"):      `{"metric.vm":{"filters":[{"bk_biz_id":"2"}]},"surreal.graph":{"filters":[{"bk_biz_id":"2"}]}}`,
			routeRedisLookupKey(DefaultResultTableDetailRedisKey, "metric.vm|tenant-a"):     `{"storage_id":6,"storage_type":"victoria_metrics"}`,
			routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph|tenant-a"): `{"storage_id":6,"storage_type":"victoria_metrics","vm_rt":"2_graph_metric_vm","surrealdb":{"storage_id":7,"storage_type":"surrealdb","database":"2_graph_rt","namespace":"mapleleaf_2","cluster_name":"surrealdb-main"}}`,
		}, &requests),
		cache: make(map[string]*bindingCacheEntry),
	}

	info, err := resolver.Resolve(ctx, "bkcc__2")

	require.NoError(t, err)
	assert.Equal(t, &BindingInfo{
		Name:        "surreal.graph",
		BkBizID:     "2",
		Database:    "2_graph_rt",
		Namespace:   "mapleleaf_2",
		ClusterName: "surrealdb-main",
		StorageID:   "7",
		StorageType: metadata.SurrealDBStorageType,
		Phase:       "Ok",
	}, info)
	assert.Equal(t, []string{
		routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a"),
		routeRedisLookupKey(DefaultResultTableDetailRedisKey, "metric.vm|tenant-a"),
		routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph|tenant-a"),
	}, requests)
}

func TestBindingResolverFetchesPlainSurrealDBResultTableRoute(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("")
	var requests []string
	resolver := &BindingResolver{
		redisLookup: routeRedisLookupForTest(map[string]string{
			routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2"):      `{"surreal.graph":{"filters":[{"bk_biz_id":"2"}]}}`,
			routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph"): `{"storage_id":"7","storage_type":"surrealdb","db":"2_graph_rt","namespace":"mapleleaf_2"}`,
		}, &requests),
		cache: make(map[string]*bindingCacheEntry),
	}

	info, err := resolver.Resolve(ctx, "bkcc__2")

	require.NoError(t, err)
	assert.Equal(t, "surreal.graph", info.Name)
	assert.Equal(t, "7", info.StorageID)
	assert.Equal(t, []string{
		routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2"),
		routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph"),
	}, requests)
}

func TestBindingResolverDoesNotFallbackToPlainRouteForTenant(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	var requests []string
	resolver := &BindingResolver{
		redisLookup: routeRedisLookupForTest(map[string]string{
			routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2"): `{"surreal.graph":{"filters":[]}}`,
		}, &requests),
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(ctx, "bkcc__2")

	require.ErrorContains(t, err, "no usable SurrealDB result table route")
	assert.Equal(t, []string{routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a")}, requests)
}

func TestBindingResolverSkipsNonSurrealDBResultTable(t *testing.T) {
	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	resolver := &BindingResolver{
		redisLookup: routeRedisLookupForTest(map[string]string{
			routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a"):  `{"metric.vm":{"filters":[]}}`,
			routeRedisLookupKey(DefaultResultTableDetailRedisKey, "metric.vm|tenant-a"): `{"storage_id":6,"storage_type":"victoria_metrics"}`,
		}, nil),
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(ctx, "bkcc__2")

	require.ErrorContains(t, err, "no usable SurrealDB result table route")
}

func TestBindingResolverRejectsIncompleteSurrealDBResultTableDetail(t *testing.T) {
	testCases := []struct {
		name   string
		detail string
		error  string
	}{
		{name: "missing storage id", detail: `{"storage_type":"surrealdb","database":"2_graph_rt","namespace":"mapleleaf_2"}`, error: "missing storage_id"},
		{name: "missing database", detail: `{"storage_id":7,"storage_type":"surrealdb","namespace":"mapleleaf_2"}`, error: "missing database or namespace"},
		{name: "missing namespace", detail: `{"storage_id":7,"storage_type":"surrealdb","database":"2_graph_rt"}`, error: "missing database or namespace"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := contextWithTenantForBindingResolverTest("tenant-a")
			resolver := &BindingResolver{
				redisLookup: routeRedisLookupForTest(map[string]string{
					routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a"):      `{"surreal.graph":{"filters":[]}}`,
					routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph|tenant-a"): testCase.detail,
				}, nil),
				cache: make(map[string]*bindingCacheEntry),
			}

			_, err := resolver.Resolve(ctx, "bkcc__2")

			require.ErrorContains(t, err, testCase.error)
		})
	}
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

func TestBindingResolverSweepsExpiredEntriesForOtherKeys(t *testing.T) {
	resolver := &BindingResolver{cache: map[string]*bindingCacheEntry{
		"expired-a": {info: &BindingInfo{Name: "a"}, expiry: time.Now().Add(-time.Minute)},
		"expired-b": {info: &BindingInfo{Name: "b"}, expiry: time.Now().Add(-time.Second)},
		"active":    {info: &BindingInfo{Name: "active"}, expiry: time.Now().Add(time.Minute)},
	}}

	resolver.sweepExpiredCache(time.Now())

	assert.Equal(t, 1, resolver.cacheSize())
	assert.NotNil(t, resolver.lookupCache("active"))
}

func TestBindingResolverCacheHasMaximumCapacity(t *testing.T) {
	previousMaxSize := BindingCacheMaxSize
	BindingCacheMaxSize = 2
	t.Cleanup(func() { BindingCacheMaxSize = previousMaxSize })
	resolver := &BindingResolver{cache: make(map[string]*bindingCacheEntry)}

	resolver.storeCache("one", &BindingInfo{Name: "one"})
	resolver.storeCache("two", &BindingInfo{Name: "two"})
	resolver.storeCache("three", &BindingInfo{Name: "three"})

	assert.Equal(t, 2, resolver.cacheSize())
	assert.NotNil(t, resolver.lookupCache("three"))
}

func contextWithTenantForBindingResolverTest(tenantID string) context.Context {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{TenantID: tenantID})
	return ctx
}

func routeRedisLookupForTest(values map[string]string, requests *[]string) bindingRedisLookup {
	return func(_ context.Context, key, field string) (string, error) {
		lookupKey := routeRedisLookupKey(key, field)
		if requests != nil {
			*requests = append(*requests, lookupKey)
		}
		value, ok := values[lookupKey]
		if !ok {
			return "", goRedis.Nil
		}
		return value, nil
	}
}

func routeRedisLookupKey(key, field string) string {
	return fmt.Sprintf("%s#%s", key, field)
}
