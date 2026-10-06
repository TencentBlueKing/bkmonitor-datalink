package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type workflowMetrics struct {
	ranges, expiredSlots   *prometheus.CounterVec
	run, execute, progress *prometheus.CounterVec
	attempted              prometheus.Counter
	active, ready, delayed prometheus.Gauge
	permitWait             *prometheus.HistogramVec
}

func newWorkflowMetrics() workflowMetrics {
	counter := func(name, help, label string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: name, Help: help}, []string{label})
	}
	gauge := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: name, Help: help})
	}
	return workflowMetrics{
		ranges:       counter("expired_range_total", "Expired range operation returns, not logical Slot completions.", "result"),
		expiredSlots: counter("expired_slots_finalized_total", "Logical age-expired Slots finalized by a new successful range commit.", "reason"),
		run:          counter("run_one_return_total", "RunOne exits including panic, classified once by actual control flow.", "outcome"),
		execute:      counter("execute_return_total", "Executor returns; not successful Slot completions.", "outcome"),
		progress:     counter("progress_completed_total", "Acknowledged Progress commit observations by existing completion kind.", "kind"),
		attempted:    prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "run_one_attempted_total", Help: "Returned attempted=true, including source retries without Execute."}),
		active:       gauge("scheduler_active_executions", "Worker task occupancy, including preparation and execution, excluding pending result delivery."),
		ready:        gauge("scheduler_ready_runners", "Runners in the outer ready queue."),
		delayed:      gauge("scheduler_delayed_runners", "Runners in the outer delayed queue."),
		permitWait:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_permit_wait_seconds", Help: "Actual query admission call wall duration including immediate grants and failures; parallel waits are not additive Slot time.", Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 15, 30, 60}}, []string{"queue_kind"}),
	}
}
func (m workflowMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.run, m.execute, m.progress, m.attempted, m.active, m.ready, m.delayed, m.permitWait, m.ranges, m.expiredSlots}
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
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageRunnerReturned:
		if observability.ValidRunOutcome(o.RunOutcome) {
			m.run.WithLabelValues(o.RunOutcome).Inc()
			if o.Attempted {
				m.attempted.Inc()
			}
		}
	case o.Component == observability.ComponentScheduler && o.Stage == observability.StageSlotCompleted:
		if observability.ValidExecuteOutcome(o.ExecuteOutcome) {
			m.execute.WithLabelValues(o.ExecuteOutcome).Inc()
		}
	case o.Component == observability.ComponentProgress && o.Stage == observability.StageProgressCommitted:
		if phaseTwoWorkCompleted(o) && observability.ValidProgressCompletionKind(o.ProgressCompletionKind) {
			m.progress.WithLabelValues(o.ProgressCompletionKind).Inc()
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
