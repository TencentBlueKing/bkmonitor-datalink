// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Evaluate carries the request's named outputs to the ABNORMAL event of the record
// they were aligned to, by RecordID, as its detection snapshot; without
// them the same record's event carries none, and is otherwise the same.
func TestEvaluateCarriesTheNamedOutputsToTheEventsSnapshot(t *testing.T) {
	native := func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	}
	events := map[bool]contract.TriggerEventV1{}
	for _, named := range []bool{true, false} {
		plan := compiledTwoLevelsShaped(t, "50", "50", native)
		history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
			{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		}}}
		req := requestFixtureTwoLevels(t, plan, json.RawMessage(`60`), history)
		record, ok := req.Inputs[0].Inputs[0].View.Record(0)
		if !ok {
			t.Fatal("the fixture has no record")
		}
		if named {
			req.NamedOutputs = &execution.NamedOutputRecords{Refs: []string{"A", "RESULT"}, Records: map[string][]execution.NamedOutputValue{
				record.RecordID(): {{Value: json.RawMessage("7"), State: execution.NamedOutputSuccess}, {Value: json.RawMessage("60"), State: execution.NamedOutputSuccess}},
			}}
		}
		result, err := newEvaluator(t).Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate()=%v", err)
		}
		if len(result.Plans[0].StateResults) != 1 || len(result.Plans[0].StateResults[0].Events) != 1 {
			t.Fatalf("state results %+v, want one record with one event", result.Plans[0].StateResults)
		}
		events[named] = result.Plans[0].StateResults[0].Events[0]
	}
	if events[false].DetectionSnapshot != nil {
		t.Fatalf("an event without named outputs carries %s", events[false].DetectionSnapshot)
	}
	var snapshot contract.DetectionSnapshotV1
	if err := json.Unmarshal(events[true].DetectionSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.RefValues["A"].State != "SUCCESS" || string(snapshot.RefValues["A"].Value) != "7" ||
		string(snapshot.RefValues["RESULT"].Value) != "60" || len(snapshot.Levels) == 0 {
		t.Fatalf("snapshot %s", events[true].DetectionSnapshot)
	}
	if events[true].EventID != events[false].EventID || events[true].EventSemanticDigest != events[false].EventSemanticDigest {
		t.Fatal("the named outputs moved the event's identity")
	}
}

// Each snapshot an event carries is heard once, with its named outputs'
// states; an event without one is not heard.
func TestEvaluateHearsEachDetectionSnapshot(t *testing.T) {
	native := func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	}
	for _, named := range []bool{true, false} {
		plan := compiledTwoLevelsShaped(t, "50", "50", native)
		history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
			{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		}}}
		req := requestFixtureTwoLevels(t, plan, json.RawMessage(`60`), history)
		record, _ := req.Inputs[0].Inputs[0].View.Record(0)
		if named {
			req.NamedOutputs = &execution.NamedOutputRecords{Refs: []string{"A", "RESULT"}, Records: map[string][]execution.NamedOutputValue{
				record.RecordID(): {{State: execution.NamedOutputMissing}, {Value: json.RawMessage("60"), State: execution.NamedOutputSuccess}},
			}}
		}
		var heard [][]string
		evaluator := newEvaluator(t).WithDetectionSnapshots(func(states []string, result string) {
			if result != contract.DetectionSnapshotWhole {
				t.Errorf("a small snapshot was heard as %s", result)
			}
			heard = append(heard, states)
		})
		if _, err := evaluator.Evaluate(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if named && (len(heard) != 1 || len(heard[0]) != 2 || heard[0][0] != "MISSING" || heard[0][1] != "SUCCESS") {
			t.Fatalf("heard %v, want one snapshot of MISSING and SUCCESS", heard)
		}
		if !named && len(heard) != 0 {
			t.Fatalf("an event without a snapshot was heard: %v", heard)
		}
	}
}
