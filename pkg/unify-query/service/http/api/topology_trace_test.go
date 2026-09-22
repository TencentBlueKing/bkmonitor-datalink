// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

type topologyTraceWriter struct {
	*httptest.ResponseRecorder
	t        *testing.T
	recorder *tracetest.SpanRecorder
	fail     bool
}

func (w *topologyTraceWriter) Write(body []byte) (int, error) {
	for _, span := range w.recorder.Ended() {
		require.NotEqual(w.t, "handler-api-relation-v1beta3-topology", span.Name(), "root must remain open through write")
	}
	if w.fail {
		return 0, errors.New("test writer failed")
	}
	return w.ResponseRecorder.Write(body)
}

func TestTopologyFullHTTPTraceLifecycle(t *testing.T) {
	log.InitTestLogger()
	for _, proxied := range []bool{false, true} {
		for _, test := range []struct {
			name, body   string
			writeFailure bool
		}{
			{name: "empty", body: `{"query_list":[]}`},
			{name: "item validation", body: `{"query_list":[{}]}`},
			{name: "decode rejection", body: `{"query_list":"invalid"}`},
			{name: "write failure", body: `{"query_list":[]}`, writeFailure: true},
		} {
			t.Run(test.name+map[bool]string{false: "/direct", true: "/proxy"}[proxied], func(t *testing.T) {
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				old := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
				w := &topologyTraceWriter{ResponseRecorder: httptest.NewRecorder(), t: t, recorder: recorder, fail: test.writeFailure}
				c, _ := gin.CreateTestContext(w)
				body := test.body
				if proxied {
					const route = "/test-full-topology-trace"
					metadata.AddHandler(route, HandlerAPIRelationV1Beta3Topology)
					body = `{"path":"` + route + `","data":` + body + `}`
				}
				c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
				if proxied {
					proxy.HandleProxy(c)
				} else {
					HandlerAPIRelationV1Beta3Topology(c)
				}
				require.Len(t, recorder.Ended(), len(recorder.Started()), "all paths end all spans")
				spans := map[string]sdktrace.ReadOnlySpan{}
				byID := map[string]sdktrace.ReadOnlySpan{}
				for _, span := range recorder.Ended() {
					spans[span.Name()] = span
					byID[span.SpanContext().SpanID().String()] = span
				}
				for _, name := range []string{"handler-api-relation-v1beta3-topology", "timegraph-admission", "topology-read-request-body", "topology-decode-request", "http-response-encode-write", "timegraph-release-admission"} {
					require.NotNil(t, spans[name], name)
				}
				root := spans["handler-api-relation-v1beta3-topology"]
				for _, span := range recorder.Ended() {
					require.Equal(t, root.SpanContext().TraceID(), span.SpanContext().TraceID(), span.Name())
					if span.Parent().IsValid() {
						parent := byID[span.Parent().SpanID().String()]
						require.NotNil(t, parent, span.Name())
						require.False(t, span.StartTime().Before(parent.StartTime()), span.Name())
						require.False(t, span.EndTime().After(parent.EndTime()), span.Name())
					}
				}
				if proxied {
					require.Equal(t, spans["handler-proxy"].SpanContext().SpanID(), root.Parent().SpanID())
					require.NotNil(t, spans["proxy-decode-request"])
				}
				if test.writeFailure {
					require.Equal(t, codes.Error, spans["http-response-encode-write"].Status().Code)
					require.Equal(t, codes.Error, root.Status().Code)
				}
				if test.name == "item validation" {
					require.NotNil(t, spans["timegraph-encode-topology-item"])
				}
				require.Zero(t, topologyObservation(t, "cmdb_topology_admission_active", nil).GetGauge().GetValue())
			})
		}
	}
}
