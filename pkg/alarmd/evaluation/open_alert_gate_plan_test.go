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

type openAlertSetStub map[string]bool

func (set openAlertSetStub) Contains(tenantID, strategyID, fingerprint string) bool {
	return set[tenantID+"/"+strategyID+"/"+fingerprint]
}

// The second recovery gate on a real evaluator result. The record every
// Level agreed on goes only if the consumer holds an open alert on its
// series; a record it holds none on is held, and the hold has to travel the
// same way the first gate's does: on each RECOVERY outcome, so that the
// result contract the Worker runs on this result accepts the record without
// its envelope. That is the path the first gate shipped without and was
// refused on for a whole release.
func TestEvaluatorGatesARecoveryEnvelopeOnTheOpenAlertSet(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	native := func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &identity
	}
	// The fixture record carries no dimensions; the fingerprint is the
	// strategy's and business's alone, which is what the gate asks about.
	fingerprint, err := contract.MonitorDedupeMD5("7", "2", map[string]json.RawMessage{}, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range []struct {
		name          string
		shape         func(*contract.EvaluationPlanV2)
		set           contract.OpenAlertSet
		wantCounts    execution.OpenAlertGateCounts
		wantEnvelopes int
		wantHeld      bool
	}{
		{name: "an open alert on the series: the envelope goes", shape: native, set: openAlertSetStub{"tenant/7/" + fingerprint: true},
			wantCounts: execution.OpenAlertGateCounts{Passed: 1}, wantEnvelopes: 1},
		{name: "no open alert on the series: held on every RECOVERY outcome, state still written", shape: native, set: openAlertSetStub{},
			wantCounts: execution.OpenAlertGateCounts{HeldNoOpenAlert: 1}, wantEnvelopes: 0, wantHeld: true},
		{name: "no set passed: the envelope goes and the wiring gap is counted", shape: native, set: nil,
			wantCounts: execution.OpenAlertGateCounts{NotConfigured: 1}, wantEnvelopes: 1},
		{name: "a Plan off the consumer's protocol: the set is not asked", shape: nil, set: openAlertSetStub{},
			wantCounts: execution.OpenAlertGateCounts{ProtocolNotGated: 1}, wantEnvelopes: 1},
	} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevelsShaped(t, "50", "50", arm.shape)
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
				{LevelID: 6, DetectFingerprint: plan.Levels()[1].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			}}}
			req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
			req.OpenAlerts = arm.set
			result, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			plan0 := result.Plans[0]
			if len(plan0.LevelOutcomes) != 2 || plan0.LevelOutcomes[0].Outcome != execution.LevelOutcomeRecovery || plan0.LevelOutcomes[1].Outcome != execution.LevelOutcomeRecovery {
				t.Fatalf("outcomes=%+v want both Levels RECOVERY: the fixture does not reach the second gate", plan0.LevelOutcomes)
			}
			if plan0.OpenAlertGate != arm.wantCounts {
				t.Fatalf("open alert gate counts=%+v want %+v", plan0.OpenAlertGate, arm.wantCounts)
			}
			if plan0.RecoveryGate != (execution.RecoveryGateCounts{}) {
				t.Fatalf("first gate counts=%+v, want none: a record is counted by one gate only", plan0.RecoveryGate)
			}
			if len(plan0.StateResults) != 1 {
				t.Fatalf("state results=%d want the record's state written either way", len(plan0.StateResults))
			}
			if got := len(plan0.StateResults[0].Events); got != arm.wantEnvelopes {
				t.Fatalf("envelopes=%d want %d", got, arm.wantEnvelopes)
			}
			if plan0.LevelOutcomes[0].EnvelopeHeld != arm.wantHeld || plan0.LevelOutcomes[1].EnvelopeHeld != arm.wantHeld {
				t.Fatalf("EnvelopeHeld = (%t, %t), want both %t", plan0.LevelOutcomes[0].EnvelopeHeld, plan0.LevelOutcomes[1].EnvelopeHeld, arm.wantHeld)
			}
			if err := result.Validate(req); err != nil {
				t.Fatalf("the result contract refused the evaluator's own result: %v", err)
			}
		})
	}
}
