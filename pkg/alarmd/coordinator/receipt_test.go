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
