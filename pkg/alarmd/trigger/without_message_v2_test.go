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

// compatiblePlanV2 is a Plan publishing the compatibility protocol with the
// context its conversion reads: the Plans whose RECOVERY the sink used to
// take and leave without a message.
func compatiblePlanV2(t *testing.T, levels []contract.LevelIRV2) *strategy.CompiledPlan {
	t.Helper()
	plan := compilePlanV2WithOutput(t, levels, func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatPythonCompatible
		p.LegacyOutput = &contract.LegacyOutputContext{
			Strategy:        json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1756684800}`),
			DimensionFields: []string{"host"}, ItemID: "1",
		}
	})
	if plan.LegacyOutput() == nil || !plan.PublishesCompatibleProtocol() {
		t.Fatal("the fixture did not produce a Plan publishing the compatibility protocol with its context")
	}
	return plan
}

func recoveryRequestV2(t *testing.T, plan *strategy.CompiledPlan) EvaluationRequestV2 {
	t.Helper()
	const source = int64(300)
	levels := plan.Levels().Copy()
	facts := make([]DetectionFact, len(levels))
	histories := make([]LevelHistory, len(levels))
	for index, level := range levels {
		facts[index] = factV2(level, DetectionNormal)
		histories[index] = LevelHistory{LevelID: level.Definition().LevelID, View: pointHistory{step: 60, points: map[int64]bool{source: false}}}
	}
	request := requestV2(t, plan, source, facts, histories, activeFactsV2(t, plan, source))
	request.OpenAlerts = &openAlertSetFixture{}
	return request
}

// A RECOVERY the Plan's protocol has no message for is decided and not built:
// the record still says RECOVERY, the gate still says why the set was not
// asked, and what stands for the envelope is the format it had no message
// under. Exactly where the sink would have taken the envelope and sent
// nothing, and nowhere else: a compatibility Plan without the context the
// sink converts by builds it as before, so the sink still refuses it, and a
// standard Plan's RECOVERY is built for the consumer.
func TestARecoveryItsProtocolHasNoMessageForIsNotBuilt(t *testing.T) {
	recovered := []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	tests := []struct {
		name     string
		plan     func(t *testing.T) *strategy.CompiledPlan
		built    bool
		format   string
		gateWant string
	}{
		{
			name:  "compatibility protocol with its context: not built",
			plan:  func(t *testing.T) *strategy.CompiledPlan { return compatiblePlanV2(t, recovered) },
			built: false, format: contract.WireFormatPythonCompatible, gateWant: OpenAlertGateProtocolNotGated,
		},
		{
			name:  "compatibility protocol without its context: built, for the sink to refuse as before",
			plan:  func(t *testing.T) *strategy.CompiledPlan { return compilePlanV2(t, recovered) },
			built: true, gateWant: OpenAlertGateProtocolNotGated,
		},
		{
			name:  "standard protocol, no set passed: built for the consumer",
			plan:  func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			built: true, gateWant: OpenAlertGateNotConfigured,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.plan(t)
			request := recoveryRequestV2(t, plan)
			if test.gateWant == OpenAlertGateNotConfigured {
				request.OpenAlerts = nil
			}
			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != contract.LevelResultRecovery {
				t.Fatalf("record = %q, want RECOVERY: the fixture does not reach the envelope", result.RecordResult)
			}
			if result.RecoveryGate.OpenAlertGate != test.gateWant || result.RecoveryGate.Held {
				t.Fatalf("gate = %+v, want %s and not held", result.RecoveryGate, test.gateWant)
			}
			if (result.TriggerEvent != nil) != test.built {
				t.Fatalf("envelope = %+v, want built=%v", result.TriggerEvent, test.built)
			}
			if result.WithoutMessageFormat != test.format {
				t.Fatalf("without-message format = %q, want %q", result.WithoutMessageFormat, test.format)
			}
			if test.built && contract.DroppedAtSink(result.TriggerEvent) {
				t.Fatalf("an envelope the sink would drop was built: %+v", result.TriggerEvent)
			}
			if wantEvents := map[bool]uint64{true: 1, false: 0}[test.built]; result.Counts.Events != wantEvents {
				t.Fatalf("Counts.Events = %d, want %d", result.Counts.Events, wantEvents)
			}
		})
	}
}

// The anomaly of the same compatibility Plan is its protocol's one message:
// it is built, with the context the sink converts it by.
func TestTheCompatibilityProtocolStillBuildsItsAnomaly(t *testing.T) {
	plan := compatiblePlanV2(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)})
	const source = int64(300)
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous)}, []LevelHistory{{
		LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{180: true, 240: true, 300: true}},
	}}, activeFactsV2(t, plan, source))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil || result.TriggerEvent.LegacyOutput == nil {
		t.Fatalf("record %q built %+v, want an ABNORMAL envelope carrying its context", result.RecordResult, result.TriggerEvent)
	}
	if result.WithoutMessageFormat != "" {
		t.Fatalf("an anomaly was marked without message under %q", result.WithoutMessageFormat)
	}
}

// Building the envelope checked its invariants, and one that failed failed
// the record. A compatibility RECOVERY is no longer built, so those checks no
// longer run for it and it no longer fails; a standard RECOVERY, which is
// built, still does. Evidence over the admitted bytes is the invariant used.
func TestAnEnvelopeThatIsNotBuiltCannotFailItsInvariants(t *testing.T) {
	recovered := []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}

	compatible := recoveryRequestV2(t, compatiblePlanV2(t, recovered))
	compatible.Limits.MaxEvidenceBytesPerEvent = 1
	result, err := EvaluateV2(compatible)
	if err != nil {
		t.Fatalf("a compatibility RECOVERY failed an invariant of an envelope it no longer builds: %v", err)
	}
	if result.WithoutMessageFormat != contract.WireFormatPythonCompatible {
		t.Fatalf("without-message format = %q, want %q", result.WithoutMessageFormat, contract.WireFormatPythonCompatible)
	}

	standard := recoveryRequestV2(t, nativePlanV2(t, recovered, identity))
	standard.OpenAlerts = nil
	standard.Limits.MaxEvidenceBytesPerEvent = 1
	if _, err := EvaluateV2(standard); err == nil {
		t.Fatal("a standard RECOVERY over the evidence limit was built without failing: the control does not reach the invariant")
	}
}
