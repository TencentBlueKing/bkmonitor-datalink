package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type workflowMetrics struct {
	ranges, expiredSlots   *prometheus.CounterVec
	run, execute, progress *prometheus.CounterVec
	progressCauses         *prometheus.CounterVec
	slotReasons            *prometheus.CounterVec
	active, ready, delayed prometheus.Gauge
	abandonedSlots         prometheus.Counter
	shareRefusalGaps       prometheus.Counter
	permitWait             *prometheus.HistogramVec
}

// slotReasonWords are the cells a Slot's end is counted under from the
// start: none for a Slot that ended whole, reason_not_reported for an end that
// named no reason where one was due, _other for a word outside the contract
// catalogue, and the words an edit's reading is made of. Any other word of
// the catalogue is counted under its own name once a Slot ends with it; the
// catalogue's exact words are not published as standing cells, which the
// metrics keep to bounded classes (TestKnownM0ReasonMapsToBoundedMetricValue).
var slotReasonWords = []string{string(observability.ReasonNone), string(observability.ReasonNotReported), string(observability.ReasonOther),
	contract.ReasonPlanNotActive, contract.ReasonConfigDrift, contract.ReasonPlanReactivated}

// slotReasonWord is the word a Slot's end is counted under.
func slotReasonWord(reason observability.ReasonCode, result observability.Result) string {
	reason = observability.NormalizeReason(reason, result)
	if reason == observability.ReasonNone || reason == observability.ReasonNotReported || contract.IsKnownReasonV2(string(reason)) {
		return string(reason)
	}
	return string(observability.ReasonOther)
}

func newWorkflowMetrics() workflowMetrics {
	counter := func(name, help, label string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Subsystem: metricSubsystem, Name: name, Help: help}, []string{label})
	}
	gauge := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: name, Help: help})
	}
	metrics := workflowMetrics{
		ranges:       counter("expired_range_total", "Expired range operation returns, not logical Slot completions.", "result"),
		expiredSlots: counter("expired_slots_finalized_total", "Logical age-expired Slots finalized by a new successful range commit.", "reason"),
		run:          counter("run_one_return_total", "RunOne exits including panic, classified once by actual control flow.", "outcome"),
		execute:      counter("execute_return_total", "Executor returns; not successful Slot completions.", "outcome"),
		progress:     counter("progress_completed_total", "Acknowledged Progress commit observations by existing completion kind.", "kind"),
		progressCauses: prometheus.NewCounterVec(prometheus.CounterOpts{Subsystem: metricSubsystem,
			Name: "progress_completion_causes_total",
			Help: "Acknowledged Slots that completed short of whole - COMPLETED_WITH_UNAVAILABLE, COMPLETED_WITH_PARTIAL_GAP - by " +
				"the cause the completion carried (the most actionable of what the round hit) and that cause's reason: " +
				"which Plan, Level or query it was and when are on the progress_committed line (completion_strategy, " +
				"completion_level, completion_query). NONE is a completion that carried no cause. origin says whose the " +
				"reason is: physical for what the round met itself - a primary input's reason always is - and carried " +
				"for a stored guard's reason a Level outcome repeated on a later round (LEVEL_OUTCOME_UNKNOWN, " +
				"GAP_GUARD_WARMING); the guard's origin Slot is on the progress_committed line " +
				"(evaluation_reason_origin_slot). reason=QUERY_TIMEOUT,origin=physical is a timeout this round. " +
				"A Slot is filed here by its cause, so one that ended PLAN_NOT_ACTIVE while its primary input was " +
				"unavailable is under PRIMARY_INPUT_UNAVAILABLE: how many Slots ended PLAN_NOT_ACTIVE is " +
				"slot_completed_reasons_total{reason=\"PLAN_NOT_ACTIVE\"}, and the two families are not summed."},
			[]string{"completion_kind", "cause", "reason", "origin"}),
		slotReasons: prometheus.NewCounterVec(prometheus.CounterOpts{Subsystem: metricSubsystem,
			Name: "slot_completed_reasons_total",
			Help: "Slots that ended, by the reason word their slot_completed line carries: one per Slot, counted once, " +
				"when it ends - a round that retries it is not an end - whatever the log samples. none is a Slot that " +
				"ended whole. The answer to how many Slots ended PLAN_NOT_ACTIVE is the PLAN_NOT_ACTIVE cell, whatever " +
				"cause progress_completion_causes_total files them under. The none, PLAN_NOT_ACTIVE, CONFIG_DRIFT, " +
				"PLAN_REACTIVATED, reason_not_reported and _other cells exist from the start, so a zero there is a " +
				"reading; another word of the contract catalogue appears once a Slot ends with it."},
			[]string{"reason"}),
		abandonedSlots: prometheus.NewCounter(prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "unfinished_slots_abandoned_total",
			Help: "Unfinished Slots the Progress store dropped because a later Segment moved the cursor past them, counted when the write recording the skip applied. Each is one slot_skipped line with finalizing_path=progress_store; its Slot was never queried and only the Progress record's recent gap holds it."}),
		shareRefusalGaps: prometheus.NewCounter(prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "share_refusal_gaps_total",
			Help: "Slots finalized without a query as RESOURCE_SHARE_EXCEEDED: refused for the share - twice the first time a Query Group meets it, once for each later Slot of the group - because their own retained bytes exceed the share one Query Group may hold of the replica's pool (QG_BUDGET_SHARE_EXCEEDED with no shared usage), and given up instead of re-read until the replay age. Each is one slot_skipped line with skip_cause=RESOURCE_SHARE_EXCEEDED naming the Query Group, its own bytes and the share; the strategy has to be sharded, and until it is every Slot of the group lands here."}),
		active:     gauge("scheduler_active_executions", "Worker task occupancy, including preparation and execution, excluding pending result delivery."),
		ready:      gauge("scheduler_ready_runners", "Runners in the outer ready queue."),
		delayed:    gauge("scheduler_delayed_runners", "Runners in the outer delayed queue."),
		permitWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{Subsystem: metricSubsystem, Name: "query_permit_wait_seconds", Help: "Actual query admission call wall duration including immediate grants and failures; parallel waits are not additive Slot time.", Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 15, 30, 60}}, []string{"queue_kind"}),
	}
	// Every cell from the start, so a word that never happens reads zero.
	for _, word := range slotReasonWords {
		metrics.slotReasons.WithLabelValues(word)
	}
	return metrics
}
func (m workflowMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.run, m.execute, m.progress, m.progressCauses, m.slotReasons, m.active, m.ready, m.delayed, m.permitWait, m.ranges, m.expiredSlots, m.abandonedSlots, m.shareRefusalGaps}
}
func (m workflowMetrics) observe(o observability.Observation) {
	switch {
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageExpiredRangeReturned:
		if f := o.ExpiredRange; f != nil {
			switch f.Result {
			case "committed", "retrying", "blocked", "error":
				m.ranges.WithLabelValues(f.Result).Inc()
				if f.Result == "committed" && f.CommittedSlots > 0 {
					switch f.ReasonCode {
					case "", contract.ReasonSnapshotUnavailable: // Original age-only producer had no ReasonCode.
						m.expiredSlots.WithLabelValues("recovery_expired").Add(float64(f.CommittedSlots))
					case contract.ReasonGapSkipped:
						m.expiredSlots.WithLabelValues("replay_distance_expired").Add(float64(f.CommittedSlots))
					}
				}
			}
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageSlotSkipped:
		// The line and the count are one event: the store reports the dropped
		// Slot once its write applied, and nowhere else.
		if f := o.SlotSkipped; f != nil && f.Path == observability.SlotSkippedProgressStore {
			m.abandonedSlots.Inc()
		}
		if f := o.SlotSkipped; f != nil && f.Cause == observability.CompletionCauseResourceShareExceeded {
			m.shareRefusalGaps.Add(float64(max(f.SlotCount, 1)))
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageRunnerReturned:
		if observability.ValidRunOutcome(o.RunOutcome) {
			m.run.WithLabelValues(o.RunOutcome).Inc()
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageSlotCompleted:
		// A round's outcome; a supplement completes no Slot and is counted by
		// the lookback (lookback_supplement_windows_total).
		if observability.ValidExecuteOutcome(o.ExecuteOutcome) && o.Operation != observability.OperationSupplement {
			m.execute.WithLabelValues(o.ExecuteOutcome).Inc()
			if o.ExecuteOutcome == "completed" {
				m.slotReasons.WithLabelValues(slotReasonWord(o.ReasonCode, o.Result)).Inc()
			}
		}
	case o.Component == observability.ComponentProgress && o.Stage == observability.StageProgressCommitted:
		if phaseTwoWorkCompleted(o) && observability.ValidProgressCompletionKind(o.ProgressCompletionKind) {
			m.progress.WithLabelValues(o.ProgressCompletionKind).Inc()
			switch o.ProgressCompletionKind {
			case "COMPLETED_WITH_UNAVAILABLE", "COMPLETED_WITH_PARTIAL_GAP":
				m.progressCauses.WithLabelValues(o.ProgressCompletionKind,
					observability.NormalizeProgressCompletionCause(o.ProgressCompletionCause),
					string(observability.NormalizeReason(observability.ReasonCode(o.ProgressCompletionReason), observability.ResultDegraded)),
					observability.ReasonOriginOf(observability.ReasonCode(o.ProgressCompletionReason), o.CarriedReason != nil)).Inc()
			}
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageDispatcherSnapshot:
		if f := o.Dispatcher; f != nil && f.Active >= 0 && f.Ready >= 0 && f.Delayed >= 0 {
			m.active.Set(float64(f.Active))
			if f.QueuesKnown {
				m.ready.Set(float64(f.Ready))
				m.delayed.Set(float64(f.Delayed))
			}
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageQueryPermitWait:
		if o.PermitWait != nil && o.Duration >= 0 {
			kind := "normal"
			if o.PermitWait.Recovery {
				kind = "recovery"
			}
			m.permitWait.WithLabelValues(kind).Observe(o.Duration.Seconds())
		}
	}
}
