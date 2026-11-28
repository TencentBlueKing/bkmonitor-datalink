// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDispatchHTTPWriteBudget(t *testing.T) {
	for _, budget := range []time.Duration{0, 10 * time.Second, 35 * time.Second, 90 * time.Second} {
		server := newDispatchHTTPServer("", http.NotFoundHandler(), budget)
		if server.WriteTimeout <= budget || server.WriteTimeout < 25*time.Second {
			t.Fatalf("write timeout %s cuts off receive budget %s", server.WriteTimeout, budget)
		}
		if server.ReadTimeout != 10*time.Second || server.ReadHeaderTimeout != 3*time.Second {
			t.Fatal("plugin execution budget changed input read deadlines")
		}
	}
}

func TestDispatchHTTPCancelsActiveRequestOnTaskStop(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, canceled := make(chan struct{}), make(chan error, 1)
	server := newDispatchHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		canceled <- r.Context().Err()
		w.WriteHeader(http.StatusServiceUnavailable)
	}), 35*time.Second)
	t.Cleanup(func() { _ = server.Close() })
	done := make(chan error, 1)
	go func() { done <- serveDispatchHTTP(ctx, server, listener) }()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			requestDone <- err
			return
		}
		response, err := client.Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach server")
	}
	// 模拟 taskgroup 因调度中心失去授权而取消；不是客户端先断开。
	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active request outlived service task")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not drain canceled request")
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not finish")
	}
}

func TestDispatchHTTPReturnsListenerFailure(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	server := newDispatchHTTPServer("", http.NotFoundHandler(), 0)
	if err := serveDispatchHTTP(t.Context(), server, listener); err == nil {
		t.Fatal("failed listener reported successful stop")
	}
}
