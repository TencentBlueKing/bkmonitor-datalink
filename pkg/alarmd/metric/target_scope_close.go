// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// targetScopeCloseCollector reports the close of alerts whose target left
// the strategy's monitoring scope, every outcome from the first scrape.
type targetScopeCloseCollector struct {
	mu        sync.Mutex
	outcomes  func() map[string]uint64
	outcomeIs *prometheus.Desc
}

func newTargetScopeCloseCollector() *targetScopeCloseCollector {
	return &targetScopeCloseCollector{
		outcomeIs: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "target_scope_close_total"),
			"What the close of alerts whose target left the strategy's monitoring scope decided, by outcome. "+
				"unconfirmed is a fingerprint of an open alert turned away by one Slot, waiting for a second; "+
				"closed counts alerts a close was sent for, and would_send the ones decided while the close is "+
				"not armed (absent_close_send false). cache_unavailable counts rejections decided on facts that "+
				"were not all read or current, which are never closed; not_member definitive rejections whose "+
				"fingerprint is not an open alert; set_unavailable decisions refused because the open set could "+
				"not be judged (not calibrated, disjoint, unavailable); producer_foreign open alerts of another "+
				"source; send_failed closes the producer refused; memory_full first observations past the "+
				"table's bound; fingerprint_unsupported definitive rejections of Plans fed by several inputs, "+
				"whose alert fingerprint no single series carries; stale_deferred closes held back because the "+
				"last observation was older than the freshness bound; indefinite rejections that are not a verdict on "+
				"the record's place at all (a key or object identity that could not be built), apart from "+
				"cache_unavailable, which is a verdict reached without its facts. Every cell exists from the start so a zero is a reading and not an absence.",
			[]string{"outcome"}, nil),
	}
}

// SetTargetScopeCloseSource binds the collector to the close's counts. Safe
// before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetTargetScopeCloseSource(outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.targetScopeClose == nil {
		return
	}
	r.phaseTwo.targetScopeClose.mu.Lock()
	r.phaseTwo.targetScopeClose.outcomes = outcomes
	r.phaseTwo.targetScopeClose.mu.Unlock()
}

func (c *targetScopeCloseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outcomeIs
}

func (c *targetScopeCloseCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	outcomes := c.outcomes
	c.mu.Unlock()
	var counts map[string]uint64
	if outcomes != nil {
		counts = outcomes()
	}
	for _, outcome := range scopeclose.Outcomes {
		ch <- prometheus.MustNewConstMetric(c.outcomeIs, prometheus.CounterValue, float64(counts[outcome]), outcome)
	}
}
