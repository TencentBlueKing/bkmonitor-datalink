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
)

// stateSeriesHeldCollector reads, at scrape time, how many series the Query
// Groups this replica holds bound in their latest rounds. Written as zero
// before the source is bound.
type stateSeriesHeldCollector struct {
	mu     sync.Mutex
	source func() uint64
	held   *prometheus.Desc
}

func newStateSeriesHeldCollector() *stateSeriesHeldCollector {
	return &stateSeriesHeldCollector{
		held: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "state_series_held"),
			"Series the Query Groups this replica holds now bound in their latest evaluated rounds: each Plan's "+
				"series_matched, over the Plans evaluated within the last day (the longest interval alarmd evaluates "+
				"at), summed over the Query Groups this replica runs. Not exact either way: no-data series it "+
				"synthesizes and series absent from the latest round are not in it, and a Plan that left a Query "+
				"Group still held here -- edited onto another, or deleted -- stays counted for up to a day after its "+
				"last round, twice when the Query Group it moved to is held here too. Per replica; read the largest "+
				"replica against the State cache's budget.", nil, nil),
	}
}

// SetStateSeriesHeldSource binds the collector to the replica's count.
func (r *Recorder) SetStateSeriesHeldSource(source func() uint64) {
	if r == nil || r.phaseTwo.stateSeriesHeld == nil {
		return
	}
	r.phaseTwo.stateSeriesHeld.mu.Lock()
	r.phaseTwo.stateSeriesHeld.source = source
	r.phaseTwo.stateSeriesHeld.mu.Unlock()
}

func (c *stateSeriesHeldCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.held
}

func (c *stateSeriesHeldCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	held := uint64(0)
	if source != nil {
		held = source()
	}
	ch <- prometheus.MustNewConstMetric(c.held, prometheus.GaugeValue, float64(held))
}
