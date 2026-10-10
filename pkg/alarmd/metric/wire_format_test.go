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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every wire format is a series from startup, each ACK line adds its counts
// to its cells, a word the build does not name folds to _other, and lines
// that are not ACKs add nothing.
func TestOutputEventsByWireFormatAreSeriesFromStartupAndSumTheACKLines(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_output_events_by_wire_format_total"
	if before := gatherFamily(t, r, family); len(before) != len(observability.WireFormats) {
		t.Fatalf("%d series before any ACK, want every format (%d) so a standard_raw_event nobody sent reads as zero",
			len(before), len(observability.WireFormats))
	}
	ctx := context.Background()
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultSuccess,
		OutputWireFormats: observability.OutputWireFormatCounts{contract.WireFormatPythonCompatible: 12, contract.WireFormatStandardRawEvent: 1},
	})
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultDegraded,
		OutputWireFormats: observability.OutputWireFormatCounts{contract.WireFormatPythonCompatible: 3, "some_future_word": 2},
	})
	// The same counts on a line that is not an ACK are not this family's.
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		OutputWireFormats: observability.OutputWireFormatCounts{contract.WireFormatStandardRawEvent: 100},
	})
	count := func(format string) float64 {
		return testutil.ToFloat64(r.phaseTwo.outputEventsByWireFormat.WithLabelValues(format))
	}
	for format, want := range map[string]float64{
		contract.WireFormatPythonCompatible: 15, contract.WireFormatStandardRawEvent: 1, observability.WireFormatOther: 2,
	} {
		if got := count(format); got != want {
			t.Fatalf("%s = %v, want %v", format, got, want)
		}
	}
	if after := gatherFamily(t, r, family); len(after) != len(observability.WireFormats) {
		t.Fatalf("%d series after the lines, want the same %d: an unknown word folds rather than creating a cell", len(after), len(observability.WireFormats))
	}
}

// The leader's Catalog composition publishes its Plans by wire format, every
// format present even at zero, from the composition the source hands it.
func TestCatalogPlansByWireFormatArePublishedFromTheComposition(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	composition := &controlplane.CatalogComposition{PlansByWireFormat: map[string]int{
		contract.WireFormatPythonCompatible: 12, contract.WireFormatStandardRawEvent: 0, observability.WireFormatOther: 0,
	}}
	r.SetCatalogCompositionSource(func() *controlplane.CatalogComposition { return composition })
	const family = "bkmonitor_alarmd_catalog_plans_by_wire_format"
	series := gatherFamily(t, r, family)
	if len(series) != len(observability.WireFormats) {
		t.Fatalf("%d series, want one per format (%d) with the zeros published", len(series), len(observability.WireFormats))
	}
	values := map[string]float64{}
	for _, metric := range series {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "format" {
				values[label.GetValue()] = metric.GetGauge().GetValue()
			}
		}
	}
	if values[contract.WireFormatPythonCompatible] != 12 || values[contract.WireFormatStandardRawEvent] != 0 {
		t.Fatalf("values = %v, want 12 python_compatible and a published 0 standard_raw_event", values)
	}
}

// Every format × kind is a series from startup, and each ACK line adds the
// sink's breakdown to its cells: the recoveries the Python-compatible
// protocol dropped land under python_compatible/RECOVERY, a kind or format
// the build does not name folds, and a line without a breakdown adds nothing.
func TestOutputEventsWithoutMessageAreSeriesFromStartupAndSumTheSinksBreakdown(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_output_events_without_message_total"
	cells := len(observability.WireFormats) * len(observability.OutputEventKinds)
	if before := gatherFamily(t, r, family); len(before) != cells {
		t.Fatalf("%d series before any ACK, want every format x kind (%d) so a protocol that dropped nothing reads as zero", len(before), cells)
	}
	ctx := context.Background()
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultSuccess,
		OutputWrite: &observability.OutputWriteFacts{Published: 2, WithoutMessage: 4, WithoutMessageBy: []observability.OutputWithoutMessage{
			{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery, Events: 3},
			{Format: contract.WireFormatPythonCompatible, EventKind: "SOMETHING_NEW", Events: 1},
		}},
	})
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultDegraded,
		OutputWrite: &observability.OutputWriteFacts{Published: 0, WithoutMessage: 2, WithoutMessageBy: []observability.OutputWithoutMessage{
			{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery, Events: 2},
		}},
	})
	// A sink from before the breakdown: counts, no buckets. Nothing to add.
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultSuccess,
		OutputWrite: &observability.OutputWriteFacts{Published: 1, WithoutMessage: 7},
	})
	count := func(format, kind string) float64 {
		return testutil.ToFloat64(r.phaseTwo.outputEventsWithoutMessage.WithLabelValues(format, kind))
	}
	if got := count(contract.WireFormatPythonCompatible, contract.TriggerEventRecovery); got != 5 {
		t.Fatalf("python_compatible/RECOVERY = %v, want 5", got)
	}
	if got := count(contract.WireFormatPythonCompatible, observability.EventKindOther); got != 1 {
		t.Fatalf("python_compatible/_other = %v, want the 1 unknown kind folded", got)
	}
	if got := count(contract.WireFormatStandardRawEvent, contract.TriggerEventRecovery); got != 0 {
		t.Fatalf("standard_raw_event/RECOVERY = %v, want 0", got)
	}
	if after := gatherFamily(t, r, family); len(after) != cells {
		t.Fatalf("%d series after the lines, want the same %d: an unknown kind folds rather than creating a cell", len(after), cells)
	}
}

// Every format x kind is a series from startup, and each ACK line adds its
// kinds to the cells; an unknown kind folds; a line that is not an ACK adds
// nothing. The standard-line recovery is its own cell: the question this
// family exists for.
func TestOutputEventsByKindAreSeriesFromStartupAndSumTheACKLines(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_output_events_by_kind_total"
	cells := len(observability.WireFormats) * len(observability.OutputEventKinds)
	if before := gatherFamily(t, r, family); len(before) != cells {
		t.Fatalf("%d series before any ACK, want every format x kind (%d)", len(before), cells)
	}
	ctx := context.Background()
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultSuccess,
		OutputEventKinds: observability.OutputEventKindCounts{
			{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery}: 2,
			{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventAbnormal}: 5,
			{Format: contract.WireFormatPythonCompatible, EventKind: "SOMETHING_NEW"}:               1,
		},
	})
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultDegraded,
		OutputEventKinds: observability.OutputEventKindCounts{{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery}: 3},
	})
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		OutputEventKinds: observability.OutputEventKindCounts{{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery}: 100},
	})
	count := func(format, kind string) float64 {
		return testutil.ToFloat64(r.phaseTwo.outputEventsByKind.WithLabelValues(format, kind))
	}
	if got := count(contract.WireFormatStandardRawEvent, contract.TriggerEventRecovery); got != 5 {
		t.Fatalf("standard_raw_event/RECOVERY = %v, want 5 (2 taken + 3 refused)", got)
	}
	if got := count(contract.WireFormatStandardRawEvent, contract.TriggerEventAbnormal); got != 5 {
		t.Fatalf("standard_raw_event/ABNORMAL = %v, want 5", got)
	}
	if got := count(contract.WireFormatPythonCompatible, observability.EventKindOther); got != 1 {
		t.Fatalf("python_compatible/_other = %v, want the unknown kind folded", got)
	}
	if after := gatherFamily(t, r, family); len(after) != cells {
		t.Fatalf("%d series after, want the same %d", len(after), cells)
	}
}
