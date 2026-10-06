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
)

// renewalGateCollector reports how often this process forgot every key life it
// had remembered.
type renewalGateCollector struct {
	mu     sync.Mutex
	source func() uint64
	resets *prometheus.Desc
}

func newRenewalGateCollector() *renewalGateCollector {
	return &renewalGateCollector{
		resets: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "state_renewal_gate_resets_total"),
			"Times this process forgot every generation-scoped key life it had remembered, because the "+
				"keys it is being asked about no longer fit. Renewal hangs on the load, so without the "+
				"memory every Plan sends one EVAL per Slot to decide something that changes twice a day; "+
				"the memory is what turns that into one ask per key per six hours. A reset puts all of "+
				"that traffic back on Redis until the memory fills again. "+
				"A steady zero is the expected reading and the only healthy one, and it is a computed "+
				"zero: a worker reporting it has a store bound to it and is answering from that store. "+
				"Nothing else reports this -- the "+
				"Slots keep passing and the keys keep being renewed, and the only other symptom is the "+
				"EVAL rate climbing back to where it was before the memory existed. Any non-zero means "+
				"this worker owns more Plans than it can remember the key lives of. "+
				"The series is absent, rather than zero, on a process whose execution store was never "+
				"bound to it. That is deliberate: a zero reported by a collector nobody wired up reads "+
				"exactly like the healthy zero, and the wiring is a single call that nothing else fails "+
				"without. Absent here means the process is not reporting, not that it has nothing to "+
				"report.",
			nil, nil,
		),
	}
}

// SetRenewalGateSource binds the collector to the live execution store. A nil
// recorder is a no-op so wiring never has to be ordered against construction.
func (r *Recorder) SetRenewalGateSource(source func() uint64) {
	if r == nil || r.phaseTwo.renewalGate == nil {
		return
	}
	r.phaseTwo.renewalGate.mu.Lock()
	r.phaseTwo.renewalGate.source = source
	r.phaseTwo.renewalGate.mu.Unlock()
}

func (c *renewalGateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.resets
}

func (c *renewalGateCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		// Nothing is reported, rather than a zero. The whole reading of this
		// family is that the number never moves, so a zero from a collector
		// nobody bound is indistinguishable from the healthy answer, and the
		// binding is one call in the bundle that nothing else fails without.
		// Absent says "this process is not reporting"; zero says "it is, and
		// it has forgotten nothing".
		return
	}
	ch <- prometheus.MustNewConstMetric(c.resets, prometheus.CounterValue, float64(source()))
}
