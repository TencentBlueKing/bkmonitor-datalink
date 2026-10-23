// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqPromql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// Exercise negotiation and the actual query/ts handler without any remote
// query I/O. Client/server versions are independent of configuration gates.
func TestSharedSchemaHandlerUpgradeCompatibility(t *testing.T) {
	metadata.InitMetadata()
	uqPromql.MockEngine()
	largeGroup := strings.Repeat("x", sharedSchemaMaxFrameBytes)
	for _, test := range []struct {
		name, accept, expression, fallback string
		shared, flushFailure, reject       bool
	}{
		{name: "old client"},
		{name: "json client", accept: "application/json"},
		{name: "wildcard client", accept: "*/*"},
		{name: "unknown version", accept: "application/vnd.bkmonitor.uq.shared-schema.v2+ndjson"},
		{name: "unknown version with json", accept: "application/vnd.bkmonitor.uq.shared-schema.v2+ndjson, application/json"},
		{name: "explicit v1", accept: sharedSchemaV1MediaType + ", application/json;q=0.9", shared: true},
		{name: "v1 unacceptable", accept: sharedSchemaV1MediaType + ";q=0, application/json"},
		{name: "json preferred", accept: sharedSchemaV1MediaType + ";q=0.5, application/json"},
		{name: "json fallback required", accept: sharedSchemaV1MediaType, reject: true},
		{name: "same result frame fallback", accept: sharedSchemaV1MediaType + ", application/json;q=0.9", expression: `label_replace(vector(1), "device", "` + largeGroup + `", "__name__", ".*")`, fallback: sharedFallbackFrame},
		{name: "final flush failure", accept: sharedSchemaV1MediaType + ", application/json;q=0.9", shared: true, flushFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
			expression := test.expression
			if expression == "" {
				expression = "vector(1)"
			}
			body, err := json.Marshal(map[string]any{
				"metric_merge": expression, "start_time": "1717027200", "end_time": "1717027200", "step": "60s", "instant": true,
			})
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "/query/ts", bytes.NewReader(body)).WithContext(metadata.InitHashID(context.Background()))
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			transport := &sharedFinalFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			if test.flushFailure {
				transport.finalError = io.ErrClosedPipe
			}
			engine := gin.New()
			engine.POST("/query/ts", HandlerQueryTs)
			labels := map[string]string{"api": "/query/ts", "space_uid": "", "source_type": "", "status": "success"}
			successBefore := sharedMetricCounter(t, "unify_query_api_request_total", labels)
			labels["status"] = "failed"
			failureBefore := sharedMetricCounter(t, "unify_query_api_request_total", labels)
			fallbackLabels := map[string]string{"codec": "legacy-json", "result": "fallback", "reason": test.fallback}
			fallbackBefore := sharedMetricCounter(t, "unify_query_shared_schema_response_total", fallbackLabels)
			engine.ServeHTTP(transport, request)

			queryExecutions := 0
			for _, span := range exporter.GetSpans() {
				if span.Name == "query-ts" {
					queryExecutions++
				}
				if test.flushFailure && (span.Name == "shared-schema-response" || span.Name == "handler-query-ts") {
					require.Equal(t, codes.Error, span.Status.Code)
				}
			}
			labels["status"] = "success"
			successDelta := sharedMetricCounter(t, "unify_query_api_request_total", labels) - successBefore
			labels["status"] = "failed"
			failureDelta := sharedMetricCounter(t, "unify_query_api_request_total", labels) - failureBefore
			if test.reject {
				require.Equal(t, http.StatusNotAcceptable, transport.Code)
				require.Zero(t, queryExecutions)
				require.Contains(t, transport.Body.String(), `"error":`)
				require.Equal(t, 0.0, successDelta)
				require.Equal(t, 1.0, failureDelta)
				return
			}
			require.Equal(t, http.StatusOK, transport.Code, transport.Body.String())
			require.Equal(t, 1, queryExecutions) // Fallback never repeats the query.
			if test.shared {
				require.Equal(t, sharedSchemaV1MediaType, transport.Header().Get("Content-Type"))
				require.Equal(t, "Accept", transport.Header().Get("Vary"))
				lines := bytes.Split(bytes.TrimSpace(transport.Body.Bytes()), []byte{'\n'})
				var footer sharedFrame
				require.NoError(t, json.Unmarshal(lines[len(lines)-1], &footer))
				require.NotNil(t, footer.End)
				require.Equal(t, int64(1), footer.End.Series)
				require.Equal(t, int64(1), footer.End.Points)
				if test.flushFailure {
					require.NotContains(t, transport.visible.String(), `"end":`)
					require.Equal(t, 0.0, successDelta)
					require.Equal(t, 1.0, failureDelta)
					return
				}
				require.Equal(t, transport.Body.String(), transport.visible.String())
			} else {
				require.Contains(t, transport.Header().Get("Content-Type"), "application/json")
				var legacy struct {
					Tables []struct {
						GroupValues []string                   `json:"group_values"`
						Stat        map[string]json.RawMessage `json:"stat"`
					} `json:"series"`
				}
				require.NoError(t, json.Unmarshal(transport.Body.Bytes(), &legacy))
				require.Len(t, legacy.Tables, 1)
				require.Contains(t, legacy.Tables[0].Stat, "count")
				require.NotContains(t, transport.Body.String(), `"seq":`)
				if test.fallback != "" {
					require.Equal(t, []string{largeGroup}, legacy.Tables[0].GroupValues)
					require.Equal(t, 1.0, sharedMetricCounter(t, "unify_query_shared_schema_response_total", fallbackLabels)-fallbackBefore)
				}
			}
			require.Equal(t, 1.0, successDelta)
			require.Equal(t, 0.0, failureDelta)
		})
	}
}
