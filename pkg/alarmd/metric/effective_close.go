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

// effectiveCloseCollector reports the effective-time maintenance's outcome
// counts at scrape time, one cell per outcome of the closed list, every cell
// present from the first scrape after the source is bound. The counts are
// the maintenance loop's own; nothing here is incremented on a path.
type effectiveCloseCollector struct {
	mu       sync.Mutex
	source   func() map[string]uint64
	outcomes *prometheus.Desc
}

func newEffectiveCloseCollector() *effectiveCloseCollector {
	return &effectiveCloseCollector{
		outcomes: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "effective_close_total"),
			"What the effective-time maintenance did, by outcome. An inactive Plan's alerts are closed by the link's "+
				"filter close, one close per Plan and version: close_done is one the filter ran (done, or carried on "+
				"by the next step), close_nothing_matched one whose dry run matched no active alert of the Plan's "+
				"version, close_not_settled one the link did not settle this step (busy, failed, refused, past its "+
				"expectation; the linkd filter close metric names which), tried again by the next step; the alerts "+
				"themselves are counted by the filter close's items. filter_close_unsupported is the link answering "+
				"501, 404 or 405 to a filtered close, and close_unrouted an inactive Plan on a deployment that names "+
				"no link control plane: in both nothing closes the Plan's alerts. The others count events: "+
				"close_precheck_failed is the owner and content check refusing before a send (the fence, the content "+
				"scope or the timeline moved); maintenance_busy is a close that could not take the Query Group's flight "+
				"because a Slot was executing (one is nothing, a steady rate is a Query Group whose close never "+
				"happens); maintenance_plan_uncompilable is an activated Plan the maintenance could not compile and so "+
				"cannot judge; unavailable is a Query Group whose Plans could not be read or whose owner stopped "+
				"accepting during the read - an owner not accepting when the step begins is a handover and is skipped "+
				"uncounted; view_not_executable is a Query Group whose Plans the executable view does not allow "+
				"reading yet (not installed, or the lease behind it), a rollout's shape rather than a store failure; group_retiring is a Query Group leaving the catalog (its timeline retired), read once per lease rather than every second; "+
				"unsupported_runner is a Query Group whose owner runner cannot run the maintenance; effective_time_unknown, "+
				"legacy_effective_time_unavailable and close_identity_invalid name the judgement that could not be "+
				"made; calendar_deletion_unsettled is a close held back because a calendar the strategy reads deleted "+
				"has not settled as a deletion: read deleted for too short a time, read present elsewhere, or every "+
				"calendar this replica's strategies name read deleted at once, a calendar source gone; "+
				"close_boundary_active is a Plan the recheck just before a send found active again, its close not sent "+
				"while the Query Group's other Plans go on (effective_time_unknown counts the same recheck finding the "+
				"time unreadable). Every cell exists from the "+
				"start so a zero is a reading and not an absence.", []string{"outcome"}, nil),
	}
}

// SetEffectiveCloseSource binds the collector to the maintenance loop's
// counts. Safe before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetEffectiveCloseSource(source func() map[string]uint64) {
	if r == nil || r.phaseTwo.effectiveClose == nil {
		return
	}
	r.phaseTwo.effectiveClose.mu.Lock()
	r.phaseTwo.effectiveClose.source = source
	r.phaseTwo.effectiveClose.mu.Unlock()
}

func (c *effectiveCloseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outcomes
}

func (c *effectiveCloseCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	counts := source()
	for _, outcome := range observability.EffectiveCloseOutcomes {
		ch <- prometheus.MustNewConstMetric(c.outcomes, prometheus.CounterValue, float64(counts[string(outcome)]), string(outcome))
	}
}
