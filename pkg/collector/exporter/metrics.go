// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/elastic/beats/libbeat/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	sentDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_sent_duration_seconds",
			Help:      "Exporter sent duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	sentTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_sent_total",
			Help:      "Events the beat pipeline took from the exporter; the ones it refused count as exporter_send_failures_total reason send_rejected",
		},
	)

	handleEventTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_handled_event_total",
			Help:      "Exporter handled event total",
		},
		[]string{"record_type", "id"},
	)

	beatSentBytesSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "beat_sent_bytes_size",
			Help:      "Encoded size of the events handed to the GSE client; events dropped as oversize are not observed",
			Buckets:   define.DefSizeDistribution,
		},
		[]string{"id"},
	)

	beatSentBytesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "beat_sent_bytes_total",
			Help:      "Encoded bytes of the events handed to the GSE client; events dropped as oversize are not counted",
		},
		[]string{"id"},
	)
)

// Reasons an event was not delivered, for bk_collector_exporter_send_failures_total.
const (
	sendFailureOversize     = "oversize"
	sendFailureSendRejected = "send_rejected"
	sendFailureGseWrite     = "gse_write"
	sendFailureGseRetry     = "gse_retry"
)

// The GSE client counts its failed writes in the beat monitoring registry
// (pkg/libgse/gse/client.go); they are read from there at scrape time, so
// nothing is counted twice and the beat's own reports keep their values.
const (
	gseClientSendFailed = "bkbeat.gse_client_send_failed"
	gseClientSendRetry  = "bkbeat.gse_client_send_retry"
)

var sendFailuresDesc = prometheus.NewDesc(
	prometheus.BuildFQName(define.MonitoringNamespace, "", "exporter_send_failures_total"),
	"Events the exporter did not deliver, and GSE socket write retries. "+
		"oversize: the encoded message, the events of one data id batched into one, reached max_message_bytes and was dropped whole; "+
		"send_rejected: the beat pipeline was not running and refused the event; "+
		"gse_write: a message whose writes to the GSE agent socket all failed, dropped; "+
		"gse_retry: one failed write to the GSE agent socket, including the last one before gse_write. "+
		"gse_write and gse_retry are the GSE client's own counters and cover every message it sends.",
	[]string{"reason"}, nil,
)

// sendFailures holds the counts the exporter takes itself and reads the GSE
// client's. All four cells are there from the start.
type sendFailures struct {
	oversize     atomic.Uint64
	sendRejected atomic.Uint64
	gseWrite     *monitoring.Int
	gseRetry     *monitoring.Int
}

func newSendFailures() *sendFailures {
	return &sendFailures{
		gseWrite: monitoringInt(gseClientSendFailed),
		gseRetry: monitoringInt(gseClientSendRetry),
	}
}

func monitoringInt(name string) *monitoring.Int {
	v, _ := monitoring.Default.Get(name).(*monitoring.Int)
	return v
}

func (f *sendFailures) Describe(ch chan<- *prometheus.Desc) { ch <- sendFailuresDesc }

func (f *sendFailures) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(sendFailuresDesc, prometheus.CounterValue, float64(f.oversize.Load()), sendFailureOversize)
	ch <- prometheus.MustNewConstMetric(sendFailuresDesc, prometheus.CounterValue, float64(f.sendRejected.Load()), sendFailureSendRejected)
	// A GSE counter that is not registered has no cell rather than a 0.
	if f.gseWrite != nil {
		ch <- prometheus.MustNewConstMetric(sendFailuresDesc, prometheus.CounterValue, float64(f.gseWrite.Get()), sendFailureGseWrite)
	}
	if f.gseRetry != nil {
		ch <- prometheus.MustNewConstMetric(sendFailuresDesc, prometheus.CounterValue, float64(f.gseRetry.Get()), sendFailureGseRetry)
	}
}

var sendFailureCounts = newSendFailures()

func init() {
	prometheus.MustRegister(sendFailureCounts)
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncSentCounter() {
	sentTotal.Inc()
}

func (m *metricMonitor) IncOversizeCounter() {
	sendFailureCounts.oversize.Add(1)
}

func (m *metricMonitor) IncSendRejectedCounter() {
	sendFailureCounts.sendRejected.Add(1)
}

func (m *metricMonitor) ObserveSentDuration(t time.Time) {
	sentDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) AddHandledEventCounter(n int, rtype define.RecordType, dataId int32) {
	handleEventTotal.WithLabelValues(rtype.S(), strconv.Itoa(int(dataId))).Add(float64(n))
}

func (m *metricMonitor) ObserveBeatSentBytes(dataId int32, n float64) {
	beatSentBytesSize.WithLabelValues(strconv.Itoa(int(dataId))).Observe(n)
	beatSentBytesTotal.WithLabelValues(strconv.Itoa(int(dataId))).Add(n)
}
