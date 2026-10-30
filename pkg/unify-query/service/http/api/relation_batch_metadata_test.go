// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

type relationBatchContextRecorder struct {
	*tracetest.SpanRecorder
	mu       sync.Mutex
	contexts []context.Context
}

func (r *relationBatchContextRecorder) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	r.SpanRecorder.OnStart(ctx, span)
	if span.Name() == "get-resource-indexMatcher" {
		r.mu.Lock()
		r.contexts = append(r.contexts, ctx)
		r.mu.Unlock()
	}
}

func TestRelationBatchMetadataIsolation(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	v1beta1.InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	for _, rangeQuery := range []bool{false, true} {
		for _, allPaths := range []bool{false, true} {
			for _, proxied := range []bool{false, true} {
				for _, fallback := range []bool{false, true} {
					t.Run(fmt.Sprintf("range=%t/all_paths=%t/proxy=%t/fallback=%t", rangeQuery, allPaths, proxied, fallback), func(t *testing.T) {
						recorder := &relationBatchContextRecorder{SpanRecorder: tracetest.NewSpanRecorder()}
						provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
						oldProvider := otel.GetTracerProvider()
						otel.SetTracerProvider(provider)
						t.Cleanup(func() {
							otel.SetTracerProvider(oldProvider)
							require.NoError(t, provider.Shutdown(context.Background()))
						})
						handler := HandlerAPIRelationMultiResource
						if rangeQuery {
							handler = HandlerAPIRelationMultiResourceRange
						}
						if fallback {
							handler = HandlerAPIRelationV1Beta3MultiResource
							if rangeQuery {
								handler = HandlerAPIRelationV1Beta3MultiResourceRange
							}
						}
						const count = 8
						queries := make([]string, count)
						wantStarts := make(map[int64]bool, count)
						for i := range queries {
							start := int64(1700000000 + i*120)
							wantStarts[start] = true
							fields := fmt.Sprintf(`"timestamp":%d`, start)
							if rangeQuery {
								fields = fmt.Sprintf(`"start_time":%d,"end_time":%d,"step":"60s"`, start, start+60)
							}
							queries[i] = fmt.Sprintf(`{%s,"return_all_paths":%t,"source_type":"pod","source_info":{"pod":"pod-1"}}`, fields, allPaths)
						}
						parent := metadata.InitHashID(context.Background())
						metadata.SetUser(parent, &metadata.User{SpaceUID: "test-space"})
						params := metadata.GetQueryParams(parent).SetTime(time.Unix(1, 0), time.Unix(1, 0), time.Unix(2, 0), time.Second, "s", "UTC")
						params.SetStorageType(metadata.InfluxDBStorageType).SetIsReference(true)
						body := `{"query_list":[` + strings.Join(queries, ",") + `]}`
						if proxied {
							path := fmt.Sprintf("/test-relation-batch-metadata-%t-%t", rangeQuery, fallback)
							if _, registered := metadata.GetHandler(path); !registered {
								metadata.AddHandler(path, handler)
							}
							body = `{"path":"` + path + `","data":` + body + `}`
						}
						writer := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(writer)
						c.Request = httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body)).WithContext(parent)
						if fallback {
							c.Request.Header.Set(relationSchemaVersionHeader, "not-ready")
						}
						if proxied {
							proxy.HandleProxy(c)
						} else {
							handler(c)
						}
						require.Equal(t, http.StatusOK, writer.Code, writer.Body.String())
						require.True(t, json.Valid(writer.Body.Bytes()), writer.Body.String())
						require.Equal(t, int64(1), params.Start.Unix(), "批次执行不能改写请求级时间窗")
						require.False(t, params.IsSkipK8s)
						require.True(t, params.IsReference)
						// 生产静态 schema 进入真实 QueryTs 时间解析后在存储访问前失败。
						require.Len(t, recorder.contexts, count)
						seen := make(map[*metadata.QueryParams]bool, count)
						for _, ctx := range recorder.contexts {
							item := metadata.GetQueryParams(ctx)
							require.NotSame(t, params, item)
							require.False(t, seen[item], "每个子查询必须独占可变参数")
							seen[item] = true
							require.True(t, wantStarts[item.Start.Unix()], "子查询保留自己的时间窗")
							delete(wantStarts, item.Start.Unix())
							wantEnd := item.Start
							if rangeQuery {
								wantEnd = wantEnd.Add(time.Minute)
								require.Equal(t, time.Minute, item.Step)
							}
							require.Equal(t, wantEnd, item.End)
							require.True(t, item.IsSkipK8s)
							require.True(t, item.StorageType.Existed(metadata.InfluxDBStorageType))
							require.Equal(t, "test-space", metadata.GetUser(ctx).SpaceUID)
						}
						require.Empty(t, wantStarts)
					})
				}
			}
		}
	}
}
