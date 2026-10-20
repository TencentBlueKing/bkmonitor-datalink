// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/middleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

// cancelSettleBound is how long a cancelled request may take to stop every
// backend call and return.
const cancelSettleBound = 2 * time.Second

// blockingBackend answers the storage calls of one test. A call whose query
// matches block waits until its own outbound request context is done; every
// other call is answered at once with an empty result.
type blockingBackend struct {
	block func(query string) bool

	inFlight  atomic.Int64
	started   chan string
	mu        sync.Mutex
	answered  []string
	cancelled []string
	released  []string
	release   chan struct{}
}

func newBlockingBackend(t *testing.T, block func(query string) bool) *blockingBackend {
	t.Helper()
	backend := &blockingBackend{block: block, started: make(chan string, 16), release: make(chan struct{})}
	matcher := httpmock.NewMatcher("request-cancel-probe", func(*http.Request) bool { return true })
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, backend.respond)
	t.Cleanup(func() {
		// Frees a call whose context never ended, so a failing test does not
		// leave it behind for the next one.
		close(backend.release)
		httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, httpmock.NewMatcher("request-cancel-probe", nil), nil)
	})
	return backend
}

func (b *blockingBackend) respond(req *http.Request) (*http.Response, error) {
	var request mock.VmRequest
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		return nil, err
	}
	var params mock.VmParams
	if err := json.Unmarshal([]byte(request.Sql), &params); err != nil {
		return nil, fmt.Errorf("not a vm call: %s", request.Sql)
	}
	query := fmt.Sprintf("%v", params.ApiParams["query"])
	if !b.block(query) {
		b.mu.Lock()
		b.answered = append(b.answered, query)
		b.mu.Unlock()
		return httpmock.NewJsonResponse(http.StatusOK, victoriaMetrics.Data{ResultType: victoriaMetrics.MatrixType})
	}
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	b.started <- query
	select {
	case <-req.Context().Done():
		b.mu.Lock()
		b.cancelled = append(b.cancelled, query)
		b.mu.Unlock()
		return nil, req.Context().Err()
	case <-b.release:
		b.mu.Lock()
		b.released = append(b.released, query)
		b.mu.Unlock()
		return nil, fmt.Errorf("released by test cleanup")
	}
}

// cancelServer is the public query routes behind the production middleware
// chain on a real HTTP server, counting handlers that have not returned.
type cancelServer struct {
	*httptest.Server
	active atomic.Int64
}

func newCancelServer(t *testing.T) *cancelServer {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(metadata.InitHashID(context.Background()))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	public := engine.Group("/")
	public.Use(gin.Recovery(), middleware.MetaData(nil), middleware.JwtAuthMiddleware(false, "", nil))
	// The two handlers at their default paths, not registerDefaultHandlers:
	// the paths come from configuration other tests in this package reset.
	register := endpoint.NewRegisterHandler(context.Background(), public)
	register.RegisterWithOutHandlerMap(http.MethodPost, "/query/ts", HandlerQueryTs)
	register.RegisterWithOutHandlerMap(http.MethodPost, "/query/ts/promql", HandlerQueryPromQL)

	server := &cancelServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.active.Add(1)
		defer server.active.Add(-1)
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// settled waits until no backend call is in flight and no handler is running.
func settled(server *cancelServer, backend *blockingBackend) bool {
	deadline := time.Now().Add(cancelSettleBound)
	for time.Now().Before(deadline) {
		if backend.inFlight.Load() == 0 && server.active.Load() == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func waitBackendCall(t *testing.T, backend *blockingBackend) string {
	t.Helper()
	select {
	case query := <-backend.started:
		return query
	case <-time.After(10 * time.Second):
		t.Fatal("the request never reached the blocking backend call")
		return ""
	}
}

type cancelRoute struct {
	name  string
	path  string
	body  string
	block func(query string) bool
	// optional is a route whose required output answers before the
	// optional one blocks.
	optional bool
}

func cancelRoutes() []cancelRoute {
	blockAll := func(string) bool { return true }
	// The required (legacy) output is the count, answered at once; the
	// optional output is the sum, which blocks.
	blockOptional := func(query string) bool { return strings.HasPrefix(query, "sum") }
	return []cancelRoute{
		{
			name: "named outputs promql",
			path: "/query/ts/promql",
			body: `{"promql":"count(container_cpu_usage_seconds_total) by (bcs_cluster_id)",` +
				`"start":"1741056443","end":"1741060043","step":"10m",` +
				`"response_contract":"named_outputs/v1","legacy_output_ref":"C",` +
				`"output_list":[{"reference_name":"C","expression":"count(container_cpu_usage_seconds_total) by (bcs_cluster_id)"},` +
				`{"reference_name":"A","expression":"sum(container_cpu_usage_seconds_total) by (bcs_cluster_id)"}]}`,
			block:    blockOptional,
			optional: true,
		},
		{
			name: "named outputs query ts",
			path: "/query/ts",
			body: `{"query_list":[` +
				`{"field_name":"container_cpu_usage_seconds_total","reference_name":"a","function":[{"method":"count","dimensions":["bcs_cluster_id"]}]},` +
				`{"field_name":"container_cpu_usage_seconds_total","reference_name":"b","function":[{"method":"sum","dimensions":["bcs_cluster_id"]}]}],` +
				`"metric_merge":"a","start_time":"1741056443","end_time":"1741060043","step":"10m",` +
				`"response_contract":"named_outputs/v1","legacy_output_ref":"C",` +
				`"output_list":[{"reference_name":"C","expression":"a"},{"reference_name":"A","expression":"b"}]}`,
			block:    blockOptional,
			optional: true,
		},
		{
			name: "plain query ts",
			path: "/query/ts",
			body: `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","reference_name":"a","function":[{"method":"count","dimensions":["bcs_cluster_id"]}]}],` +
				`"metric_merge":"a","start_time":"1741056443","end_time":"1741060043","step":"10m"}`,
			block: blockAll,
		},
		{
			name:  "plain promql",
			path:  "/query/ts/promql",
			body:  `{"promql":"count(container_cpu_usage_seconds_total) by (bcs_cluster_id)","start":"1741056443","end":"1741060043","step":"10m"}`,
			block: blockAll,
		},
	}
}

func requireStopped(t *testing.T, route cancelRoute, server *cancelServer, backend *blockingBackend) {
	t.Helper()
	require.True(t, settled(server, backend),
		"%d backend calls still in flight and %d handlers still running %s after the caller went away",
		backend.inFlight.Load(), server.active.Load(), cancelSettleBound)
	backend.mu.Lock()
	defer backend.mu.Unlock()
	require.NotEmpty(t, backend.cancelled, "no backend call saw its context end")
	require.Empty(t, backend.released, "a backend call kept running until the test freed it")
	if route.optional {
		require.NotEmpty(t, backend.answered, "the required output was not answered before the optional one blocked")
		for _, query := range backend.answered {
			require.True(t, strings.HasPrefix(query, "count"), "answered %q", query)
		}
	}
}

// A caller that cancels its request stops every backend call the request
// made: each outbound context ends, and the handler returns.
func TestACancelledRequestStopsItsBackendCalls(t *testing.T) {
	for _, route := range cancelRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server := newCancelServer(t)
			backend := newBlockingBackend(t, route.block)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+route.path, bytes.NewBufferString(route.body))
			require.NoError(t, err)
			request.Header.Set(metadata.SpaceUIDHeader, influxdb.SpaceUid)
			// Not http.DefaultTransport: the storage mock has replaced it.
			client := &http.Client{Transport: &http.Transport{}}
			done := make(chan error, 1)
			go func() {
				response, err := client.Do(request)
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				done <- err
			}()
			waitBackendCall(t, backend)
			cancel()
			require.Error(t, <-done)
			requireStopped(t, route, server, backend)
		})
	}
}

// A caller whose connection drops without a word stops the request the same
// way.
func TestADisconnectedCallerStopsItsBackendCalls(t *testing.T) {
	for _, route := range cancelRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server := newCancelServer(t)
			backend := newBlockingBackend(t, route.block)
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close()
			_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: uq\r\nContent-Type: application/json\r\n%s: %s\r\nContent-Length: %d\r\n\r\n%s",
				route.path, metadata.SpaceUIDHeader, influxdb.SpaceUid, len(route.body), route.body)
			require.NoError(t, err)
			waitBackendCall(t, backend)
			require.NoError(t, conn.Close())
			requireStopped(t, route, server, backend)
		})
	}
}
