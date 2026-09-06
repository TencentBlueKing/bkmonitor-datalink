package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type shortPeriodMetrics struct {
	completed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	lag       *prometheus.HistogramVec
}

func newShortPeriodMetrics() shortPeriodMetrics {
	buckets := []float64{0.01, 0.1, 1, 5, 10, 15, 25, 30, 60}
	return shortPeriodMetrics{
		completed: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_completions_total", Help: "Acknowledged short-period Slot Progress completions by actual completion kind."}, []string{"cohort", "operation", "completion_kind"}),
		duration:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_execution_duration_seconds", Help: "Duration of the committed executor call, excluding SlotSource preparation and earlier attempts.", Buckets: buckets}, []string{"cohort"}),
		lag:       prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_completion_lag_seconds", Help: "Elapsed wall time from Slot EvaluationTime to acknowledged completion, including backlog.", Buckets: buckets}, []string{"cohort"}),
	}
}

func (m shortPeriodMetrics) observe(o observability.Observation) {
	// Normalize independently so direct metric use cannot create arbitrary labels.
	o = observability.NormalizeObservation(o)
	f := o.ShortPeriodCompletion
	if f == nil {
		return
	}
	m.completed.WithLabelValues(f.Cohort, string(o.Operation), f.CompletionKind).Inc()
	m.duration.WithLabelValues(f.Cohort).Observe(o.Duration.Seconds())
	m.lag.WithLabelValues(f.Cohort).Observe(f.LagSeconds)
}
