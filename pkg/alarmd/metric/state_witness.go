// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// stateWitnessCollector reports the runtime witness cache of the execution
// store bound to it. Nothing charges that cache to a budget, so these two are
// the only place its size is read; a process with no store bound publishes
// neither.
type stateWitnessCollector struct {
	mu           sync.Mutex
	source       func() state.RuntimeWitnessStats
	items, bytes *prometheus.Desc
}

func newStateWitnessCollector() *stateWitnessCollector {
	descriptor := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, nil, nil)
	}
	return &stateWitnessCollector{
		items: descriptor("state_witness_items",
			"Preflight witnesses held in the runtime witness cache, kept from a Slot's load of Runtime State to its "+
				"apply so the write compares without reading the value again. No budget charges the cache. It is "+
				"bounded at 2^18 witnesses and 4096 Query Groups, the least recently filled Query Group evicted "+
				"first, and a Query Group's witnesses leave when this worker releases its lease. Read with "+
				"state_witness_bytes against the resident heap term in the sizing table."),
		bytes: descriptor("state_witness_bytes",
			"The runtime witness cache's estimated heap, a fixed 320 B per witness for its map entry plus the "+
				"digests it alone keeps alive, each rounded to its allocation. No budget charges the cache. It is "+
				"bounded at 2^18 witnesses, and a Query Group's witnesses leave when this worker releases its "+
				"lease. Read it against the resident heap term in the sizing table. It is an estimate from the "+
				"layout, not a measurement, and leaves out the Plan identity every series of a Plan shares."),
	}
}

// SetStateWitnessSource binds the collector to the live execution store.
func (r *Recorder) SetStateWitnessSource(source func() state.RuntimeWitnessStats) {
	if r == nil || r.phaseTwo.stateWitness == nil {
		return
	}
	r.phaseTwo.stateWitness.mu.Lock()
	r.phaseTwo.stateWitness.source = source
	r.phaseTwo.stateWitness.mu.Unlock()
}

func (c *stateWitnessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.items
	ch <- c.bytes
}

func (c *stateWitnessCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	ch <- prometheus.MustNewConstMetric(c.items, prometheus.GaugeValue, float64(stats.Items))
	ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(stats.Bytes))
}
