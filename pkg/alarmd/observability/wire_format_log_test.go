// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The wire format is on the log: the Plan's word on its evaluation line, and
// one key per format the batch carried on the ACK line, under the format's
// own name -- so a search for standard_raw_event finds the lines that sent
// one, and finds nothing only when none did. Asserted on the rendered keys:
// the word lived in the Plan and on every event for months and reached no
// line, and a search that found nothing nearly read as "none do".
func TestTheWireFormatIsOnTheEvaluationAndACKLines(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
		Trace:            TraceFields{StrategyID: "4101", QueryGroupKey: "qg-wire", EvaluationTime: 600},
		OutputWireFormat: contract.WireFormatStandardRawEvent,
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode evaluation log: %v; log=%s", err, output.String())
	}
	if event["wire_format"] != contract.WireFormatStandardRawEvent {
		t.Fatalf("evaluation line wire_format = %#v, want %q; event=%#v", event["wire_format"], contract.WireFormatStandardRawEvent, event)
	}
	if _, present := event["series_matched"]; present {
		t.Fatalf("a line without a series count carries series_matched; event=%#v", event)
	}
	// The count, zero included: a Plan bound to no series is the reading.
	zero := 0
	output.Reset()
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
		Trace:             TraceFields{StrategyID: "4102", QueryGroupKey: "qg-wire", EvaluationTime: 600},
		PlanSeriesMatched: &zero,
	})
	event = map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode evaluation log: %v; log=%s", err, output.String())
	}
	if event["series_matched"] != float64(0) {
		t.Fatalf("evaluation line series_matched = %#v, want a rendered 0; event=%#v", event["series_matched"], event)
	}

	output.Reset()
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentOutput, Stage: StageEventACKed, Result: ResultSuccess,
		Trace:             TraceFields{StrategyID: "4101", QueryGroupKey: "qg-wire", EvaluationTime: 600},
		OutputWireFormats: OutputWireFormatCounts{contract.WireFormatPythonCompatible: 12, contract.WireFormatStandardRawEvent: 1},
		OutputEventKinds: OutputEventKindCounts{
			{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventAbnormal}: 12,
			{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery}: 1,
		},
	})
	event = map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode ACK log: %v; log=%s", err, output.String())
	}
	// The kinds under their own keys beside the formats: the standard-line
	// recovery is findable by name.
	if event["wire_format_events_standard_raw_event_recovery"] != float64(1) || event["wire_format_events_python_compatible_abnormal"] != float64(12) {
		t.Fatalf("ACK line kinds = %#v / %#v, want 1 and 12; event=%#v",
			event["wire_format_events_standard_raw_event_recovery"], event["wire_format_events_python_compatible_abnormal"], event)
	}
	if event["wire_format_events_python_compatible"] != float64(12) || event["wire_format_events_standard_raw_event"] != float64(1) {
		t.Fatalf("ACK line counts = %#v / %#v, want 12 and 1; event=%#v",
			event["wire_format_events_python_compatible"], event["wire_format_events_standard_raw_event"], event)
	}
	if _, present := event["wire_format"]; present {
		t.Fatalf("the ACK line carries a single wire_format; a batch has counts, not one word: %#v", event)
	}
	// A line with neither says neither: no empty key, no zero the reader
	// learns to skip.
	output.Reset()
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentOutput, Stage: StageEventACKed, Result: ResultSuccess,
		Trace: TraceFields{StrategyID: "4101", QueryGroupKey: "qg-wire", EvaluationTime: 660},
	})
	event = map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode ACK log: %v; log=%s", err, output.String())
	}
	for key := range event {
		if key == "wire_format" || len(key) > 18 && key[:18] == "wire_format_events" {
			t.Fatalf("a line without formats carries %q; event=%#v", key, event)
		}
	}
}

// The ACK line renders the sink's breakdown under one key per bucket that
// counted, so a reader of one line sees which protocol dropped which kind.
func TestTheACKLineRendersWhichProtocolDroppedWhichKind(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentOutput, Stage: StageEventACKed, Result: ResultSuccess,
		Trace: TraceFields{StrategyID: "4101", QueryGroupKey: "qg-wire", EvaluationTime: 600},
		OutputWrite: &OutputWriteFacts{Published: 2, WithoutMessage: 3, WithoutMessageBy: []OutputWithoutMessage{
			{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery, Events: 3},
			{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery, Events: 0},
		}},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode ACK log: %v; log=%s", err, output.String())
	}
	if event["events_without_message"] != float64(3) || event["events_without_message_python_compatible_recovery"] != float64(3) {
		t.Fatalf("ACK line = %#v, want events_without_message 3 and the python_compatible/recovery bucket 3", event)
	}
	if _, present := event["events_without_message_standard_raw_event_recovery"]; present {
		t.Fatalf("a bucket that counted nothing is on the line: %#v", event)
	}
}
