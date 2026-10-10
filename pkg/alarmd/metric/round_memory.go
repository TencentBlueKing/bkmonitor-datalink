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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// roundMemoryCollector reads what this replica's fleet tracker holds to read
// window holes by, at scrape time: the rounds it keeps for the objects it
// runs, how many per object, and the bytes. Every series is written, zero
// before the source is bound.
type roundMemoryCollector struct {
	mu          sync.Mutex
	source      func() fleet.RoundMemoryFacts
	objects     *prometheus.Desc
	rounds      *prometheus.Desc
	bytes       *prometheus.Desc
	maxRounds   *prometheus.Desc
	windowSized *prometheus.Desc
	lineHeld    *prometheus.Desc
}

func newRoundMemoryCollector() *roundMemoryCollector {
	name := func(suffix string) string { return prometheus.BuildFQName(metricNamespace, metricSubsystem, suffix) }
	return &roundMemoryCollector{
		objects: prometheus.NewDesc(name("fleet_round_memory_objects"),
			"Objects this replica runs, by how many rounds its fleet tracker keeps for each to read window holes by "+
				"(le_16, le_64, le_256, le_1440, gt_1440): every round from where the object's windows start, so the "+
				"distribution is the windows' spans in rounds.", []string{"rounds"}, nil),
		rounds: prometheus.NewDesc(name("fleet_round_memory_rounds"),
			"Rounds the fleet tracker keeps over every object this replica runs.", nil, nil),
		bytes: prometheus.NewDesc(name("fleet_round_memory_bytes"),
			"Bytes those rounds hold: the kept rounds' slices at their capacity, sixteen bytes a round.", nil, nil),
		maxRounds: prometheus.NewDesc(name("fleet_round_memory_max_rounds"),
			"The most rounds kept for any one object: the longest window span in rounds.", nil, nil),
		windowSized: prometheus.NewDesc(name("fleet_round_memory_window_sized_objects"),
			"Objects whose rounds are kept by the window start their worker reported rather than by the last "+
				"sixteen: every object once every worker reports it.", nil, nil),
		lineHeld: prometheus.NewDesc(name("fleet_round_memory_line_held_objects"),
			"Objects that let go a round their windows still name because the observation memory line refused "+
				"their rounds more room: their older holes read NOT_IN_MEMORY for the line, not for the window.", nil, nil),
	}
}

// SetRoundMemorySource binds the collector to the fleet tracker.
func (r *Recorder) SetRoundMemorySource(source func() fleet.RoundMemoryFacts) {
	if r == nil || r.phaseTwo.roundMemory == nil {
		return
	}
	r.phaseTwo.roundMemory.mu.Lock()
	r.phaseTwo.roundMemory.source = source
	r.phaseTwo.roundMemory.mu.Unlock()
}

func (c *roundMemoryCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.objects, c.rounds, c.bytes, c.maxRounds, c.windowSized, c.lineHeld} {
		ch <- desc
	}
}

func (c *roundMemoryCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	facts := fleet.RoundMemoryFacts{}
	if source != nil {
		facts = source()
	}
	for _, bucket := range fleet.RoundMemoryBuckets {
		ch <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(facts.Objects[bucket]), bucket)
	}
	ch <- prometheus.MustNewConstMetric(c.rounds, prometheus.GaugeValue, float64(facts.Rounds))
	ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(facts.Bytes))
	ch <- prometheus.MustNewConstMetric(c.maxRounds, prometheus.GaugeValue, float64(facts.MaxRounds))
	ch <- prometheus.MustNewConstMetric(c.windowSized, prometheus.GaugeValue, float64(facts.WindowSized))
	ch <- prometheus.MustNewConstMetric(c.lineHeld, prometheus.GaugeValue, float64(facts.HeldByLine))
}
