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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

type Terminal struct {
	InputID           string
	RecordID          string
	Outcome           string
	ReasonCode        string
	Level             int
	AnomalyTimestamps []int64
}

type DecisionSink interface {
	// WriteBatch confirms one complete batch. Delivery is at least once: decision
	// IDs stay stable across replay, while the payload is only stable when the
	// evaluator state is the same. Implementations must use
	// contract.EncodeTriggerDecisionBatch and fail before acknowledgement when
	// validation or the encoded-size gate rejects the batch.
	WriteBatch(context.Context, *contract.TriggerDecisionBatch) error
}

type Processor struct {
	evaluator  *Evaluator
	sink       DecisionSink
	strategies map[strategyIdentity][32]byte
}

func NewProcessor(sink DecisionSink) *Processor {
	return &Processor{
		evaluator:  NewEvaluator(),
		sink:       sink,
		strategies: make(map[strategyIdentity][32]byte),
	}
}

type strategyIdentity struct {
	tenantID   string
	purpose    string
	strategyID string
	itemID     string
	generation string
	contentSHA string
}

func (p *Processor) Process(ctx context.Context, key, payload []byte) error {
	if p == nil || p.sink == nil {
		return errors.New("trigger processor: decision sink is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := contract.DecodeTriggerInput(payload)
	if err != nil {
		return fmt.Errorf("decode trigger input: %w", err)
	}
	expectedKey, err := input.PartitionKey()
	if err != nil {
		return fmt.Errorf("derive trigger input partition key: %w", err)
	}
	if !bytes.Equal(key, expectedKey) {
		return errors.New("trigger processor: partition key mismatch")
	}

	handle := newValidatedStrategyHandle(input.StrategyIR)
	identity := strategyIdentity{
		tenantID:   handle.tenantID,
		purpose:    handle.purpose,
		strategyID: handle.strategyRef.StrategyID,
		itemID:     handle.strategyRef.ItemID,
		generation: handle.strategyRef.Generation,
		contentSHA: handle.strategyRef.ContentSHA256,
	}
	if previous, ok := p.strategies[identity]; ok && previous != handle.executionFingerprint {
		return errors.New("trigger processor: conflicting execution plan for strategy identity")
	}
	transaction := p.evaluator.begin()
	committed := false
	defer func() {
		if !committed {
			transaction.discard()
		}
	}()
	// The whole micro-batch is recorded before any decision so that records
	// delivered out of source-time order still evaluate on event time.
	for _, outcome := range input.DetectionOutcomes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := recordOutcome(transaction, handle, outcome); err != nil {
			return err
		}
	}
	terminals := make([]Terminal, 0, len(input.DetectionOutcomes))
	for _, outcome := range input.DetectionOutcomes {
		if err := ctx.Err(); err != nil {
			return err
		}
		terminal, err := p.decideOutcome(transaction, handle, outcome)
		if err != nil {
			return err
		}
		terminals = append(terminals, terminal)
	}
	transaction.evict(handle)
	if err := ctx.Err(); err != nil {
		return err
	}
	batch, err := buildDecisionBatch(input, terminals)
	if err != nil {
		return fmt.Errorf("build trigger decision batch: %w", err)
	}
	if err := p.sink.WriteBatch(ctx, batch); err != nil {
		return fmt.Errorf("write trigger decision batch: %w", err)
	}
	transaction.commit()
	committed = true
	p.strategies[identity] = handle.executionFingerprint
	return nil
}

// recordOutcome advances the window only for business outcomes. ERROR and
// UNSUPPORTED stay bounded terminals and must not move the window.
func recordOutcome(transaction *evaluationTransaction, handle *StrategyHandle, outcome *contract.DetectionOutcome) error {
	if handle.purpose != contract.PurposeDetect {
		return nil
	}
	switch outcome.Outcome {
	case contract.OutcomeNormal, contract.OutcomeAnomalous:
		return transaction.record(handle, outcome)
	default:
		return nil
	}
}

func (p *Processor) decideOutcome(transaction *evaluationTransaction, handle *StrategyHandle, outcome *contract.DetectionOutcome) (Terminal, error) {
	terminal := Terminal{InputID: outcome.InputID, RecordID: outcome.Record.RecordID}
	if handle.purpose != contract.PurposeDetect {
		terminal.Outcome = contract.OutcomeUnsupported
		terminal.ReasonCode = "UNSUPPORTED_STRATEGY"
		return terminal, nil
	}
	switch outcome.Outcome {
	case contract.OutcomeError, contract.OutcomeUnsupported:
		terminal.Outcome = outcome.Outcome
		if err := json.Unmarshal(outcome.ErrorCode, &terminal.ReasonCode); err != nil {
			return Terminal{}, fmt.Errorf("decode terminal error_code: %w", err)
		}
		return terminal, nil
	case contract.OutcomeNormal, contract.OutcomeAnomalous:
		decision, err := transaction.decide(handle, outcome)
		if err != nil {
			return Terminal{}, err
		}
		if decision == nil {
			terminal.Outcome = DecisionNoTrigger
			terminal.ReasonCode = contract.DecisionReasonInputNormal
			return terminal, nil
		}
		terminal.Outcome = decision.Outcome
		switch decision.Outcome {
		case DecisionTrigger:
			terminal.ReasonCode = contract.DecisionReasonTriggerConditionMet
		case DecisionNoTrigger:
			terminal.ReasonCode = contract.DecisionReasonTriggerConditionNotMet
		default:
			return Terminal{}, fmt.Errorf("trigger processor: unsupported decision %q", decision.Outcome)
		}
		if decision.Outcome == DecisionTrigger {
			terminal.Level = decision.Level
			terminal.AnomalyTimestamps = append([]int64(nil), decision.AnomalyTimestamps...)
		}
		return terminal, nil
	default:
		return Terminal{}, fmt.Errorf("trigger processor: unsupported outcome %q", outcome.Outcome)
	}
}

func buildDecisionBatch(input *contract.TriggerInput, terminals []Terminal) (*contract.TriggerDecisionBatch, error) {
	decisions := make([]contract.TriggerDecision, 0, len(terminals))
	for _, terminal := range terminals {
		decisionID, err := contract.DeriveTriggerDecisionID(terminal.InputID)
		if err != nil {
			return nil, err
		}
		var level *int
		if terminal.Level > 0 {
			value := terminal.Level
			level = &value
		}
		decisions = append(decisions, contract.TriggerDecision{
			DecisionID:        decisionID,
			InputID:           terminal.InputID,
			RecordID:          terminal.RecordID,
			Outcome:           terminal.Outcome,
			ReasonCode:        terminal.ReasonCode,
			Level:             level,
			AnomalyTimestamps: append([]int64{}, terminal.AnomalyTimestamps...),
		})
	}
	return input.BuildTriggerDecisionBatch(decisions)
}
