// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

// newSlotWaitMetrics counts and times the blocking waits inside one Slot
// attempt.
//
// It answers a question nothing else could: a Slot attempt that is stuck
// reports no failure, because nothing failed -- so the only readable fact was
// the gap between two timestamps, and a gap cannot say which of the several
// things it could have been waiting on it was. The buckets run out to a minute
// because the case worth catching is the one that outlasts the Slot itself.
var slotWaitBuckets = []float64{0.001, 0.01, 0.1, 1, 5, 10, 30, 60}

func newSlotWaitMetrics() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "slot_wait_duration_seconds",
		Help: "Blocking waits inside one Slot attempt, by which wait. progress_begin is the fenced " +
			"Progress write that opens the Slot; finalization is deciding whether the Slot needs a " +
			"query, which reads the frozen Plan and the Segment's content objects; object_share is " +
			"waiting on another goroutine's in-flight read of the same catalog object, which has no " +
			"deadline of its own and reports no error however long it takes. An attempt that sits " +
			"between slot_started and slot_completed with nothing in between is in one of these.",
		Buckets: slotWaitBuckets,
	}, []string{"wait"})
}

func (m phaseTwoMetrics) observeSlotWait(o observability.Observation) {
	if o.SlotWait == nil || o.Duration < 0 {
		return
	}
	m.slotWait.WithLabelValues(o.SlotWait.Wait).Observe(o.Duration.Seconds())
}

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
