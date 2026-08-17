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
	StrategyRef       contract.StrategyRef
	Outcome           string
	ErrorCode         string
	Level             int
	AnomalyTimestamps []int64
}

type DecisionSink interface {
	// WriteBatch must be idempotent by input_id. Returning nil confirms that all
	// terminals are durable; a consumer may commit its input offset afterwards.
	WriteBatch(context.Context, []Terminal) error
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
	terminals := make([]Terminal, 0, len(input.DetectionOutcomes))
	for _, outcome := range input.DetectionOutcomes {
		if err := ctx.Err(); err != nil {
			return err
		}
		terminal, err := p.processOutcome(transaction, handle, outcome)
		if err != nil {
			return err
		}
		terminals = append(terminals, terminal)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.sink.WriteBatch(ctx, terminals); err != nil {
		return fmt.Errorf("write trigger terminals: %w", err)
	}
	transaction.commit()
	committed = true
	p.strategies[identity] = handle.executionFingerprint
	return nil
}

func (p *Processor) processOutcome(transaction *evaluationTransaction, handle *StrategyHandle, outcome *contract.DetectionOutcome) (Terminal, error) {
	terminal := Terminal{InputID: outcome.InputID, StrategyRef: outcome.StrategyRef}
	if handle.purpose != contract.PurposeDetect {
		terminal.Outcome = contract.OutcomeUnsupported
		terminal.ErrorCode = "UNSUPPORTED_STRATEGY"
		return terminal, nil
	}
	switch outcome.Outcome {
	case contract.OutcomeError, contract.OutcomeUnsupported:
		terminal.Outcome = outcome.Outcome
		if err := json.Unmarshal(outcome.ErrorCode, &terminal.ErrorCode); err != nil {
			return Terminal{}, fmt.Errorf("decode terminal error_code: %w", err)
		}
		return terminal, nil
	case contract.OutcomeNormal, contract.OutcomeAnomalous:
		decision, err := transaction.processValidated(handle, outcome)
		if err != nil {
			return Terminal{}, err
		}
		if decision == nil {
			terminal.Outcome = DecisionNoTrigger
			return terminal, nil
		}
		terminal.Outcome = decision.Outcome
		terminal.Level = decision.Level
		terminal.AnomalyTimestamps = append([]int64(nil), decision.AnomalyTimestamps...)
		return terminal, nil
	default:
		return Terminal{}, fmt.Errorf("trigger processor: unsupported outcome %q", outcome.Outcome)
	}
}
