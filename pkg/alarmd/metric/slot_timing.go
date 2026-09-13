package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func newSlotTimingMetrics() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "slot_operation_duration_seconds",
		Help:    "Nested wall durations of RunOne, SlotSource.Next and Executor.Execute; not additive and excluding dispatcher queue waiting.",
		Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 15, 30, 60},
	}, []string{"stage"})
}

func (m phaseTwoMetrics) observeSlotTiming(o observability.Observation) {
	if (o.Component != observability.ComponentScheduler && o.Component != observability.ComponentState) || o.Duration < 0 {
		return
	}
	var stage string
	switch o.Stage {
	case observability.StageRunnerCompleted:
		stage = "run_one"
	case observability.StageSlotSourceCompleted:
		stage = "source_next"
	case observability.StageSlotCompleted:
		stage = "execute"
	case observability.StageStatePreflight:
		stage = "state_preflight"
	case observability.StageStateApplied:
		stage = "state_apply"
	default:
		return
	}
	m.slotTiming.WithLabelValues(stage).Observe(o.Duration.Seconds())
}
