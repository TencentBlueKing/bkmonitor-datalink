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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type recordEvaluation struct {
	RecordOrdinal uint32
	Result        trigger.EvaluationResultV2
}

type receiptSlot struct {
	result                string
	unavailable           bool
	terminal              bool
	levelTerminalAffected bool
}

type planReceiptState struct {
	planID string
	slots  map[uint32]*receiptSlot
}

func buildMessageReceipt(
	input *inputv2.EvaluationInput,
	evaluations map[string][]recordEvaluation,
	terminals []inputv2.Terminal,
) (*contract.MessageReceiptV1, error) {
	if input == nil {
		return nil, errors.New("alarmd coordinator: Receipt input is required")
	}
	plans, err := initializeReceiptPlans(input)
	if err != nil {
		return nil, err
	}
	reasonCounts := make(map[string]uint64)
	for planID, results := range evaluations {
		plan, ok := plans[planID]
		if !ok {
			return nil, fmt.Errorf("alarmd coordinator: Receipt evaluation references unknown Plan %s", planID)
		}
		for _, evaluation := range results {
			slot, ok := plan.slots[evaluation.RecordOrdinal]
			if !ok || slot.result != "" || slot.unavailable || slot.terminal {
				return nil, errors.New("alarmd coordinator: Receipt evaluation does not match one unclassified selected slot")
			}
			if err := classifyReceiptEvaluation(slot, evaluation.Result, reasonCounts); err != nil {
				return nil, err
			}
		}
	}
	if err := applyReceiptTerminals(plans, terminals, reasonCounts); err != nil {
		return nil, err
	}

	execution := input.Execution()
	receipt := contract.MessageReceiptV1{
		ExecutionID: execution.ExecutionID, MessageID: execution.MessageID,
		PayloadDigest: execution.PayloadDigest, PlanSetDigest: execution.PlanSetDigest,
		SourceWindow: execution.SourceWindow, Status: contract.ReceiptStatusCompleted,
		Counts:  contract.ReceiptCountsV1{Received: uint64(input.RecordBatch().Len())},
		PerPlan: make([]contract.PlanReceiptV1, 0, len(plans)),
	}
	for _, selection := range input.PlanSelections() {
		plan := plans[selection.PlanID()]
		entry := contract.PlanReceiptV1{PlanID: plan.planID, Selected: uint64(len(plan.slots))}
		for _, slot := range plan.slots {
			switch {
			case slot.result == contract.LevelResultAbnormal:
				entry.Abnormal++
			case slot.result == contract.LevelResultNormal:
				entry.Normal++
			case slot.result == contract.LevelResultRecovery:
				entry.Recovery++
			case slot.unavailable:
				entry.Unavailable++
			case slot.terminal:
				entry.Terminal++
			default:
				return nil, fmt.Errorf("alarmd coordinator: Plan %s has an unclassified selected slot", plan.planID)
			}
			if slot.levelTerminalAffected {
				entry.LevelTerminalAffected++
			}
		}
		processed := entry.Abnormal + entry.Normal + entry.Recovery
		receipt.Counts.Selected += entry.Selected
		receipt.Counts.Processed += processed
		receipt.Counts.Unavailable += entry.Unavailable
		receipt.Counts.Terminal += entry.Terminal
		receipt.Counts.LevelTerminalAffected += entry.LevelTerminalAffected
		receipt.Counts.Events += entry.Abnormal + entry.Recovery
		receipt.PerPlan = append(receipt.PerPlan, entry)
	}
	if receipt.Counts.Terminal > 0 || receipt.Counts.LevelTerminalAffected > 0 {
		receipt.Status = contract.ReceiptStatusCompletedWithTerminal
	}
	for reason, count := range reasonCounts {
		receipt.ReasonCounts = append(receipt.ReasonCounts, contract.ReasonCountV1{ReasonCode: reason, Count: count})
	}
	return contract.BuildMessageReceiptV1(receipt)
}

func initializeReceiptPlans(input *inputv2.EvaluationInput) (map[string]*planReceiptState, error) {
	plans := make(map[string]*planReceiptState, len(input.PlanSelections()))
	for _, selection := range input.PlanSelections() {
		if selection.PlanID() == "" {
			return nil, errors.New("alarmd coordinator: cannot build per-Plan Receipt without a trusted Plan ID")
		}
		if _, duplicate := plans[selection.PlanID()]; duplicate {
			return nil, fmt.Errorf("alarmd coordinator: duplicate Receipt Plan %s", selection.PlanID())
		}
		plan := &planReceiptState{planID: selection.PlanID(), slots: make(map[uint32]*receiptSlot, selection.SelectedCount())}
		if err := selection.ForEachSelectedSlot(func(ordinal uint32, _ inputv2.RecordView, _ bool) error {
			plan.slots[ordinal] = &receiptSlot{}
			return nil
		}); err != nil {
			return nil, err
		}
		plans[selection.PlanID()] = plan
	}
	return plans, nil
}

func classifyReceiptEvaluation(slot *receiptSlot, result trigger.EvaluationResultV2, reasonCounts map[string]uint64) error {
	switch result.Completion {
	case trigger.CompletionEvaluated:
		if result.RecordResult != contract.LevelResultAbnormal && result.RecordResult != contract.LevelResultNormal &&
			result.RecordResult != contract.LevelResultRecovery {
			return errors.New("alarmd coordinator: evaluated record has no valid three-state result")
		}
		slot.result = result.RecordResult
	case trigger.CompletionSuppressed, trigger.CompletionUnavailable:
		reasons := make(map[string]struct{})
		for _, outcome := range result.LevelOutcomes {
			reason := outcome.SuppressedReason
			if reason == "" {
				reason = outcome.UnavailableReason
			}
			if reason != "" {
				reasons[reason] = struct{}{}
			}
		}
		if len(reasons) == 0 {
			return errors.New("alarmd coordinator: unavailable record has no controlled reason")
		}
		slot.unavailable = true
		for reason := range reasons {
			reasonCounts[reason]++
		}
	default:
		return fmt.Errorf("alarmd coordinator: unknown Trigger completion %q", result.Completion)
	}
	return nil
}

func applyReceiptTerminals(
	plans map[string]*planReceiptState,
	terminals []inputv2.Terminal,
	reasonCounts map[string]uint64,
) error {
	for _, terminal := range terminals {
		if terminal.ReasonCode == "" || !contract.ReasonAllowedForV2(terminal.ReasonCode, contract.ReasonDomainReceipt) {
			return errors.New("alarmd coordinator: terminal has an invalid Receipt reason")
		}
		switch terminal.Scope {
		case inputv2.ScopeMessage:
			for _, plan := range plans {
				classifyTerminalSlots(plan.slots, terminal.ReasonCode, reasonCounts)
			}
		case inputv2.ScopePlan:
			plan, ok := plans[terminal.PlanID]
			if !ok {
				return fmt.Errorf("alarmd coordinator: terminal references unknown Plan %s", terminal.PlanID)
			}
			classifyTerminalSlots(plan.slots, terminal.ReasonCode, reasonCounts)
		case inputv2.ScopeRecord:
			if terminal.RecordOrdinal == nil {
				return errors.New("alarmd coordinator: record terminal has no ordinal")
			}
			for _, plan := range plans {
				if slot, ok := plan.slots[*terminal.RecordOrdinal]; ok && slot.result == "" && !slot.unavailable && !slot.terminal {
					slot.terminal = true
					reasonCounts[terminal.ReasonCode]++
				}
			}
		case inputv2.ScopeLevel:
			plan, ok := plans[terminal.PlanID]
			if !ok {
				return fmt.Errorf("alarmd coordinator: Level terminal references unknown Plan %s", terminal.PlanID)
			}
			for _, slot := range plan.slots {
				if slot.result != "" {
					if !slot.levelTerminalAffected {
						slot.levelTerminalAffected = true
						reasonCounts[terminal.ReasonCode]++
					}
				} else if !slot.unavailable && !slot.terminal {
					slot.terminal = true
					reasonCounts[terminal.ReasonCode]++
				}
			}
		default:
			return fmt.Errorf("alarmd coordinator: unsupported terminal scope %q", terminal.Scope)
		}
	}
	return nil
}

func classifyTerminalSlots(slots map[uint32]*receiptSlot, reason string, reasonCounts map[string]uint64) {
	for _, slot := range slots {
		if slot.result == "" && !slot.unavailable && !slot.terminal {
			slot.terminal = true
			reasonCounts[reason]++
		}
	}
}
