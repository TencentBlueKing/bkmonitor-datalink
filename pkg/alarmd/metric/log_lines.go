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

// logLinesCollector reports, at scrape time, how many lines the process's
// log observer wrote, how many its limiter held back and how many routine
// lines it counted without writing, by stage. Every stage of the closed list
// is present from the first scrape after the source is bound, so a zero is a
// reading; unwritten is present for the workflow stages, the only ones that
// have such lines.
type logLinesCollector struct {
	mu     sync.Mutex
	source func() observability.LogLineCounts
	lines  *prometheus.Desc
}

func newLogLinesCollector() *logLinesCollector {
	return &logLinesCollector{lines: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "log_lines_total"),
		"Lines the process's log observer wrote (admission=written), held back (admission=limited) or counted without "+
			"writing (admission=unwritten: a routine stage's success, a Control Leader round that changed nothing), by "+
			"stage; a stage outside the closed list counts as _other. Which stage fills the log is read here rather than "+
			"from the log, which a busy Pod keeps for minutes. "+
			"Read the three together as the check that a stage runs at all: Slots starting (slot_started moving) while a "+
			"routine stage moves under none of them is a stage that is not wired. Stages the observer never writes by "+
			"design -- scheduler waits and snapshots, catalog object reads -- are not counted: they are metrics only.",
		[]string{"stage", "admission"}, nil)}
}

// SetLogLineSource binds the collector to the log observer's counts. Safe
// before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetLogLineSource(source func() observability.LogLineCounts) {
	if r == nil || r.phaseTwo.logLines == nil {
		return
	}
	r.phaseTwo.logLines.mu.Lock()
	r.phaseTwo.logLines.source = source
	r.phaseTwo.logLines.mu.Unlock()
}

func (c *logLinesCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lines
}

func (c *logLinesCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	counts := source()
	for stage, n := range counts.Written {
		ch <- prometheus.MustNewConstMetric(c.lines, prometheus.CounterValue, float64(n), string(stage), "written")
	}
	for stage, n := range counts.Limited {
		ch <- prometheus.MustNewConstMetric(c.lines, prometheus.CounterValue, float64(n), string(stage), "limited")
	}
	for stage, n := range counts.Unwritten {
		ch <- prometheus.MustNewConstMetric(c.lines, prometheus.CounterValue, float64(n), string(stage), "unwritten")
	}
}
