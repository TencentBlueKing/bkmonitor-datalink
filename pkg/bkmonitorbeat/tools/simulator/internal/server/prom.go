// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package server

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func serveProm(port int) error {
	namespace := "test"
	labelNames := []string{"uri", "code", "method"}
	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "requests_total",
		Help:      "total requests",
		ConstLabels: prometheus.Labels{
			"cmd": os.Args[0],
		},
	}, labelNames)
	requestsCurrent := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "requests_current",
		Help:      "total requests",
		ConstLabels: prometheus.Labels{
			"cmd": os.Args[0],
		},
	}, []string{"uri"})
	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "request_duration_seconds",
		ConstLabels: prometheus.Labels{
			"cmd": os.Args[0],
		},
	}, labelNames)
	requestDurationSummary := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: namespace,
		Name:      "request_duration_summary",
		ConstLabels: prometheus.Labels{
			"cmd": os.Args[0],
		},
	}, []string{"uri", "method"})
	reg := prometheus.NewRegistry()
	reg.MustRegister(requestsTotal, requestsCurrent, requestDuration, requestDurationSummary)
	addr := fmt.Sprintf(":%d", port)
	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
	curryLabels := prometheus.Labels{"uri": "/metrics"}
	handler = promhttp.InstrumentHandlerInFlight(requestsCurrent.With(curryLabels), handler)
	handler = promhttp.InstrumentHandlerCounter(requestsTotal.MustCurryWith(curryLabels), handler)
	handler = promhttp.InstrumentHandlerDuration(requestDuration.MustCurryWith(curryLabels), handler)
	newHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		defer func() {
			seconds := time.Since(t).Seconds()
			requestDurationSummary.MustCurryWith(curryLabels).With(prometheus.Labels{
				"method": r.Method,
			}).Observe(seconds)
		}()
		handler.ServeHTTP(w, r)
	})
	m := http.NewServeMux()
	m.Handle("/metrics", newHandler)
	return http.ListenAndServe(addr, m)
}
