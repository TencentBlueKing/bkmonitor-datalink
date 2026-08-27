// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

func TestBuildMessageReceiptIsolatesPlanTerminalFromProcessedSibling(t *testing.T) {
	t.Parallel()

	input := evaluationInputWithPlanIDs(t, "1001", "1002")
	receipt, err := buildMessageReceipt(input, map[string][]recordEvaluation{
		"1002": {{RecordOrdinal: 0, Result: trigger.EvaluationResultV2{
			Completion: trigger.CompletionEvaluated, RecordResult: contract.LevelResultNormal,
		}}},
	}, []inputv2.Terminal{{Scope: inputv2.ScopePlan, PlanID: "1001", ReasonCode: contract.ReasonPlanBudgetExceeded}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
		receipt.Counts != (contract.ReceiptCountsV1{Received: 1, Selected: 2, Processed: 1, Terminal: 1}) ||
		len(receipt.PerPlan) != 2 || receipt.PerPlan[0].Terminal != 1 || receipt.PerPlan[1].Normal != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestBuildMessageReceiptOmitsUnknownSelectorCountAndKeepsSiblingResult(t *testing.T) {
	t.Parallel()

	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeSharedG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels = append(
		envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels,
		envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels[0],
	)
	invalidRanges := []contract.SelectorRangeV2{{Start: 0, End: uint32(len(envelope.Records) + 1)}}
	envelope.Selectors[0].Selector.Ranges = &invalidRanges
	var err error
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := inputv2.New(g1ReaderLimits()).Decode(context.Background(), payload)
	if err != nil || decoded.Rejected || decoded.Input == nil {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	selections := decoded.Input.PlanSelections()
	if len(selections) != 1 || selections[0].PlanID() != "1002" || selections[0].SelectedCount() != 2 {
		t.Fatalf("PlanSelections() = %#v, want only trusted selector for sibling 1002", selections)
	}

	receipt, err := buildMessageReceipt(decoded.Input, map[string][]recordEvaluation{
		"1002": {
			{RecordOrdinal: 0, Result: trigger.EvaluationResultV2{Completion: trigger.CompletionEvaluated, RecordResult: contract.LevelResultNormal}},
			{RecordOrdinal: 1, Result: trigger.EvaluationResultV2{Completion: trigger.CompletionEvaluated, RecordResult: contract.LevelResultNormal}},
		},
	}, decoded.Terminals.Items())
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := contract.ReceiptCountsV1{Received: 2, Selected: 2, Processed: 2}
	if receipt.Status != contract.ReceiptStatusCompletedWithTerminal || receipt.Counts != wantCounts || len(receipt.PerPlan) != 1 ||
		receipt.PerPlan[0].PlanID != "1002" || receipt.PerPlan[0].Selected != 2 || receipt.PerPlan[0].Normal != 2 {
		t.Fatalf("receipt = %#v, want terminal status and only known sibling selection in conserved counts", receipt)
	}
	if !receiptHasReason(receipt, contract.ReasonPlanDuplicateLevelID) || !receiptHasReason(receipt, contract.ReasonSelectorInvalid) {
		t.Fatalf("reason counts = %#v, want one Plan diagnostic fact per isolated error, not Plan x Record counts", receipt.ReasonCounts)
	}
}

func TestApplyReceiptTerminalsKeepsLevelFactForUnknownSelector(t *testing.T) {
	t.Parallel()

	levelID := uint32(5)
	reasonCounts := make(map[string]uint64)
	uncountedTerminal, err := applyReceiptTerminals(map[string]*planReceiptState{}, []inputv2.Terminal{
		{Scope: inputv2.ScopePlan, PlanID: "1001", ReasonCode: contract.ReasonSelectorInvalid},
		{Scope: inputv2.ScopeLevel, PlanID: "1001", LevelID: &levelID, ReasonCode: contract.ReasonLevelInvalid},
	}, reasonCounts)
	if err != nil {
		t.Fatal(err)
	}
	if !uncountedTerminal || reasonCounts[contract.ReasonSelectorInvalid] != 1 || reasonCounts[contract.ReasonLevelInvalid] != 1 {
		t.Fatalf("uncountedTerminal = %t, reasonCounts = %#v, want one diagnostic fact per invalid Plan/Level", uncountedTerminal, reasonCounts)
	}
}

func TestBuildMessageReceiptCountsRecordTerminalOnlyForSelectedSlot(t *testing.T) {
	t.Parallel()

	input := evaluationInputWithPlanIDs(t, "1001")
	ordinal := uint32(0)
	receipt, err := buildMessageReceipt(input, nil, []inputv2.Terminal{{
		Scope: inputv2.ScopeRecord, RecordOrdinal: &ordinal, ReasonCode: contract.ReasonRecordInvalid,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
		receipt.Counts != (contract.ReceiptCountsV1{Received: 1, Selected: 1, Terminal: 1}) ||
		len(receipt.PerPlan) != 1 || receipt.PerPlan[0].Terminal != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestBuildMessageReceiptKeepsSiblingLevelResult(t *testing.T) {
	t.Parallel()

	input := evaluationInputWithPlanIDs(t, "1001")
	levelID := uint32(5)
	secondLevelID := uint32(6)
	receipt, err := buildMessageReceipt(input, map[string][]recordEvaluation{
		"1001": {{RecordOrdinal: 0, Result: trigger.EvaluationResultV2{
			Completion: trigger.CompletionEvaluated, RecordResult: contract.LevelResultNormal,
		}}},
	}, []inputv2.Terminal{
		{Scope: inputv2.ScopeLevel, PlanID: "1001", LevelID: &levelID, ReasonCode: contract.ReasonAlgorithmUnsupported},
		{Scope: inputv2.ScopeLevel, PlanID: "1001", LevelID: &secondLevelID, ReasonCode: contract.ReasonLevelInvalid},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
		receipt.Counts != (contract.ReceiptCountsV1{Received: 1, Selected: 1, Processed: 1, LevelTerminalAffected: 1}) ||
		receipt.PerPlan[0].Normal != 1 || receipt.PerPlan[0].LevelTerminalAffected != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}
	if len(receipt.ReasonCounts) != 2 || receipt.ReasonCounts[0].Count != 1 || receipt.ReasonCounts[1].Count != 1 {
		t.Fatalf("reason counts = %#v, want both isolated Level terminals", receipt.ReasonCounts)
	}
}

func TestBuildMessageReceiptPrefersTerminalOverUnavailable(t *testing.T) {
	t.Parallel()

	ordinal := uint32(0)
	levelID := uint32(5)
	tests := []struct {
		name     string
		terminal inputv2.Terminal
	}{
		{name: "message", terminal: inputv2.Terminal{Scope: inputv2.ScopeMessage, ReasonCode: contract.ReasonMessageBudgetExceeded}},
		{name: "plan", terminal: inputv2.Terminal{Scope: inputv2.ScopePlan, PlanID: "1001", ReasonCode: contract.ReasonPlanBudgetExceeded}},
		{name: "record", terminal: inputv2.Terminal{Scope: inputv2.ScopeRecord, RecordOrdinal: &ordinal, ReasonCode: contract.ReasonRecordInvalid}},
		{name: "level", terminal: inputv2.Terminal{Scope: inputv2.ScopeLevel, PlanID: "1001", LevelID: &levelID, ReasonCode: contract.ReasonAlgorithmUnsupported}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			receipt, err := buildMessageReceipt(evaluationInputWithPlanIDs(t, "1001"), map[string][]recordEvaluation{
				"1001": {{RecordOrdinal: 0, Result: trigger.EvaluationResultV2{
					Completion:    trigger.CompletionUnavailable,
					LevelOutcomes: []trigger.LevelOutcomeV2{{LevelID: 1, UnavailableReason: contract.ReasonHistoryWarming}},
				}}},
			}, []inputv2.Terminal{test.terminal})
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
				receipt.Counts != (contract.ReceiptCountsV1{Received: 1, Selected: 1, Terminal: 1}) ||
				len(receipt.PerPlan) != 1 || receipt.PerPlan[0].Terminal != 1 {
				t.Fatalf("receipt = %#v, want terminal to classify the selected slot", receipt)
			}
			if !receiptHasReason(receipt, contract.ReasonHistoryWarming) || !receiptHasReason(receipt, test.terminal.ReasonCode) {
				t.Fatalf("reason counts = %#v, want unavailable and terminal facts", receipt.ReasonCounts)
			}
		})
	}
}

func receiptHasReason(receipt *contract.MessageReceiptV1, reason string) bool {
	for _, count := range receipt.ReasonCounts {
		if count.ReasonCode == reason && count.Count == 1 {
			return true
		}
	}
	return false
}
