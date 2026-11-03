// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A planned handover keeps the moved Query Group's lease until its running
// Slot is through its commit boundary (design 02 §6.2); these say how often
// that wait happened, how it ended and how long it took, and how often a
// Slot's events were acknowledged and its State then refused anyway -- the
// duplicate the wait exists to remove, which the next owner sends again.
type handoverMetrics struct {
	drains          *prometheus.CounterVec
	drainSeconds    prometheus.Histogram
	outputUnapplied *prometheus.CounterVec
	executionsHung  *prometheus.CounterVec
	// tierPaused counts the control rounds a fallback Leader ran without
	// the second tier's functions, by function.
	tierPaused *prometheus.CounterVec
}

// ControlTierLookbackPlanning is the second-tier control function that plans
// the lookback; ControlTierFunctions every such function, closed.
const ControlTierLookbackPlanning = "lookback_planning"

var ControlTierFunctions = []string{ControlTierLookbackPlanning}

// The waits are a Slot's commit boundary at the short end and a lease's
// grace -- its TTL plus the switch margin -- at the long end.
var handoverDrainBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 35, 60}

func newHandoverMetrics() handoverMetrics {
	metrics := handoverMetrics{
		drains: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "handover_drains_total",
			Help: "Planned handovers - a Query Group this Worker is no longer desired for - by how the wait for its " +
				"running Slot ended before the lease was released. idle: nothing was running, the release did not " +
				"wait. finished: the Slot returned and the lease was released after its commit boundary. lease_ended: " +
				"the lease stopped renewing first (the grace the move gave it ran out, or the store refused it), so " +
				"the Slot's later writes are refused and the next owner may send its events again. stopped: the " +
				"process began stopping while it waited, and the Slot was waited for within the shutdown timeout. " +
				"Every outcome has a series at startup. Read finished against lease_ended for whether handovers " +
				"complete inside the grace; output_unapplied_total says whether a duplicate followed." +
				" Reader: the release acceptance, from /metrics after each deploy.",
		}, []string{"outcome"}),
		drainSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Subsystem: metricSubsystem, Name: "handover_drain_seconds",
			Help: "How long a planned handover waited for the moved Query Group's running Slot before releasing its " +
				"lease; 0 for one with nothing running. The next owner cannot start the Query Group for this long." +
				" Reader: the release acceptance, from /metrics after each deploy.",
			Buckets: append([]float64(nil), handoverDrainBuckets...),
		}),
		outputUnapplied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "output_unapplied_total",
			Help: "Slots whose events the broker acknowledged and whose State write the ownership store then refused, " +
				"by the refusal: the Slot stops there, Progress does not move, and the next owner redoes it from " +
				"Progress and sends the same events again (same event identities). Counted where the Slot holds both " +
				"facts, once per Slot. A planned handover or stop that waits for the Slot keeps this at zero; what is " +
				"left is a Slot that outlived its lease - a crash, a hung Slot, a drain deadline - and a lease lost to " +
				"the store. ownership_refusals_total{site=\"state_apply\"} counts every refused State write, with or " +
				"without events before it; this is the subset that sent something. Every refusal has a series at startup." +
				" Reader: the release acceptance, from /metrics after each deploy.",
		}, []string{"refusal"}),
		executionsHung: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "executions_hung_total",
			Help: "Executions found running a minute past their own deadline before writing any output - ignoring " +
				"their cancellation, which only a call without a deadline or a deadlock does - by what followed. " +
				"Their own deadline is a normal Slot's query deadline, or a replay's, retry's or probe's counted " +
				"from its own arrival, never its frozen Slot's first attempt's; an execution writing its gap marks, " +
				"output, State or Progress is never counted. declined: the replica let the Query Group's lease go " +
				"at once, the hung execution still running and writing nothing more, and named the Query Group on " +
				"its registration so a leader places it on another worker and not back here. " +
				"returned_after_decline: the hung execution returned at last and " +
				"the decline was lifted. The execution_hung line names the Query Group and the stage it hung in; a " +
				"Query Group every worker declines is the leader round's declined_everywhere. Both series exist " +
				"from startup." +
				" Reader: the release acceptance, from /metrics after each deploy.",
		}, []string{"outcome"}),
	}
	metrics.tierPaused = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: metricSubsystem, Name: "control_tier_paused_total",
		Help: "Control rounds this process ran as a fallback Leader without a second-tier function, by function: " +
			"the functions only the preferred control process runs, which detection never depends on. Non-zero " +
			"while a Worker holds the control lease for want of a control process; it stops when control is handed back.",
	}, []string{"function"})
	for _, function := range ControlTierFunctions {
		metrics.tierPaused.WithLabelValues(function)
	}
	for _, outcome := range observability.ExecutionHungOutcomes {
		metrics.executionsHung.WithLabelValues(outcome)
	}
	for _, outcome := range handoverDrainOutcomes() {
		metrics.drains.WithLabelValues(outcome)
	}
	for _, refusal := range ownership.RefusalReasons {
		metrics.outputUnapplied.WithLabelValues(refusal)
	}
	return metrics
}

// handoverDrainOutcomes is the outcomes a handover can end with: every drain
// outcome but the stop's own two at its deadline.
func handoverDrainOutcomes() []string {
	outcomes := make([]string, 0, len(observability.SlotDrainOutcomes))
	for _, outcome := range observability.SlotDrainOutcomes {
		if outcome != observability.SlotDrainDeadline && outcome != observability.SlotDrainDeadlineUnreturned {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

func (m handoverMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.drains, m.drainSeconds, m.outputUnapplied, m.executionsHung, m.tierPaused}
}

// RecordHandoverDrain counts one planned handover's wait for its Slot.
func (r *Recorder) RecordHandoverDrain(outcome string, wait time.Duration) {
	if r == nil || !knownLabel(handoverDrainOutcomes(), outcome) {
		return
	}
	r.phaseTwo.handover.drains.WithLabelValues(outcome).Inc()
	r.phaseTwo.handover.drainSeconds.Observe(wait.Seconds())
}

// RecordControlTierPaused counts one control round a fallback Leader ran
// without the second-tier function.
func (r *Recorder) RecordControlTierPaused(function string) {
	if r == nil || !knownLabel(ControlTierFunctions, function) {
		return
	}
	r.phaseTwo.handover.tierPaused.WithLabelValues(function).Inc()
}

// RecordExecutionHung counts one hung execution declined or returned.
func (r *Recorder) RecordExecutionHung(outcome string) {
	if r == nil || !knownLabel(observability.ExecutionHungOutcomes, outcome) {
		return
	}
	r.phaseTwo.handover.executionsHung.WithLabelValues(outcome).Inc()
}
