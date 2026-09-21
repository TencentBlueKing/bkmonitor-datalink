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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// openAlertSetFixture answers membership from a fixed set and records what it
// was asked, so a test can assert both the verdict and that the gate asked
// with the identity the consumer keys alerts by.
type openAlertSetFixture struct {
	members map[string]bool
	asked   []string
}

func (set *openAlertSetFixture) Contains(tenantID, strategyID, fingerprint string) bool {
	key := tenantID + "/" + strategyID + "/" + fingerprint
	set.asked = append(set.asked, key)
	return set.members[key]
}

func nativePlanV2(t *testing.T, levels []contract.LevelIRV2, identity *contract.MonitorOutputIdentity) *strategy.CompiledPlan {
	t.Helper()
	return compilePlanV2WithOutput(t, levels, func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = identity
	})
}

// The consumer resolves whatever alert it holds on a RECOVERY envelope and
// closes the envelope as an orphan when it holds none. So once every Level
// has agreed, the envelope goes only if the consumer holds an open alert on
// the series. The set answers membership only: the Level results and the
// first gate are unchanged by it.
func TestRecoveryEnvelopeGoesOnlyToAnOpenAlert(t *testing.T) {
	const source = int64(300)
	recovered := []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	fingerprint := func(t *testing.T) string {
		t.Helper()
		// The same dimensions requestV2 puts on the record, so the fixture
		// member is the fingerprint the gate will ask about.
		md5, err := contract.MonitorDedupeMD5("1001", "2", map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, *identity)
		if err != nil {
			t.Fatal(err)
		}
		return md5
	}
	tests := []struct {
		name     string
		plan     func(t *testing.T) *strategy.CompiledPlan
		set      func(t *testing.T) *openAlertSetFixture
		wantGate RecoveryGateV2
		envelope bool
		asked    int
	}{
		{
			name: "the consumer holds an open alert: the envelope goes",
			plan: func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set: func(t *testing.T) *openAlertSetFixture {
				return &openAlertSetFixture{members: map[string]bool{"default/1001/" + fingerprint(t): true}}
			},
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGatePassed},
			envelope: true, asked: 1,
		},
		{
			name:     "the consumer holds no alert on the series: held, nothing to resolve",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		},
		{
			name:     "no fingerprint can be built: held as unknown, the set is not asked",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, nil) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldFingerprintUnknown, OpenAlertGate: OpenAlertGateHeldFingerprintUnknown},
			envelope: false, asked: 0,
		},
		{
			name:     "no set was passed: the envelope goes as before the gate, and says so",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set:      func(t *testing.T) *openAlertSetFixture { return nil },
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGateNotConfigured},
			envelope: true, asked: 0,
		},
		{
			name:     "a Plan on the compatibility protocol: the set is not asked",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return compilePlanV2(t, recovered) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGateProtocolNotGated},
			envelope: true, asked: 0,
		},
		{
			name: "a historical decision-event Plan now uses the consumer recovery gate",
			plan: func(t *testing.T) *strategy.CompiledPlan {
				return compilePlanV2WithOutput(t, recovered, func(p *contract.EvaluationPlanV2) {
					p.WireFormat = contract.WireFormatTriggerEvent
					p.StrategyRef.SnapshotRevision = 7
					p.StrategyIR.StrategyRef.SnapshotRevision = 7
					p.OutputIdentity = identity
				})
			},
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		}, {
			name: "a historical empty-format Plan with revision uses the consumer recovery gate",
			plan: func(t *testing.T) *strategy.CompiledPlan {
				return compilePlanV2WithOutput(t, recovered, func(p *contract.EvaluationPlanV2) {
					p.WireFormat = ""
					p.StrategyRef.SnapshotRevision = 7
					p.StrategyIR.StrategyRef.SnapshotRevision = 7
					p.OutputIdentity = identity
				})
			},
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.plan(t)
			levels := plan.Levels()
			request := requestV2(t, plan, source,
				[]DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)},
				[]LevelHistory{
					{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
					{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				}, activeFactsV2(t, plan, source))
			set := test.set(t)
			if set != nil {
				request.OpenAlerts = set
			}
			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != contract.LevelResultRecovery || result.LevelOutcomes[0].Result != contract.LevelResultRecovery {
				t.Fatalf("record = %q / Level 1 = %q, want RECOVERY: the fixture does not reach the second gate", result.RecordResult, result.LevelOutcomes[0].Result)
			}
			if result.RecoveryGate != test.wantGate {
				t.Fatalf("gate = %+v, want %+v", result.RecoveryGate, test.wantGate)
			}
			if (result.TriggerEvent != nil) != test.envelope {
				t.Fatalf("envelope = %+v, want present=%v", result.TriggerEvent, test.envelope)
			}
			if set != nil && len(set.asked) != test.asked {
				t.Fatalf("the set was asked %d times %v, want %d", len(set.asked), set.asked, test.asked)
			}
			if test.envelope && result.Counts.Events != 1 || !test.envelope && result.Counts.Events != 0 {
				t.Fatalf("Counts.Events = %d with envelope present=%v", result.Counts.Events, test.envelope)
			}
		})
	}
}

// The two gates are asked in order and a record is counted by at most one:
// when a Level holds the envelope the set is not asked, and its outcome
// stays empty rather than reading as "passed" or "not configured".
func TestOpenAlertSetIsNotAskedWhenALevelHolds(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}, identity)
	levels := plan.Levels()
	set := &openAlertSetFixture{}
	request := requestV2(t, plan, source,
		[]DetectionFact{unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing), factV2(levels[1], DetectionNormal)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
			{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, activeFactsV2(t, plan, source))
	request.OpenAlerts = set
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	want := RecoveryGateV2{Held: true, Cause: RecoveryHeldLevelUnavailable, LevelID: 1}
	if result.RecoveryGate != want {
		t.Fatalf("gate = %+v, want %+v", result.RecoveryGate, want)
	}
	if len(set.asked) != 0 {
		t.Fatalf("the set was asked %v although a Level held the envelope", set.asked)
	}
}

// An ABNORMAL record is never gated by the set: an anomaly is pushed whether
// or not the consumer already holds an alert, and the set is not asked.
func TestOpenAlertSetDoesNotTouchAnAbnormalRecord(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}, identity)
	set := &openAlertSetFixture{}
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels()[0], DetectionAnomalous)},
		[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: true}}}}, activeFactsV2(t, plan, source))
	request.OpenAlerts = set
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil || result.TriggerEvent.EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("result = %q envelope = %+v, want an ABNORMAL envelope", result.RecordResult, result.TriggerEvent)
	}
	if result.RecoveryGate != (RecoveryGateV2{}) || len(set.asked) != 0 {
		t.Fatalf("gate = %+v asked = %v, want untouched", result.RecoveryGate, set.asked)
	}
}
