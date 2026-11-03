// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// targetScopeCloseCollector reports the superseded-identity close, every
// outcome from the first scrape. The family keeps the name of the per-series
// target-scope close it used to count as well.
type targetScopeCloseCollector struct {
	mu        sync.Mutex
	outcomes  func() map[string]uint64
	outcomeIs *prometheus.Desc
}

func newTargetScopeCloseCollector() *targetScopeCloseCollector {
	return &targetScopeCloseCollector{
		outcomeIs: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "target_scope_close_total"),
			"What the close of alerts a series opened under the fingerprint of its own address, before its alert "+
				"identity read CMDB's (reason superseded_identity), decided, by outcome. The admission step offers the "+
				"old fingerprint of every admitted series whose host overlay moves it, every Slot, for a strategy alarmd's "+
				"copy of the link's active alerts knows an open alert of; an offer the copy has no word on or judges not "+
				"open counts nothing. unconfirmed is an old fingerprint of an open alert offered by one Slot, waiting for "+
				"a second; close_sent counts closes sent and acknowledged by the broker, one per alert: the broker's "+
				"acknowledgement, not the link's, so it includes closes the link finds no active alert for (orphaned "+
				"there); superseded_identity_close_sent counts the same closes by their reason; send_failed closes the "+
				"link did not settle, kept for the next step; filter_close_unsupported closes the link cannot run (501, "+
				"404, 405); unrouted confirmed closes on a replica with no link to close through; close_identity_invalid "+
				"closes whose request could not be built from the observation's facts (strategy id or revision "+
				"unusable), nothing sent, the word the effective-time close uses for the same facts; memory_full first "+
				"observations past the table's bound; stale_deferred closes held back because the last observation was "+
				"older than the freshness bound; observation_expired first observations whose second did not arrive "+
				"within two of the strategy's periods plus its Slot's read offset (at least half an hour), forgotten. "+
				"Alerts whose target left the strategy's scope are closed by the control leader, per strategy "+
				"(strategy_scope_close_total). Every cell exists from the start so a zero is a reading and not an absence.",
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
