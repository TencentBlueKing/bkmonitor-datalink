// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/tracing"
)

type traceExportMetrics struct {
	slots   *prometheus.CounterVec
	dropped *prometheus.CounterVec
	state   *prometheus.GaugeVec
}

func newTraceExportMetrics() traceExportMetrics {
	metrics := traceExportMetrics{
		slots: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "trace_slots_total",
			Help: "Slots by what the span export decided for them: sampled (its spans are exported), " +
				"not_sampled_ratio (outside phase_two.tracing.sample_ratio) or rate_limited (inside the ratio " +
				"but past max_traces_per_second). Nothing is counted while the export is off. sampled is " +
				"bounded by the rate whatever the Slot count; rate_limited rising is the rate, not the ratio, " +
				"deciding what is exported.",
		}, []string{"decision"}),
		dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Subsystem: metricSubsystem, Name: "trace_spans_dropped_total",
			Help: "Spans of sampled Slots that were not exported, by why: queue_full (the export queue of 2048 " +
				"spans was full when the span ended), export_failed (the collector did not take a batch; every " +
				"span in it is counted) or per_slot_cap (a Slot's query spans past 32; the query is sent the " +
				"Slot span's context instead). A Slot never waits on the export, so this is where a slow or " +
				"absent collector shows.",
		}, []string{"reason"}),
		state: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Subsystem: metricSubsystem, Name: "trace_export",
			Help: "Whether this process exports its own spans, one-hot by state: on (a collector was given " +
				"and the exporter built), off_not_configured (no phase_two.tracing.otlp_endpoint) or off_role " +
				"(the process does not run the worker role, the one that exports). Set at startup. The span counters stay at zero both while the export is off and while it has nothing " +
				"to sample; this says which. Both series exist from the start; the fleet overview names the " +
				"collector's host and the sampling.",
		}, []string{"state"}),
	}
	for _, decision := range tracing.Decisions {
		metrics.slots.WithLabelValues(decision)
	}
	for _, reason := range tracing.DropReasons {
		metrics.dropped.WithLabelValues(reason)
	}
	for _, state := range fleet.TracingStates {
		metrics.state.WithLabelValues(state)
	}
	return metrics
}

func (m traceExportMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.slots, m.dropped, m.state}
}

// SetTraceExportState sets the export's state, one-hot: the named state 1,
// every other 0. An unnamed state changes nothing.
func (r *Recorder) SetTraceExportState(state string) {
	if r == nil || !knownLabel(fleet.TracingStates, state) {
		return
	}
	for _, each := range fleet.TracingStates {
		value := 0.0
		if each == state {
			value = 1
		}
		r.phaseTwo.traceExport.state.WithLabelValues(each).Set(value)
	}
}

// TraceSlotDecided counts one Slot's sampling decision.
func (r *Recorder) TraceSlotDecided(decision string) {
	if r == nil || !knownLabel(tracing.Decisions, decision) {
		return
	}
	r.phaseTwo.traceExport.slots.WithLabelValues(decision).Inc()
}

// TraceSpansDropped counts spans the export lost, by why.
func (r *Recorder) TraceSpansDropped(reason string, spans int) {
	if r == nil || spans <= 0 || !knownLabel(tracing.DropReasons, reason) {
		return
	}
	r.phaseTwo.traceExport.dropped.WithLabelValues(reason).Add(float64(spans))
}
