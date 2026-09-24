// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package curl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
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
