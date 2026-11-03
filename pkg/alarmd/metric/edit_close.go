// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
)

// editCloseCollector reports the control leader's close of the alerts an
// edit of their strategy orphaned, by outcome.
type editCloseCollector struct {
	mu        sync.Mutex
	outcomes  func() map[string]uint64
	outcomeIs *prometheus.Desc
}

func newEditCloseCollector() *editCloseCollector {
	return &editCloseCollector{
		outcomeIs: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "strategy_edit_close_total"),
			"What the control leader's close of alerts orphaned by an edit of their strategy came to, by outcome. "+
				"Per listed alert: current_revision (raised under the strategy's current revision, not a candidate), "+
				"revision_ahead, revision_unknown, family_unknown, kept (a candidate every decidable rule keeps), "+
				"levels_partial, severity_unmapped and no_data_suspended (a rule could not decide it; not closed), "+
				"and the closes decided - metric_changed, condition_changed, dimensions_changed, level_removed, "+
				"no_data_disabled; remembered counts, among those, the ones taken from memory without reading the record "+
				"again. Beside a decision, facts_missing, facts_unknown_schema and plan_facts_missing count candidates "+
				"the metric and condition rules could not judge: the alert's opening event carried no strategy facts "+
				"(every alert opened before events carried them), carried a schema this build does not read, or the "+
				"current Plans have none; without facts a grouping is judged only for a dimension removed or renamed. "+
				"condition_rule_held counts no-data candidates whose condition digest changed while the condition rule is "+
				"held (it closes nothing until the publisher states what a catalog metric's snapshot conditions are), "+
				"snapshot_unreadable those whose digest was taken beside a metric snapshot whose conditions could not be "+
				"read. close_sent and send_failed "+
				"count closes the producer accepted and refused, business_unknown closes not sent for want of a business, "+
				"record_unreadable alerts whose record could not be read (read again later), read_deferred alerts not "+
				"read because the round's reads were spent, producer_foreign and producer_unknown alerts of another "+
				"source or none. Per strategy: strategy_unpublished, plan_retained, plans_disagree, plan_unrevisioned, "+
				"evidence_unavailable, evidence_truncated. Per round: not_leader, publication_unavailable, "+
				"roster_unavailable. memory_full counts drops of the decision memory. Every cell exists from the start "+
				"so a zero is a reading and not an absence.", []string{"outcome"}, nil),
	}
}

// SetEditCloseSource binds the collector to the loop's counts. Safe before
// or after registration; a nil recorder is a no-op.
func (r *Recorder) SetEditCloseSource(outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.editClose == nil {
		return
	}
	r.phaseTwo.editClose.mu.Lock()
	r.phaseTwo.editClose.outcomes = outcomes
	r.phaseTwo.editClose.mu.Unlock()
}

func (c *editCloseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outcomeIs
}

func (c *editCloseCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	outcomes := c.outcomes
	c.mu.Unlock()
	if outcomes == nil {
		return
	}
	counts := outcomes()
	for _, outcome := range editclose.Outcomes {
		ch <- prometheus.MustNewConstMetric(c.outcomeIs, prometheus.CounterValue, float64(counts[outcome]), outcome)
	}
}
