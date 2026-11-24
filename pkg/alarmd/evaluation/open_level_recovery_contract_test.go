// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// silentLink is a link the copy never needs to search here: every open Level
// it is asked about comes from its own acknowledged sends.
type silentLink struct{}

func (silentLink) Search(context.Context, string, json.RawMessage, string, int) (linkdmanage.SearchPage, error) {
	return linkdmanage.SearchPage{}, nil
}

// A RECOVERY round of a two-Level record, gated on the open Levels the
// production copy learned from its own ABNORMAL: the envelope carries only
// the RECOVERY of the Level the alert is at, and the result
// contract the Worker runs on the result (stream_session's evaluated.Validate)
// accepts it. The Level left out of the envelope still recovers in the state.
// Before the decomposition said which Level it left out, every such round
// failed the Slot as EVENT_OMITS_SIBLING_OUTCOME and was skipped as a gap.
func TestAPartialLevelRecoveryPassesTheResultContract(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	native := func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &identity
	}
	fingerprint, err := contract.MonitorDedupeMD5("7", "2", map[string]json.RawMessage{}, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range []struct {
		name string
		// opened is the Levels the copy's own ABNORMAL opened; anomalous the
		// Levels the record's history holds anomalous, which recover now.
		opened, anomalous []uint32
		sent              []uint32
		omitted           []uint32
		held              bool
	}{
		{name: "the alert at Level 5, Levels 5 and 6 recover: only 5 goes", opened: []uint32{5, 6}, anomalous: []uint32{5, 6}, sent: []uint32{5}, omitted: []uint32{6}},
		{name: "the alert at Level 6 alone, Levels 5 and 6 recover: only 6 goes", opened: []uint32{6}, anomalous: []uint32{5, 6}, sent: []uint32{6}, omitted: []uint32{5}},
		// The alert is at a more severe Level than any recovering now (here
		// one the Plan's current revision no longer has): no RECOVERY of the
		// alert's own Level, so the record is held whole, as before.
		{name: "the alert at a Level not recovering: the record is held", opened: []uint32{1}, anomalous: []uint32{5, 6}, held: true},
	} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevelsShaped(t, "50", "50", native)
			copy, err := activecopy.New(activecopy.Options{Link: silentLink{}, EventSourceID: "source"})
			if err != nil {
				t.Fatal(err)
			}
			abnormal := contract.TriggerEventV1{TenantID: "tenant", DedupeMD5: fingerprint, EventKind: contract.TriggerEventAbnormal,
				StrategyRef: &contract.StrategySnapshotRef{TenantID: "tenant", BusinessID: 2, StrategyID: 7, Revision: 7}}
			abnormal.PlanRef.StrategyID = "7"
			for _, level := range arm.opened {
				abnormal.LevelResults = append(abnormal.LevelResults, contract.LevelResultV1{LevelID: level, Result: contract.LevelResultAbnormal})
			}
			copy.Acknowledged([]contract.TriggerEventV1{abnormal})
			if open, known := copy.OpenLevels("tenant", "7", fingerprint); !known || len(open) != len(arm.opened) {
				t.Fatalf("the copy's open Levels %v %t, want %v: the fixture does not reach the gate", open, known, arm.opened)
			}
			result := func(level uint32) execution.LevelFactResult {
				for _, anomalous := range arm.anomalous {
					if anomalous == level {
						return execution.LevelFactAnomalous
					}
				}
				return execution.LevelFactNormal
			}
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: result(5)},
				{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: result(6)},
			}}}
			req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
			req.OpenAlerts = copy
			evaluated, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			if err := evaluated.Validate(req); err != nil {
				t.Fatalf("the result contract refused the evaluator's own result: %v", err)
			}
			plan0 := evaluated.Plans[0]
			recovered := map[uint32]execution.LevelOutcome{}
			for _, outcome := range plan0.LevelOutcomes {
				if outcome.Outcome == execution.LevelOutcomeRecovery {
					recovered[outcome.LevelID] = outcome
				}
			}
			if len(recovered) != len(arm.anomalous) {
				t.Fatalf("outcomes=%+v, want a RECOVERY on each of %v: the omitted Level recovers in the state too", plan0.LevelOutcomes, arm.anomalous)
			}
			if len(plan0.StateResults) != 1 {
				t.Fatalf("state results=%d, want the record's state written", len(plan0.StateResults))
			}
			events := plan0.StateResults[0].Events
			if arm.held {
				if len(events) != 0 {
					t.Fatalf("envelopes=%+v, want the record held", events)
				}
				for level, outcome := range recovered {
					if !outcome.EnvelopeHeld || outcome.EventOmitted {
						t.Fatalf("Level %d held %t omitted %t, want held and not omitted", level, outcome.EnvelopeHeld, outcome.EventOmitted)
					}
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("envelopes=%d, want one", len(events))
			}
			var carried []uint32
			for _, level := range events[0].LevelResults {
				if level.Result == contract.LevelResultRecovery {
					carried = append(carried, level.LevelID)
				}
			}
			if len(carried) != len(arm.sent) || carried[0] != arm.sent[0] {
				t.Fatalf("the envelope carries RECOVERY of %v, want %v", carried, arm.sent)
			}
			// The contract holds the decomposition and the envelope to each
			// other both ways: the omitted Level carried in the envelope is
			// refused, and so is the omission unsaid.
			if len(arm.omitted) > 0 {
				omitted := arm.omitted[0]
				carrying := evaluated
				carrying.Plans = append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
				carrying.Plans[0].StateResults = append([]execution.StateEvaluation(nil), plan0.StateResults...)
				carrying.Plans[0].StateResults[0].Events = append([]contract.TriggerEventV1(nil), events...)
				event := &carrying.Plans[0].StateResults[0].Events[0]
				extra := event.LevelResults[0]
				extra.LevelID = omitted
				event.LevelResults = append(append([]contract.LevelResultV1(nil), event.LevelResults...), extra)
				if err := carrying.Validate(req); err == nil || !strings.Contains(err.Error(), "carries a Level its decomposition says was left out") {
					t.Fatalf("an envelope carrying the omitted Level %d must be refused, got %v", omitted, err)
				}
				unsaid := evaluated
				unsaid.Plans = append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
				unsaid.Plans[0].LevelOutcomes = append([]execution.LevelOutcome(nil), plan0.LevelOutcomes...)
				for i := range unsaid.Plans[0].LevelOutcomes {
					unsaid.Plans[0].LevelOutcomes[i].EventOmitted = false
				}
				if err := unsaid.Validate(req); err == nil || !strings.Contains(err.Error(), "omitted a successful sibling Level outcome") {
					t.Fatalf("an omission the decomposition does not say must still be refused, got %v", err)
				}
			}
			for _, level := range arm.sent {
				if recovered[level].EventOmitted || recovered[level].EnvelopeHeld {
					t.Fatalf("Level %d sent but marked %+v", level, recovered[level])
				}
			}
			// The reason stays the one a sent RECOVERY has: its readers read
			// the outcome, and the omission is the flag's alone.
			sentReason := recovered[arm.sent[0]].ReasonCode
			for _, level := range arm.omitted {
				outcome := recovered[level]
				if !outcome.EventOmitted || outcome.EnvelopeHeld || outcome.ReasonCode != sentReason {
					t.Fatalf("Level %d left out: omitted %t held %t reason %q, want omitted, not held, the sent Level's reason %q",
						level, outcome.EventOmitted, outcome.EnvelopeHeld, outcome.ReasonCode, sentReason)
				}
			}
		})
	}
}
