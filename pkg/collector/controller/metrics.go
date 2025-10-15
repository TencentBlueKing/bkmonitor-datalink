// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	uptime = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "uptime",
			Help:      "uptime of program",
		},
	)

	appBuildInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "app_build_info",
			Help:      "Build information of app",
		},
		[]string{"version", "git_hash", "build_time"},
	)

	reloadSuccessTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "controller_reload_success_total",
			Help:      "Controller reload config successfully total",
		},
	)

	reloadFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "controller_reload_failed_total",
			Help:      "Controller reload config failed total",
		},
	)

	reloadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "controller_reload_duration_seconds",
			Help:      "Controller reload duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	droppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_dropped_total",
			Help:      "Pipeline dropped records total",
		},
		[]string{"pipeline", "record_type", "id", "processor"},
	)

	skippedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_skipped_total",
			Help:      "Pipeline skipped records total",
		},
		[]string{"pipeline", "record_type", "id", "processor", "token"},
	)

	handledTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_handled_total",
			Help:      "Pipeline handled records total",
		},
		[]string{"pipeline", "record_type", "id", "token"},
	)

	handledDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_handled_duration_seconds",
			Help:      "Pipeline handled duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"pipeline", "record_type", "id"},
	)

	exportedDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_exported_duration_seconds",
			Help:      "Pipeline exported duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"pipeline", "record_type", "id"},
	)
)

func init() {
	maxprocs.Logger(func(s string, i ...any) {
		logger.Infof(s, i...)
	})
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) UpdateUptime(n int) {
	uptime.Add(float64(n))
}

func (m *metricMonitor) IncReloadSuccessCounter() {
	reloadSuccessTotal.Inc()
}

func (m *metricMonitor) IncReloadFailedCounter() {
	reloadFailedTotal.Inc()
}

func (m *metricMonitor) ObserveReloadDuration(t time.Time) {
	reloadDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) SetAppBuildInfo(info define.BuildInfo) {
	appBuildInfo.WithLabelValues(info.Version, info.GitHash, info.Time).Set(1)
}

func (m *metricMonitor) IncDroppedCounter(pipeline string, rtype define.RecordType, dataId int32, processor string) {
	lvs := []string{pipeline, rtype.S(), strconv.Itoa(int(dataId)), processor}
	droppedTotal.WithLabelValues(lvs...).Inc()
}

func (m *metricMonitor) IncSkippedCounter(pipeline string, rtype define.RecordType, dataId int32, processor string, token string) {
	lvs := []string{pipeline, rtype.S(), strconv.Itoa(int(dataId)), processor, token}
	skippedTotal.WithLabelValues(lvs...).Inc()
}

func (m *metricMonitor) IncHandledCounter(pipeline string, rtype define.RecordType, dataId int32, token string) {
	lvs := []string{pipeline, rtype.S(), strconv.Itoa(int(dataId)), token}
	handledTotal.WithLabelValues(lvs...).Inc()
}

func (m *metricMonitor) ObserveHandledDuration(t time.Time, pipeline string, rtype define.RecordType, dataId int32) {
	lvs := []string{pipeline, rtype.S(), strconv.Itoa(int(dataId))}
	handledDuration.WithLabelValues(lvs...).Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) ObserveExportedDuration(t time.Time, pipeline string, rtype define.RecordType, dataId int32) {
	lvs := []string{pipeline, rtype.S(), strconv.Itoa(int(dataId))}
	exportedDuration.WithLabelValues(lvs...).Observe(time.Since(t).Seconds())
}
