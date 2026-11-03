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
			"What the effective-time maintenance did, by outcome. close_acked counts alerts, one per alert the "+
				"broker acknowledged a close for: the close was sent, at every level of the strategy (__ALL__); "+
				"whether the alert closed is the consumer's to say. The others count "+
				"events: maintenance_busy is a close that could not take the Query Group's flight because a Slot was "+
				"executing (one is nothing, a steady rate is a Query Group whose close never happens); "+
				"close_precheck_failed is the owner or content check refusing before a send; close_send_failed is "+
				"the producer not acknowledging; maintenance_plan_uncompilable is an activated Plan the maintenance "+
				"could not compile and so cannot judge; unavailable is a Query Group whose Plans could not be read "+
				"or whose owner stopped accepting during the read - an owner not accepting when the step begins is a "+
				"handover and is skipped uncounted, and a Console that does not answer is not one; source_last_known is a "+
				"step whose read of the event source failed or was backed off and whose closes went on under the last "+
				"source known, once per step; source_unknown is a step whose closes were deferred because no source "+
				"has been known yet, once per step; effective_time_unknown, legacy_effective_time_unavailable and "+
				"close_identity_invalid name the judgement that could not be made; calendar_deletion_unsettled is a "+
				"close held back because a calendar the strategy reads deleted has not settled as a deletion: read "+
				"deleted for too short a time, read present elsewhere, or every calendar this replica's strategies "+
				"name read deleted at once, a calendar source gone; close_boundary_active is a Plan the recheck just "+
				"before a send found active again, its batch not sent while the Query Group's other Plans go on "+
				"(effective_time_unknown counts the same recheck finding the time unreadable); close_business_unknown "+
				"counts alerts of a global business Plan whose record names no business they were filed under, or that "+
				"were addressed from the index set, which names none - not closed that round; evidence_unavailable is "+
				"a Plan outside its effective time whose open alerts could not be listed because its set has no current "+
				"calibration (never calibrated, past its age, failing, or truncated with nothing listed) and its index "+
				"set was not read fresh either, once per Plan per step - a calibrated set with no alerts is not counted; "+
				"index_set_close_acked counts, among close_acked, alerts closed by fingerprint from the index set's "+
				"members because the set had no current calibration to list them; index_set_group_draining is such a "+
				"Plan whose set was held back instead, its Query Group in the current active set and in the draining "+
				"projection at once, once per Plan per step. Every cell exists from the "+
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
