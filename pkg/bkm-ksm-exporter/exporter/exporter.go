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
	"io"
	"net/http"
)

// Source renders a block of Prometheus text exposition for one metric family set.
type Source interface {
	Write(io.Writer) error
}

// Server exposes registered Sources on /metrics plus a /healthz probe.
type Server struct {
	addr    string
	sources []Source
}

// New returns a Server listening on addr.
func New(addr string) *Server { return &Server{addr: addr} }

// Register adds a metric source. Not safe for concurrent use with Run; register
// all sources during startup.
func (s *Server) Register(src Source) { s.sources = append(s.sources, src) }

// Handler returns the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	return mux
}

// Run starts the blocking HTTP server.
func (s *Server) Run() error {
	server := &http.Server{Addr: s.addr, Handler: s.Handler()}
	return server.ListenAndServe()
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
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
