// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

type topologyInterruptContextKey struct{}

type topologyInterruptPoint struct {
	span string
	stop func()
}

type topologyInterruptRecorder struct{ *tracetest.SpanRecorder }

func (r topologyInterruptRecorder) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	r.SpanRecorder.OnStart(ctx, span)
	if point, ok := ctx.Value(topologyInterruptContextKey{}).(topologyInterruptPoint); ok && point.span == span.Name() {
		point.stop()
	}
}

func TestTopologyExecutionDeadlineReturnsJSON(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	for _, proxied := range []bool{false, true} {
		for _, rangeQuery := range []bool{false, true} {
			for _, phase := range []string{"topology-get-model", "timegraph-encode-topology-item"} {
				for _, failure := range []string{"deadline", "cancel"} {
					t.Run(fmt.Sprintf("proxy=%t/range=%t/%s/%s", proxied, rangeQuery, phase, failure), func(t *testing.T) {
						recorder := tracetest.NewSpanRecorder()
						provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(topologyInterruptRecorder{recorder}))
						oldProvider := otel.GetTracerProvider()
						otel.SetTracerProvider(provider)
						t.Cleanup(func() {
							otel.SetTracerProvider(oldProvider)
							require.NoError(t, provider.Shutdown(context.Background()))
						})
						handler := HandlerAPIRelationV1Beta3Topology
						query := `{"timestamp":1700000000,"source_type":"unknown-resource"}`
						if rangeQuery {
							handler = HandlerAPIRelationV1Beta3TopologyRange
							query = `{"start_time":1700000000,"end_time":1700000060,"step":"60s","source_type":"unknown-resource"}`
						}
						innerPath := fmt.Sprintf("/test-topology-execution-deadline-%t", rangeQuery)
						if _, exists := metadata.GetHandler(innerPath); !exists {
							metadata.AddHandler(innerPath, handler)
						}
						router := gin.New()
						router.POST("/query", func(c *gin.Context) {
							ctx := metadata.InitHashID(c.Request.Context())
							metadata.SetUser(ctx, &metadata.User{SpaceUID: "test-space"})
							if c.GetHeader("X-Test-Interrupt") == "enabled" {
								var cancel context.CancelFunc
								var stop func()
								if failure == "deadline" {
									ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
									stop = func() { <-ctx.Done() }
								} else {
									ctx, cancel = context.WithCancel(ctx)
									stop = cancel
								}
								defer cancel()
								// 在真实执行边界触发取消，保留生产模型、schema 和实际结果；客户端连接仍有效。
								ctx = context.WithValue(ctx, topologyInterruptContextKey{}, topologyInterruptPoint{span: phase, stop: stop})
							}
							c.Request = c.Request.WithContext(ctx)
							if proxied {
								proxy.HandleProxy(c)
							} else {
								handler(c)
							}
						})
						server := httptest.NewServer(router)
						defer server.Close()
						client := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
						defer client.CloseIdleConnections()
						post := func(interrupt bool) (int, []byte) {
							body := `{"query_list":[` + query + `]}`
							if proxied {
								body = `{"path":"` + innerPath + `","data":` + body + `}`
							}
							request, err := http.NewRequest(http.MethodPost, server.URL+"/query", strings.NewReader(body))
							require.NoError(t, err)
							request.Header.Set("Content-Type", "application/json")
							if interrupt {
								request.Header.Set("X-Test-Interrupt", "enabled")
							}
							response, err := client.Do(request)
							require.NoError(t, err)
							raw, err := io.ReadAll(response.Body)
							require.NoError(t, response.Body.Close())
							require.NoError(t, err)
							return response.StatusCode, raw
						}
						status, raw := post(false)
						require.Equal(t, http.StatusOK, status, string(raw))
						require.True(t, json.Valid(raw), string(raw))
						before := len(recorder.Ended())
						status, raw = post(true)
						queries := 0
						for _, span := range recorder.Ended()[before:] {
							if span.Name() == "timegraph-query-shared-topology" {
								queries++
							}
						}
						wantQueries := 0
						if phase == "timegraph-encode-topology-item" {
							wantQueries = 1
						}
						require.Equal(t, wantQueries, queries)
						wantStatus, wantMessage := http.StatusBadRequest, "context canceled"
						if failure == "deadline" {
							wantStatus, wantMessage = http.StatusGatewayTimeout, "context deadline exceeded"
						}
						t.Logf("status=%d body_bytes=%d executed_queries=%d", status, len(raw), queries)
						require.Equal(t, wantStatus, status, string(raw))
						require.True(t, json.Valid(raw), "服务器中断查询时，已连接的客户端应收到错误 JSON")
						require.Contains(t, string(raw), wantMessage)
					})
				}
			}
		}
	}
}

func TestTopologySuccessStillHonorsCancellation(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	for _, proxied := range []bool{false, true} {
		t.Run(fmt.Sprintf("proxy=%t", proxied), func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(topologyInterruptRecorder{recorder}))
			oldProvider := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(oldProvider)
				require.NoError(t, provider.Shutdown(context.Background()))
			})
			const innerPath = "/test-topology-success-cancellation"
			if _, exists := metadata.GetHandler(innerPath); !exists {
				metadata.AddHandler(innerPath, HandlerAPIRelationV1Beta3Topology)
			}
			for _, interrupted := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				if interrupted {
					ctx = context.WithValue(ctx, topologyInterruptContextKey{}, topologyInterruptPoint{
						span: "http-response-encode-write", stop: cancel,
					})
				}
				body := `{"query_list":[]}`
				if proxied {
					body = `{"path":"` + innerPath + `","data":` + body + `}`
				}
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body)).WithContext(ctx)
				if proxied {
					proxy.HandleProxy(c)
				} else {
					HandlerAPIRelationV1Beta3Topology(c)
				}
				cancel()
				if interrupted {
					require.ErrorIs(t, proxy.ResponseWriteError(c), context.Canceled)
					require.Empty(t, writer.Body.String())
				} else {
					require.NoError(t, proxy.ResponseWriteError(c))
					require.Equal(t, http.StatusOK, writer.Code)
					require.True(t, json.Valid(writer.Body.Bytes()), writer.Body.String())
					require.Contains(t, writer.Body.String(), `"data":[]`)
				}
			}
		})
	}
}
