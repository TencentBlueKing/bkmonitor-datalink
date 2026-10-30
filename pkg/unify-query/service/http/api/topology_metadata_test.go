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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// An empty schema exercises the real model and encoders without a backend.
type topologyMetadataSchema struct{}

func (topologyMetadataSchema) GetResourcePrimaryKeys(string, v1beta3.ResourceType) []string {
	return []string{"node"}
}

func (topologyMetadataSchema) GetResourceFields(string, v1beta3.ResourceType) []string {
	return []string{"node"}
}

func (topologyMetadataSchema) ListResourceTypes(string) []v1beta3.ResourceType {
	return []v1beta3.ResourceType{"node"}
}
func (topologyMetadataSchema) ListRelationSchemas(string) []v1beta3.RelationSchema { return nil }

type topologyMetadataExpiry struct {
	*tracetest.SpanRecorder
	itemName string
	expired  bool
	cancel   context.CancelFunc
}

func (p *topologyMetadataExpiry) OnEnd(s sdktrace.ReadOnlySpan) {
	p.SpanRecorder.OnEnd(s)
	if s.Name() == p.itemName && !p.expired {
		p.expired = true
		// Deterministically evict the global cache between two batch items.
		// The metadata package separately tests real TTL expiration.
		metadata.InitMetadata()
		if p.cancel != nil {
			p.cancel()
		}
	}
}

type topologyMetadataWriter struct {
	*httptest.ResponseRecorder
	check func()
}

func (w *topologyMetadataWriter) Write(b []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(b)
}

func TestTopologyBatchMetadataLifetime(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	model, err := v1beta3.GetModel(context.Background())
	require.NoError(t, err)
	model.SetSchemaProvider(topologyMetadataSchema{})
	t.Cleanup(func() { model.SetSchemaProvider(v1beta3.GetSchemaProvider()) })
	for _, yolo := range []bool{false, true} {
		for _, rangeQuery := range []bool{false, true} {
			for _, proxied := range []bool{false, true} {
				for _, cancelAfterFirst := range []bool{false, true} {
					t.Run(fmt.Sprintf("yolo=%t/range=%t/proxy=%t/cancel=%t", yolo, rangeQuery, proxied, cancelAfterFirst), func(t *testing.T) {
						viper.Set(v1beta3.YoloModeConfigPath, yolo)
						v1beta3.LoadConfig()
						metadata.InitMetadata()
						parent, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
						defer cancel()
						metadata.SetUser(parent, &metadata.User{Key: "test:caller", TenantID: "test-tenant", SpaceUID: "test-space", SkipSpace: "true"})
						expectedUser := *metadata.GetUser(parent)
						expiry := &topologyMetadataExpiry{SpanRecorder: tracetest.NewSpanRecorder(), itemName: "handler-api-relation-v1beta3-topology-item"}
						if rangeQuery {
							expiry.itemName += "-range"
						}
						if cancelAfterFirst {
							expiry.cancel = cancel
						}
						tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(expiry))
						oldTracer := otel.GetTracerProvider()
						otel.SetTracerProvider(tp)
						t.Cleanup(func() { otel.SetTracerProvider(oldTracer); require.NoError(t, tp.Shutdown(context.Background())) })
						count := 2
						if yolo {
							count = 64
						}
						points := 1
						query := `"timestamp":1700000000`
						handler := HandlerAPIRelationV1Beta3Topology
						if rangeQuery {
							points = 60
							query = `"start_time":1700000000,"end_time":1700003540,"step":"1m"`
							handler = HandlerAPIRelationV1Beta3TopologyRange
						}
						items := make([]string, count)
						for i := range items {
							format := "snapshots"
							if i%2 != 0 {
								format = "compact-v1"
							}
							items[i] = fmt.Sprintf(`{"source_type":"node","look_back_delta":"5m","response_format":%q,%s}`, format, query)
						}
						body := `{"query_list":[` + strings.Join(items, ",") + `]}`
						if proxied {
							route := fmt.Sprintf("/test-topology-metadata-lifetime-%t-%t-%t", yolo, rangeQuery, cancelAfterFirst)
							if _, registered := metadata.GetHandler(route); !registered {
								metadata.AddHandler(route, handler)
							}
							body = `{"path":"` + route + `","data":` + body + `}`
						}
						w := &topologyMetadataWriter{ResponseRecorder: httptest.NewRecorder()}
						c, _ := gin.CreateTestContext(w)
						c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(parent)
						writes := 0
						w.check = func() {
							writes++
							require.Equal(t, expectedUser, *metadata.GetUser(c.Request.Context()), "identity must survive through response write")
							if cancelAfterFirst {
								require.ErrorIs(t, parent.Err(), context.Canceled)
								require.NoError(t, c.Request.Context().Err(), "bounded failure response survives query cancellation")
							}
						}
						if proxied {
							proxy.HandleProxy(c)
						} else {
							handler(c)
						}
						require.True(t, expiry.expired, "must evict between items")
						require.Empty(t, metadata.GetUser(parent).SpaceUID, "global metadata really was evicted")
						require.Equal(t, expectedUser, *metadata.GetUser(c.Request.Context()))
						require.Len(t, expiry.Ended(), len(expiry.Started()), "all spans released")
						if cancelAfterFirst {
							endedItems := 0
							for _, span := range expiry.Ended() {
								if span.Name() == expiry.itemName {
									endedItems++
								}
							}
							require.Equal(t, 1, endedItems, "cancellation stops subsequent queries")
							require.ErrorIs(t, parent.Err(), context.Canceled)
							require.Equal(t, 1, writes, "only the bounded failure response is written")
							require.Equal(t, http.StatusBadRequest, w.Code)
							require.True(t, json.Valid(w.Body.Bytes()), w.Body.String())
							require.Contains(t, w.Body.String(), "context canceled")
							return
						}
						require.Positive(t, writes)
						require.Equal(t, http.StatusOK, w.Code)
						raw := json.RawMessage(w.Body.Bytes())
						if proxied {
							var envelope struct {
								Data json.RawMessage `json:"data"`
							}
							require.NoError(t, json.Unmarshal(raw, &envelope))
							raw = envelope.Data
						}
						var result struct {
							Data []struct {
								Code       int               `json:"code"`
								PointCount int               `json:"point_count"`
								Message    string            `json:"message"`
								Snapshots  []json.RawMessage `json:"snapshots"`
								Compact    json.RawMessage   `json:"compact"`
							} `json:"data"`
						}
						require.NoError(t, json.Unmarshal(raw, &result))
						require.Len(t, result.Data, count)
						for i, item := range result.Data {
							require.Equal(t, http.StatusOK, item.Code, "item %d: %s", i, item.Message)
							require.Equal(t, points, item.PointCount, "item %d", i)
							if i%2 == 0 {
								require.Len(t, item.Snapshots, points)
							} else {
								require.NotEqual(t, "null", string(item.Compact))
								require.NotEmpty(t, item.Compact)
							}
						}
					})
				}
			}
		}
	}
}
