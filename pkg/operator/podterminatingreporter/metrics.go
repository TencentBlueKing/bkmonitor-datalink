// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"io"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Allow one overlapping scrape while bounding concurrent snapshot collection.
const maxMetricsRequestsInFlight = 2

type reporterCollector struct {
	state *State
	now   func() time.Time

	terminatingSeconds  *prometheus.Desc
	refreshSuccess      *prometheus.Desc
	refreshDuration     *prometheus.Desc
	lastSuccess         *prometheus.Desc
	activeEntries       *prometheus.Desc
	recoveryEntries     *prometheus.Desc
	stateBytes          *prometheus.Desc
	kubernetesAPIErrors *prometheus.Desc
}

func newReporterCollector(state *State, now func() time.Time) *reporterCollector {
	return &reporterCollector{
		state: state,
		now:   now,
		terminatingSeconds: prometheus.NewDesc(
			"pod_terminating_seconds",
			"Seconds since deletion was requested for a Pod.",
			[]string{"namespace", "pod", "node"},
			nil,
		),
		refreshSuccess: prometheus.NewDesc(
			"pod_terminating_reporter_refresh_success",
			"Whether the latest complete refresh succeeded and remains fresh.",
			nil,
			nil,
		),
		refreshDuration: prometheus.NewDesc(
			"pod_terminating_reporter_refresh_duration_seconds",
			"Duration of the latest complete refresh attempt, including Pod listing and state persistence.",
			nil,
			nil,
		),
		lastSuccess: prometheus.NewDesc(
			"pod_terminating_reporter_last_success_timestamp_seconds",
			"Unix timestamp of the latest successful refresh.",
			nil,
			nil,
		),
		activeEntries: prometheus.NewDesc(
			"pod_terminating_reporter_active_entries",
			"Number of currently deleting Pod dimensions in the latest persisted state.",
			nil,
			nil,
		),
		recoveryEntries: prometheus.NewDesc(
			"pod_terminating_reporter_recovery_entries",
			"Number of recovery tombstones in the latest persisted state.",
			nil,
			nil,
		),
		stateBytes: prometheus.NewDesc(
			"pod_terminating_reporter_state_bytes",
			"UTF-8 bytes of compact state.json in the latest persisted state.",
			nil,
			nil,
		),
		kubernetesAPIErrors: prometheus.NewDesc(
			"pod_terminating_reporter_kubernetes_api_errors_total",
			"Total Kubernetes API request errors observed by the reporter.",
			[]string{"operation"},
			nil,
		),
	}
}

func (c *reporterCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.terminatingSeconds
	ch <- c.refreshSuccess
	ch <- c.refreshDuration
	ch <- c.lastSuccess
	ch <- c.activeEntries
	ch <- c.recoveryEntries
	ch <- c.stateBytes
	ch <- c.kubernetesAPIErrors
}

func (c *reporterCollector) Collect(ch chan<- prometheus.Metric) {
	snapshot := c.state.MetricsSnapshot(c.now())
	ch <- prometheus.MustNewConstMetric(c.refreshSuccess, prometheus.GaugeValue, snapshot.RefreshSuccess)
	ch <- prometheus.MustNewConstMetric(c.refreshDuration, prometheus.GaugeValue, snapshot.RefreshDurationSeconds)
	ch <- prometheus.MustNewConstMetric(c.lastSuccess, prometheus.GaugeValue, snapshot.LastSuccessTimestamp)
	ch <- prometheus.MustNewConstMetric(c.activeEntries, prometheus.GaugeValue, float64(snapshot.ActiveEntries))
	ch <- prometheus.MustNewConstMetric(c.recoveryEntries, prometheus.GaugeValue, float64(snapshot.RecoveryEntries))
	ch <- prometheus.MustNewConstMetric(c.stateBytes, prometheus.GaugeValue, float64(snapshot.StateBytes))
	for _, operation := range kubernetesAPIOperations {
		ch <- prometheus.MustNewConstMetric(
			c.kubernetesAPIErrors,
			prometheus.CounterValue,
			snapshot.KubernetesAPIErrors[operation],
			operation,
		)
	}
	for _, row := range snapshot.Rows {
		ch <- prometheus.MustNewConstMetric(
			c.terminatingSeconds,
			prometheus.GaugeValue,
			float64(row.Seconds),
			row.Namespace,
			row.Pod,
			row.Node,
		)
	}
}

func NewRegistry(state *State, now func() time.Time) *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(newReporterCollector(state, now))
	return registry
}

func NewHTTPHandler(state *State, now func() time.Time, requestTimeout time.Duration) http.Handler {
	return newHTTPHandlerWithGatherer(state, now, requestTimeout, NewRegistry(state, now))
}

func newHTTPHandlerWithGatherer(
	state *State,
	now func() time.Time,
	requestTimeout time.Duration,
	gatherer prometheus.Gatherer,
) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		MaxRequestsInFlight: maxMetricsRequestsInFlight,
		Timeout:             requestTimeout,
	}))
	liveness := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	mux.HandleFunc("/livez", liveness)
	mux.HandleFunc("/healthz", liveness)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !state.IsFresh(now()) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "stale")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}
