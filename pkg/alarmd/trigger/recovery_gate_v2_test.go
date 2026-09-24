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

// A RECOVERY speaks for its own Level: the envelope carries that Level's
// result and the consumer ends only an alert of that Level's severity. So
// another Level's state does not hold it. Whatever that Level is -- unknown,
// NORMAL with a triggering window still in its recovery span, NORMAL without
// recovery -- the envelope goes, with the recovering Level as its primary
// and the other Level never reading RECOVERY in it, and the gate names the
// Level it went beside. The Level results themselves are unchanged.
func TestARecoveryIsNotHeldOnAnotherLevel(t *testing.T) {
	const source = int64(300)
	type verdict struct {
		beside        string
		besideLevelID uint32
	}
	tests := []struct {
		name      string
		levels    []contract.LevelIRV2
		facts     func(levels []strategy.CompiledLevel) []DetectionFact
		histories []LevelHistory
		want      verdict
		// primary is the Level the envelope is aggregated to: the lowest
		// priority among the Levels that said RECOVERY.
		primary uint32
	}{
		{
			name:   "beside a Level whose detect fact is unavailable",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{beside: RecoveryBesideLevelUnavailable, besideLevelID: 1},
			primary: 2,
		},
		{
			name:   "beside a Level whose history is still warming",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 2, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{beside: RecoveryBesideLevelUnavailable, besideLevelID: 1},
			primary: 2,
		},
		{
			name:   "beside a NORMAL Level whose recovery span still triggers",
			levels: []contract.LevelIRV2{levelV2(1, 1, 1, 1, 2, nil), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true, source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{beside: RecoveryBesideLevelRecovering, besideLevelID: 1},
			primary: 2,
		},
		{
			name:   "beside a NORMAL Level without recovery",
			levels: []contract.LevelIRV2{levelWithoutRecoveryV2(1, 1, 1, 1), levelV2(2, 2, 1, 1, 1, nil)},
			facts: func(levels []strategy.CompiledLevel) []DetectionFact {
				return []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			},
			histories: []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			},
			want:    verdict{beside: RecoveryBesideLevelWithoutRecovery, besideLevelID: 1},
			primary: 2,
		},
		{
			name:   "every Level recovered",
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
			got := verdict{beside: result.RecoveryGate.Beside, besideLevelID: result.RecoveryGate.BesideLevelID}
			if got != test.want || result.RecoveryGate.Held {
				t.Fatalf("gate = %+v, want %+v and not held", result.RecoveryGate, test.want)
			}
			event := result.TriggerEvent
			if event == nil || event.EventKind != contract.TriggerEventRecovery || event.PrimaryLevelID != test.primary {
				t.Fatalf("envelope = %+v, want RECOVERY with primary Level %d", event, test.primary)
			}
			// The envelope says RECOVERY only for the Levels that recovered:
			// the other Level is absent or NORMAL in it, never RECOVERY.
			for _, level := range event.LevelResults {
				if level.LevelID == test.want.besideLevelID && level.Result == contract.LevelResultRecovery {
					t.Fatalf("the envelope reads RECOVERY for Level %d, which did not recover: %+v", level.LevelID, event.LevelResults)
				}
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
	if result.RecordResult != contract.LevelResultRecovery || result.RecoveryGate.Held || result.RecoveryGate.Beside != "" || result.TriggerEvent == nil ||
		result.TriggerEvent.EventKind != contract.TriggerEventRecovery || result.TriggerEvent.PrimaryLevelID != 5 {
		t.Fatalf("result = %+v gate = %+v, want the envelope sent past the suppressed Level", result.TriggerEvent, result.RecoveryGate)
	}
}

// The gate reads whether a Level's recovery is enabled off the outcome, so
// every outcome has to carry it, including the ones decided before the
// recovery plan is otherwise looked at: a Level suppressed by its effective
// time and a Level whose detect fact is unavailable both return early. If
// either path left the flag unset, the gate would read a Level with recovery
// as one without, and nothing else would notice.
func TestEveryLevelOutcomeCarriesItsRecoveryFlag(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 20, 1, 1, 1, staticUptimeV2()), levelWithoutRecoveryV2(5, 1, 1, 1)})
	levels := plan.Levels()
	source := int64(64800)
	facts := effectiveFactsV2(t, plan, source, func(string) (*time.Location, error) { return time.UTC, nil })
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{factV2(levels[0], DetectionNormal), unavailableFactV2(levels[1], contract.ReasonRequiredValueMissing)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, facts))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	suppressed, unavailable := result.LevelOutcomes[0], result.LevelOutcomes[1]
	if suppressed.SuppressedReason == "" || unavailable.UnavailableReason == "" {
		t.Fatalf("fixture did not take the early paths: %+v / %+v", suppressed, unavailable)
	}
	if !suppressed.RecoveryEnabled {
		t.Fatal("the suppressed Level, whose recovery is enabled, returned an outcome saying it is not")
	}
	if unavailable.RecoveryEnabled {
		t.Fatal("the unavailable Level, whose recovery is disabled, returned an outcome saying it is enabled")
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

// The Level a RECOVERY is counted beside is the first that used to hold it --
// unavailable or still recovering -- even when a NORMAL Level without recovery
// comes earlier in Level order: that shape never held, and counting under it
// would hide exactly the records the change is read by.
func TestARecoveryIsCountedBesideTheLevelThatUsedToHoldIt(t *testing.T) {
	const source = int64(300)
	plan := compilePlanV2(t, []contract.LevelIRV2{levelWithoutRecoveryV2(1, 1, 1, 1), levelV2(2, 2, 1, 1, 1, nil), levelV2(3, 3, 1, 1, 1, nil)})
	levels := plan.Levels()
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{factV2(levels[0], DetectionNormal), unavailableFactV2(levels[1], contract.ReasonRequiredValueMissing), factV2(levels[2], DetectionNormal)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
			{LevelID: 3, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, activeFactsV2(t, plan, source)))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultRecovery || result.LevelOutcomes[2].Result != contract.LevelResultRecovery {
		t.Fatalf("record %q outcomes %+v: the fixture does not exercise the gate", result.RecordResult, result.LevelOutcomes)
	}
	if result.RecoveryGate.Beside != RecoveryBesideLevelUnavailable || result.RecoveryGate.BesideLevelID != 2 || result.TriggerEvent == nil {
		t.Fatalf("gate = %+v event = %+v, want counted beside the unavailable Level 2 and sent", result.RecoveryGate, result.TriggerEvent)
	}
}
