// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The due index predicts, for each owned Query Group, the second before which
// asking the Slot source cannot produce work. These metrics exist to say
// whether that prediction is sound, and they are built around one problem:
// every series here that reads as success reads as zero, and zero is also what
// an unregistered metric, an unscraped endpoint and a comparison that never ran
// look like.
//
// Two rules follow, and both are load-bearing.
//
// First, every label combination is created at construction, so a combination
// that has not happened publishes an explicit zero rather than being absent. An
// absent series and a zero series are different claims: one says nothing
// happened, the other says nothing is watching.
//
// Second, every counter whose healthy value is zero is published next to one
// that is not. dueIndexPredictions counts all four outcomes rather than only
// the wrong one, so the violation series sits beside a series that has to grow
// with dispatch; dueIndexVersionChecks counts every header comparison, so the
// fail-open series sits beside the count of checks that actually ran. Without
// the companion, "no violations" and "no comparisons" are the same reading.
type dueIndexMetrics struct {
	entries        prometheus.Gauge
	predictions    *prometheus.CounterVec
	recomputes     *prometheus.CounterVec
	versionChecks  *prometheus.CounterVec
	horizon        prometheus.Histogram
	skipped        *prometheus.CounterVec
	crowdedOut     *prometheus.CounterVec
	turnaways      *prometheus.CounterVec
	auditOvershoot *prometheus.HistogramVec
}

// The horizon is how far ahead a bound sits. The buckets are the evaluation
// periods the deployment actually runs, not a generic latency ladder: their job
// is to show which cadences the owned set is made of.
var dueIndexHorizonBuckets = []float64{1, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600}

var dueIndexPredictionValues = []string{"due", "not_due"}

// The triggers name why a bound was recomputed, and the vocabulary is closed
// because it is a label.
//
//	absent         the return wrote a bound where there was no entry at all,
//	               which is the only thing an absent entry ever means: nothing
//	               has been evaluated since this replica took the object over.
//	deferral       the bound came from the Runner's own backoff rather than from
//	               the schedule.
//	retired_ttl    the schedule is retired, so the bound is the bounded recheck
//	               rather than a Slot time. Retirement can be revoked, which is
//	               why the bound cannot be infinite.
//	execute        the round resolved a frozen Slot and ran it.
//	attempt        the round did not resolve a Slot, and the bound was taken
//	               from the schedule it read. In steady state this is the idle
//	               refresh, which is the bulk of them.
//	version_change a publication was observed and every schedule bound was
//	               dropped, because a publication is the only event that can
//	               make an object due earlier than its bound says.
//	version_unknown the header could not be read, so every schedule bound was
//	               dropped for the same reason, without waiting to find out.
//	ownership      the entry belonged to a lifecycle this replica no longer owns.
var dueIndexTriggers = []string{
	"absent", "deferral", "retired_ttl", "execute", "attempt",
	"version_change", "version_unknown", "ownership",
}

// unchanged is the liveness companion: it grows once per tick for as long as
// the header is being read and compared at all, so "unknown never happened" can
// be told apart from "nothing ever checked".
var dueIndexVersionResults = []string{"unchanged", "changed", "unknown"}

// The two reasons a dispatch is skipped look identical from the queue and mean
// opposite things: not_due is an object that is healthy and early, backoff is
// an object waiting out a failure or a readiness deferral of its own. Counting
// them together would hide a deployment where the second was climbing.
var dispatchSkipReasons = []string{"not_due", "backoff", "query_cooldown"}

// A crowded-out turn is not a skip and must not be counted as one. The three
// skip reasons all say the object was not supposed to run; this says it was
// supposed to run and the dispatcher's own pipeline took its turn away, because
// the previous round's result had not been collected yet. Merging the two would
// put an operator looking at a starving deployment in front of three counters
// that all read zero.
//
//	active  the previous invocation has not returned. Look at how long rounds
//	        are taking, not at capacity.
//	queued  the object already has a place in the queue and has not started.
//	        Look at capacity, not at round duration.
var dispatchCrowdedOutHolders = []string{"active", "queued"}

// dueIndexAuditOvershootBuckets span a fraction of one walk over the owned set
// up to a full minute. The reading is against the walk's own duration, not
// against zero: the prediction is taken when the object is offered a place and
// the verdict is reached after the round runs, so a bound that was correct when
// it was read can be crossed in between. Comparing against zero reports every
// one of those as a defect.
var dueIndexAuditOvershootBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 15, 30, 60}

func newDueIndexMetrics() dueIndexMetrics {
	metrics := dueIndexMetrics{
		entries: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "due_index_entries",
			Help: "Query Groups the due index currently holds a bound for on this replica.",
		}),
		predictions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "due_index_prediction_total",
			Help: "Dispatched rounds by what the due index predicted and what the round found; " +
				"prediction=not_due with actual=due is the index being wrong and must stay zero, " +
				"and prediction=due with actual=due growing is the proof that the comparison is running.",
		}, []string{"prediction", "actual"}),
		recomputes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "due_index_recomputed_total",
			Help: "Due index bounds recomputed or dropped, by what forced it.",
		}, []string{"trigger"}),
		versionChecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "due_index_version_check_total",
			Help: "Activation header comparisons the due index made, by outcome; " +
				"unchanged growing is what says the fail-open path is wired at all.",
		}, []string{"result"}),
		skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dispatch_skipped_total",
			Help: "Dispatches the due index held back, by why the Query Group was not worth dispatching.",
		}, []string{"reason"}),
		turnaways: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dispatch_queue_turnaways_total",
			Help: "Query Groups a dispatcher queue turned away, by what happened and by the cohort of the " +
				"Query Group's shortest due interval. normal_queue_full is an arrival the full ready queue " +
				"held back, the walk resuming from it once a dispatch frees a place; normal_queue_evicted " +
				"is the entry expiring last that a full ready queue gave up for an arrival expiring " +
				"earlier; delayed_not_better and delayed_evicted are the same two on the recovery queue, " +
				"ordered by readiness. Queues order by deadline, so the short cohorts should never be the " +
				"ones held back or evicted: a 10s or 15s series rising here is the ordering not being " +
				"applied. This is not dispatch_crowded_out_total, which counts an object still busy from " +
				"its previous round.",
		}, []string{"outcome", "cohort"}),
		crowdedOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dispatch_crowded_out_total",
			Help: "Turns a Query Group lost because the dispatcher still held it from a previous round, " +
				"by what held it. These are not skips: the object was due and the pipeline took its turn, " +
				"so a deployment starving this way reads zero on every dispatch_skipped_total reason. " +
				"Counted once per rotation pass, not once per delayed round: the rotation reaches every " +
				"owned Query Group in well under a second, so a single execution in flight for ten " +
				"seconds raises this ten times. Divide by the rotation rate before reading it as a rate " +
				"of lost work; read as-is it is the fraction of passes that found the object busy. " +
				"Neither label means a queue was full. A Query Group turned away because a queue had no " +
				"room is a rotation deferral and is counted there, not here. by=active is the object's " +
				"own previous round still executing. by=queued is the object sitting in a queue, and on a " +
				"settled deployment that is mostly the recovery queue holding objects parked on their own " +
				"next ready instant - after a readiness deferral that instant is when the data becomes " +
				"readable, so those objects are waiting on purpose, and this counter rising says the " +
				"rotation passed them, not that anything is wrong. Measured directly over two one-minute " +
				"windows: a rotation offers every owned Query Group exactly once (77,307 offers over 73 " +
				"rotations against 1,059 owned) and by=queued was 425 to 439 of each rotation's offers, " +
				"against 37 for by=active. Read by=active for contention. Do not pair this with a scraped " +
				"queue depth to check the arithmetic: the recovery queue swings from about 20 to its bound " +
				"and back once per evaluation cadence, so a scrape interval that divides that cadence " +
				"samples one phase and reports it as a level.",
		}, []string{"by"}),
		auditOvershoot: newDueIndexAuditOvershoot(),
		horizon: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "due_index_horizon_seconds",
			Help:    "How far ahead of now a newly written due index bound sits.",
			Buckets: append([]float64(nil), dueIndexHorizonBuckets...),
		}),
	}
	for _, prediction := range dueIndexPredictionValues {
		for _, actual := range dueIndexPredictionValues {
			metrics.predictions.WithLabelValues(prediction, actual)
		}
	}
	for _, trigger := range dueIndexTriggers {
		metrics.recomputes.WithLabelValues(trigger)
	}
	for _, result := range dueIndexVersionResults {
		metrics.versionChecks.WithLabelValues(result)
	}
	for _, reason := range dispatchSkipReasons {
		metrics.skipped.WithLabelValues(reason)
	}
	for _, outcome := range dispatchTurnawayOutcomes {
		for _, cohort := range dispatchTurnawayCohorts {
			metrics.turnaways.WithLabelValues(outcome, cohort)
		}
	}
	for _, holder := range dispatchCrowdedOutHolders {
		metrics.crowdedOut.WithLabelValues(holder)
	}
	return metrics
}

func (m dueIndexMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.entries, m.predictions, m.recomputes, m.versionChecks, m.horizon, m.skipped, m.crowdedOut, m.turnaways,
		m.auditOvershoot,
	}
}

// SetDueIndexEntries publishes how many bounds the index holds.
func (r *Recorder) SetDueIndexEntries(count int) {
	if r == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	r.phaseTwo.dueIndex.entries.Set(float64(count))
}

// RecordDueIndexPrediction counts one dispatched round against the prediction
// the index made before it was dispatched. Rounds that reached no verdict on
// dueness are not offered here: counting them would put returns that say
// nothing about the schedule into the counter that exists to falsify it.
func (r *Recorder) RecordDueIndexPrediction(predictedDue, actualDue bool) {
	if r == nil {
		return
	}
	r.phaseTwo.dueIndex.predictions.WithLabelValues(
		dueIndexPredictionLabel(predictedDue), dueIndexPredictionLabel(actualDue),
	).Inc()
}

func dueIndexPredictionLabel(due bool) string {
	if due {
		return "due"
	}
	return "not_due"
}

// RecordDueIndexRecomputed counts one bound recomputed or dropped. An unknown
// trigger is dropped rather than collapsed: the vocabulary is fixed in this
// package, so an unknown value is a coding error, and publishing it under a
// catch-all would hide it.
func (r *Recorder) RecordDueIndexRecomputed(trigger string, count int) {
	if r == nil || count <= 0 {
		return
	}
	for _, known := range dueIndexTriggers {
		if trigger == known {
			r.phaseTwo.dueIndex.recomputes.WithLabelValues(trigger).Add(float64(count))
			return
		}
	}
}

// RecordDueIndexVersionCheck counts one activation header comparison.
func (r *Recorder) RecordDueIndexVersionCheck(result string) {
	if r == nil {
		return
	}
	for _, known := range dueIndexVersionResults {
		if result == known {
			r.phaseTwo.dueIndex.versionChecks.WithLabelValues(result).Inc()
			return
		}
	}
}

// ObserveDueIndexHorizon records how far ahead a newly written bound sits. A
// bound already in the past contributes zero rather than a negative number:
// "due now" is a real and frequent answer, and dropping it would understate how
// many bounds the index writes.
func (r *Recorder) ObserveDueIndexHorizon(seconds float64) {
	if r == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	r.phaseTwo.dueIndex.horizon.Observe(seconds)
}

// RecordDispatchSkipped counts one dispatch the index held back.
func (r *Recorder) RecordDispatchSkipped(reason string) {
	if r == nil {
		return
	}
	for _, known := range dispatchSkipReasons {
		if reason == known {
			r.phaseTwo.dueIndex.skipped.WithLabelValues(reason).Inc()
			return
		}
	}
}

// dispatchTurnawayOutcomes and dispatchTurnawayCohorts are the closed label
// sets of dispatch_queue_turnaways_total; every series is created at
// construction so a zero is a zero and not an absent series.
var (
	dispatchTurnawayOutcomes = []string{"normal_queue_full", "normal_queue_evicted", "delayed_not_better", "delayed_evicted"}
	dispatchTurnawayCohorts  = []string{"10s", "15s", "30s", "other", "unknown"}
)

// RecordDispatchTurnaway counts one Query Group a dispatcher queue turned
// away, at the branch that turned it away.
func (r *Recorder) RecordDispatchTurnaway(outcome, cohort string) {
	if r == nil || !knownLabel(dispatchTurnawayOutcomes, outcome) || !knownLabel(dispatchTurnawayCohorts, cohort) {
		return
	}
	r.phaseTwo.dueIndex.turnaways.WithLabelValues(outcome, cohort).Inc()
}

// RecordDispatchCrowdedOut counts one turn the dispatcher took away from a
// Query Group it was still holding from an earlier round. It is called from the
// branch that takes the turn, not reconstructed afterwards: the whole reason
// this count exists is that the branch returns silently, and a count assembled
// somewhere else would be the same silence with more steps.
func (r *Recorder) RecordDispatchCrowdedOut(holder string) {
	if r == nil {
		return
	}
	for _, known := range dispatchCrowdedOutHolders {
		if holder == known {
			r.phaseTwo.dueIndex.crowdedOut.WithLabelValues(holder).Inc()
			return
		}
	}
}

// newDueIndexAuditOvershoot builds the one measurement that can say why the due
// index held back an object that was already due.
//
// The name carries "audit" because the population is not every dispatch. A
// Query Group predicted not due is only dispatched when it wins the per-
// generation audit claim, so every observation here comes from that sample --
// while due_index_prediction_total's prediction=due rows come from the whole
// population. Two of that counter's four cells are sampled and two are not, and
// nothing in its name or its HELP says so; a share computed across them is a
// numerator and a denominator drawn from different populations. Putting the
// sampled quantity under a separate name is the only version of that warning a
// reader cannot skip.
func newDueIndexAuditOvershoot() *prometheus.HistogramVec {
	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem,
		Name: "due_index_audit_overshoot_seconds",
		Help: "The due index's own bound minus the instant it was consulted, for an audited dispatch " +
			"the index predicted was not due and the round then found due. It measures how much " +
			"longer the index intended to hold that object back. It is NOT how late the Slot ran: " +
			"the Slot's evaluation time is not read here and does not enter this number. " +
			"Over the audited dispatches only -- one Query Group per generation, never the whole " +
			"population -- which is why this is not a cell on due_index_prediction_total, whose four " +
			"cells mix that sample with the full population. Observed on the violation alone, so its " +
			"count is the same population as that counter's not_due/due cell. " +
			"Read against the duration of one walk over the owned set rather than against zero: mass " +
			"below one walk is the boundary being crossed between the prediction and the verdict, and " +
			"mass well above it means the bound reached that far past an object that was already due, " +
			"which is the failure the index's own file calls a correctness defect. Resolution is one " +
			"second, because the bound is stored and compared as a whole second. " +
			"cooldown says the object was in query cooldown when the bound was recorded. Cooldown is " +
			"a deliberate backing-off, so those observations are a suppression working as intended " +
			"being counted as a wrong prediction, and the two must be read apart.",
		Buckets: append([]float64(nil), dueIndexAuditOvershootBuckets...),
	}, []string{"cooldown"})
	// Both label values pre-created. A histogram that has never observed
	// anything and one whose population is entirely on the other side of the
	// label look identical when the series is simply absent, and the second is
	// a result.
	for _, cooldown := range []string{"true", "false"} {
		histogram.WithLabelValues(cooldown)
	}
	return histogram
}

// RecordDueIndexAuditOvershoot observes one audited dispatch that the index
// predicted was not due and the round found due.
func (r *Recorder) RecordDueIndexAuditOvershoot(heldFor time.Duration, cooldown bool) {
	if r == nil || r.phaseTwo.dueIndex.auditOvershoot == nil {
		return
	}
	seconds := heldFor.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	label := "false"
	if cooldown {
		label = "true"
	}
	r.phaseTwo.dueIndex.auditOvershoot.WithLabelValues(label).Observe(seconds)
}
