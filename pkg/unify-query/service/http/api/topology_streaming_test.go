// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// 逐帧编码一项与把全部帧放进 Snapshots 后编码得到相同的片段（字节和分块边界都相同）。
func TestTopologyItemFrameEncodingMatchesSliceEncoding(t *testing.T) {
	item := largeTopologyBenchmarkItem()
	item.Snapshots = item.Snapshots[:60]
	item.PointCount = len(item.Snapshots)
	item.Snapshots[7].Partial, item.Snapshots[7].PartialReason = true, "backend_partial"
	want, wantBytes, err := marshalTopologyItem(context.Background(), item)
	require.NoError(t, err)
	header := cmdb.SharedTopologyResult{StartTime: item.StartTime, EndTime: item.EndTime, Step: item.Step, PointCount: item.PointCount}
	frames := v1beta3.TopologyFrames(func(yield func(cmdb.SharedTopologySnapshot) error) error {
		for _, snapshot := range item.Snapshots {
			if err := yield(snapshot); err != nil {
				return err
			}
		}
		return nil
	})
	encoded := encodeTopologyItem(context.Background(), "", header, frames, "")
	require.NoError(t, encoded.err)
	require.Equal(t, want, encoded.parts)
	require.Equal(t, wantBytes, encoded.bytes)
	require.Equal(t, len(item.Snapshots), encoded.snapshots)
	require.Equal(t, 1, encoded.counts.partial)

	short := v1beta3.TopologyFrames(func(yield func(cmdb.SharedTopologySnapshot) error) error { return yield(item.Snapshots[0]) })
	require.ErrorContains(t, encodeTopologyItem(context.Background(), "", header, short, "").err, "ended after 1 of 60 frames")
}

type topologyStreamProbe struct {
	*tracetest.SpanRecorder
	body    *bytes.Buffer
	written []int
	cancel  func(query int)
}

func (p *topologyStreamProbe) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.SpanRecorder.OnStart(ctx, span)
	if span.Name() == "timegraph-query-shared-topology" {
		p.written = append(p.written, p.body.Len())
		if p.cancel != nil {
			p.cancel(len(p.written))
		}
	}
}

// 批次逐项写出：后一项的模型查询开始前，前一项已经写到响应里。已开始写出后请求被取消时，
// 当前项及其后各项按单项失败写出，响应仍是完整 JSON；取消前已写出的项保持原样。
func TestTopologyBatchWritesEachItemBeforeTheNext(t *testing.T) {
	useRelationTestConfig(t)
	log.InitTestLogger()
	metadata.InitMetadata()
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	v1beta3.InitSchemaProvider(nil)
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	const items = 3
	list := make([]string, items)
	for i := range list {
		list[i] = `{"timestamp":1700000000,"source_type":"unknown-resource"}`
	}
	body := `{"query_list":[` + strings.Join(list, ",") + `]}`
	for _, proxied := range []bool{false, true} {
		for _, cancelAt := range []int{0, 2} {
			t.Run(fmt.Sprintf("proxy=%t/cancel_at_query_%d", proxied, cancelAt), func(t *testing.T) {
				writer := httptest.NewRecorder()
				ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
				defer cancel()
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
				probe := &topologyStreamProbe{SpanRecorder: tracetest.NewSpanRecorder(), body: writer.Body}
				if cancelAt > 0 {
					probe.cancel = func(query int) {
						if query == cancelAt {
							cancel()
						}
					}
				}
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(probe))
				old := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
				c, _ := gin.CreateTestContext(writer)
				if proxied {
					const route = "/test-topology-streaming"
					if _, registered := metadata.GetHandler(route); !registered {
						metadata.AddHandler(route, HandlerAPIRelationV1Beta3Topology)
					}
					c.Request = httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(`{"path":"`+route+`","data":`+body+`}`)).WithContext(ctx)
					proxy.HandleProxy(c)
				} else {
					c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)).WithContext(ctx)
					HandlerAPIRelationV1Beta3Topology(c)
				}
				require.Equal(t, http.StatusOK, writer.Code, writer.Body.String())
				payload := writer.Body.Bytes()
				require.True(t, stdjson.Valid(payload), writer.Body.String())
				if proxied {
					var envelope struct {
						Result bool               `json:"result"`
						Data   stdjson.RawMessage `json:"data"`
					}
					require.NoError(t, stdjson.Unmarshal(payload, &envelope))
					require.True(t, envelope.Result)
					payload = envelope.Data
				}
				var response struct {
					Data []cmdb.SharedTopologyResponseData `json:"data"`
				}
				require.NoError(t, stdjson.Unmarshal(payload, &response))
				require.Len(t, response.Data, items)

				queries := items
				if cancelAt > 0 {
					queries = cancelAt
				}
				require.Len(t, probe.written, queries)
				require.Zero(t, probe.written[0], "nothing is written before the first item is encoded")
				for i := 1; i < len(probe.written); i++ {
					require.Greater(t, probe.written[i], probe.written[i-1], "item %d is written before query %d starts", i-1, i)
				}
				for index, item := range response.Data {
					if cancelAt > 0 && index >= cancelAt-1 {
						require.Equal(t, http.StatusBadRequest, item.Code)
						require.Contains(t, item.Message, "context canceled", "item %d", index)
					} else {
						require.NotContains(t, item.Message, "context canceled", "item %d", index)
					}
				}
				if cancelAt > 0 {
					require.NoError(t, proxy.ResponseWriteError(c), "the failure tail is written after cancellation")
				}
			})
		}
	}
}
