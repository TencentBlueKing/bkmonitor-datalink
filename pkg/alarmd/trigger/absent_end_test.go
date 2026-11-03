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
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func absentEndPlan(t *testing.T, end string) *strategy.CompiledPlan {
	t.Helper()
	level := levelV2(5, 1, 2, 1, 3, nil)
	recovery := map[string]any{"enabled": true, "consecutive_windows": 3, "missing_data": contract.RecoveryMissingDataEvaluateAsZero}
	if end != "" {
		recovery["end"] = end
	}
	level.RecoveryPlan.Config = mustJSONV2(recovery)
	plain := levelV2(6, 2, 2, 1, 3, nil)
	result := compileResultV2(t, []contract.LevelIRV2{level, plain}, func(p *contract.EvaluationPlanV2) {
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
	})
	plan, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal = %+v", result.PlanTerminal())
	}
	return plan
}

func absentEnd(plan *strategy.CompiledPlan, levels ...uint32) AbsentNoDataEndV1 {
	return AbsentNoDataEndV1{TenantID: "default", BusinessID: "2", Plan: plan,
		DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"host-a"`)},
		DedupeMD5: strings.Repeat("d", 32), SourceTime: 1_788_000_000, EvaluationTime: 1_788_000_060,
		ExecutionID: "execution", LevelIDs: levels, MaxEvidenceBytes: 1 << 16}
}

// The RECOVERY of an absent series no data ended: marked absent, each named
// level ended by no data with its ending and no detect evidence, and read
// back whole by the event's own reader.
func TestAnAbsentSeriesNoDataEndedIsARecoveryWithNoDetectEvidence(t *testing.T) {
	event, err := BuildAbsentNoDataEndV1(absentEnd(absentEndPlan(t, contract.RecoveryEndClose), 5))
	if err != nil {
		t.Fatal(err)
	}
	if event.EventKind != contract.TriggerEventRecovery || !event.RecordRef.Absent || event.DedupeMD5 != strings.Repeat("d", 32) ||
		len(event.LevelResults) != 1 || event.LevelResults[0].DetectEvidence != nil {
		t.Fatalf("event = %+v, want an absent RECOVERY of level 5 with no detect evidence", event)
	}
	recovery := event.LevelResults[0].DecisionWindow.Recovery
	if recovery.EndedBy != contract.RecoveryEndedByNoData || recovery.End != contract.RecoveryEndClose {
		t.Fatalf("recovery = %+v, want ended by no data and closed", recovery)
	}
	encoded, err := contract.EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "detect_evidence") || !strings.Contains(string(encoded), `"absent":true`) {
		t.Fatalf("encoded = %s, want absent and no detect evidence", encoded)
	}
	decoded, err := contract.DecodeTriggerEventV1(encoded)
	if err != nil || !decoded.RecordRef.Absent || decoded.LevelResults[0].DetectEvidence != nil {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
}

// Only a level written to end on no data, and only one the Plan has.
func TestAnAbsentNoDataEndRefusesALevelNotWrittenSo(t *testing.T) {
	plan := absentEndPlan(t, "")
	for _, levels := range [][]uint32{{6}, {9}, nil} {
		if _, err := BuildAbsentNoDataEndV1(absentEnd(plan, levels...)); err == nil {
			t.Fatalf("levels %v built, want refused", levels)
		}
	}
}

// An event about a record still needs its detect evidence: the reader
// refuses the absent event with its mark taken off. (The other directions
// are the contract's own validator's, tested beside it: any change to a
// level result here would also move the event's semantic digest.)
func TestARecordsEventWithoutDetectEvidenceIsRefused(t *testing.T) {
	event, err := BuildAbsentNoDataEndV1(absentEnd(absentEndPlan(t, ""), 5))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := contract.EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	delete(wire["record_ref"].(map[string]any), "absent")
	changed, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.DecodeTriggerEventV1(changed); err == nil {
		t.Fatal("an event about a record decoded without detect evidence")
	}
}
