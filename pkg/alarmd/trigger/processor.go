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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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
	strategies map[strategyIdentity]cachedStrategy
}

func NewProcessor(sink DecisionSink) *Processor {
	return &Processor{
		evaluator:  NewEvaluator(),
		sink:       sink,
		strategies: make(map[strategyIdentity]cachedStrategy),
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

type cachedStrategy struct {
	executionFingerprint [32]byte
	lastSeen             time.Time
	retention            time.Duration
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
	return p.ProcessOutcomes(
		ctx,
		key,
		input.PartitionHashVersion,
		input.StrategyIR,
		input.DetectionOutcomes,
	)
}

// ProcessInputs preserves the legacy TriggerInput entry point while evaluating
// all chunks in one transaction.
func (p *Processor) ProcessInputs(ctx context.Context, key []byte, inputs []*contract.TriggerInput) error {
	if len(inputs) == 0 {
		return errors.New("trigger processor: inputs are required")
	}
	var strategy *contract.TriggerStrategyIR
	var executionFingerprint [32]byte
	partitionHashVersion := ""
	batchID := ""
	outcomes := make([]*contract.DetectionOutcome, 0, contract.MaxTriggerInputItemsV1)
	for index, input := range inputs {
		if input == nil {
			return errors.New("trigger processor: input is required")
		}
		expectedKey, err := input.PartitionKey()
		if err != nil {
			return fmt.Errorf("derive trigger input partition key: %w", err)
		}
		if !bytes.Equal(key, expectedKey) {
			return errors.New("trigger processor: partition key mismatch")
		}
		candidateBatchID := input.DetectionOutcomes[0].BatchID
		candidateFingerprint := newValidatedStrategyHandle(input.StrategyIR).executionFingerprint
		if index == 0 {
			strategy = input.StrategyIR
			executionFingerprint = candidateFingerprint
			partitionHashVersion = input.PartitionHashVersion
			batchID = candidateBatchID
		} else if input.StrategyIR.StrategyRef != strategy.StrategyRef ||
			input.StrategyIR.TenantID != strategy.TenantID ||
			input.StrategyIR.Purpose != strategy.Purpose ||
			input.PartitionHashVersion != partitionHashVersion ||
			candidateFingerprint != executionFingerprint ||
			candidateBatchID != batchID {
			return errors.New("trigger processor: inputs must share one logical batch and execution plan")
		}
		outcomes = append(outcomes, input.DetectionOutcomes...)
		if len(outcomes) > contract.MaxTriggerInputItemsV1 {
			return errors.New("trigger processor: logical batch exceeds outcome count limit")
		}
	}
	return p.ProcessOutcomes(ctx, key, partitionHashVersion, strategy, outcomes)
}

// ProcessOutcomes is the in-process Detect to Trigger boundary. Wire limits are
// applied only when decisions are emitted to Kafka.
func (p *Processor) ProcessOutcomes(
	ctx context.Context,
	key []byte,
	partitionHashVersion string,
	strategy *contract.TriggerStrategyIR,
	outcomes []*contract.DetectionOutcome,
) error {
	if p == nil || p.sink == nil {
		return errors.New("trigger processor: decision sink is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(outcomes) == 0 || len(outcomes) > contract.MaxTriggerInputItemsV1 {
		return errors.New("trigger processor: outcomes must contain between 1 and 500 items")
	}
	expectedKey, err := contract.TriggerPartitionKey(partitionHashVersion, strategy)
	if err != nil {
		return fmt.Errorf("derive trigger partition key: %w", err)
	}
	if !bytes.Equal(key, expectedKey) {
		return errors.New("trigger processor: partition key mismatch")
	}
	batchID := ""
	seenInputIDs := make(map[string]struct{}, len(outcomes))
	for index, outcome := range outcomes {
		if outcome == nil {
			return errors.New("trigger processor: outcome is required")
		}
		if err := outcome.Validate(strategy); err != nil {
			return fmt.Errorf("validate detection outcome: %w", err)
		}
		if index == 0 {
			batchID = outcome.BatchID
		} else if outcome.BatchID != batchID {
			return errors.New("trigger processor: outcomes must share one batch_id")
		}
		if _, exists := seenInputIDs[outcome.InputID]; exists {
			return errors.New("trigger processor: outcomes must not contain duplicate input_id")
		}
		seenInputIDs[outcome.InputID] = struct{}{}
	}
	handle := newValidatedStrategyHandle(strategy)
	identity := identityForHandle(handle)
	previous, hasPrevious := p.strategies[identity]
	if hasPrevious && previous.executionFingerprint != handle.executionFingerprint {
		return errors.New("trigger processor: conflicting execution plan for strategy identity")
	}
	transaction := p.evaluator.begin()
	committed := false
	defer func() {
		if !committed {
			transaction.discard()
		}
	}()
	// The whole logical batch is recorded before any decision so that records
	// delivered out of source-time order still evaluate on event time.
	for _, outcome := range outcomes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := recordOutcome(transaction, handle, outcome); err != nil {
			return err
		}
	}
	terminals := make([]Terminal, 0, len(outcomes))
	for _, outcome := range outcomes {
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
	decisions, err := buildDecisions(terminals)
	if err != nil {
		return fmt.Errorf("build trigger decisions: %w", err)
	}
	batches, err := buildDecisionBatches(partitionHashVersion, strategy, outcomes, decisions)
	if err != nil {
		return fmt.Errorf("build trigger decision batches: %w", err)
	}
	for _, batch := range batches {
		if err := p.sink.WriteBatch(ctx, batch); err != nil {
			return fmt.Errorf("write trigger decision batch: %w", err)
		}
	}
	transaction.commit()
	committed = true
	now := p.evaluator.now()
	p.strategies[identity] = cachedStrategy{
		executionFingerprint: handle.executionFingerprint,
		lastSeen:             now,
		retention:            stateRetention(handle),
	}
	p.pruneStrategies(now, identity)
	return nil
}

func identityForHandle(handle *StrategyHandle) strategyIdentity {
	return strategyIdentity{
		tenantID:   handle.tenantID,
		purpose:    handle.purpose,
		strategyID: handle.strategyRef.StrategyID,
		itemID:     handle.strategyRef.ItemID,
		generation: handle.strategyRef.Generation,
		contentSHA: handle.strategyRef.ContentSHA256,
	}
}

func (p *Processor) pruneStrategies(now time.Time, active strategyIdentity) {
	for identity, strategy := range p.strategies {
		if identity == active || now.Sub(strategy.lastSeen) < strategy.retention {
			continue
		}
		p.evaluator.retire(identity)
		delete(p.strategies, identity)
	}
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

func buildDecisions(terminals []Terminal) ([]contract.TriggerDecision, error) {
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
	return decisions, nil
}

func buildDecisionBatches(
	partitionHashVersion string,
	strategy *contract.TriggerStrategyIR,
	outcomes []*contract.DetectionOutcome,
	decisions []contract.TriggerDecision,
) ([]*contract.TriggerDecisionBatch, error) {
	return appendDecisionBatches(nil, partitionHashVersion, strategy, outcomes, decisions)
}

func appendDecisionBatches(
	batches []*contract.TriggerDecisionBatch,
	partitionHashVersion string,
	strategy *contract.TriggerStrategyIR,
	outcomes []*contract.DetectionOutcome,
	decisions []contract.TriggerDecision,
) ([]*contract.TriggerDecisionBatch, error) {
	batch, err := contract.BuildTriggerDecisionBatchFromOutcomes(
		partitionHashVersion,
		strategy,
		outcomes,
		decisions,
	)
	if err == nil {
		_, err = contract.EncodeTriggerDecisionBatch(batch)
	}
	if err == nil {
		return append(batches, batch), nil
	}
	if len(decisions) <= 1 {
		return nil, err
	}
	middle := len(decisions) / 2
	batches, err = appendDecisionBatches(
		batches,
		partitionHashVersion,
		strategy,
		outcomes[:middle],
		decisions[:middle],
	)
	if err != nil {
		return nil, err
	}
	return appendDecisionBatches(
		batches,
		partitionHashVersion,
		strategy,
		outcomes[middle:],
		decisions[middle:],
	)
}
