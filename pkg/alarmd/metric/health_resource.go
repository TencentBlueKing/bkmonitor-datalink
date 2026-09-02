// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type healthCollector struct {
	source observability.HealthSource

	ready               *prometheus.Desc
	state               *prometheus.Desc
	reason              *prometheus.Desc
	assignedClaims      *prometheus.Desc
	inflightMessages    *prometheus.Desc
	workerQueueDepth    *prometheus.Desc
	workerQueueBytes    *prometheus.Desc
	consumerLagRecords  *prometheus.Desc
	lastProgress        *prometheus.Desc
	lastRecoverySeconds *prometheus.Desc
}

func newHealthCollector(source observability.HealthSource) *healthCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &healthCollector{
		source:              source,
		ready:               descriptor("health_ready", "Whether the alarmd health snapshot permits new work.", nil),
		state:               descriptor("health_state", "Current bounded alarmd health state.", []string{"health_state"}),
		reason:              descriptor("health_reason", "Current bounded alarmd health reasons.", []string{"reason_code"}),
		assignedClaims:      descriptor("health_assigned_claims", "Assigned claims in the current health snapshot.", nil),
		inflightMessages:    descriptor("health_inflight_messages", "Inflight messages in the current health snapshot.", nil),
		workerQueueDepth:    descriptor("health_worker_queue_depth", "Worker queue depth in the current health snapshot.", nil),
		workerQueueBytes:    descriptor("health_worker_queue_bytes", "Worker queue bytes in the current health snapshot.", nil),
		consumerLagRecords:  descriptor("health_consumer_lag_records", "Known aggregate high-water minus processed and marked offset; this is not broker committed-group lag.", nil),
		lastProgress:        descriptor("health_last_progress_timestamp_seconds", "Unix timestamp of the last pipeline progress.", []string{"stage"}),
		lastRecoverySeconds: descriptor("health_last_recovery_timestamp_seconds", "Unix timestamp of the last dependency or restart recovery.", nil),
	}
}

func (c *healthCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.ready
	descriptions <- c.state
	descriptions <- c.reason
	descriptions <- c.assignedClaims
	descriptions <- c.inflightMessages
	descriptions <- c.workerQueueDepth
	descriptions <- c.workerQueueBytes
	descriptions <- c.consumerLagRecords
	descriptions <- c.lastProgress
	descriptions <- c.lastRecoverySeconds
}

func (c *healthCollector) Collect(metrics chan<- prometheus.Metric) {
	snapshot := observability.NormalizeHealthSnapshot(c.source.HealthSnapshot())
	metrics <- prometheus.MustNewConstMetric(c.ready, prometheus.GaugeValue, boolFloat(snapshot.Ready))
	metrics <- prometheus.MustNewConstMetric(c.state, prometheus.GaugeValue, 1, string(snapshot.State))
	for _, reason := range observability.NormalizeHealthMetricReasons(snapshot.Reasons) {
		metrics <- prometheus.MustNewConstMetric(c.reason, prometheus.GaugeValue, 1, string(reason))
	}
	if snapshot.AssignedClaims >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.assignedClaims, prometheus.GaugeValue, float64(snapshot.AssignedClaims))
	}
	if snapshot.InflightMessages >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.inflightMessages, prometheus.GaugeValue, float64(snapshot.InflightMessages))
	}
	if snapshot.WorkerQueueDepth >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.workerQueueDepth, prometheus.GaugeValue, float64(snapshot.WorkerQueueDepth))
	}
	if snapshot.WorkerQueueBytes >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.workerQueueBytes, prometheus.GaugeValue, float64(snapshot.WorkerQueueBytes))
	}
	if snapshot.ConsumerLagKnown && snapshot.ConsumerLagRecords >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.consumerLagRecords, prometheus.GaugeValue, float64(snapshot.ConsumerLagRecords))
	}
	if !snapshot.LastProgressAt.IsZero() {
		metrics <- prometheus.MustNewConstMetric(
			c.lastProgress, prometheus.GaugeValue, float64(snapshot.LastProgressAt.Unix()), string(snapshot.LastProgressStage),
		)
	}
	if !snapshot.LastRecoveryAt.IsZero() {
		metrics <- prometheus.MustNewConstMetric(c.lastRecoverySeconds, prometheus.GaugeValue, float64(snapshot.LastRecoveryAt.Unix()))
	}
}

type resourceCollector struct {
	source observability.ResourceSource

	state              *prometheus.Desc
	cpuCores           *prometheus.Desc
	rssBytes           *prometheus.Desc
	heapBytes          *prometheus.Desc
	gcPauseSeconds     *prometheus.Desc
	workerQueueDepth   *prometheus.Desc
	workerQueueBytes   *prometheus.Desc
	inflightMessages   *prometheus.Desc
	inflightBytes      *prometheus.Desc
	consumerLagRecords *prometheus.Desc
	stateBytes         *prometheus.Desc
}

func newResourceCollector(source observability.ResourceSource) *resourceCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &resourceCollector{
		source:             source,
		state:              descriptor("resource_state", "Observe-only resource signal state.", []string{"resource_state"}),
		cpuCores:           descriptor("resource_cpu_cores", "Observed alarmd process CPU cores.", nil),
		rssBytes:           descriptor("resource_rss_bytes", "Observed alarmd process RSS bytes.", nil),
		heapBytes:          descriptor("resource_heap_bytes", "Observed alarmd Go heap bytes.", nil),
		gcPauseSeconds:     descriptor("resource_gc_pause_seconds", "Observed alarmd GC pause seconds.", nil),
		workerQueueDepth:   descriptor("resource_worker_queue_depth", "Observed worker queue depth.", nil),
		workerQueueBytes:   descriptor("resource_worker_queue_bytes", "Observed worker queue bytes.", nil),
		inflightMessages:   descriptor("resource_inflight_messages", "Observed inflight messages.", nil),
		inflightBytes:      descriptor("resource_inflight_bytes", "Observed inflight bytes.", nil),
		consumerLagRecords: descriptor("resource_consumer_lag_records", "Observed aggregate high-water minus processed and marked offset; it is not broker committed-group lag and does not trigger resource actions.", nil),
		stateBytes:         descriptor("resource_state_bytes", "Observed runtime state bytes touched by alarmd.", nil),
	}
}

func (c *resourceCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.state
	descriptions <- c.cpuCores
	descriptions <- c.rssBytes
	descriptions <- c.heapBytes
	descriptions <- c.gcPauseSeconds
	descriptions <- c.workerQueueDepth
	descriptions <- c.workerQueueBytes
	descriptions <- c.inflightMessages
	descriptions <- c.inflightBytes
	descriptions <- c.consumerLagRecords
	descriptions <- c.stateBytes
}

func (c *resourceCollector) Collect(metrics chan<- prometheus.Metric) {
	snapshot := observability.NormalizeResourceSnapshot(c.source.ResourceSnapshot())
	signal := observability.NormalizeResourceSignal(c.source.ResourceSignal())
	metrics <- prometheus.MustNewConstMetric(c.state, prometheus.GaugeValue, 1, string(signal.State))
	emitResourceMetric(metrics, c.cpuCores, snapshot.CPUCores)
	emitResourceMetric(metrics, c.rssBytes, snapshot.RSSBytes)
	emitResourceMetric(metrics, c.heapBytes, snapshot.HeapBytes)
	emitResourceMetric(metrics, c.gcPauseSeconds, snapshot.GCPauseSeconds)
	emitResourceMetric(metrics, c.workerQueueDepth, snapshot.WorkerQueueDepth)
	emitResourceMetric(metrics, c.workerQueueBytes, snapshot.WorkerQueueBytes)
	emitResourceMetric(metrics, c.inflightMessages, snapshot.InflightMessages)
	emitResourceMetric(metrics, c.inflightBytes, snapshot.InflightBytes)
	emitResourceMetric(metrics, c.consumerLagRecords, snapshot.ConsumerLagRecords)
	emitResourceMetric(metrics, c.stateBytes, snapshot.StateBytes)
}

func emitResourceMetric(metrics chan<- prometheus.Metric, descriptor *prometheus.Desc, value float64) {
	if value >= 0 {
		metrics <- prometheus.MustNewConstMetric(descriptor, prometheus.GaugeValue, value)
	}
}

func (r *Recorder) BindHealth(source observability.HealthSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: health source is required")
	}
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	if r.healthBound {
		return errors.New("metric: health source is already bound")
	}
	if err := r.registry.Register(newHealthCollector(source)); err != nil {
		return err
	}
	r.healthBound = true
	return nil
}

func (r *Recorder) BindResources(source observability.ResourceSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: resource source is required")
	}
	r.resourceMu.Lock()
	defer r.resourceMu.Unlock()
	if r.resourceBound {
		return errors.New("metric: resource source is already bound")
	}
	if err := r.registry.Register(newResourceCollector(source)); err != nil {
		return err
	}
	r.resourceBound = true
	return nil
}

func healthResourceCustomSeries() int {
	health := 1 + len(observability.AllHealthStates()) + len(observability.AllReasons(observability.ComponentResource)) + 7 + len(observability.AllComponentStages())
	resources := len(observability.AllResourceStates()) + 10
	return health + resources
}
