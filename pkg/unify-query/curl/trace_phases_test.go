// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
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
			for _, name := range []string{"http-curl", "http-curl-response-headers", "http-curl-read-body"} {
				require.NotNil(t, spans[name])
			}
			if test.rejected {
				require.Equal(t, codes.Error, spans["http-curl-read-body"].Status().Code)
				require.Nil(t, spans["http-curl-json-decode"])
			} else {
				require.NotNil(t, spans["http-curl-json-decode"])
				if test.decodeFailed {
					require.Equal(t, codes.Error, spans["http-curl-json-decode"].Status().Code)
				}
			}
		})
	}
}

func TestCurlConnectionTraceAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	for range 2 {
		var result map[string]bool
		_, err := (&HttpCurl{}).Request(context.Background(), Get, Options{UrlPath: server.URL}, &result)
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
	require.Contains(t, first, "outbound.connection.fingerprint")
	firstFingerprint := first["outbound.connection.fingerprint"].AsString()
	require.Regexp(t, `^[0-9a-f]{32}$`, firstFingerprint)
	require.Equal(t, firstFingerprint, second["outbound.connection.fingerprint"].AsString())
	require.False(t, first["outbound.connection.reused"].AsBool())
	require.True(t, second["outbound.connection.reused"].AsBool())
	require.True(t, first["outbound.request.write_complete"].AsBool())
	require.True(t, second["outbound.request.write_complete"].AsBool())
}

func TestCurlTraceRecordsRequestWriteError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })

	ctx, span := trace.NewSpan(context.Background(), "http-curl")
	clientTrace := httptrace.ContextClientTrace(withHTTPClientTrace(ctx, span))
	require.NotNil(t, clientTrace)
	clientTrace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write: broken pipe")})
	var err error
	span.End(&err)

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attributes := make(map[string]attribute.Value)
	for _, attr := range ended[0].Attributes() {
		attributes[string(attr.Key)] = attr.Value
	}
	require.False(t, attributes["outbound.request.write_complete"].AsBool())
	require.Equal(t, "write: broken pipe", attributes["outbound.request.write_error"].AsString())
}
