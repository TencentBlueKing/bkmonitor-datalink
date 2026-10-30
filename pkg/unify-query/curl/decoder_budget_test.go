// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestHttpCurlDecoderBudgetBoundaries(t *testing.T) {
	body := []byte(`{"value":"0123456789"}`)
	length := int64(len(body))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	for _, custom := range []bool{false, true} {
		for _, tc := range []struct {
			name                           string
			contextLimit, option, expected int64
			rejected                       bool
		}{
			{name: "unlimited"},
			{name: "context only", contextLimit: length, expected: length},
			{name: "negative option", contextLimit: length, option: -1, expected: length},
			{name: "smaller option", contextLimit: length, option: length - 1, expected: length - 1, rejected: true},
			{name: "equal option", contextLimit: length, option: length, expected: length},
			{name: "larger option", contextLimit: length, option: length * 2, expected: length},
			{name: "option only", option: length - 1, expected: length - 1, rejected: true},
			{name: "smaller context", contextLimit: length - 1, option: length, expected: length - 1, rejected: true},
			{name: "negative context", contextLimit: -1},
		} {
			t.Run(fmt.Sprintf("custom_%t/%s", custom, tc.name), func(t *testing.T) {
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				previous := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) })
				client := &HttpCurl{}
				phase := "http-curl-read-body"
				if custom {
					phase = "http-curl-decode-body"
					client.WithDecoder(func(_ context.Context, reader io.Reader, result any) (int, error) {
						data, err := io.ReadAll(reader)
						if err != nil {
							return len(data), err
						}
						return len(data), json.Unmarshal(data, result)
					})
				}
				ctx := metadata.WithBackendResponseLimit(context.Background(), tc.contextLimit)
				var result map[string]string
				size, err := client.Request(ctx, Get, Options{UrlPath: server.URL, MaxResponseBytes: tc.option}, &result)
				if tc.rejected {
					var limitError *ResponseBodyLimitError
					require.ErrorAs(t, err, &limitError)
					require.Equal(t, tc.expected, limitError.Limit)
					require.EqualValues(t, tc.expected+1, size)
				} else {
					require.NoError(t, err)
					require.EqualValues(t, length, size)
					require.Equal(t, "0123456789", result["value"])
				}
				require.Equal(t, tc.rejected, metadata.BackendResponseLimitExceeded(ctx))
				found := false
				for _, ended := range recorder.Ended() {
					if ended.Name() == phase {
						found = true
						require.Equal(t, tc.rejected, ended.Status().Code == codes.Error)
					}
				}
				require.True(t, found)
			})
		}
	}
}
