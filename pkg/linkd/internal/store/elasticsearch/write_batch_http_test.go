// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 响应头先到、Body 等调用方放行，避免小响应已被 HTTP 缓冲掩盖提前取消。
func TestWriteBatchSaturatedDirectResponseKeepsContextAndConnection(t *testing.T) {
	var connections atomic.Int64
	releases := make(chan chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		release := make(chan struct{})
		releases <- release
		select {
		case <-release:
			_, _ = io.WriteString(w, `{"found":true}`)
		case <-r.Context().Done():
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	transport := newTestHTTPTransport(t, []string{server.URL}, 2)
	defer transport.Close()
	b := newTestWriteBatch(t, transport, func(c *WriteBatchConfig) { c.MaxCalls = 1 })
	// 确定性模拟唯一合批名额被占用，让每个请求走真正满载直连路径。
	b.slots <- struct{}{}
	defer func() { <-b.slots }()
	for range 20 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/alerts/_doc/a?realtime=true", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := b.Perform(req)
		if err != nil {
			t.Fatal(err)
		}
		close(<-releases)
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || string(body) != `{"found":true}` {
			t.Fatalf("response body=%q read=%v close=%v", body, err, closeErr)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("connections=%d, want 1 for 20 direct requests", got)
	}
}

func TestWriteBatchCloseCancelsDirectResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	transport := newTestHTTPTransport(t, []string{server.URL}, 1)
	defer transport.Close()
	b := newTestWriteBatch(t, transport, func(c *WriteBatchConfig) { c.MaxCalls = 1 })
	b.slots <- struct{}{}
	defer func() { <-b.slots }()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/alerts/_doc/a?realtime=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := b.Perform(req)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { b.Close(); close(done) }()
	_, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read after close=%v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not release direct response")
	}
}
