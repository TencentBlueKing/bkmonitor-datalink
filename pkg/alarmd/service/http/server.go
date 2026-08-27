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
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type Server struct {
	handler      http.Handler
	ready        atomic.Bool
	source       lifecycle.Source
	healthSource observability.HealthSource
}

func New(recorder *metric.Recorder) *Server {
	return newServer(recorder, nil)
}

func NewWithLifecycle(recorder *metric.Recorder, source lifecycle.Source) (*Server, error) {
	if recorder == nil {
		return nil, errors.New("HTTP service: metric recorder is required")
	}
	if source == nil {
		return nil, errors.New("HTTP service: lifecycle source is required")
	}
	if err := recorder.BindLifecycle(source); err != nil {
		return nil, err
	}
	return newServer(recorder, source), nil
}

func NewWithHealth(recorder *metric.Recorder, source observability.HealthSource) (*Server, error) {
	if recorder == nil {
		return nil, errors.New("HTTP service: metric recorder is required")
	}
	if source == nil {
		return nil, errors.New("HTTP service: health source is required")
	}
	if err := recorder.BindHealth(source); err != nil {
		return nil, err
	}
	server := newServer(recorder, nil)
	server.healthSource = source
	return server, nil
}

func newServer(recorder *metric.Recorder, source lifecycle.Source) *Server {
	server := &Server{source: source}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.health)
	mux.HandleFunc("/readyz", server.readiness)
	mux.Handle("/metrics", promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}))
	server.handler = mux
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) Run(ctx context.Context, address string, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}

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
