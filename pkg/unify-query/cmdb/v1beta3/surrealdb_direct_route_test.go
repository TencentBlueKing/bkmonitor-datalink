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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestSurrealDBRouteResolvesStorageAndExecutesDirectQuery(t *testing.T) {
	const (
		storageID = "700007"
		username  = "root"
		password  = "secret"
	)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/sql", request.URL.Path)
		assert.Equal(t, "mapleleaf_2", request.Header.Get("NS"))
		assert.Equal(t, "2_graph_rt", request.Header.Get("DB"))
		actualUsername, actualPassword, ok := request.BasicAuth()
		require.True(t, ok)
		assert.Equal(t, username, actualUsername)
		assert.Equal(t, password, actualPassword)
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Equal(t, "USE NS `mapleleaf_2` DB `2_graph_rt`;RETURN [];", string(body))
		response.Header().Set("Content-Type", "application/json")
		_, err = response.Write([]byte(`[{"result":[]}]`))
		require.NoError(t, err)
	}))
	defer server.Close()

	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:     metadata.SurrealDBStorageType,
		Address:  server.URL,
		Username: username,
		Password: password,
	})

	ctx := contextWithTenantForBindingResolverTest("tenant-a")
	resolver := &BindingResolver{
		redisLookup: routeRedisLookupForTest(map[string]string{
			routeRedisLookupKey(DefaultSpaceToResultTableRedisKey, "bkcc__2|tenant-a"):      `{"surreal.graph":{"filters":[{"bk_biz_id":"2"}]}}`,
			routeRedisLookupKey(DefaultResultTableDetailRedisKey, "surreal.graph|tenant-a"): `{"storage_id":700007,"storage_type":"surrealdb","database":"2_graph_rt","namespace":"mapleleaf_2","cluster_name":"surrealdb-main"}`,
		}, nil),
		cache: make(map[string]*bindingCacheEntry),
	}

	binding, err := resolver.Resolve(ctx, "bkcc__2")
	require.NoError(t, err)

	graphs, err := (&BKBaseSurrealDBClient{}).ExecuteWithBinding(ctx, "bkcc__2", *binding, "RETURN [];", 1000, 2000)

	require.NoError(t, err)
	assert.Empty(t, graphs)
}
