// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRolloutFallbackPreservesRequestAndExplicitProbes(t *testing.T) {
	log.InitTestLogger()
	// An unavailable/static provider cannot promise a Redis rollout version.
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	for _, version := range []string{"", "stale-version"} {
		metadata.InitMetadata()
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
		body := `{"query_list":[{"source_type":"pod","timestamp":1700000000}]}`
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body)).WithContext(ctx)
		c.Request.Header.Set(relationSchemaVersionHeader, version)
		c.Request.Header.Set("X-Bk-Tenant-Id", "tenant-a")
		called := false
		fallback := fallbackRelationSchema(c, func(c *gin.Context) {
			called = true
			got, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(got))
			require.Equal(t, "tenant-a", c.GetHeader("X-Bk-Tenant-Id"))
			require.Equal(t, "bkcc__2", metadata.GetUser(c.Request.Context()).SpaceUID)
		})
		require.Equal(t, version != "", called)
		require.Equal(t, called, fallback)
	}
}

func TestRolloutFallbackInstantAndRangeUseLegacyHandlers(t *testing.T) {
	log.InitTestLogger()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	for name, handler := range map[string]gin.HandlerFunc{
		"instant": HandlerAPIRelationV1Beta3MultiResource,
		"range":   HandlerAPIRelationV1Beta3MultiResourceRange,
	} {
		t.Run(name, func(t *testing.T) {
			metadata.InitMetadata()
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"query_list":[]}`)).WithContext(ctx)
			c.Request.Header.Set(relationSchemaVersionHeader, "stale-version")
			handler(c)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, "schema-not-ready", recorder.Header().Get("X-Bk-Relation-Fallback"))
			require.Contains(t, recorder.Body.String(), `"data":[]`)
		})
	}
}

func TestRolloutRequestLoadsDefinitionsAfterMissedNotification(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	resourceKey := relation.RedisKeyPrefix + ":" + relation.KindResourceDefinition
	relationKey := relation.RedisKeyPrefix + ":" + relation.KindRelationDefinition
	require.NoError(t, client.HSet(ctx, resourceKey, relation.NamespaceAll, `{"service":{"name":"service","fields":[{"name":"shared_id","required":true}]}}`).Err())
	require.NoError(t, client.HSet(ctx, relationKey, relation.NamespaceAll, `{"calls":{"name":"calls","from_resource":"service","to_resource":"service"}}`).Err())
	provider, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	require.NoError(t, err)
	defer provider.Close()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(provider)
	defer v1beta3.InitSchemaProvider(previous)
	channel := relationKey + relation.DefaultRedisPubSubChannelSuffix
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, channel).Result()
		return err == nil && counts[channel] == 1
	}, time.Second, time.Millisecond)
	// Even with no Pub/Sub publication, the first versioned HTTP request must
	// synchronously load the business override before returning query results.
	require.NoError(t, client.HSet(ctx, resourceKey, "bkcc__2", `{"service":{"name":"service","fields":[{"name":"business_id","required":true}]}}`).Err())
	require.NoError(t, client.Set(ctx, relation.RelationSchemaReadyKey, `{"version":"ready-v1","biz_ids":[2]}`, 0).Err())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"query_list":[]}`)).WithContext(ctx)
	c.Request.Header.Set(relationSchemaVersionHeader, "ready-v1")
	HandlerAPIRelationV1Beta3MultiResource(c)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, recorder.Header().Get("X-Bk-Relation-Fallback"))
	require.Equal(t, []string{"business_id"}, v1beta3.GetSchemaProvider().GetResourcePrimaryKeys("bkcc__2", "service"))
}
