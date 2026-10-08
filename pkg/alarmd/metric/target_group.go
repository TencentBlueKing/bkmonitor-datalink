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

// TargetGroupStates are the states the dynamic group store counts its
// groups by: referenced by a Plan; loaded, a usable snapshot held; and
// unavailable, a snapshot that names why it cannot be used; failing, served
// past a refresh that could not read it; emptied_pending, an empty read held
// back until it is believed; emptied_held, held back as the writer's.
var TargetGroupStates = []string{"referenced", "loaded", "unavailable", "failing", "emptied_pending", "emptied_held"}

// TargetGroupReading is the dynamic group store as its health reads, by
// TargetGroupStates.
type TargetGroupReading struct {
	Groups map[string]int
	// RefreshFailed says the latest refresh failed: at the round trip, or
	// Redis answered some group's key with an error.
	RefreshFailed bool
	// UnansweredReads is how many group reads Redis has answered with an
	// error, every refresh's.
	UnansweredReads uint64
	// OldestFailingSeconds is how long the group served past failed
	// refreshes the longest has been; zero with none.
	OldestFailingSeconds float64
}

// targetGroupCollector reads the dynamic group store at scrape time. Every
// series is written, zero before the store is bound or where the deployment
// has none.
type targetGroupCollector struct {
	mu              sync.Mutex
	source          func() TargetGroupReading
	groups          *prometheus.Desc
	refreshFailed   *prometheus.Desc
	unansweredReads *prometheus.Desc
	oldestFailing   *prometheus.Desc
}

func newTargetGroupCollector() *targetGroupCollector {
	name := func(suffix string) string { return prometheus.BuildFQName(metricNamespace, metricSubsystem, suffix) }
	return &targetGroupCollector{
		groups: prometheus.NewDesc(name("target_group_groups"),
			"Dynamic groups the target group store holds, by state: referenced by a Plan; loaded, a usable snapshot "+
				"held; unavailable, a snapshot naming why it cannot be used; failing, served past a refresh that could "+
				"not read it (the replica's dependencies name each, since when and why); emptied_pending and "+
				"emptied_held, an empty read held back.", []string{"state"}, nil),
		refreshFailed: prometheus.NewDesc(name("target_group_refresh_failed"),
			"1 when the latest refresh of the dynamic groups failed: at the round trip, or Redis answered some "+
				"group's key with an error (LOADING, BUSY, a key of another type), which the group is not read from.", nil, nil),
		unansweredReads: prometheus.NewDesc(name("target_group_unanswered_reads_total"),
			"Group reads Redis answered with an error rather than the group's document; each such group keeps the "+
				"snapshot it had.", nil, nil),
		oldestFailing: prometheus.NewDesc(name("target_group_oldest_failing_seconds"),
			"How long the group served past failed refreshes the longest has been, since the first of them after "+
				"its last read; 0 with none.", nil, nil),
	}
}

// SetTargetGroupSource binds the collector to the dynamic group store.
func (r *Recorder) SetTargetGroupSource(source func() TargetGroupReading) {
	if r == nil || r.phaseTwo.targetGroup == nil {
		return
	}
	r.phaseTwo.targetGroup.mu.Lock()
	r.phaseTwo.targetGroup.source = source
	r.phaseTwo.targetGroup.mu.Unlock()
}

func (c *targetGroupCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.groups, c.refreshFailed, c.unansweredReads, c.oldestFailing} {
		ch <- desc
	}
}

func (c *targetGroupCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	reading := TargetGroupReading{}
	if source != nil {
		reading = source()
	}
	for _, state := range TargetGroupStates {
		ch <- prometheus.MustNewConstMetric(c.groups, prometheus.GaugeValue, float64(reading.Groups[state]), state)
	}
	failed := 0.0
	if reading.RefreshFailed {
		failed = 1
	}
	ch <- prometheus.MustNewConstMetric(c.refreshFailed, prometheus.GaugeValue, failed)
	ch <- prometheus.MustNewConstMetric(c.unansweredReads, prometheus.CounterValue, float64(reading.UnansweredReads))
	ch <- prometheus.MustNewConstMetric(c.oldestFailing, prometheus.GaugeValue, reading.OldestFailingSeconds)
}
