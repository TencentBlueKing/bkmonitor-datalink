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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPTransportValidatesConnectionBudget(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{0, maxHTTPConnectionsPerHost + 1} {
		transport, err := NewHTTPTransport(HTTPTransportConfig{
			Addresses: []string{"http://127.0.0.1:9200"}, MaxConnectionsPerHost: budget,
		})
		if err == nil || transport != nil {
			t.Fatalf("NewHTTPTransport(budget=%d)=(%#v,%v)", budget, transport, err)
		}
	}
}

func TestHTTPTransportReusesConnectionsAndIsolatesClose(t *testing.T) {
	t.Parallel()
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	first := newTestHTTPTransport(t, []string{server.URL}, 2)
	second := newTestHTTPTransport(t, []string{server.URL}, 2)
	defer second.Close()
	for range 10 {
		performAndClose(t, first, context.Background())
	}
	performAndClose(t, second, context.Background())
	if got := connections.Load(); got != 2 {
		t.Fatalf("new connections=%d, want one per transport", got)
	}
	first.Close()
	performAndClose(t, second, context.Background())
	if got := connections.Load(); got != 2 {
		t.Fatalf("closing first transport closed second pool: connections=%d", got)
	}
}

func TestHTTPTransportBoundsConcurrentConnectionsAndHonorsContext(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(response, "ok")
	}))
	defer server.Close()
	transport := newTestHTTPTransport(t, []string{server.URL}, 2)
	defer transport.Close()

	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsCh <- performAndCloseResult(transport, context.Background())
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("two connections did not reach the server")
		}
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := performAndCloseResult(transport, waitCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting request error=%v", err)
	}
	select {
	case <-entered:
		t.Fatal("request exceeded MaxConnsPerHost")
	default:
	}
	releaseAll()
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPTransportRoundRobinsAddresses(t *testing.T) {
	t.Parallel()
	var firstCalls, secondCalls atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		_, _ = io.WriteString(response, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		_, _ = io.WriteString(response, "second")
	}))
	defer second.Close()
	transport := newTestHTTPTransport(t, []string{first.URL, second.URL}, 2)
	defer transport.Close()
	for range 4 {
		performAndClose(t, transport, context.Background())
	}
	if firstCalls.Load() != 2 || secondCalls.Load() != 2 {
		t.Fatalf("round-robin calls=%d/%d", firstCalls.Load(), secondCalls.Load())
	}
}

func newTestHTTPTransport(t *testing.T, addresses []string, budget int) *HTTPTransport {
	t.Helper()
	transport, err := NewHTTPTransport(HTTPTransportConfig{
		Addresses: addresses, Timeout: time.Second, MaxConnectionsPerHost: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func performAndClose(t *testing.T, transport *HTTPTransport, ctx context.Context) {
	t.Helper()
	if err := performAndCloseResult(transport, ctx); err != nil {
		t.Fatal(err)
	}
}

func performAndCloseResult(transport *HTTPTransport, ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/test", nil)
	if err != nil {
		return err
	}
	response, err := transport.Perform(request)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	return errors.Join(readErr, closeErr)
}
