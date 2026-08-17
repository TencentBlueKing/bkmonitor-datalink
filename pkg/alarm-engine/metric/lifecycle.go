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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/lifecycle"
)

var drainResultLabels = [lifecycle.DrainResultCount]string{"success", "timeout", "failed", otherLabel}

type lifecycleCollector struct {
	source lifecycle.Source

	ready              *prometheus.Desc
	assignedClaims     *prometheus.Desc
	fatalTotal         *prometheus.Desc
	draining           *prometheus.Desc
	drainTotal         *prometheus.Desc
	inflightRecords    *prometheus.Desc
	consumerLagRecords *prometheus.Desc
}

func newLifecycleCollector(source lifecycle.Source) *lifecycleCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &lifecycleCollector{
		source:             source,
		ready:              descriptor("ready", "Whether the alarm engine consumer lifecycle is ready.", nil),
		assignedClaims:     descriptor("assigned_claims", "Current claims in this alarm engine assignment.", nil),
		fatalTotal:         descriptor("fatal_total", "Process-level fatal lifecycle events.", nil),
		draining:           descriptor("draining", "Whether the alarm engine consumer lifecycle is draining.", nil),
		drainTotal:         descriptor("drain_total", "Alarm engine consumer drain results.", []string{"result"}),
		inflightRecords:    descriptor("inflight_records", "Records currently being processed by the alarm engine consumer.", nil),
		consumerLagRecords: descriptor("consumer_lag_records", "Known local lag across claims in the current alarm engine assignment.", nil),
	}
}

func (c *lifecycleCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.ready
	descriptions <- c.assignedClaims
	descriptions <- c.fatalTotal
	descriptions <- c.draining
	descriptions <- c.drainTotal
	descriptions <- c.inflightRecords
	descriptions <- c.consumerLagRecords
}

func (c *lifecycleCollector) Collect(metrics chan<- prometheus.Metric) {
	snapshot := c.source.LifecycleSnapshot()
	metrics <- prometheus.MustNewConstMetric(c.ready, prometheus.GaugeValue, boolFloat(snapshot.Ready))
	metrics <- prometheus.MustNewConstMetric(c.fatalTotal, prometheus.CounterValue, float64(snapshot.FatalTotal))
	metrics <- prometheus.MustNewConstMetric(c.draining, prometheus.GaugeValue, boolFloat(snapshot.Draining))
	for result, label := range drainResultLabels {
		metrics <- prometheus.MustNewConstMetric(c.drainTotal, prometheus.CounterValue, float64(snapshot.DrainTotal[result]), label)
	}
	if snapshot.AssignedClaims >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.assignedClaims, prometheus.GaugeValue, float64(snapshot.AssignedClaims))
	}
	if snapshot.InflightRecords >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.inflightRecords, prometheus.GaugeValue, float64(snapshot.InflightRecords))
	}
	if snapshot.ConsumerLagKnown && snapshot.ConsumerLagRecords >= 0 {
		metrics <- prometheus.MustNewConstMetric(c.consumerLagRecords, prometheus.GaugeValue, float64(snapshot.ConsumerLagRecords))
	}
}

func (r *Recorder) BindLifecycle(source lifecycle.Source) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: lifecycle source is required")
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.lifecycleBound {
		return errors.New("metric: lifecycle source is already bound")
	}
	if err := r.registry.Register(newLifecycleCollector(source)); err != nil {
		return err
	}
	r.lifecycleBound = true
	return nil
}

func lifecycleCustomSeries() int {
	return 6 + int(lifecycle.DrainResultCount)
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
