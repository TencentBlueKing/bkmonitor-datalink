// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTopologyBodyReadBoundaries(t *testing.T) {
	for _, limit := range []int{-1, 0, 6, 7, 8, math.MaxInt} {
		data, err := readTopologyRequestBody(context.Background(), bytes.NewBufferString("payload"), httptest.NewRecorder(), limit)
		require.Equal(t, "payload", string(data), "limit=%d", limit)
		if limit == 6 {
			var exceeded *v1beta3.ResultLimitError
			require.ErrorAs(t, err, &exceeded)
			require.Equal(t, "max_topology_request_bytes", exceeded.Reason)
			require.Equal(t, 7, exceeded.Count)
			require.Equal(t, limit, exceeded.Limit)
		} else {
			require.NoError(t, err, "limit=%d", limit)
		}
	}
	reader := bytes.NewBufferString("payload continues")
	data, err := readTopologyRequestBody(context.Background(), reader, httptest.NewRecorder(), 6)
	require.Error(t, err)
	require.Equal(t, "payload", string(data))
	require.Equal(t, " continues", reader.String())

	ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	release()
	reader = bytes.NewBufferString("unread")
	data, err = readTopologyRequestBody(ctx, reader, httptest.NewRecorder(), 6)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, data)
	require.Equal(t, "unread", reader.String())
}

func TestTopologyBodyReadUnsupportedCancellationControl(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("a"))
		cancel()
		_ = writer.CloseWithError(io.ErrUnexpectedEOF)
		written <- err
	}()
	data, err := readTopologyRequestBody(ctx, reader, httptest.NewRecorder(), 6)
	require.NoError(t, <-written)
	require.Equal(t, "a", string(data))
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, recorder.Ended(), 1)
	attrs := make(map[string]any)
	for _, attr := range recorder.Ended()[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	require.Equal(t, false, attrs["request-body-cancel-control-supported"])
	require.Contains(t, attrs["request-body-cancel-control-error"], "not supported")
}

func TestTopologyBodyRead(t *testing.T) {
	t.Run("normal_read_keeps_connection_reusable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			data, err := readTopologyRequestBody(ctx, r.Body, w, 1024)
			cancel() // the stopped callback must not poison this keepalive socket
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			_, _ = w.Write(data)
		}))
		defer server.Close()
		client := server.Client()
		defer client.CloseIdleConnections()
		for i := 0; i < 2; i++ {
			reused := false
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
			req, err := http.NewRequestWithContext(ctx, "POST", server.URL, bytes.NewBufferString("payload"))
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			data, err := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			require.NoError(t, err)
			require.Equal(t, "payload", string(data))
			if i == 1 {
				require.True(t, reused)
			}
		}
	})
	t.Run("slow_body_stops_at_context_deadline", func(t *testing.T) {
		finished := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
			defer cancel()
			_, err := readTopologyRequestBody(ctx, r.Body, w, 1024)
			finished <- err
			if err != nil {
				http.Error(w, err.Error(), 400)
			}
		}))
		defer server.Close()
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		req, err := http.NewRequest("POST", server.URL, reader)
		require.NoError(t, err)
		clientDone := make(chan struct{})
		go func() {
			defer close(clientDone)
			resp, _ := server.Client().Do(req)
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
		_, err = writer.Write([]byte("a"))
		require.NoError(t, err)
		select {
		case err := <-finished:
			require.ErrorIs(t, err, context.DeadlineExceeded)
		case <-time.After(time.Second):
			t.Fatal("slow body read ignored the deadline")
		}
		_ = writer.Close()
		select {
		case <-clientDone:
		case <-time.After(time.Second):
			t.Fatal("client did not finish")
		}
	})
}
