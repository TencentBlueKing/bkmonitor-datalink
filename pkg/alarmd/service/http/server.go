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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet/ui"
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
	grpcHandler        atomic.Pointer[http.Handler]
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

// SetGRPC installs the gRPC surface -- decision-016's control stream -- on
// the query listener. gRPC needs HTTP/2, and the listener speaks it in the
// clear (h2c) for exactly this: the stream shares the port every replica
// already reaches every other on, and no second port or setting exists for
// it. Until this is called a gRPC request is answered 503, like the API.
//
// Two facts this rests on, for whoever edits the listener: http.Server here
// sets ReadHeaderTimeout alone. ReadTimeout, WriteTimeout and IdleTimeout
// stay unset, because any of them would cut a long-lived stream at the
// deadline and the symptom would be "the stream drops for no reason". A
// timeout added for the request/response routes has to leave the gRPC
// route out. And the stream's client runs gRPC keepalive at thirty
// seconds, which this listener tolerates only because grpc.Server.ServeHTTP
// applies no keepalive enforcement policy; a native gRPC listener would
// refuse that ping rate with too_many_pings under its default five-minute
// MinTime and close every stream. Whoever moves the stream off this
// listener sets the policy to match.
func (s *Server) SetGRPC(handler http.Handler) {
	s.grpcHandler.Store(&handler)
}

// isGRPC tells a gRPC request from the rest by what gRPC guarantees: HTTP/2
// and the application/grpc content type.
func isGRPC(request *http.Request) bool {
	return request.ProtoMajor == 2 && strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc")
}

func (s *Server) serveGRPC(response http.ResponseWriter, request *http.Request) {
	if handler := s.grpcHandler.Load(); handler != nil && *handler != nil {
		(*handler).ServeHTTP(response, request)
		return
	}
	notReadyGRPC.ServeHTTP(response, request)
}

// notReadyGRPC answers every call UNAVAILABLE in gRPC's own framing while
// the runtime that serves the stream is not open. A plain 503 would do for
// a browser; a gRPC client given one may treat the whole connection as
// bad, and the next call over it -- after the stream is installed -- would
// hang rather than be served.
var notReadyGRPC = grpc.NewServer(grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
	return status.Error(codes.Unavailable, "control stream is not ready yet")
}))

// listenerHandler routes a request to the gRPC surface or the mux, over a
// listener that accepts HTTP/2 in the clear.
func (s *Server) listenerHandler() http.Handler {
	return h2c.NewHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if isGRPC(request) {
			s.serveGRPC(response, request)
			return
		}
		s.handler.ServeHTTP(response, request)
	}), &http2.Server{})
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
	// The page is static and carries no runtime dependency, so it is mounted
	// unconditionally: when the runtime is not open yet the page still loads and
	// says which channel cannot answer, which is the degradation it was designed
	// for. "/" is the least specific pattern, so it cannot shadow the routes
	// above.
	mux.Handle("/", ui.Handler())
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
		Addr:    address,
		Handler: s.listenerHandler(),
		// The only timeout, on purpose: see SetGRPC.
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
		// Drained concurrently, not one after the other. They share the deadline,
		// so draining the diagnostics listener first would let one in-flight
		// profile spend the whole budget and hand the query listener a context
		// that has already expired -- turning an ordinary rollout that happened
		// to catch a running profile into a forced close of in-flight scrapes
		// and a non-zero exit on every replica.
		diagnosticsStopped := make(chan struct{})
		go func() {
			defer close(diagnosticsStopped)
			stopDiagnostics(shutdownCtx)
		}()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		<-diagnosticsStopped
		if err := shutdownErr; err != nil {
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
