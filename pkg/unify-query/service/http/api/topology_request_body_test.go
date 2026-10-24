// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
