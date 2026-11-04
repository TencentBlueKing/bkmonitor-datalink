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

// activationRebuildCollector reads the Control Leader's activation body
// rebuilds at scrape time. Every outcome is emitted, zero included: a rebuild
// that never happened reads as zero, not as a missing series. So is every
// reason a written rebuild kept a Query Group without reading its timeline.
type activationRebuildCollector struct {
	mu           sync.Mutex
	source       func() map[controlplane.ActivationRebuildOutcome]uint64
	unreadSource func() map[controlplane.SkippedTimelineReason]uint64
	rebuilds     *prometheus.Desc
	unread       *prometheus.Desc
}

func newActivationRebuildCollector() *activationRebuildCollector {
	return &activationRebuildCollector{rebuilds: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "activation_rebuild_total"),
		"Activation bodies the Control Leader rebuilt under a header present without its body, by outcome. "+
			"rebuilt: written back byte for byte from the copy last read under the same header. "+
			"rebuilt_draining_unknown: rebuilt from the open Segments without that copy; the Plans are exact, "+
			"and Query Groups a cutover removed that had not drained stop executing now rather than at their boundary. "+
			"not_needed: the body was back or the header gone. conflict: another writer moved first; nothing written. "+
			"Either rebuilt outcome may keep Query Groups whose timeline it could not read; they are counted in "+
			"alarmd_activation_rebuild_unread_query_groups_total. "+
			"coverage_invalid, header_unparsable, header_pending: refused by name, nothing written, "+
			"and the fleet stays without an activation until the cause is fixed.", []string{"outcome"}, nil),
		unread: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "activation_rebuild_unread_query_groups_total"),
			"Query Groups of the current publication an activation body rebuild wrote without reading their timeline, by reason "+
				"(absent: no key; undecodable: bytes that do not decode; newer_format: a timeline of a later schema). "+
				"Each keeps its place in the active set, or in Draining where the copy last read held it, so no reader "+
				"takes it for a Query Group that left; every other Query Group's body is written back. "+
				"The activation reconciler then repairs absent and undecodable ones (alarmd_timeline_repairs_total).",
			[]string{"reason"}, nil)}
}

// SetActivationRebuildSource binds the collector to the repository's counts.
func (r *Recorder) SetActivationRebuildSource(source func() map[controlplane.ActivationRebuildOutcome]uint64) {
	if r == nil || r.phaseTwo.activationRebuild == nil {
		return
	}
	r.phaseTwo.activationRebuild.mu.Lock()
	r.phaseTwo.activationRebuild.source = source
	r.phaseTwo.activationRebuild.mu.Unlock()
}

// SetActivationRebuildUnreadSource binds the collector to the repository's
// unread Query Group counts.
func (r *Recorder) SetActivationRebuildUnreadSource(source func() map[controlplane.SkippedTimelineReason]uint64) {
	if r == nil || r.phaseTwo.activationRebuild == nil {
		return
	}
	r.phaseTwo.activationRebuild.mu.Lock()
	r.phaseTwo.activationRebuild.unreadSource = source
	r.phaseTwo.activationRebuild.mu.Unlock()
}

func (c *activationRebuildCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rebuilds
	ch <- c.unread
}

func (c *activationRebuildCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, unreadSource := c.source, c.unreadSource
	c.mu.Unlock()
	var counts map[controlplane.ActivationRebuildOutcome]uint64
	if source != nil {
		counts = source()
	}
	for _, outcome := range controlplane.ActivationRebuildOutcomes {
		ch <- prometheus.MustNewConstMetric(c.rebuilds, prometheus.CounterValue, float64(counts[outcome]), string(outcome))
	}
	var unread map[controlplane.SkippedTimelineReason]uint64
	if unreadSource != nil {
		unread = unreadSource()
	}
	for _, reason := range controlplane.SkippedTimelineReasons {
		ch <- prometheus.MustNewConstMetric(c.unread, prometheus.CounterValue, float64(unread[reason]), string(reason))
	}
}
