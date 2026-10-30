// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package trace

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRedactHeaders(t *testing.T) {
	headers := http.Header{
		"Cookie":                {"session=private"},
		"Authorization":         {"Bearer private"},
		"X-Bkapi-Authorization": {"private"},
		"Content-Type":          {"application/json"},
		"Traceparent":           {"00-trace-span-01"},
	}
	got := redactHeaders(headers).(http.Header)
	require.Equal(t, []string{"[REDACTED]"}, got["Cookie"])
	require.Equal(t, []string{"[REDACTED]"}, got["Authorization"])
	require.Equal(t, []string{"[REDACTED]"}, got["X-Bkapi-Authorization"])
	require.Equal(t, headers["Content-Type"], got["Content-Type"])
	require.Equal(t, headers["Traceparent"], got["Traceparent"])
	require.Equal(t, []string{"session=private"}, headers["Cookie"], "caller headers must remain unchanged")

	backend := redactHeaders(map[string]string{"X-Bkapi-Authorization": "private", "Content-Type": "application/json"}).(map[string]string)
	require.Equal(t, "[REDACTED]", backend["X-Bkapi-Authorization"])
	require.Equal(t, "application/json", backend["Content-Type"])
	require.True(t, isHeaderAttribute("handler-headers"))
	require.False(t, isHeaderAttribute("response-headers-duration"))
}

func TestSpanSetRedactsRequestHeaders(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { require.NoError(t, provider.Shutdown(context.Background())) }()
	_, rawSpan := provider.Tracer("test").Start(context.Background(), "request")
	span := &Span{span: rawSpan}
	span.Set("handler-headers", http.Header{
		"Cookie":        {"session=private"},
		"Authorization": {"Bearer private"},
		"Content-Type":  {"application/json"},
	})
	span.Set("query-headers", map[string]string{"X-Bkapi-Authorization": "backend-private"})
	rawSpan.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	for _, attribute := range ended[0].Attributes() {
		value := attribute.Value.AsString()
		require.False(t, strings.Contains(value, "private"), attribute.Key)
		require.Contains(t, value, "[REDACTED]")
	}
}

func TestSpanHeaderAttributesAcrossRepresentations(t *testing.T) {
	for _, key := range []string{"request-header", "handler-headers", "headers", "query-headers", "http-request"} {
		for _, stringMap := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/string_map=%t", key, stringMap), func(t *testing.T) {
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
				_, rawSpan := provider.Tracer("headers").Start(context.Background(), "request")
				span := &Span{span: rawSpan}
				var value any = http.Header{"Authorization": {"private-token"}, "Content-Type": {"application/json"}}
				if stringMap {
					value = map[string]string{"Authorization": "private-token", "Content-Type": "application/json"}
				}
				span.Set(key, value)
				span.Set("response-headers-duration", int64(17))
				rawSpan.End()
				require.Len(t, recorder.Ended(), 1)
				attributes := recorder.Ended()[0].Attributes()
				require.Len(t, attributes, 2)
				require.NotContains(t, attributes[0].Value.AsString(), "private-token")
				require.Contains(t, attributes[0].Value.AsString(), "[REDACTED]")
				require.Contains(t, attributes[0].Value.AsString(), "application/json")
				require.Equal(t, int64(17), attributes[1].Value.AsInt64())
			})
		}
	}
}

func TestDiagnosticHeadersPreserveValuesAndOwnTheirCopies(t *testing.T) {
	for _, name := range []string{"accept", "accept-encoding", "content-length", "content-type", "traceparent", "user-agent", "x-bk-scope-space-uid", "bk-query-source"} {
		for _, spelling := range []string{name, http.CanonicalHeaderKey(name), strings.ToUpper(name)} {
			t.Run(spelling, func(t *testing.T) {
				headers := http.Header{spelling: {"first", "second"}, "Cookie": {"private-cookie"}, "X-Unknown": {"private-extra"}}
				redacted := RedactHeaders(headers)
				require.Equal(t, headers[spelling], redacted[spelling])
				require.Equal(t, []string{"[REDACTED]"}, redacted["Cookie"])
				require.Equal(t, []string{"[REDACTED]"}, redacted["X-Unknown"])
				redacted[spelling][0] = "changed"
				require.Equal(t, "first", headers[spelling][0])
				flat := map[string]string{spelling: "first", "Cookie": "private-cookie", "X-Unknown": "private-extra"}
				flatRedacted := redactHeaders(flat).(map[string]string)
				require.Equal(t, "first", flatRedacted[spelling])
				require.Equal(t, "[REDACTED]", flatRedacted["Cookie"])
				require.Equal(t, "[REDACTED]", flatRedacted["X-Unknown"])
				flatRedacted[spelling] = "changed"
				require.Equal(t, "first", flat[spelling])
			})
		}
	}
	require.Empty(t, RedactHeaders(nil))
	require.Empty(t, RedactHeaders(http.Header{}))
	require.Empty(t, redactHeaders(map[string]string{}))
	require.Equal(t, "diagnostic", redactHeaders("diagnostic"))
	require.Nil(t, redactHeaders(nil))
}
