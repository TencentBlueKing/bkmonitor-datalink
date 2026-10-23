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
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observerPanicsCollector reports, at scrape time, the panics each observer
// raised since the process started, every name of the closed list present
// from the first scrape. Its reader is the release check: any count is a
// defect, and the name says which observer to look at.
type observerPanicsCollector struct {
	panics *prometheus.Desc
}

func newObserverPanicsCollector() *observerPanicsCollector {
	return &observerPanicsCollector{panics: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observer_panics_total"),
		"Panics an observer raised and the fan-out recovered, by observer; every other observer still received the "+
			"observation. entry is a panic before any member. Any count is a defect. Reader: the release "+
			"acceptance, at +5 and +15 minutes after each deploy (its observer_panics column).",
		[]string{"observer"}, nil)}
}

func (c *observerPanicsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.panics
}

func (c *observerPanicsCollector) Collect(ch chan<- prometheus.Metric) {
	for name, n := range observability.ObserverPanicCounts() {
		ch <- prometheus.MustNewConstMetric(c.panics, prometheus.CounterValue, float64(n), name)
	}
}
