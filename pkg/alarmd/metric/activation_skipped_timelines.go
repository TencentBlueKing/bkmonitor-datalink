// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// activationSkippedTimelinesCollector reads, at scrape time, the timelines
// the Control Leader's activation read left out. Every reason is emitted,
// zero included.
type activationSkippedTimelinesCollector struct {
	mu      sync.Mutex
	source  func() controlplane.SkippedTimelinesReading
	skipped *prometheus.Desc
}

func newActivationSkippedTimelinesCollector() *activationSkippedTimelinesCollector {
	return &activationSkippedTimelinesCollector{skipped: prometheus.NewDesc(
		prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_timelines_skipped_total"),
		"Schedule timelines the Control Leader left out when it read the activation's Plan records back from the open "+
			"Segments, by why: one per Query Group per activation it read. The rest of the activation loads and is "+
			"cut over as usual. undecodable: the bytes do not decode; the Leader rebuilds that Query Group's records "+
			"from the publication it runs and rewrites the timeline (timeline_repairs_total). newer_format: the bytes "+
			"are a timeline of another schema version, written by a build that reads them, and are left alone. absent: "+
			"the key is not there at all; the Leader rebuilds that Query Group's records and opens the timeline again "+
			"(timeline_repairs_total{reopened}). A read "+
			"that fails is never counted here: it fails the activation read as before. The fleet's activation row names "+
			"the Query Groups. Counted by the Leader; read it summed over replicas."+
			" Reader: the release acceptance, from /metrics after each deploy.",
		[]string{"reason"}, nil)}
}

// SetActivationSkippedTimelinesSource binds the collector to the repository's
// reading.
func (r *Recorder) SetActivationSkippedTimelinesSource(source func() controlplane.SkippedTimelinesReading) {
	if r == nil || r.phaseTwo.activationSkippedTimelines == nil {
		return
	}
	r.phaseTwo.activationSkippedTimelines.mu.Lock()
	r.phaseTwo.activationSkippedTimelines.source = source
	r.phaseTwo.activationSkippedTimelines.mu.Unlock()
}

func (c *activationSkippedTimelinesCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.skipped }

func (c *activationSkippedTimelinesCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var reading controlplane.SkippedTimelinesReading
	if source != nil {
		reading = source()
	}
	for _, reason := range controlplane.SkippedTimelineReasons {
		ch <- prometheus.MustNewConstMetric(c.skipped, prometheus.CounterValue, float64(reading.Counts[reason]), string(reason))
	}
}
