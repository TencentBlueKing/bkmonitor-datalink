// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	uqjson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type responseSegments [][]byte

func (s responseSegments) JSONSegments() ([][]byte, error) { return s, nil }

type segmentErrorWriter struct {
	*httptest.ResponseRecorder
	short bool
}

func (w *segmentErrorWriter) Write(data []byte) (int, error) {
	if w.short {
		return len(data) - 1, nil
	}
	return 0, errors.New("test writer failed")
}

func TestSegmentedResponseDirectAndProxyEnvelope(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	value := responseSegments{[]byte(`{"trace_id":"abc","data":`), []byte(`["中文","<&>"]}`)}
	for _, proxied := range []bool{false, true} {
		recorder.Reset()
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		TraceJSONEncoding(ctx)
		var data any = value
		want := `{"trace_id":"abc","data":["中文","<&>"]}`
		if proxied {
			data = &apiGwResponse{Result: true, Data: value, Message: SuccessMessage}
			want = `{"result":true,"data":` + want + `,"message":"success"}`
		}
		WriteJSON(context.Background(), ctx, http.StatusOK, data)
		require.NoError(t, ResponseWriteError(ctx))
		require.Equal(t, want, writer.Body.String())
		require.Equal(t, "application/json; charset=utf-8", writer.Header().Get("Content-Type"))
		require.Len(t, recorder.Ended(), 3)
		for _, span := range recorder.Ended() {
			attrs := map[string]any{}
			for _, attr := range span.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsInterface()
			}
			if span.Name() == "http-response-encode" {
				require.Equal(t, true, attrs["segmented"])
				require.Equal(t, int64(len(want)), attrs["encoded-body-bytes"])
			} else {
				require.Equal(t, int64(len(want)), attrs["writer-body-bytes"])
			}
		}
	}
}

func TestSegmentedResponseEnvelopeFallbacks(t *testing.T) {
	for _, data := range []any{
		map[string]string{"key": "value"},
		responseSegments(nil),
		responseSegments{},
		&apiGwResponse{Result: false, Data: responseSegments{[]byte(`{}`)}, Message: SuccessMessage},
		&apiGwResponse{Result: true, Data: responseSegments{[]byte(`{}`)}, Message: "custom"},
		&apiGwResponse{Result: true, Data: map[string]string{"key": "value"}, Message: SuccessMessage},
		&apiGwResponse{Result: true, Data: responseSegments(nil), Message: SuccessMessage},
	} {
		want, err := json.Marshal(data)
		require.NoError(t, err)
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		TraceJSONEncoding(ctx)
		_ = ctx.Error(errors.New("previous handler error"))
		WriteJSON(context.Background(), ctx, 200, data)
		require.NoError(t, ResponseWriteError(ctx))
		require.JSONEq(t, string(want), writer.Body.String())
	}
}

func TestSegmentedResponseWriteErrors(t *testing.T) {
	for _, short := range []bool{false, true} {
		writer := &segmentErrorWriter{ResponseRecorder: httptest.NewRecorder(), short: short}
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		TraceJSONEncoding(ctx)
		WriteJSON(context.Background(), ctx, http.StatusOK, responseSegments{[]byte("{}")})
		if short {
			require.ErrorIs(t, ResponseWriteError(ctx), io.ErrShortWrite)
		} else {
			require.ErrorContains(t, ResponseWriteError(ctx), "test writer failed")
		}
	}
}

func TestPreencodedTopologyEnvelopePreservesExactJSON(t *testing.T) {
	item := struct {
		ID    uint64   `json:"id"`
		Text  string   `json:"text"`
		Empty []string `json:"empty"`
		Null  []string `json:"null"`
	}{^uint64(0), "<&>\u2028\x00\"", []string{}, nil}
	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	typed := struct {
		Data any `json:"data"`
	}{[]any{item}}
	raw := struct {
		Data any `json:"data"`
	}{[]json.RawMessage{encoded}}
	want, err := json.Marshal(typed)
	require.NoError(t, err)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	TraceJSONEncoding(ctx)
	WriteJSON(context.Background(), ctx, 200, raw)
	require.NoError(t, ResponseWriteError(ctx))
	require.Equal(t, string(want), writer.Body.String())
	require.Equal(t, "application/json; charset=utf-8", writer.Header().Get("Content-Type"))
}

func BenchmarkTopologyEnvelopeEncoding(b *testing.B) {
	type node struct {
		ID         uint64            `json:"id"`
		Dimensions map[string]string `json:"dimensions"`
	}
	nodes := make([]node, 10000)
	for i := range nodes {
		nodes[i] = node{uint64(i), map[string]string{"id": "fixed", "name": "representative-label"}}
	}
	for _, reuse := range []bool{false, true} {
		name := "typed"
		if reuse {
			name = "raw"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			run := func() {
				item, err := uqjson.Marshal(nodes)
				require.NoError(b, err)
				var value any = nodes
				if reuse {
					value = json.RawMessage(item)
				}
				marshal := json.Marshal
				if reuse {
					marshal = uqjson.Marshal
				}
				encoded, err := marshal(struct {
					Data []any `json:"data"`
				}{[]any{value}})
				require.NoError(b, err)
				b.ReportMetric(float64(len(encoded)), "wire-bytes")
			}
			if os.Getenv("TG_BENCH_WARM") == "true" {
				run()
				b.ResetTimer()
			}
			for i := 0; i < b.N; i++ {
				run()
			}
		})
	}
}
