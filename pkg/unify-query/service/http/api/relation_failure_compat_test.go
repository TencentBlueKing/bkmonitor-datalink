// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

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
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestRelationFailureResponseCompatibility(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	v1beta1.InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	model, err := v1beta3.GetModel(context.Background())
	require.NoError(t, err)
	model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return model, nil })
	previousYolo := viper.GetBool(v1beta3.YoloModeConfigPath)
	t.Cleanup(func() {
		viper.Set(v1beta3.YoloModeConfigPath, previousYolo)
		v1beta3.LoadConfig()
	})

	// 使用生产静态 schema 和实际查询入口；这些错误均发生在存储访问前。
	cases := []struct {
		name   string
		fields string
	}{
		{"root", `"source_type":"pod","source_info":{"bcs_cluster_id":"cluster","namespace":"namespace","pod":"pod-1","ignored":"value"}`},
		{"root self", `"source_type":"pod","target_type":"pod","source_info":{"bcs_cluster_id":"cluster","namespace":"namespace","pod":"pod-1"}`},
		{"root inferred", `"source_info":{"bcs_cluster_id":"cluster","namespace":"namespace","pod":"pod-1"}`},
		{"root without matcher", `"source_type":"pod"`},
		{"root without info fields", `"source_type":"pod","target_info_show":true,"source_info":{"pod":"pod-1"}`},
		{"root expanded", `"source_type":"container","source_info":{"bcs_cluster_id":"cluster","namespace":"namespace","pod":"pod-1","container":"container-1"},"source_expand_info":{"version":"v1"}`},
		{"unknown inference", `"source_info":{"device":"example"}`},
		{"empty inference", `"source_info":{}`},
		{"unknown source", `"source_type":"unknown","source_info":{"id":"example"}`},
		{"no matching path", `"source_type":"pod","target_type":"system","source_info":{"pod":"pod-1"},"path_resource":["unreachable"]`},
	}
	for _, variant := range []struct{ rangeQuery, yolo, proxy bool }{
		{},
		{rangeQuery: true},
		{yolo: true},
		{rangeQuery: true, yolo: true},
		{proxy: true},
		{rangeQuery: true, proxy: true},
		{yolo: true, proxy: true},
		{rangeQuery: true, yolo: true, proxy: true},
	} {
		t.Run(fmt.Sprintf("range=%t/yolo=%t/proxy=%t", variant.rangeQuery, variant.yolo, variant.proxy), func(t *testing.T) {
			viper.Set(v1beta3.YoloModeConfigPath, variant.yolo)
			v1beta3.LoadConfig()
			timeFields := `"timestamp":1700000000`
			handlers := []gin.HandlerFunc{HandlerAPIRelationMultiResource, HandlerAPIRelationV1Beta3MultiResource}
			if variant.rangeQuery {
				timeFields = `"start_time":1700000000,"end_time":1700000060,"step":"60s"`
				handlers = []gin.HandlerFunc{HandlerAPIRelationMultiResourceRange, HandlerAPIRelationV1Beta3MultiResourceRange}
			}
			var queries []string
			var standalone []map[string]any
			for _, tc := range cases {
				queries = append(queries, "{"+timeFields+","+tc.fields+"}")
			}
			for index, query := range append(append([]string(nil), queries...), strings.Join(queries, ",")) {
				name := "mixed batch"
				if index < len(cases) {
					name = cases[index].name
				}
				t.Run(name, func(t *testing.T) {
					var responses [][]map[string]any
					for handlerIndex, handler := range handlers {
						ctx := metadata.InitHashID(context.Background())
						metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
						w := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(w)
						body := `{"query_list":[` + query + `]}`
						if variant.proxy {
							route := fmt.Sprintf("/test-relation-failure-%t-%d", variant.rangeQuery, handlerIndex)
							if _, registered := metadata.GetHandler(route); !registered {
								metadata.AddHandler(route, handler)
							}
							body = `{"path":"` + route + `","data":` + body + `}`
							c.Request = httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(body)).WithContext(ctx)
							proxy.HandleProxy(c)
						} else {
							c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
							handler(c)
						}
						require.Equal(t, http.StatusOK, w.Code, w.Body.String())
						payload := w.Body.Bytes()
						if variant.proxy {
							var envelope struct {
								Result bool            `json:"result"`
								Data   json.RawMessage `json:"data"`
							}
							require.NoError(t, json.Unmarshal(payload, &envelope))
							require.True(t, envelope.Result)
							payload = envelope.Data
						}
						var response struct {
							Data []map[string]any `json:"data"`
						}
						require.NoError(t, json.Unmarshal(payload, &response))
						for _, item := range response.Data {
							require.Len(t, item, 7)
							require.Equal(t, float64(400), item["code"])
							require.Equal(t, []any{}, item["target_list"])
							require.Nil(t, item["path"])
						}
						responses = append(responses, response.Data)
					}
					require.Equal(t, responses[0], responses[1])
					if index < len(cases) {
						standalone = append(standalone, responses[1][0])
					} else {
						require.Equal(t, standalone, responses[1])
					}
				})
			}
		})
	}
}
