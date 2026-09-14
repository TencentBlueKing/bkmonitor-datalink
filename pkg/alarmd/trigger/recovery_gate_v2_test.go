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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A RECOVERY envelope resolves the alert on its series at the consumer,
// whatever Level that alert stands at. So the envelope goes only once every
// Level has agreed: a Level whose state is unknown, or whose recovery span
// still holds a triggering window, holds it. Two shapes are not consulted
// because a hold on them would never lift: a Level suppressed by its
// effective time, and a NORMAL Level whose recovery is disabled. In every
// case the Level results themselves are unchanged and still reach the state.
func TestRecoveryEnvelopeWaitsForEveryLevel(t *testing.T) {
	const source = int64(300)
	type verdict struct {
		held                       bool
		cause                      string
		levelID                    uint32
		passedLevelWithoutRecovery bool
	}
	tests := []struct {
		name      string
		levels    []contract.LevelIRV2
		facts     func(levels []strategy.CompiledLevel) []DetectionFact
		histories []LevelHistory
		want      verdict
		// primary is the Level the sent envelope is aggregated to: the lowest
		// priority among the Levels that said RECOVERY.
		primary uint32
	}{
		{
			name:   "a Level whose detect fact is unavailable holds the envelope",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want: verdict{held: true, cause: RecoveryHeldLevelUnavailable, levelID: 1},
		},
		{
			name:   "a Level whose history is still warming holds the envelope",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 2, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want: verdict{held: true, cause: RecoveryHeldLevelUnavailable, levelID: 1},
		},
		{
			name:   "a NORMAL Level whose recovery span still triggers holds the envelope",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 2, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true, source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want: verdict{held: true, cause: RecoveryHeldLevelRecovering, levelID: 1},
		},
		{
			name:   "a NORMAL Level without recovery is passed and counted, never held on",
			levels: []contract.LevelIRV2{levelWithoutRecoveryV2(1, 1, 1, 1), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{passedLevelWithoutRecovery: true},
			primary: 2,
		},
		{
			name:   "every Level recovered sends the envelope",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{},
			primary: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := compilePlanV2(t, test.levels)
			result, err := EvaluateV2(requestV2(t, plan, source, test.facts(plan.Levels()), test.histories, activeFactsV2(t, plan, source)))
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != contract.LevelResultRecovery {
				t.Fatalf("record result = %q, want RECOVERY: the fixture does not exercise the gate", result.RecordResult)
			}
			if result.LevelOutcomes[1].Result != contract.LevelResultRecovery {
				t.Fatalf("the recovering Level reads %q, want RECOVERY: the Level result must not change", result.LevelOutcomes[1].Result)
			}
			got := verdict{
				held: result.RecoveryGate.Held, cause: result.RecoveryGate.Cause, levelID: result.RecoveryGate.LevelID,
				passedLevelWithoutRecovery: result.RecoveryGate.PassedLevelWithoutRecovery,
			}
			if got != test.want {
				t.Fatalf("gate = %+v, want %+v", got, test.want)
			}
			if test.want.held {
				if result.TriggerEvent != nil {
					t.Fatalf("a held record still produced an envelope: %+v", result.TriggerEvent)
				}
				return
			}
			if result.TriggerEvent == nil || result.TriggerEvent.EventKind != contract.TriggerEventRecovery || result.TriggerEvent.PrimaryLevelID != test.primary {
				t.Fatalf("envelope = %+v, want RECOVERY with primary Level %d", result.TriggerEvent, test.primary)
			}
		})
	}
}

// Effective time can keep a Level suppressed for as long as its schedule
// says, so a hold on a suppressed Level would keep every strategy with a
// part-time Level in alarm until it comes back on. The suppressed Level is
// not consulted.
func TestRecoveryEnvelopeIsNotHeldOnASuppressedLevel(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 20, 1, 1, 1, staticUptimeV2()), levelV2(5, 1, 1, 1, 1, nil)})
	levels := plan.Levels()
	source := int64(64800)
	facts := effectiveFactsV2(t, plan, source, func(string) (*time.Location, error) { return time.UTC, nil })
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, facts))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.LevelOutcomes[0].SuppressedReason != contract.ReasonEffectiveTimeInactive {
		t.Fatalf("the part-time Level was not suppressed: %+v", result.LevelOutcomes[0])
	}
	if result.RecordResult != contract.LevelResultRecovery || result.RecoveryGate.Held || result.TriggerEvent == nil ||
		result.TriggerEvent.EventKind != contract.TriggerEventRecovery || result.TriggerEvent.PrimaryLevelID != 5 {
		t.Fatalf("result = %+v gate = %+v, want the envelope sent past the suppressed Level", result.TriggerEvent, result.RecoveryGate)
	}
}

// An abnormal Level decides the record before the gate is consulted: the
// envelope is ABNORMAL as before, and the gate says nothing.
func TestRecoveryGateDoesNotTouchAnAbnormalRecord(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)})
	levels := plan.Levels()
	const source = int64(300)
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing), factV2(levels[1], DetectionAnomalous)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
			{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: true}}},
		}, activeFactsV2(t, plan, source)))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil ||
		result.TriggerEvent.EventKind != contract.TriggerEventAbnormal || result.RecoveryGate != (RecoveryGateV2{}) {
		t.Fatalf("result = %q event = %+v gate = %+v, want an ABNORMAL envelope and an empty gate", result.RecordResult, result.TriggerEvent, result.RecoveryGate)
	}
}
