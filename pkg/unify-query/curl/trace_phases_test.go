// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestCurlFullResponsePhases(t *testing.T) {
	for _, test := range []struct {
		name, body             string
		rejected, decodeFailed bool
	}{
		{name: "decoded", body: `{"value":1}`}, {name: "decode failed", body: `{`, decodeFailed: true}, {name: "limit", body: strings.Repeat("x", 2048), rejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			old := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			var result any
			_, err := (&HttpCurl{}).Request(metadata.WithBackendResponseLimit(context.Background(), 1024), Get, Options{UrlPath: server.URL}, &result)
			if test.rejected || test.decodeFailed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, recorder.Ended(), len(recorder.Started()))
			spans := map[string]sdktrace.ReadOnlySpan{}
			for _, span := range recorder.Ended() {
				spans[span.Name()] = span
			}
			requestSpan := spans["http-curl"]
			require.NotNil(t, requestSpan)
			require.Equal(t, test.rejected || test.decodeFailed, requestSpan.Status().Code == codes.Error)
			attributes := make(map[string]attribute.Value)
			for _, attr := range requestSpan.Attributes() {
				attributes[string(attr.Key)] = attr.Value
			}
			for _, key := range []string{"response-headers-duration", "response-body-read-duration", "response-body-bytes", "response-body-byte-limit"} {
				require.Contains(t, attributes, key)
			}
			if test.rejected {
				require.NotContains(t, attributes, "json-decode-duration")
			} else {
				require.Contains(t, attributes, "json-decode-duration")
			}
		})
	}
}

func TestCurlRequestAttemptAttribute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	for attempt := 1; attempt <= 2; attempt++ {
		var result map[string]bool
		_, err := (&HttpCurl{}).Request(context.Background(), Get, Options{UrlPath: server.URL, Attempt: attempt}, &result)
		require.NoError(t, err)
		require.True(t, result["ok"])
	}

	var curlSpans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "http-curl" {
			curlSpans = append(curlSpans, span)
		}
	}
	sort.Slice(curlSpans, func(i, j int) bool { return curlSpans[i].StartTime().Before(curlSpans[j].StartTime()) })
	require.Len(t, curlSpans, 2)

	attributes := func(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
		values := make(map[string]attribute.Value)
		for _, attr := range span.Attributes() {
			values[string(attr.Key)] = attr.Value
		}
		return values
	}
	first, second := attributes(curlSpans[0]), attributes(curlSpans[1])
	require.Equal(t, int64(1), first["outbound.request.attempt"].AsInt64())
	require.Equal(t, int64(2), second["outbound.request.attempt"].AsInt64())
}
