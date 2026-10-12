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

// timelineRepairCollector reads the Control Leader's unreadable-timeline
// repairs at scrape time. Every outcome is emitted, zero included.
type timelineRepairCollector struct {
	mu      sync.Mutex
	source  func() map[controlplane.TimelineRepairOutcome]uint64
	repairs *prometheus.Desc
}

func newTimelineRepairCollector() *timelineRepairCollector {
	return &timelineRepairCollector{repairs: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "timeline_repairs_total"),
		"Schedule timelines the Control Leader looked at because a Worker reported them unreadable (SCHEDULE_UNREADABLE), "+
			"or because a cutover read bytes that did not decode, by what became of each. rewritten: still unreadable, "+
			"rewritten as one Segment from the Leader's boundary on the content and the activation records the Query "+
			"Group runs, marked so its Worker records the Slots lost with the old timeline (SCHEDULE_REPAIRED). "+
			"decodes_again: the Leader decoded it and left it; a Worker's report outlives the rewrite until its next Slot "+
			"runs, and a Worker of another build may report a timeline this one reads. conflict: the rewrite lost its "+
			"compare-and-set and wrote nothing; the report stands and the next round tries again. failed: the replacement "+
			"could not be built; a line names the Query Group and why. other_schema: the bytes are a timeline of another "+
			"schema version, written by a build that reads them, and left alone. Counted by the Leader; read it summed "+
			"over replicas.", []string{"outcome"}, nil)}
}

// SetTimelineRepairSource binds the collector to the repository's counts.
func (r *Recorder) SetTimelineRepairSource(source func() map[controlplane.TimelineRepairOutcome]uint64) {
	if r == nil || r.phaseTwo.timelineRepair == nil {
		return
	}
	r.phaseTwo.timelineRepair.mu.Lock()
	r.phaseTwo.timelineRepair.source = source
	r.phaseTwo.timelineRepair.mu.Unlock()
}

func (c *timelineRepairCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.repairs }

func (c *timelineRepairCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var counts map[controlplane.TimelineRepairOutcome]uint64
	if source != nil {
		counts = source()
	}
	for _, outcome := range controlplane.TimelineRepairOutcomes {
		ch <- prometheus.MustNewConstMetric(c.repairs, prometheus.CounterValue, float64(counts[outcome]), string(outcome))
	}
}

func newTimelineUnreadableReports() prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "timeline_unreadable_reports_total",
		Help: "Owned Query Groups this replica began naming on its registration because a round met a Schedule timeline " +
			"that does not decode (SCHEDULE_UNREADABLE): one per Query Group each time it goes from unnamed to named. " +
			"The name is dropped by the first later round of that Query Group that runs a Slot, or when it leaves this " +
			"replica. A leader reads each named timeline again and rewrites it (timeline_repairs_total). A count that " +
			"keeps rising for the same Query Groups is a rewrite that does not take." +
			" Reader: the release acceptance, from /metrics after each deploy.",
	})
}

// RecordTimelineUnreadableReported counts one Query Group newly named as
// having an unreadable timeline.
func (r *Recorder) RecordTimelineUnreadableReported() {
	if r == nil || r.phaseTwo.timelineUnreadableReports == nil {
		return
	}
	r.phaseTwo.timelineUnreadableReports.Inc()
}
