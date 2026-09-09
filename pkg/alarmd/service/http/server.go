// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The query surface answers health, metrics and, later, the observability API.
// It is the only surface a host platform may route to. The diagnostics surface
// carries pprof, which can dump goroutines and start a CPU profile that costs
// real load, so it listens separately and defaults to loopback: routing the
// query surface must never expose it.
//
// Port-forward still reaches a loopback listener, but it must now target the
// diagnostics port: anything that forwarded the query port to collect profiles
// has to be updated, or it gets a 404.
type Server struct {
	handler            http.Handler
	apiHandler         atomic.Pointer[http.Handler]
	diagnosticsHandler http.Handler
	diagnosticsAddress string
	ready              atomic.Bool
	source             lifecycle.Source
	healthSource       observability.HealthSource
}

// Option configures a Server. Options are variadic so callers that do not care
// about the diagnostics surface keep compiling unchanged.
type Option func(*Server)

// WithDiagnosticsAddress serves pprof on its own listener. An empty address
// leaves the diagnostics surface unserved rather than folding it back into the
// query surface.
func WithDiagnosticsAddress(address string) Option {
	return func(server *Server) { server.diagnosticsAddress = address }
}

// SetAPI installs the observability API served under /api/ on the query
// surface. The route exists from startup and answers 503 until this is called,
// because the runtime that produces the object facts opens after the listener
// does. Answering "not ready yet" is the honest response; answering 404 would
// be indistinguishable from a deployment that has no API at all.
func (s *Server) SetAPI(handler http.Handler) {
	s.apiHandler.Store(&handler)
}

func (s *Server) serveAPI(response http.ResponseWriter, request *http.Request) {
	if handler := s.apiHandler.Load(); handler != nil && *handler != nil {
		(*handler).ServeHTTP(response, request)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": "observability API is not ready yet"})
}

func New(recorder *metric.Recorder, options ...Option) *Server {
	return newServer(recorder, nil, options...)
}

func NewWithLifecycle(recorder *metric.Recorder, source lifecycle.Source, options ...Option) (*Server, error) {
	if recorder == nil {
		return nil, errors.New("HTTP service: metric recorder is required")
	}
	if source == nil {
		return nil, errors.New("HTTP service: lifecycle source is required")
	}
	if err := recorder.BindLifecycle(source); err != nil {
		return nil, err
	}
	return newServer(recorder, source, options...), nil
}

func NewWithHealth(recorder *metric.Recorder, source observability.HealthSource, options ...Option) (*Server, error) {
	if recorder == nil {
		return nil, errors.New("HTTP service: metric recorder is required")
	}
	if source == nil {
		return nil, errors.New("HTTP service: health source is required")
	}
	if err := recorder.BindHealth(source); err != nil {
		return nil, err
	}
	server := newServer(recorder, nil, options...)
	server.healthSource = source
	return server, nil
}

func newServer(recorder *metric.Recorder, source lifecycle.Source, options ...Option) *Server {
	server := &Server{source: source}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.health)
	mux.HandleFunc("/readyz", server.readiness)
	mux.Handle("/metrics", promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/api/", server.serveAPI)
	server.handler = mux

	// Allocation and CPU attribution has no in-process answer today: the
	// runtime metrics say the process is allocation driven but not where the
	// allocations come from. pprof stays inert until something requests a
	// profile, but a request is expensive enough that it must not be reachable
	// from whatever can reach the query surface.
	diagnostics := http.NewServeMux()
	diagnostics.HandleFunc("/debug/pprof/", pprof.Index)
	diagnostics.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	diagnostics.HandleFunc("/debug/pprof/profile", pprof.Profile)
	diagnostics.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	diagnostics.HandleFunc("/debug/pprof/trace", pprof.Trace)
	server.diagnosticsHandler = diagnostics

	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// DiagnosticsHandler exposes the pprof surface for tests and for callers that
// serve it themselves. It is never mounted on the query surface.
func (s *Server) DiagnosticsHandler() http.Handler {
	return s.diagnosticsHandler
}

func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) Run(ctx context.Context, address string, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}

	// A diagnostics bind failure fails startup rather than degrading quietly.
	// The address is deterministic configuration, not a runtime dependency: it
	// either binds every time or never. Losing pprof silently would only be
	// discovered during the next incident, when it is needed and gone.
	stop, err := s.serveDiagnostics()
	if err != nil {
		_ = listener.Close()
		return err
	}
	// Both surfaces share one shutdown deadline. Giving the diagnostics
	// listener a second full budget would let a long in-flight profile push the
	// total past what the caller waits for, and the caller would report a
	// shutdown timeout for a process that stopped correctly.
	var stopOnce sync.Once
	stopDiagnostics := func(stopCtx context.Context) { stopOnce.Do(func() { stop(stopCtx) }) }
	defer func() {
		lateCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		stopDiagnostics(lateCtx)
	}()

	httpServer := &http.Server{
		Addr:              address,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	s.SetReady(true)
	defer s.SetReady(false)

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		stopDiagnostics(shutdownCtx)
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			closeErr := httpServer.Close()
			<-serveErrors
			if closeErr != nil {
				return errors.Join(
					fmt.Errorf("shutdown HTTP: %w", err),
					fmt.Errorf("force close HTTP: %w", closeErr),
				)
			}
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		return nil
	}
}

// serveDiagnostics starts the pprof listener and returns the function that
// stops it. An unset address serves nothing and stops nothing; pprof is never
// folded back onto the query surface as a fallback.
func (s *Server) serveDiagnostics() (func(context.Context), error) {
	if s.diagnosticsAddress == "" {
		return func(context.Context) {}, nil
	}
	listener, err := net.Listen("tcp", s.diagnosticsAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for diagnostics on %s: %w", s.diagnosticsAddress, err)
	}
	server := &http.Server{
		Addr:              s.diagnosticsAddress,
		Handler:           s.diagnosticsHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	return func(stopCtx context.Context) {
		if err := server.Shutdown(stopCtx); err != nil {
			_ = server.Close()
		}
		<-done
	}, nil
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func (s *Server) readiness(response http.ResponseWriter, _ *http.Request) {
	ready := s.ready.Load()
	if s.healthSource != nil {
		snapshot := observability.NormalizeHealthSnapshot(s.healthSource.HealthSnapshot())
		response.Header().Set("Content-Type", "application/json")
		if !snapshot.Ready {
			response.WriteHeader(http.StatusServiceUnavailable)
		}
		if err := json.NewEncoder(response).Encode(snapshot); err != nil {
			return
		}
		return
	}
	if s.source != nil {
		ready = s.source.LifecycleSnapshot().Ready
	}
	if !ready {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusOK)
}
