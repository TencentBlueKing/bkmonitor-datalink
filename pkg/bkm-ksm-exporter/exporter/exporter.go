// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package exporter serves registered metric sources over HTTP. It is the small,
// extensible core of the high-version compatibility exporter: HPA is the first
// source; future removed-API resource families register the same way.
package exporter

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Source renders a block of Prometheus text exposition for one metric family set.
type Source interface {
	Write(io.Writer) error
}

// Server exposes registered Sources on /metrics plus /healthz and /readyz probes.
type Server struct {
	addr    string
	sources []Source
	ready   atomic.Bool
}

// New returns a Server listening on addr.
func New(addr string) *Server { return &Server{addr: addr} }

// Register adds a metric source. Not safe for concurrent use with Run; register
// all sources during startup.
func (s *Server) Register(src Source) { s.sources = append(s.sources, src) }

// SetReady marks the exporter ready/not-ready. The caller flips it to true once
// the informer cache has synced. Until then /metrics and /readyz report
// not-ready (503) so a scraper does not ingest a successful-but-empty scrape
// that is indistinguishable from "zero HPAs" -- the dark-dashboard failure mode
// this exporter exists to prevent.
func (s *Server) SetReady(v bool) { s.ready.Store(v) }

// Handler returns the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	// /healthz is pure liveness: 200 as soon as the process is up, so a slow
	// cache sync does not get the pod restarted by its liveness probe.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	// /readyz reflects cache-synced readiness; wire it to the readiness probe so
	// the pod is not added to the scrape targets until it can emit real data.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.ready.Load() {
			http.Error(w, "cache not synced", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	return mux
}

// Run starts the HTTP server and blocks until ctx is cancelled or
// ListenAndServe fails. A cancelled ctx triggers a graceful shutdown and Run
// returns nil; any other return is a real serve error.
func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second, // bound slow request headers (gosec G112)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		// Before the informer cache has synced, an empty render is
		// indistinguishable from "zero HPAs". Fail the scrape (503) so the
		// scraper marks the target down instead of ingesting false zeros.
		http.Error(w, "cache not synced", http.StatusServiceUnavailable)
		return
	}
	var buf bytes.Buffer
	for _, src := range s.sources {
		if err := src.Write(&buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write(buf.Bytes())
}
