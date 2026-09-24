// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// ViewStreamCounts is the Control Leader's account of decision-016's view
// stream: the four numbers of the version it follows now, the receipts it
// could not attribute, and what it sent. Read only on the Leader; a
// process that is not leading reports its leading gauge as 0 and nothing
// else, because the four numbers are a Leader's and a follower has none.
type ViewStreamCounts struct {
	Leading  bool
	Revision uint64
	Sessions int
	// The four numbers and their denominator for the current version.
	Expected, Sent, Acked, Installed, Switched int
	// Receipts ignored since the term began, by why.
	IgnoredUnknownVersion, IgnoredUnexpectedReceiver, IgnoredDigestMismatch, IgnoredStaleIncarnation int
	// Counters since the process started.
	Publications, PublicationsSkipped, SnapshotChunksSent, DeltasSent, EmptyDeltasSent, DeltasOversized, Refusals uint64
	// PublishFailures is every desired set the Leader could not publish, by
	// why, every reason present; PublishFailingSeconds how long the current
	// run of failures has lasted (0 while publishing works); and
	// NoSessionsSeconds how long Workers have been expected with none
	// holding a stream (0 otherwise).
	PublishFailures       map[string]uint64
	PublishFailingSeconds float64
	NoSessionsSeconds     float64
}

type viewStreamCollector struct {
	mu           sync.Mutex
	source       func() ViewStreamCounts
	leading      *prometheus.Desc
	revision     *prometheus.Desc
	sessions     *prometheus.Desc
	receivers    *prometheus.Desc
	ignored      *prometheus.Desc
	publications *prometheus.Desc
	sent         *prometheus.Desc
	oversized    *prometheus.Desc
	refusals     *prometheus.Desc
	failures     *prometheus.Desc
	failing      *prometheus.Desc
	noSessions   *prometheus.Desc
}

func newViewStreamCollector() *viewStreamCollector {
	name := func(suffix string) string { return prometheus.BuildFQName(metricNamespace, metricSubsystem, suffix) }
	return &viewStreamCollector{
		leading: prometheus.NewDesc(name("view_stream_leading"),
			"1 while this process is the Control Leader of the view stream and holds a publisher for its term, else 0. "+
				"Every other view_* series is the Leader's and is absent on a follower.", nil, nil),
		revision: prometheus.NewDesc(name("view_revision"),
			"The term's current transport revision: one for the whole fleet, advanced when any Worker's projection "+
				"of the desired set changed. Flat while nothing changes; a step per publication that moved something.", nil, nil),
		sessions: prometheus.NewDesc(name("view_stream_sessions"),
			"Workers with an open stream to this Leader. Read it against the number of ready Workers: the gap is "+
				"Workers not connected, which in the shadow step is a Worker that cannot reach or is refused by the Leader.", nil, nil),
		receivers: prometheus.NewDesc(name("view_version_receivers"),
			"Receivers of the current view revision by stage: expected is the set frozen when the revision was published, "+
				"sent handed the complete message to the transport, acked received it, installed verified and installed it, "+
				"switched executes under it. 0 <= switched <= installed <= acked <= sent <= expected, each receiver once per "+
				"stage. In the shadow step switched stays 0 by design: nothing executes off the view yet, and the "+
				"reading is installed against expected. A receiver that never installs holds the revision open; "+
				"view_receipts_ignored_total says whether its receipts were arriving and refused.",
			[]string{"stage"}, nil),
		ignored: prometheus.NewDesc(name("view_receipts_ignored_total"),
			"Receipts the Leader could not attribute, by why: unknown_version names a revision no longer followed, "+
				"unexpected_receiver a Worker the revision did not expect, digest_mismatch a view the Worker installed "+
				"that is not the one the Leader sent it, stale_incarnation a process that was replaced under the revision. "+
				"Counted since the term began; a Leader that steps down starts over.",
			[]string{"reason"}, nil),
		publications: prometheus.NewDesc(name("view_publications_total"),
			"Reconcile rounds that handed the stream a desired set, by whether any Worker's projection changed "+
				"(changed) or the set was the last one again (unchanged). unchanged rising every five seconds is the "+
				"steady state; changed rising without a publication or an assignment moving is a desired set that is not stable.",
			[]string{"result"}, nil),
		sent: prometheus.NewDesc(name("view_messages_sent_total"),
			"View messages handed to the transport, by kind: snapshot_chunk, delta, empty_delta. A Worker at the previous "+
				"revision gets one delta; one further behind or newly connected gets a snapshot; one whose projection "+
				"did not move gets an empty delta and installs by receipt.",
			[]string{"kind"}, nil),
		oversized: prometheus.NewDesc(name("view_deltas_oversized_total"),
			"Deltas not sent because one message of them would have exceeded the stream's 1 MiB message bound; each "+
				"was replaced by the chunked snapshot of the same revision, counted under view_messages_sent_total. "+
				"Expected on a large view when a Worker joins or a rebalance moves thousands of Query Groups at once; "+
				"rising every publication means the delta path is out of reach for that Worker and every step costs a "+
				"snapshot.", nil, nil),
		refusals: prometheus.NewDesc(name("view_stream_refusals_total"),
			"Streams this Leader refused at Hello, for any of the protocol's reasons; the reason is on the view_session log line.", nil, nil),
		failures: prometheus.NewDesc(name("view_publish_failures_total"),
			"Desired sets the Leader could not publish, by why: activation_unreadable, content_unreadable, "+
				"draining_unreadable, active_set_unreadable, assignments_unreadable (a read the set is built from failed), "+
				"publish_rejected (the stream refused it). A Leader that cannot publish leaves view_revision flat exactly "+
				"like one with nothing new to publish; this tells them apart. Counted since the process started.",
			[]string{"reason"}, nil),
		failing: prometheus.NewDesc(name("view_publish_failing_seconds"),
			"How long this Leader has been failing to publish with no success since; 0 while publishing works. "+
				"Past a minute fleet health degrades with VIEW_PUBLISH_FAILING: Workers that restart meanwhile have no view "+
				"and execute nothing.", nil, nil),
		noSessions: prometheus.NewDesc(name("view_stream_no_sessions_seconds"),
			"How long this Leader has had Workers expected and none holding a stream; 0 otherwise. Past a minute fleet "+
				"health degrades with VIEW_STREAM_NO_SESSIONS: the view reaches nobody.", nil, nil),
	}
}

// SetViewStreamSource binds the collector to the Leader's stream. Safe
// before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetViewStreamSource(source func() ViewStreamCounts) {
	if r == nil || r.phaseTwo.viewStream == nil {
		return
	}
	r.phaseTwo.viewStream.mu.Lock()
	r.phaseTwo.viewStream.source = source
	r.phaseTwo.viewStream.mu.Unlock()
}

func (c *viewStreamCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.leading
	ch <- c.revision
	ch <- c.sessions
	ch <- c.receivers
	ch <- c.ignored
	ch <- c.publications
	ch <- c.sent
	ch <- c.oversized
	ch <- c.refusals
	ch <- c.failures
	ch <- c.failing
	ch <- c.noSessions
}

func (c *viewStreamCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	counts := source()
	leading := 0.0
	if counts.Leading {
		leading = 1
	}
	ch <- prometheus.MustNewConstMetric(c.leading, prometheus.GaugeValue, leading)
	// Counters accumulate across terms and are reported whether or not this
	// process leads now; the gauges of a version are a Leader's alone.
	ch <- prometheus.MustNewConstMetric(c.publications, prometheus.CounterValue, float64(counts.Publications), "changed")
	ch <- prometheus.MustNewConstMetric(c.publications, prometheus.CounterValue, float64(counts.PublicationsSkipped), "unchanged")
	ch <- prometheus.MustNewConstMetric(c.sent, prometheus.CounterValue, float64(counts.SnapshotChunksSent), "snapshot_chunk")
	ch <- prometheus.MustNewConstMetric(c.sent, prometheus.CounterValue, float64(counts.DeltasSent), "delta")
	ch <- prometheus.MustNewConstMetric(c.sent, prometheus.CounterValue, float64(counts.EmptyDeltasSent), "empty_delta")
	ch <- prometheus.MustNewConstMetric(c.oversized, prometheus.CounterValue, float64(counts.DeltasOversized))
	ch <- prometheus.MustNewConstMetric(c.refusals, prometheus.CounterValue, float64(counts.Refusals))
	for reason, value := range counts.PublishFailures {
		ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(value), reason)
	}
	if !counts.Leading {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.failing, prometheus.GaugeValue, counts.PublishFailingSeconds)
	ch <- prometheus.MustNewConstMetric(c.noSessions, prometheus.GaugeValue, counts.NoSessionsSeconds)
	ch <- prometheus.MustNewConstMetric(c.revision, prometheus.GaugeValue, float64(counts.Revision))
	ch <- prometheus.MustNewConstMetric(c.sessions, prometheus.GaugeValue, float64(counts.Sessions))
	for stage, value := range map[string]int{
		"expected": counts.Expected, "sent": counts.Sent, "acked": counts.Acked, "installed": counts.Installed, "switched": counts.Switched,
	} {
		ch <- prometheus.MustNewConstMetric(c.receivers, prometheus.GaugeValue, float64(value), stage)
	}
	for reason, value := range map[string]int{
		"unknown_version": counts.IgnoredUnknownVersion, "unexpected_receiver": counts.IgnoredUnexpectedReceiver,
		"digest_mismatch": counts.IgnoredDigestMismatch, "stale_incarnation": counts.IgnoredStaleIncarnation,
	} {
		ch <- prometheus.MustNewConstMetric(c.ignored, prometheus.CounterValue, float64(value), reason)
	}
}
