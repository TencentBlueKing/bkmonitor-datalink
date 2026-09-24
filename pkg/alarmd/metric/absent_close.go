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
var differenceSides = []string{"roster_strategies", "roster_unreadable", "roster_pages", "roster_complete",
	"candidates", "snapshot_strategies", "remembered_identities", "send_armed",
	"snapshot_age_seconds", "max_snapshot_age_seconds", "link_health_age_seconds", "max_link_health_age_seconds",
	"link_pending"}

func newAbsentCloseCollector() *absentCloseCollector {
	return &absentCloseCollector{
		outcomeIs: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "absent_strategy_close_total"),
			"What the control leader's difference against disabled or deleted strategies decided, by outcome. "+
				"Counted per strategy except alert_closed, send_failed, would_send, producer_foreign and "+
				"producer_unknown, which count alerts. closed counts decisions and alert_closed counts what went "+
				"out: while the close is not armed (absent_strategy_difference side=send_armed is 0) closed rises, "+
				"alert_closed stays at zero, and would_send counts the alerts arming would have sent. "+
				"identity_unknown and revision_unknown are strategies decided and not sent because no business or "+
				"revision could be found for them. Why a whole round decided nothing is "+
				"absent_strategy_round_total, not a cell here. Every cell exists from the start so a zero is a "+
				"reading and not an absence.", []string{"outcome"}, nil),
		roundIs: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "absent_strategy_round_total"),
			"How each round of the difference ended: none is a round that decided, and the rest name the fact "+
				"that was not good enough to decide on - link_unavailable (the alert link's roster could not be "+
				"read), link_unhealthy (the link says its own set maintenance is failing or has not succeeded "+
				"recently), snapshot_unusable (the source was not observed this round), snapshot_empty, "+
				"snapshot_stale and snapshot_shrunk (the strategy list itself lost a large share of its "+
				"entries). Rounds, not strategies. none is the denominator the outcome family is read against.",
			[]string{"disposition"}, nil),
		sides: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "absent_strategy_difference"),
			"The sizes the last round decided on: roster_strategies is the strategies the alert link listed "+
				"with an unrecovered alert, roster_unreadable the ones it listed and could not read, roster_pages "+
				"and roster_complete how far the walk of its roster got, candidates the difference itself, "+
				"snapshot_strategies the strategy cache it was judged against, remembered_identities "+
				"the strategies the catalog let go and still knows the business of, and send_armed whether this "+
				"deployment has armed the close at all (0 means every decision is reported and none is sent). "+
				"Each age is reported beside its bound - snapshot_age_seconds beside max_snapshot_age_seconds, "+
				"link_health_age_seconds (since the link's last successful discovery) beside "+
				"max_link_health_age_seconds - so a refused round can be read as the side falling behind rather "+
				"than as a bound that does not fit. link_pending is the link's own refresh backlog.",
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
