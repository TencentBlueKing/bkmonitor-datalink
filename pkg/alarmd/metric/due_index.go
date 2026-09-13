// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import "github.com/prometheus/client_golang/prometheus"

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
	entries       prometheus.Gauge
	predictions   *prometheus.CounterVec
	recomputes    *prometheus.CounterVec
	versionChecks *prometheus.CounterVec
	horizon       prometheus.Histogram
	skipped       *prometheus.CounterVec
	crowdedOut    *prometheus.CounterVec
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
		crowdedOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dispatch_crowded_out_total",
			Help: "Turns a Query Group lost because the dispatcher still held it from a previous round, " +
				"by what held it. These are not skips: the object was due and the pipeline took its turn, " +
				"so a deployment starving this way reads zero on every dispatch_skipped_total reason.",
		}, []string{"by"}),
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
	for _, holder := range dispatchCrowdedOutHolders {
		metrics.crowdedOut.WithLabelValues(holder)
	}
	return metrics
}

func (m dueIndexMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.entries, m.predictions, m.recomputes, m.versionChecks, m.horizon, m.skipped, m.crowdedOut,
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
