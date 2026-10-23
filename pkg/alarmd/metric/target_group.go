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
// unavailable, a snapshot that names why it cannot be used; failing, held
// while the refreshes fail, which a refresh does for every group or none.
var TargetGroupStates = []string{"referenced", "loaded", "unavailable", "failing"}

// TargetGroupReading is the dynamic group store as its health reads, by
// TargetGroupStates.
type TargetGroupReading struct {
	Groups map[string]int
	// RefreshFailed says the latest refresh failed: at the round trip, or
	// Redis answered some group's key with an error.
	RefreshFailed bool
}

// targetGroupCollector reads the dynamic group store at scrape time. Every
// series is written, zero before the store is bound or where the deployment
// has none.
type targetGroupCollector struct {
	mu            sync.Mutex
	source        func() TargetGroupReading
	groups        *prometheus.Desc
	refreshFailed *prometheus.Desc
}

func newTargetGroupCollector() *targetGroupCollector {
	name := func(suffix string) string { return prometheus.BuildFQName(metricNamespace, metricSubsystem, suffix) }
	return &targetGroupCollector{
		groups: prometheus.NewDesc(name("target_group_groups"),
			"Dynamic groups the target group store holds, by state: referenced by a Plan; loaded, a usable snapshot "+
				"held; unavailable, a snapshot naming why it cannot be used; failing, held while the refreshes fail, "+
				"which a refresh does for every group or none (the replica's dependencies say since when and why). A "+
				"group the writer states empty is loaded, with no members.", []string{"state"}, nil),
		refreshFailed: prometheus.NewDesc(name("target_group_refresh_failed"),
			"1 when the latest refresh of the dynamic groups failed: at the round trip, or Redis answered some "+
				"group's key with an error (LOADING, BUSY, a key of another type); every group keeps the snapshot it had.", nil, nil),
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
	for _, desc := range []*prometheus.Desc{c.groups, c.refreshFailed} {
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
}
