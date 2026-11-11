// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// 两个兼容入口按原始输入规格拒绝：请求体超过 max_relation_request_bytes、批次超过
// max_relation_queries 时整个 HTTP 请求失败（400），恰好等于上限时照常逐项执行。
// 代理路径按 data 内层载荷计。
func TestRelationCompatEntriesEnforceRawInputSpecs(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })

	for _, entry := range []struct {
		name    string
		handler gin.HandlerFunc
		item    string
	}{
		// 源类型不存在：逐项失败发生在存储访问前，HTTP 仍为 200。
		{name: "instant", handler: HandlerAPIRelationV1Beta3MultiResource, item: `{"timestamp":1700000000,"source_type":"unknown","source_info":{"id":"a"}}`},
		{name: "range", handler: HandlerAPIRelationV1Beta3MultiResourceRange, item: `{"start_time":1700000000,"end_time":1700000060,"step":"60s","source_type":"unknown","source_info":{"id":"a"}}`},
	} {
		for _, proxied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/proxy=%t", entry.name, proxied), func(t *testing.T) {
				post := func(body string) (int, string) {
					ctx := metadata.InitHashID(context.Background())
					metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					if proxied {
						route := "/test-relation-limits-" + entry.name
						if _, registered := metadata.GetHandler(route); !registered {
							metadata.AddHandler(route, entry.handler)
						}
						c.Request = httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(`{"path":"`+route+`","data":`+body+`}`)).WithContext(ctx)
						proxy.HandleProxy(c)
					} else {
						c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
						entry.handler(c)
					}
					return w.Code, w.Body.String()
				}
				batch := func(items int) string {
					list := make([]string, items)
					for i := range list {
						list[i] = entry.item
					}
					return `{"query_list":[` + strings.Join(list, ",") + `]}`
				}
				requireItems := func(t *testing.T, status int, body string, items int) {
					t.Helper()
					require.Equal(t, http.StatusOK, status, body)
					payload := []byte(body)
					if proxied {
						var envelope struct {
							Data json.RawMessage `json:"data"`
						}
						require.NoError(t, json.Unmarshal(payload, &envelope))
						payload = envelope.Data
					}
					var response struct {
						Data []map[string]any `json:"data"`
					}
					require.NoError(t, json.Unmarshal(payload, &response))
					require.Len(t, response.Data, items)
				}

				oldBytes, oldQueries := v1beta3.MaxRelationRequestBytes, v1beta3.MaxRelationQueries
				t.Cleanup(func() { v1beta3.MaxRelationRequestBytes, v1beta3.MaxRelationQueries = oldBytes, oldQueries })

				v1beta3.MaxRelationQueries = 2
				status, body := post(batch(2))
				requireItems(t, status, body, 2)
				status, body = post(batch(3))
				require.Equal(t, http.StatusBadRequest, status, body)
				require.Contains(t, body, "relation resource limit exceeded: max_relation_queries (3 > 2)")

				v1beta3.MaxRelationQueries = oldQueries
				exact := batch(1)
				v1beta3.MaxRelationRequestBytes = len(exact)
				status, body = post(exact)
				requireItems(t, status, body, 1)
				v1beta3.MaxRelationRequestBytes = len(exact) - 1
				status, body = post(exact)
				require.Equal(t, http.StatusBadRequest, status, body)
				require.Contains(t, body, fmt.Sprintf("relation request exceeds maximum size of %d bytes", len(exact)-1))
				require.Contains(t, body, "max_relation_request_bytes")
			})
		}
	}
}

// 兼容入口的默认规格大于 topology 的批次：按业务成批提交的目标查询不受 topology 的 16 条限制。
func TestRelationCompatDefaultsExceedTopologyBatch(t *testing.T) {
	useRelationTestConfig(t)
	require.Equal(t, 16*1024*1024, v1beta3.RelationRequestByteLimit())
	require.Equal(t, 10000, v1beta3.RelationQueryLimit())
	require.Greater(t, v1beta3.RelationQueryLimit(), v1beta3.TopologyQueryLimit())
	require.Greater(t, v1beta3.RelationRequestByteLimit(), v1beta3.TopologyRequestByteLimit())
}
