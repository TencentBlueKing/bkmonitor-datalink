// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	namespace = "sliwebhook"
	sliMetric = "bkm_sli"
)

var (
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_total",
		},
		[]string{"status_code"},
	)

	requestsDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "requests_duration_seconds",
			Buckets:   prometheus.DefBuckets,
		},
	)

	alertsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "alerts_total",
		},
	)
)

type Alert struct {
	Annotations map[string]string `json:"annotations"`
	Labels      map[string]string `json:"labels"`
}

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	server   *http.Server
	alerts   chan string
	snapshot []byte
	config   *Config
}

func NewServer(config *Config) *Server {
	if config == nil {
		config = DefaultConfig()
	}

	config.Validate()
	logger.SetOptions(config.Logger)

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		ctx:    ctx,
		cancel: cancel,
		config: config,
		alerts: make(chan string, 1),
	}

	router := mux.NewRouter()
	router.HandleFunc("/alerts", s.alertsRoute).Methods(http.MethodPost)
	router.HandleFunc("/api/v2/alerts", s.alertsRoute).Methods(http.MethodPost)
	router.HandleFunc("/metrics", s.metricsRoute).Methods(http.MethodGet)
	router.Handle("/admin/metrics", promhttp.Handler()).Methods(http.MethodGet)

	s.server = &http.Server{
		Handler:      router,
		ReadTimeout:  time.Minute * 5,
		WriteTimeout: time.Minute * 5,
	}
	return s
}

func (s *Server) Start() error {
	errs := make(chan error, 2)

	go func() {
		err := s.listenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("http server stopped")
			return
		}
		errs <- err
	}()

	go s.loopHandle()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		go func() {
			for err := range errs {
				logger.Errorf("background server got err: %v", err)
			}
		}()
		return nil
	case err := <-errs:
		return err
	}
}

func (s *Server) Close() error {
	if err := s.server.Close(); err != nil {
		return err
	}
	s.cancel()
	return nil
}

func (s *Server) listenAndServe() error {
	l, err := net.Listen("tcp", s.config.Http.Listen)
	if err != nil {
		return err
	}
	return s.server.Serve(l)
}

func (s *Server) loopHandle() {
	ticker := time.NewTicker(s.config.RefreshInterval)
	defer ticker.Stop()

	cached := make(map[string]struct{})
	for {
		select {
		case <-s.ctx.Done():
			return

		case <-ticker.C:
			var b []byte
			for alert := range cached {
				b = append(b, []byte(alert)...)
				b = append(b, []byte("\n")...)
			}
			s.snapshot = b
			cached = make(map[string]struct{})

		case alert := <-s.alerts:
			logger.Debugf("cache alert: %s", alert)
			alertsTotal.Inc()
			cached[alert] = struct{}{}
		}
	}
}

func (s *Server) alertsRoute(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, r.Body)
	if err != nil {
		requestsTotal.WithLabelValues(strconv.Itoa(http.StatusInternalServerError)).Inc()
		w.WriteHeader(http.StatusInternalServerError)
		logger.Errorf("failed to read body: %v", err)
		return
	}
	defer r.Body.Close()

	var alerts []Alert
	if err := json.Unmarshal(buf.Bytes(), &alerts); err != nil {
		requestsTotal.WithLabelValues(strconv.Itoa(http.StatusBadRequest)).Inc()
		w.WriteHeader(http.StatusBadRequest)
		logger.Warnf("failed to unmarshal body: %v", err)
		return
	}
	logger.Infof("recevie alert: %s", buf.String())

	for _, alert := range alerts {
		s.alerts <- toPromFormat(alert.Labels)
	}

	requestsTotal.WithLabelValues(strconv.Itoa(http.StatusOK)).Inc()
	requestsDuration.Observe(time.Since(now).Seconds())
}

func (s *Server) metricsRoute(w http.ResponseWriter, _ *http.Request) {
	w.Write(s.snapshot)
}
