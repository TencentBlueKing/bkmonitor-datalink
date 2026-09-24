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
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// effectiveCloseCollector reports the effective-time maintenance's outcome
// counts at scrape time, one cell per outcome of the closed list, every cell
// present from the first scrape after the source is bound. The counts are
// the maintenance loop's own; nothing here is incremented on a path.
type effectiveCloseCollector struct {
	mu       sync.Mutex
	source   func() map[string]uint64
	outcomes *prometheus.Desc
}

func newEffectiveCloseCollector() *effectiveCloseCollector {
	return &effectiveCloseCollector{
		outcomes: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "effective_close_total"),
			"What the effective-time maintenance did, by outcome. close_acked counts alerts, one per alert the "+
				"broker acknowledged a close for; a close is sent at every level of the strategy (__ALL__), so no "+
				"alert is left open for want of its level. The others count "+
				"events: maintenance_busy is a close that could not take the Query Group's flight because a Slot was "+
				"executing (one is nothing, a steady rate is a Query Group whose close never happens); "+
				"close_precheck_failed is the owner or content check refusing before a send; close_send_failed is "+
				"the producer not acknowledging; maintenance_plan_uncompilable is an activated Plan the maintenance "+
				"could not compile and so cannot judge; unavailable is a Query Group whose Plans could not be read "+
				"or whose owner is not accepting; effective_time_unknown, legacy_effective_time_unavailable and "+
				"close_identity_invalid name the judgement that could not be made. Every cell exists from the "+
				"start so a zero is a reading and not an absence.", []string{"outcome"}, nil),
	}
}

// SetEffectiveCloseSource binds the collector to the maintenance loop's
// counts. Safe before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetEffectiveCloseSource(source func() map[string]uint64) {
	if r == nil || r.phaseTwo.effectiveClose == nil {
		return
	}
	r.phaseTwo.effectiveClose.mu.Lock()
	r.phaseTwo.effectiveClose.source = source
	r.phaseTwo.effectiveClose.mu.Unlock()
}

func (c *effectiveCloseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outcomes
}

func (c *effectiveCloseCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	counts := source()
	for _, outcome := range observability.EffectiveCloseOutcomes {
		ch <- prometheus.MustNewConstMetric(c.outcomes, prometheus.CounterValue, float64(counts[string(outcome)]), string(outcome))
	}
}
