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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// activationHeaderCollector reads the Control Leader's activation header
// rebuilds and renewal conflicts at scrape time. Every cell is emitted, zero
// included.
type activationHeaderCollector struct {
	mu        sync.Mutex
	source    func() controlplane.ActivationHeaderReading
	rebuilds  *prometheus.Desc
	conflicts *prometheus.Desc
}

func newActivationHeaderCollector() *activationHeaderCollector {
	return &activationHeaderCollector{
		rebuilds: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_header_rebuild_total"),
			"Activation headers the Control Leader found missing with the body present and tried to write back, by outcome. "+
				"rebuilt: written back as the body describes it. conflict: a header appeared or the body changed first; "+
				"nothing written, the next round reads again. body_unparsable, body_pending: refused by name, nothing written; "+
				"until the header is back no strategy change is activated and nothing the activation names is renewed.",
			[]string{"outcome"}, nil),
		conflicts: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_renewal_conflict_total"),
			"Renewals of the current activation's objects that wrote nothing, by why. header_moved: another cutover's header, "+
				"the next round renews what it names. header_missing: no header at all, which no round settles by itself; "+
				"the leader writes it back (activation_header_rebuild_total).",
			[]string{"reason"}, nil),
	}
}

// SetActivationHeaderSource binds the collector to the repository's reading.
func (r *Recorder) SetActivationHeaderSource(source func() controlplane.ActivationHeaderReading) {
	if r == nil || r.phaseTwo.activationHeader == nil {
		return
	}
	r.phaseTwo.activationHeader.mu.Lock()
	r.phaseTwo.activationHeader.source = source
	r.phaseTwo.activationHeader.mu.Unlock()
}

func (c *activationHeaderCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rebuilds
	ch <- c.conflicts
}

func (c *activationHeaderCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var reading controlplane.ActivationHeaderReading
	if source != nil {
		reading = source()
	}
	for _, outcome := range controlplane.ActivationHeaderRebuildOutcomes {
		ch <- prometheus.MustNewConstMetric(c.rebuilds, prometheus.CounterValue, float64(reading.Rebuilds[outcome]), string(outcome))
	}
	for _, reason := range controlplane.ActivationRenewalConflicts {
		ch <- prometheus.MustNewConstMetric(c.conflicts, prometheus.CounterValue, float64(reading.RenewalConflicts[reason]), string(reason))
	}
}
