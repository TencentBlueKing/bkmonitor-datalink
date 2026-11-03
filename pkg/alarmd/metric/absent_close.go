// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
)

// absentCloseCollector reports the control leader's difference against the
// strategies that no longer exist: what each round decided, and the numbers
// it decided on.
//
// The second family is the point. A zero in the first one means "nothing was
// absent" on one round and "the round refused to decide" on another, and
// those need different people. The denominators say which.
type absentCloseCollector struct {
	mu         sync.Mutex
	outcomes   func() map[string]uint64
	rounds     func() map[string]uint64
	difference func() map[string]int
	outcomeIs  *prometheus.Desc
	roundIs    *prometheus.Desc
	sides      *prometheus.Desc
}

// differenceSides is the closed list of denominators, so every one of them
// has a cell from the first scrape.
var differenceSides = []string{"found_strategies", "discovery_searches", "discovery_tenants", "discovery_complete",
	"candidates", "snapshot_strategies", "remembered_identities",
	"snapshot_age_seconds", "max_snapshot_age_seconds"}

func newAbsentCloseCollector() *absentCloseCollector {
	return &absentCloseCollector{
		outcomeIs: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "absent_strategy_close_total"),
			"What the control leader's difference against disabled or deleted strategies decided, by outcome. "+
				"Counted per strategy. close_decided counts decisions and close_sent counts, once per strategy's "+
				"close decision, a decision that sent at least one real close whose items the link answered (closed, "+
				"refused or busy, item by item), so a strategy the link refuses every round is sent every round; "+
				"neither says the link closed anything, which is read per alert on the link (linkd.alerts); a "+
				"strategy whose absent.list sends keep growing while its row stays is one the link is sent and does "+
				"not close. "+
				"identity_unknown is a strategy decided and not sent: its id is not a positive integer, or there is no "+
				"link to close through. revision_unknown, business_unknown, evidence_unavailable, evidence_truncated, "+
				"send_failed, producer_foreign and producer_unknown name the close as first designed, which read the "+
				"strategy's alerts and sent through the event producer; the filtered close counts none of them, and "+
				"they stay zero. "+
				"Two paths are counted apart: a strategy with no mark is within_grace until three rounds found it "+
				"missing (rounds, not time), unconfirmed until two observations did, then close_decided; one the "+
				"strategy list's writer marked disabled or deleted (departed_strategies) is departed_waiting on its "+
				"first round under that mark, then departed_close_decided, and is re-checked just before its own close: "+
				"departed_reappeared (listed again) or departed_mark_changed (the mark gone or moved) is not closed. "+
				"marks_unreadable (once a round, the round's read or a re-check's; the implicit path then) and "+
				"mark_invalid (per mark: not decoded, or naming another tenant) are the marks' reads. The candidates are "+
				"kept across leader terms under the Control Leader's lease: memory_load_failed (per term: the term starts "+
				"from nothing), memory_invalid (per kept candidate that did not decode), memory_save_failed (per round), "+
				"and memory_fenced (the lease is gone; that round sends nothing). In a new term both paths wait for the "+
				"term's own second round, and a kept candidate is confirmed again under two observations of the term's own. "+
				"Why a whole round "+
				"decided nothing is "+
				"absent_strategy_round_total, not a cell here. Every cell exists from the start so a zero is a "+
				"reading and not an absence.", []string{"outcome"}, nil),
		roundIs: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "absent_strategy_round_total"),
			"How each round of the difference ended: none is a round that decided, and the rest name the fact "+
				"that was not good enough to decide on - link_unavailable (the alert link's search could not be "+
				"read or its control plane is not named), snapshot_unusable (the source was not observed this round), snapshot_empty, "+
				"snapshot_stale and writer_unstated (the strategy list's writer made no publication_semantics statement "+
				"about this list: a strategy missing from it is not known to be gone, so nothing is closed). Rounds, not strategies. none is the denominator the outcome family is read against.",
			[]string{"disposition"}, nil),
		sides: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "absent_strategy_difference"),
			"The sizes the last round decided on: found_strategies is the strategies the alert link's search "+
				"found with an active alert outside the running set, discovery_searches and discovery_tenants the "+
				"searches that took and the tenants walked, discovery_complete whether every tenant was walked (1) "+
				"or a search failed (0), candidates the difference itself, snapshot_strategies the strategy cache it "+
				"was judged against, remembered_identities the strategies the catalog let go and still knows the "+
				"business of. snapshot_age_seconds is reported beside its bound max_snapshot_age_seconds, so a "+
				"refused round can be read as the source falling behind rather than as a bound that does not fit.",
			[]string{"side"}, nil),
	}
}

// SetAbsentCloseSource binds the collector to the loop's counts. Safe before
// or after registration; a nil recorder is a no-op.
func (r *Recorder) SetAbsentCloseSource(outcomes func() map[string]uint64, rounds func() map[string]uint64, difference func() map[string]int) {
	if r == nil || r.phaseTwo.absentClose == nil {
		return
	}
	r.phaseTwo.absentClose.mu.Lock()
	r.phaseTwo.absentClose.outcomes = outcomes
	r.phaseTwo.absentClose.rounds = rounds
	r.phaseTwo.absentClose.difference = difference
	r.phaseTwo.absentClose.mu.Unlock()
}

func (c *absentCloseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outcomeIs
	ch <- c.roundIs
	ch <- c.sides
}

func (c *absentCloseCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	outcomes, rounds, difference := c.outcomes, c.rounds, c.difference
	c.mu.Unlock()
	if outcomes == nil || rounds == nil || difference == nil {
		return
	}
	counts := outcomes()
	for _, outcome := range absentalerts.Outcomes {
		ch <- prometheus.MustNewConstMetric(c.outcomeIs, prometheus.CounterValue, float64(counts[outcome]), outcome)
	}
	dispositions := rounds()
	for _, refusal := range absentalerts.Refusals {
		ch <- prometheus.MustNewConstMetric(c.roundIs, prometheus.CounterValue, float64(dispositions[refusal]), refusal)
	}
	sizes := difference()
	for _, side := range differenceSides {
		ch <- prometheus.MustNewConstMetric(c.sides, prometheus.GaugeValue, float64(sizes[side]), side)
	}
}
