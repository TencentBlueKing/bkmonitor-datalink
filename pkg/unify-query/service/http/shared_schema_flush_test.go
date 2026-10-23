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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// net/http's production response reports flush errors; the plain Recorder
// only exposes void Flush. Use the same error-aware contract in Gin tests.
type sharedErrorAwareRecorder struct{ *httptest.ResponseRecorder }

func (w *sharedErrorAwareRecorder) FlushError() error {
	w.ResponseRecorder.Flush()
	return nil
}

type sharedFinalFlushWriter struct {
	*httptest.ResponseRecorder
	pending, visible bytes.Buffer
	finalError       error
	errorFlushes     int
	voidFlushes      int
}

func (w *sharedFinalFlushWriter) Write(body []byte) (int, error) {
	_, _ = w.pending.Write(body)
	return w.ResponseRecorder.Write(body)
}

func (w *sharedFinalFlushWriter) FlushError() error {
	w.errorFlushes++
	if bytes.Contains(w.pending.Bytes(), []byte(`"end":`)) && w.finalError != nil {
		return w.finalError
	}
	_, _ = w.visible.Write(w.pending.Bytes())
	w.pending.Reset()
	w.ResponseRecorder.Flush()
	return nil
}

func (w *sharedFinalFlushWriter) Flush() {
	w.voidFlushes++
	_ = w.FlushError() // Reproduces errors lost by Gin's old void-flush path.
}

type sharedInnerErrorFlusher struct {
	http.ResponseWriter
	flushes int
}

func (w *sharedInnerErrorFlusher) FlushError() error {
	w.flushes++
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (*sharedInnerErrorFlusher) Flush()                        { panic("must retain the error-aware wrapper flush") }
func (w *sharedInnerErrorFlusher) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type sharedTransparentWriter struct{ http.ResponseWriter }

func (w *sharedTransparentWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type sharedOuterErrorFlusher struct {
	gin.ResponseWriter
	flush   func() error
	flushes int
}

func (w *sharedOuterErrorFlusher) FlushError() error {
	w.flushes++
	return w.flush()
}

func (*sharedOuterErrorFlusher) Unwrap() http.ResponseWriter {
	panic("must not bypass the outer wrapper's own FlushError")
}

func TestSharedSchemaGinFinalFlushError(t *testing.T) {
	for _, wrapper := range []string{"gin", "inner_error", "inner_transparent", "outer_error"} {
		for _, fail := range []bool{false, true} {
			t.Run(wrapper+map[bool]string{false: "_success", true: "_failure"}[fail], func(t *testing.T) {
				exporter := tracetest.NewInMemoryExporter()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
				previous := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })

				transport := &sharedFinalFlushWriter{ResponseRecorder: httptest.NewRecorder()}
				if fail {
					transport.finalError = errors.New("final frame flush failed")
				}
				var input http.ResponseWriter = transport
				inner := &sharedInnerErrorFlusher{ResponseWriter: transport}
				if wrapper == "inner_error" {
					input = inner
				} else if wrapper == "inner_transparent" {
					input = &sharedTransparentWriter{ResponseWriter: transport}
				}
				engine := gin.New()
				var resultErr error
				var size, status int
				var written bool
				var outer *sharedOuterErrorFlusher
				engine.Use(func(c *gin.Context) {
					c.Header("X-Flush-Test", "preserved")
					if wrapper == "outer_error" {
						outer = &sharedOuterErrorFlusher{ResponseWriter: c.Writer, flush: http.NewResponseController(transport).Flush}
						c.Writer = outer
					}
					c.Next()
					size, status, written = c.Writer.Size(), c.Writer.Status(), c.Writer.Written()
				})
				engine.POST("/query/ts", func(c *gin.Context) {
					resultErr = (&response{c: c}).sharedSchemaSuccess(c.Request.Context(), sharedTestData(2))
				})
				apiLabels := map[string]string{"api": "/query/ts", "space_uid": "", "source_type": "", "status": "success"}
				successBefore := sharedMetricCounter(t, "unify_query_api_request_total", apiLabels)
				apiLabels["status"] = "failed"
				failureBefore := sharedMetricCounter(t, "unify_query_api_request_total", apiLabels)
				codecLabels := map[string]string{"codec": "shared-schema-v1", "result": "failure", "reason": "write"}
				codecFailureBefore := sharedMetricCounter(t, "unify_query_shared_schema_response_total", codecLabels)

				engine.ServeHTTP(input, httptest.NewRequest(http.MethodPost, "/query/ts", nil))
				require.Equal(t, http.StatusOK, status)
				require.Equal(t, http.StatusOK, transport.Code)
				require.True(t, written)
				require.Equal(t, transport.Body.Len(), size)
				require.Equal(t, "preserved", transport.Header().Get("X-Flush-Test"))
				require.Equal(t, sharedSchemaV1MediaType, transport.Header().Get("Content-Type"))
				require.Contains(t, transport.Body.String(), `"end":`)
				require.NotContains(t, transport.Body.String(), `"error":`)
				require.Equal(t, 4, transport.errorFlushes)
				require.Zero(t, transport.voidFlushes)
				if wrapper == "inner_error" {
					require.Equal(t, 4, inner.flushes)
				} else if wrapper == "outer_error" {
					require.Equal(t, 4, outer.flushes)
				}
				apiLabels["status"] = "success"
				successDelta := sharedMetricCounter(t, "unify_query_api_request_total", apiLabels) - successBefore
				apiLabels["status"] = "failed"
				failureDelta := sharedMetricCounter(t, "unify_query_api_request_total", apiLabels) - failureBefore
				spans := exporter.GetSpans()
				require.Len(t, spans, 1)
				require.Equal(t, "shared-schema-response", spans[0].Name)
				attributes := make(map[string]string)
				for _, attribute := range spans[0].Attributes {
					attributes[string(attribute.Key)] = attribute.Value.AsString()
				}
				if fail {
					require.ErrorIs(t, resultErr, transport.finalError)
					require.NotContains(t, transport.visible.String(), `"end":`)
					require.Equal(t, 0.0, successDelta)
					require.Equal(t, 1.0, failureDelta)
					require.Equal(t, 1.0, sharedMetricCounter(t, "unify_query_shared_schema_response_total", codecLabels)-codecFailureBefore)
					require.Equal(t, codes.Error, spans[0].Status.Code)
					require.NotEmpty(t, spans[0].Events)
					require.Equal(t, "failure", attributes["response-result"])
					require.Equal(t, "write", attributes["response-reason"])
				} else {
					require.NoError(t, resultErr)
					require.Equal(t, transport.Body.String(), transport.visible.String())
					require.Equal(t, 1.0, successDelta)
					require.Equal(t, 0.0, failureDelta)
					require.NotEqual(t, codes.Error, spans[0].Status.Code)
					require.Equal(t, "success", attributes["response-result"])
				}
			})
		}
	}
}

type sharedVoidOnlyInnerWriter struct {
	http.ResponseWriter
	flushes int
}

func (w *sharedVoidOnlyInnerWriter) Flush()                      { w.flushes++ }
func (w *sharedVoidOnlyInnerWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type sharedVoidOnlyOuterWriter struct{ gin.ResponseWriter }

func (w *sharedVoidOnlyOuterWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestSharedSchemaGinOpaqueFlushFallback(t *testing.T) {
	for _, wrapper := range []string{"inner_void", "outer_void"} {
		t.Run(wrapper, func(t *testing.T) {
			transport := &sharedFinalFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			inner := &sharedVoidOnlyInnerWriter{ResponseWriter: transport}
			var input http.ResponseWriter = transport
			if wrapper == "inner_void" {
				input = inner
			}
			engine := gin.New()
			data := sharedTestData(2)
			var resultErr error
			engine.POST("/query/ts", func(c *gin.Context) {
				if wrapper == "outer_void" {
					c.Writer = &sharedVoidOnlyOuterWriter{ResponseWriter: c.Writer}
				}
				resultErr = (&response{c: c}).sharedSchemaSuccess(c.Request.Context(), data)
			})
			labels := map[string]string{"codec": "legacy-json", "result": "fallback", "reason": "writer_unsupported"}
			before := sharedMetricCounter(t, "unify_query_shared_schema_response_total", labels)
			engine.ServeHTTP(input, httptest.NewRequest(http.MethodPost, "/query/ts", nil))
			require.NoError(t, resultErr)
			require.Equal(t, http.StatusOK, transport.Code)
			require.Contains(t, transport.Header().Get("Content-Type"), "application/json")
			legacy, err := json.Marshal(data)
			require.NoError(t, err)
			require.JSONEq(t, string(legacy), transport.Body.String())
			require.Zero(t, inner.flushes)
			require.Zero(t, transport.errorFlushes)
			require.Equal(t, 1.0, sharedMetricCounter(t, "unify_query_shared_schema_response_total", labels)-before)
		})
	}
}
