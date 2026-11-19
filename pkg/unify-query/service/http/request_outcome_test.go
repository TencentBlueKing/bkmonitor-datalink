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
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// counterValue reads one cell of a counter family from the default registry; ok is false when the cell does not exist.
func counterValue(t *testing.T, family string, labels map[string]string) (value float64, ok bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			got := make(map[string]string)
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			match := true
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if match {
				return m.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

func outcomeValue(t *testing.T, handler, outcome string) float64 {
	t.Helper()
	value, _ := counterValue(t, "unify_query_request_outcomes_total", map[string]string{"handler": handler, "outcome": outcome})
	return value
}

func panicValue(t *testing.T, site string) float64 {
	t.Helper()
	value, _ := counterValue(t, "unify_query_panics_total", map[string]string{"site": site})
	return value
}

// newOutcomeServer serves routes registered on the production engine (newEngine) over a real HTTP server.
func newOutcomeServer(t *testing.T, register func(public, private *gin.RouterGroup)) *httptest.Server {
	t.Helper()
	mock.Init()
	gin.SetMode(gin.TestMode)
	engine, public, private := newEngine()
	register(public, private)
	prebuildRequestOutcomes(engine)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	return server
}

// outcomeClient has its own transport: mock.Init puts httpmock on the default one. Keep-alives are off,
// so a dropped connection is never retried on a fresh one.
var outcomeClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

// drain reads the body to its end before closing it: closing early drops the connection, and the server
// then rightly counts the request as canceled by the caller.
func drain(t *testing.T, res *http.Response) {
	t.Helper()
	_, err := io.Copy(io.Discard, res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
}

func failWith(err error) gin.HandlerFunc {
	return func(c *gin.Context) {
		(&response{c: c}).failed(c.Request.Context(), err)
	}
}

func succeed(c *gin.Context) {
	(&response{c: c}).success(c.Request.Context(), gin.H{"ok": true})
}

// waitOutcome waits for the outcome cell to reach want: the middleware counts after the handler returns,
// which can be after the client has read the response.
func waitOutcome(t *testing.T, handler, outcome string, want float64) {
	t.Helper()
	require.Eventually(t, func() bool { return outcomeValue(t, handler, outcome) == want }, 5*time.Second, 10*time.Millisecond,
		"handler %s outcome %s: got %v, want %v", handler, outcome, outcomeValue(t, handler, outcome), want)
}

func TestRequestOutcomeIsCountedOncePerRequestInOrder(t *testing.T) {
	cases := []struct {
		path    string
		handler gin.HandlerFunc
		status  int
		outcome string
	}{
		{"/outcome/success", succeed, http.StatusOK, "success"},
		{"/outcome/partial-in-output-scope", func(c *gin.Context) {
			// Partial status written in an output's status scope still marks the request.
			metadata.SetStatus(metadata.WithStatusScope(c.Request.Context(), 1), metadata.QueryTsPartial, "one route failed")
			succeed(c)
		}, http.StatusOK, "partial"},
		{"/outcome/raw-partial", func(c *gin.Context) {
			metadata.SetStatus(c.Request.Context(), metadata.QueryRawPartial, "one shard failed")
			succeed(c)
		}, http.StatusOK, "partial"},
		{"/outcome/other-status-is-not-partial", func(c *gin.Context) {
			metadata.SetStatus(c.Request.Context(), metadata.ExceedsMaximumLimit, "points over limit")
			succeed(c)
		}, http.StatusOK, "success"},
		{"/outcome/client-error", failWith(errors.New("invalid query")), http.StatusBadRequest, "client_error"},
		{"/outcome/backend-rejected", failWith(metadata.ErrorWithCauses("too many series", &metadata.BackendStatusError{Status: http.StatusUnprocessableEntity})), http.StatusBadRequest, "client_error"},
		{"/outcome/upstream-error", failWith(metadata.ErrorWithCauses("backend failed", &metadata.BackendStatusError{Status: http.StatusServiceUnavailable})), http.StatusBadGateway, "upstream_error"},
		{"/outcome/timeout", failWith(fmt.Errorf("query: %w", context.DeadlineExceeded)), http.StatusGatewayTimeout, "timeout"},
		{"/outcome/internal-cancel-is-not-caller-cancel", failWith(metadata.ErrorWithCauses("route canceled", context.Canceled)), http.StatusBadRequest, "client_error"},
		{"/outcome/failure-wins-over-partial", func(c *gin.Context) {
			metadata.SetStatus(c.Request.Context(), metadata.QueryTsPartial, "one route failed")
			failWith(metadata.ErrorWithCauses("backend failed", &metadata.BackendStatusError{Status: http.StatusBadGateway}))(c)
		}, http.StatusBadGateway, "upstream_error"},
		{"/outcome/plain-500", func(c *gin.Context) { c.String(http.StatusInternalServerError, "failed") }, http.StatusInternalServerError, "internal"},
		{"/outcome/plain-404", func(c *gin.Context) { c.String(http.StatusNotFound, "missing") }, http.StatusNotFound, "client_error"},
		{"/outcome/panic", func(c *gin.Context) { panic("boom") }, http.StatusInternalServerError, "internal"},
		{"/outcome/panic-after-write", func(c *gin.Context) {
			// Headers are out: the answer stays 200, the request is still internal.
			c.String(http.StatusOK, "first item")
			c.Writer.Flush()
			panic("boom")
		}, http.StatusOK, "internal"},
		{"/outcome/stream-failure-after-write", func(c *gin.Context) {
			// A streamed response that fails mid-way: the recorded failure, not the 200 already sent, decides.
			c.String(http.StatusOK, "first item")
			c.Writer.Flush()
			metadata.RecordRequestFailure(c.Request.Context(),
				metadata.ErrorWithCauses("backend failed", &metadata.BackendStatusError{Status: http.StatusServiceUnavailable}))
		}, http.StatusOK, "upstream_error"},
		{"/outcome/write-failure", func(c *gin.Context) {
			metadata.RecordResponseWriteFailure(c.Request.Context())
			succeed(c)
		}, http.StatusOK, "canceled"},
	}
	server := newOutcomeServer(t, func(public, _ *gin.RouterGroup) {
		for _, c := range cases {
			public.GET(c.path, c.handler)
		}
	})
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			before := make(map[string]float64)
			for _, outcome := range []string{"canceled", "unauthorized", "internal", "timeout", "upstream_error", "client_error", "partial", "success"} {
				before[outcome] = outcomeValue(t, c.path, outcome)
			}
			panicsBefore := panicValue(t, "http_handler")
			res, err := outcomeClient.Get(server.URL + c.path)
			require.NoError(t, err)
			drain(t, res)
			require.Equal(t, c.status, res.StatusCode)
			waitOutcome(t, c.path, c.outcome, before[c.outcome]+1)
			for outcome, value := range before {
				if outcome != c.outcome {
					require.Equal(t, value, outcomeValue(t, c.path, outcome), "outcome %s", outcome)
				}
			}
			wantPanics := panicsBefore
			if c.path == "/outcome/panic" || c.path == "/outcome/panic-after-write" {
				wantPanics++
			}
			require.Equal(t, wantPanics, panicValue(t, "http_handler"))
		})
	}
}

func TestRequestOutcomeUnmatchedRouteAndPrivatePanic(t *testing.T) {
	server := newOutcomeServer(t, func(_, private *gin.RouterGroup) {
		// The private group has no recovery: the panic reaches net/http after being counted.
		private.GET("/outcome/private-panic", func(c *gin.Context) { panic("boom") })
	})

	before := outcomeValue(t, "unmatched", "client_error")
	res, err := outcomeClient.Get(server.URL + "/outcome/no-such-route")
	require.NoError(t, err)
	drain(t, res)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	waitOutcome(t, "unmatched", "client_error", before+1)

	internalBefore := outcomeValue(t, "/outcome/private-panic", "internal")
	panicsBefore := panicValue(t, "http_handler")
	res, err = outcomeClient.Get(server.URL + "/outcome/private-panic")
	if err == nil {
		_ = res.Body.Close()
	}
	require.Error(t, err, "net/http drops the connection of a handler that panicked")
	waitOutcome(t, "/outcome/private-panic", "internal", internalBefore+1)
	require.Equal(t, panicsBefore+1, panicValue(t, "http_handler"))
}

func TestRequestOutcomeCallerDisconnect(t *testing.T) {
	entered := make(chan struct{})
	server := newOutcomeServer(t, func(public, _ *gin.RouterGroup) {
		public.GET("/outcome/slow", func(c *gin.Context) {
			ctx := c.Request.Context()
			close(entered)
			<-ctx.Done()
			// The query fails with the caller's cancellation; FailureStatus alone would call it 400.
			(&response{c: c}).failed(ctx, ctx.Err())
		})
	})
	before := outcomeValue(t, "/outcome/slow", "canceled")
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/outcome/slow", nil)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		res, err := outcomeClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	require.Error(t, <-done)
	waitOutcome(t, "/outcome/slow", "canceled", before+1)
	require.Equal(t, float64(0), outcomeValue(t, "/outcome/slow", "client_error"))
}

func TestRequestOutcomeUnauthorized(t *testing.T) {
	previous := JwtEnabled
	JwtEnabled = true
	t.Cleanup(func() { JwtEnabled = previous })
	server := newOutcomeServer(t, func(public, _ *gin.RouterGroup) {
		public.GET("/outcome/guarded", succeed)
	})
	before := outcomeValue(t, "/outcome/guarded", "unauthorized")
	res, err := outcomeClient.Get(server.URL + "/outcome/guarded")
	require.NoError(t, err)
	drain(t, res)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	waitOutcome(t, "/outcome/guarded", "unauthorized", before+1)
	require.Equal(t, float64(0), outcomeValue(t, "/outcome/guarded", "success"))
}

// A request body that does not decode is answered 400 and counted as client_error; these handlers used
// to return without writing anything, which the client read as an empty 200.
func TestInfoHandlersAnswerUndecodableBody(t *testing.T) {
	handlers := map[string]gin.HandlerFunc{
		"/outcome/decode/field_keys":  HandlerFieldKeys,
		"/outcome/decode/tag_keys":    HandlerTagKeys,
		"/outcome/decode/tag_values":  HandlerTagValues,
		"/outcome/decode/time_series": HandlerTimeSeries,
		"/outcome/decode/series":      HandlerSeries,
	}
	server := newOutcomeServer(t, func(public, _ *gin.RouterGroup) {
		for path, handler := range handlers {
			public.POST(path, handler)
		}
	})
	for path := range handlers {
		t.Run(path, func(t *testing.T) {
			before := outcomeValue(t, path, "client_error")
			res, err := outcomeClient.Post(server.URL+path, "application/json", strings.NewReader("{"))
			require.NoError(t, err)
			drain(t, res)
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			waitOutcome(t, path, "client_error", before+1)
		})
	}
}

func TestRequestOutcomeCellsArePrebuilt(t *testing.T) {
	newOutcomeServer(t, func(public, private *gin.RouterGroup) {
		public.GET("/outcome/never-called", succeed)
		private.POST("/outcome/never-called-private", succeed)
	})
	for _, handler := range []string{"/outcome/never-called", "/outcome/never-called-private", "unmatched"} {
		for _, outcome := range []string{"canceled", "unauthorized", "internal", "timeout", "upstream_error", "client_error", "partial", "success"} {
			value, ok := counterValue(t, "unify_query_request_outcomes_total", map[string]string{"handler": handler, "outcome": outcome})
			require.True(t, ok, "handler %s outcome %s", handler, outcome)
			if handler != "unmatched" {
				require.Equal(t, float64(0), value, "handler %s outcome %s", handler, outcome)
			}
		}
	}
	for _, site := range []string{"http_handler", "query_worker", "storage"} {
		_, ok := counterValue(t, "unify_query_panics_total", map[string]string{"site": site})
		require.True(t, ok, site)
	}
}

// deadlineWriter stands for net/http's response writer once the write deadline has passed: the status
// goes out and every body write fails.
type deadlineWriter struct{ http.ResponseWriter }

func (deadlineWriter) Write([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

// A response that fails to go out is the write-back to the caller failing, counted as canceled; a
// response that could not be encoded, so that nothing went out, is internal. The same holds for the plain
// JSON answer and for the proxy's.
func TestRequestOutcomeResponseFailures(t *testing.T) {
	mock.Init()
	gin.SetMode(gin.TestMode)
	engine, public, _ := newEngine()
	unencodable := map[string]any{"value": math.Inf(1)}
	public.GET("/outcome/write-timeout", succeed)
	public.GET("/outcome/proxy-write-timeout", func(c *gin.Context) {
		proxy.WriteJSON(c.Request.Context(), c, http.StatusOK, gin.H{"ok": true})
	})
	public.GET("/outcome/encode-failure", func(c *gin.Context) {
		(&response{c: c}).success(c.Request.Context(), unencodable)
	})
	public.GET("/outcome/proxy-encode-failure", func(c *gin.Context) {
		proxy.WriteJSON(c.Request.Context(), c, http.StatusOK, unencodable)
	})
	prebuildRequestOutcomes(engine)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "write-timeout") {
			w = deadlineWriter{w}
		}
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	for path, outcome := range map[string]string{
		"/outcome/write-timeout":        "canceled",
		"/outcome/proxy-write-timeout":  "canceled",
		"/outcome/encode-failure":       "internal",
		"/outcome/proxy-encode-failure": "internal",
	} {
		t.Run(path, func(t *testing.T) {
			before := make(map[string]float64)
			for _, o := range []string{"canceled", "unauthorized", "internal", "timeout", "upstream_error", "client_error", "partial", "success"} {
				before[o] = outcomeValue(t, path, o)
			}
			res, err := outcomeClient.Get(server.URL + path)
			require.NoError(t, err)
			drain(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode)
			waitOutcome(t, path, outcome, before[outcome]+1)
			for o, value := range before {
				if o != outcome {
					require.Equal(t, value, outcomeValue(t, path, o), "outcome %s", o)
				}
			}
		})
	}
}
