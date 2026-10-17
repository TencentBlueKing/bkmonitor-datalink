// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package trace

import (
	"context"
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
