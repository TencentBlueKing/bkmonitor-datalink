// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
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
)

func TestTimeGraphHandlersEndSpans(t *testing.T) {
	log.InitTestLogger()
	for _, handler := range []struct {
		name string
		fn   gin.HandlerFunc
	}{
		{"handler-api-relation-path-resources", HandlerAPIRelationPathResources},
		{"handler-api-relation-path-resources-range", HandlerAPIRelationPathResourcesRange},
	} {
		t.Run(handler.name, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				body       string
				httpStatus int
				rootStatus codes.Code
				itemCodes  []int
			}{
				{"malformed-json", `{`, http.StatusBadRequest, codes.Error, nil},
				{"empty-batch", `{"query_list":[]}`, http.StatusOK, codes.Unset, []int{}},
				// No space is installed in metadata: validation fails before any backend access.
				{"item-validation-error", `{"query_list":[{}]}`, http.StatusOK, codes.Unset, []int{http.StatusBadRequest}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					recorder := tracetest.NewSpanRecorder()
					provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
					previous := otel.GetTracerProvider()
					otel.SetTracerProvider(provider)
					t.Cleanup(func() {
						otel.SetTracerProvider(previous)
						require.NoError(t, provider.Shutdown(context.Background()))
					})

					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tc.body))
					// Deliberately omit Gin Recovery: a panic after writing HTTP 200 must fail the test.
					require.NotPanics(t, func() { handler.fn(c) })
					require.Equal(t, tc.httpStatus, response.Code)

					ended := recorder.Ended()
					require.Len(t, ended, len(recorder.Started()), "every started span must end")
					var root sdktrace.ReadOnlySpan
					var itemCount int
					for _, span := range ended {
						switch span.Name() {
						case handler.name:
							require.Nil(t, root, "handler span must end exactly once")
							root = span
						case handler.name + "-item":
							itemCount++
							require.Equal(t, codes.Error, span.Status().Code)
							require.NotEmpty(t, span.Events(), "item validation error must be recorded")
						}
					}
					require.NotNil(t, root)
					require.Equal(t, tc.rootStatus, root.Status().Code)
					require.Equal(t, len(tc.itemCodes), itemCount)
					if tc.httpStatus == http.StatusBadRequest {
						require.NotEmpty(t, root.Events(), "decode error must be recorded")
						var body ErrResponse
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
						require.NotEmpty(t, body.Err)
						return
					}

					var body struct {
						TraceID string `json:"trace_id"`
						Data    []struct {
							Code    int               `json:"code"`
							Results []json.RawMessage `json:"results"`
						} `json:"data"`
					}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					require.Equal(t, root.SpanContext().TraceID().String(), body.TraceID)
					require.NotNil(t, body.Data)
					require.Len(t, body.Data, len(tc.itemCodes))
					for i, item := range body.Data {
						require.Equal(t, tc.itemCodes[i], item.Code)
						require.NotNil(t, item.Results)
						require.Empty(t, item.Results)
					}
				})
			}
		})
	}
}
