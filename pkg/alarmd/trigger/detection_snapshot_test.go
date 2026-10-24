// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// namedOutputsAt is a table of outputs A, B, C and D - C the legacy one -
// with one record: A answered 1, B nothing there, C the record's own 50.1,
// and D a point in a state that failed.
func namedOutputsAt(recordID string) *execution.NamedOutputRecords {
	return &execution.NamedOutputRecords{Refs: []string{"A", "B", "C", "D"}, Records: map[string][]execution.NamedOutputValue{
		recordID: {
			{Value: json.RawMessage("1"), State: execution.NamedOutputSuccess},
			{State: execution.NamedOutputMissing},
			{Value: json.RawMessage("50.1"), State: execution.NamedOutputSuccess},
			{Value: json.RawMessage("2"), State: execution.NamedOutputError},
		}}}
}

// abnormalRequest decides level 5 ABNORMAL beside level 6, which stays
// NORMAL.
func abnormalRequest(t *testing.T) EvaluationRequestV2 {
	t.Helper()
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 1, 1, 1, nil), levelV2(6, 2, 1, 1, 1, nil)})
	history := []LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{300: true}}},
		{LevelID: 6, View: pointHistory{step: 60, points: map[int64]bool{300: false}}}}
	return requestV2(t, plan, 300, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous), factV2(plan.Levels().At(1), DetectionNormal)},
		history, activeFactsV2(t, plan, 300))
}

// An ABNORMAL event of a Plan with named outputs carries what its detection
// decided on: each output at the record, a value only where it answered -
// not where it failed, though a point was there - and the level that
// decided it, not the one that stayed normal - its algorithm with the matched OR group, the
// group's condition and the value it was judged at, its window, when its
// anomalies began and the unit. Its id and semantic digest are what they
// are without it.
func TestAnAbnormalEventCarriesItsDetectionSnapshot(t *testing.T) {
	request := abnormalRequest(t)
	plain, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	request.NamedOutputs = namedOutputsAt(request.Record.RecordID)
	named, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if plain.TriggerEvent.DetectionSnapshot != nil {
		t.Fatalf("an event of a Plan without named outputs carries %s", plain.TriggerEvent.DetectionSnapshot)
	}
	want := `{"version":1,"ref_values":{"A":{"value":1,"state":"SUCCESS"},"B":{"state":"MISSING"},"C":{"value":50.1,"state":"SUCCESS"},"D":{"state":"ERROR"}},` +
		`"levels":[{"level":5,"algorithms":[{"type":"Threshold","facts":{"value":50.100000,"group":0,"method":"gte","threshold":50.000000}}],` +
		`"window":{"required":1,"observed":1,"size":1},"anomaly_begin_time":300,"unit":"%"}]}`
	if got := string(named.TriggerEvent.DetectionSnapshot); got != want {
		t.Fatalf("snapshot\n%s\nwant\n%s", got, want)
	}
	if named.TriggerEvent.EventID != plain.TriggerEvent.EventID || named.TriggerEvent.EventSemanticDigest != plain.TriggerEvent.EventSemanticDigest {
		t.Fatal("the snapshot moved the event's identity")
	}
	encoded, err := contract.EncodeTriggerEventV1(named.TriggerEvent)
	if err != nil {
		t.Fatal(err)
	}
	if plainEncoded, _ := contract.EncodeTriggerEventV1(plain.TriggerEvent); string(encoded) != string(plainEncoded) {
		t.Fatal("the snapshot is in the event's own encoding")
	}
}

// A record not in the table - or a no-data point, which is never read -
// carries no ref_values; its levels it still carries.
func TestAnEventOfARecordNotReadCarriesNoRefValues(t *testing.T) {
	for name, mutate := range map[string]func(*EvaluationRequestV2){
		"not in the table": func(request *EvaluationRequestV2) { request.NamedOutputs = namedOutputsAt("another-record") },
		"no data": func(request *EvaluationRequestV2) {
			request.NamedOutputs = namedOutputsAt(request.Record.RecordID)
			request.RecordRef.Dimensions = map[string]json.RawMessage{contract.NoDataDimensionTag: json.RawMessage("true")}
		},
	} {
		request := abnormalRequest(t)
		mutate(&request)
		result, err := EvaluateV2(request)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot contract.DetectionSnapshotV1
		if err := json.Unmarshal(result.TriggerEvent.DetectionSnapshot, &snapshot); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if snapshot.RefValues != nil || len(snapshot.Levels) != 1 {
			t.Errorf("%s: %s", name, result.TriggerEvent.DetectionSnapshot)
		}
	}
}

// A RECOVERY event carries its levels and their windows, and no
// ref_values: no reader needs the outputs once the alert recovers.
func TestARecoveryEventCarriesItsLevelsAndNoRefValues(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)})
	request := requestV2(t, plan, 360, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous)}, []LevelHistory{{
		LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{180: false, 240: false, 300: false, 360: true}},
	}}, activeFactsV2(t, plan, 360))
	request.NamedOutputs = namedOutputsAt(request.Record.RecordID)
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.TriggerEvent == nil || result.TriggerEvent.EventKind != contract.TriggerEventRecovery {
		t.Fatalf("result %+v", result)
	}
	var snapshot contract.DetectionSnapshotV1
	if err := json.Unmarshal(result.TriggerEvent.DetectionSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.RefValues != nil || len(snapshot.Levels) != 1 || snapshot.Levels[0].Level != 5 ||
		snapshot.Levels[0].Window != (contract.DetectionWindowV1{Required: 2, Observed: 1, Size: 3}) ||
		len(snapshot.Levels[0].Algorithms) != 1 || snapshot.Levels[0].Algorithms[0].Type != "Threshold" {
		t.Fatalf("snapshot %s", result.TriggerEvent.DetectionSnapshot)
	}
}

// The largest snapshot a realistic named strategy decides, built as the
// trigger builds it: four outputs under long reference names, three levels
// all ABNORMAL, each of three Threshold algorithms whose matched OR group
// has three conditions. It is whole, well under its bound; its size is
// logged for the hand-over.
func TestTheLargestRealisticNamedStrategySnapshotIsWhole(t *testing.T) {
	threshold := map[string]any{
		"value_field": "value", "data_unit": "percent", "threshold_unit_prefix": "", "precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
		"groups": []any{map[string]any{"conditions": []any{
			map[string]any{"operator": "GTE", "threshold_decimal": "50.125"},
			map[string]any{"operator": "LT", "threshold_decimal": "99.875"},
			map[string]any{"operator": "NEQ", "threshold_decimal": "75.5"},
		}}},
	}
	levels := make([]contract.LevelIRV2, 0, 3)
	for id := uint32(1); id <= 3; id++ {
		level := levelV2(id, id, 5, 3, 1, nil)
		level.Connector = contract.LevelConnectorOR
		level.DetectPlan.Algorithms = nil
		for range 3 {
			level.DetectPlan.Algorithms = append(level.DetectPlan.Algorithms, contract.AlgorithmIRV2{Type: "Threshold", Version: 1, Config: mustJSONV2(threshold)})
		}
		levels = append(levels, level)
	}
	plan := compilePlanV2(t, levels)
	histories := make([]LevelHistory, 0, 3)
	facts := make([]DetectionFact, 0, 3)
	for index, level := range plan.Levels().All() {
		histories = append(histories, LevelHistory{LevelID: level.Definition().LevelID,
			View: pointHistory{step: 60, points: map[int64]bool{120: true, 180: true, 240: true, 300: true}}})
		fact := factV2(level, DetectionAnomalous)
		algorithm := uint32(index)
		fact.Evidence.MatchedAlgorithmOrdinal = &algorithm
		facts = append(facts, fact)
	}
	request := requestV2(t, plan, 300, facts, histories, activeFactsV2(t, plan, 300))
	refs := []string{"REQUEST_SUCCESS_COUNT_FROM_GATEWAY", "REQUEST_FAILURE_COUNT_FROM_GATEWAY",
		"REQUEST_TOTAL_COUNT_FROM_UPSTREAMS", "REQUEST_SUCCESS_RATE_PERCENTAGE_RESULT"}
	values := make([]execution.NamedOutputValue, 0, len(refs))
	for range refs {
		values = append(values, execution.NamedOutputValue{Value: json.RawMessage("123456789.123456"), State: execution.NamedOutputSuccess})
	}
	request.NamedOutputs = &execution.NamedOutputRecords{Refs: refs, Records: map[string][]execution.NamedOutputValue{request.Record.RecordID: values}}
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.TriggerEvent == nil || result.Snapshot == nil {
		t.Fatalf("result %+v", result)
	}
	size := len(result.TriggerEvent.DetectionSnapshot)
	t.Logf("largest realistic named strategy snapshot: %d bytes", size)
	var snapshot contract.DetectionSnapshotV1
	if err := json.Unmarshal(result.TriggerEvent.DetectionSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Result != contract.DetectionSnapshotWhole || size > contract.MaxDetectionSnapshotBytes || len(snapshot.Levels) != 3 || len(snapshot.RefValues) != 4 {
		t.Fatalf("%d bytes, %s: %s", size, result.Snapshot.Result, result.TriggerEvent.DetectionSnapshot)
	}
	if facts := snapshot.Levels[2].Algorithms[2].Facts; len(facts) != 8 {
		t.Fatalf("the third level's deciding algorithm has %d facts, want value, group and three conditions: %s", len(facts), result.TriggerEvent.DetectionSnapshot)
	}
}

type discardSnapshots struct{}

func (discardSnapshots) SaveBatch(context.Context, []legacyoutput.Snapshot) error { return nil }

// The contract promises the consumer that the legacy output's ref value is
// the event's current value. From the public entry, through each output:
// a Plan decides a record whose table holds another value for its legacy
// output, and both outputs write the legacy ref value as the very value they
// write for the event - the legacy data.value, and the standard event's
// value - byte for byte.
func TestTheLegacyRefValueIsTheEventsCurrentValueInBothOutputs(t *testing.T) {
	plan := compilePlanV2WithOutput(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}, func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatPythonCompatible
		p.LegacyOutput = &contract.LegacyOutputContext{
			Strategy: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1756684800,"name":"cpu","scenario":"os",` +
				`"items":[{"id":1,"name":"cpu usage","query_configs":[{"metric_id":"bk_monitor.system.cpu","data_type_label":"time_series"}]}]}`),
			DimensionFields: []string{"host"}, ItemID: "1",
		}
	})
	request := requestV2(t, plan, 300, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous)},
		[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{300: true}}}}, activeFactsV2(t, plan, 300))
	table := namedOutputsAt(request.Record.RecordID)
	table.LegacyRef = "C"
	table.Records[request.Record.RecordID][2].Value = json.RawMessage("49.9")
	request.NamedOutputs = table
	result, err := EvaluateV2(request)
	if err != nil || result.TriggerEvent == nil {
		t.Fatalf("EvaluateV2() = %+v, %v", result, err)
	}
	event := result.TriggerEvent

	written, err := (&legacyoutput.Converter{Store: discardSnapshots{}, Now: func() time.Time { return time.Unix(400, 0) }}).
		ConvertBatch(context.Background(), []contract.TriggerEventV1{*event})
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		ExtraInfo struct {
			OriginAlarm struct {
				Data struct {
					Value     json.RawMessage `json:"value"`
					RefValues map[string]struct {
						Value json.RawMessage `json:"value"`
					} `json:"ref_values"`
				} `json:"data"`
			} `json:"origin_alarm"`
		} `json:"extra_info"`
	}
	if err := json.Unmarshal(written[0].Payload, &legacy); err != nil {
		t.Fatal(err)
	}
	data := legacy.ExtraInfo.OriginAlarm.Data
	if len(data.Value) == 0 || string(data.RefValues["C"].Value) != string(data.Value) {
		t.Fatalf("legacy data.value %s, ref_values.C.value %s: want them the same bytes", data.Value, data.RefValues["C"].Value)
	}

	standard := *event
	standard.WireFormat = contract.WireFormatStandardRawEvent
	standard.StrategyRef = &contract.StrategySnapshotRef{TenantID: "default", BusinessID: 2, StrategyID: 1001, Revision: 7}
	standard.DedupeMD5 = strings.Repeat("b", 32)
	converter, err := linkdoutput.NewConverter(nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := converter.Convert(&standard)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Values    map[string]json.RawMessage `json:"values"`
		ExtraData struct {
			DetectionSnapshot struct {
				RefValues map[string]struct {
					Value json.RawMessage `json:"value"`
				} `json:"ref_values"`
			} `json:"detection_snapshot"`
		} `json:"extra_data"`
	}
	if err := json.Unmarshal(message.Payload, &wire); err != nil {
		t.Fatal(err)
	}
	if got, want := string(wire.ExtraData.DetectionSnapshot.RefValues["C"].Value), string(wire.Values["value"]); got == "" || got != want {
		t.Fatalf("standard values.value %s, ref_values.C.value %s: want them the same bytes", want, got)
	}
}

// A snapshot that cannot be held to its bounds is an optional field, and
// never stops detection: the event is decided and sent as it would be
// without one, the snapshot is left off, and it counts once as dropped,
// with no output states.
func TestASnapshotThatCannotBeEncodedIsDroppedAndTheEventStillGoes(t *testing.T) {
	encodeDetectionSnapshot = func(contract.DetectionSnapshotV1) (json.RawMessage, bool, error) {
		return nil, false, errors.New("past its size with no algorithm facts left to drop")
	}
	t.Cleanup(func() { encodeDetectionSnapshot = contract.EncodeDetectionSnapshotV1 })
	request := abnormalRequest(t)
	plain, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	request.NamedOutputs = namedOutputsAt(request.Record.RecordID)
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("an optional snapshot stopped detection: %v", err)
	}
	if result.TriggerEvent == nil || result.TriggerEvent.DetectionSnapshot != nil || result.TriggerEvent.EventID != plain.TriggerEvent.EventID {
		t.Fatalf("event %+v, want the event without its snapshot", result.TriggerEvent)
	}
	if result.Snapshot == nil || result.Snapshot.Result != contract.DetectionSnapshotDropped || len(result.Snapshot.States) != 0 {
		t.Fatalf("snapshot facts %+v, want dropped with no states", result.Snapshot)
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	before := snapshotCount(t, recorder, "alarmd_event_detection_snapshots_total", "dropped")
	recorder.ObserveDetectionSnapshot(result.Snapshot.States, result.Snapshot.Result)
	if after := snapshotCount(t, recorder, "alarmd_event_detection_snapshots_total", "dropped"); after != before+1 {
		t.Fatalf("dropped went %v to %v, want up by exactly one", before, after)
	}
	for _, state := range metric.DetectionSnapshotStates {
		if count := snapshotCount(t, recorder, "alarmd_event_detection_snapshot_ref_states_total", state); count != 0 {
			t.Fatalf("a dropped snapshot counted %v outputs %s", count, state)
		}
	}
}

func snapshotCount(t *testing.T, recorder *metric.Recorder, family, label string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range families {
		if candidate.GetName() != family {
			continue
		}
		for _, series := range candidate.GetMetric() {
			if series.GetLabel()[0].GetValue() == label {
				return series.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("%s{%s} is not pre-created", family, label)
	return 0
}
