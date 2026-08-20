// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"unicode/utf8"
)

const (
	triggerDecisionBatchSchema = "trigger-decision-batch"

	DecisionAlgorithmV1 = "trigger-window-v1"
	decisionIDVersionV1 = "trigger-decision-id-v1"

	DecisionOutcomeTrigger   = "TRIGGER"
	DecisionOutcomeNoTrigger = "NO_TRIGGER"

	DecisionReasonTriggerConditionMet    = "TRIGGER_CONDITION_MET"
	DecisionReasonTriggerConditionNotMet = "TRIGGER_CONDITION_NOT_MET"
	DecisionReasonInputNormal            = "INPUT_NORMAL"

	MaxTriggerDecisionBytesV1 = 512 * 1024
	MaxTriggerDecisionItemsV1 = 500
)

type TriggerDecision struct {
	DecisionID        string  `json:"decision_id"`
	InputID           string  `json:"input_id"`
	RecordID          string  `json:"record_id"`
	Outcome           string  `json:"outcome"`
	ReasonCode        string  `json:"reason_code"`
	Level             *int    `json:"level,omitempty"`
	AnomalyTimestamps []int64 `json:"anomaly_timestamps"`
}

type TriggerDecisionBatch struct {
	Schema               Schema            `json:"schema"`
	RequiredFeatures     []string          `json:"required_features"`
	PartitionHashVersion string            `json:"partition_hash_version"`
	BatchID              string            `json:"batch_id"`
	TenantID             string            `json:"tenant_id"`
	Purpose              string            `json:"purpose"`
	StrategyRef          StrategyRef       `json:"strategy_ref"`
	DecisionAlgorithm    string            `json:"decision_algorithm"`
	Decisions            []TriggerDecision `json:"decisions"`
	partitionKey         []byte
}

func DeriveTriggerDecisionID(inputID string) (string, error) {
	// Result fields are deliberately excluded: one stable input and evaluator
	// coordinate must keep one ID so replayed payload drift is detectable.
	if !sha256Pattern.MatchString(inputID) {
		return "", invalid("decision_id.input_id", "must be 64 lowercase hexadecimal characters")
	}
	fields := []string{decisionIDVersionV1, DecisionAlgorithmV1, inputID}
	digest := sha256.New()
	var prefix [4]byte
	for _, field := range fields {
		if !utf8.ValidString(field) {
			return "", invalid("decision_id", "canonical fields must contain valid UTF-8")
		}
		if uint64(len(field)) > math.MaxUint32 {
			return "", invalid("decision_id", "canonical field exceeds uint32 length")
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write([]byte(field))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// ValidateTriggerDecision validates one independently decoded decision against
// the authoritative outcome carried by this decoded TriggerInput. Batch shape
// and batch_id are transport details and are deliberately not compared.
func (i *TriggerInput) ValidateTriggerDecision(decision TriggerDecision) error {
	if i == nil || i.StrategyIR == nil || len(i.partitionKey) == 0 {
		return invalid("trigger_input", "must be produced by DecodeTriggerInput")
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	for _, source := range i.DetectionOutcomes {
		if source == nil || source.InputID != decision.InputID {
			continue
		}
		if source.Record.RecordID != decision.RecordID {
			return invalid("trigger_decision.record_id", "does not match authoritative input")
		}
		return validateDecisionAgainstSource(i.StrategyIR, source, decision)
	}
	return invalid("trigger_decision.input_id", "authoritative input not found")
}

func (i *TriggerInput) BuildTriggerDecisionBatch(decisions []TriggerDecision) (*TriggerDecisionBatch, error) {
	if i == nil || i.StrategyIR == nil || len(i.partitionKey) == 0 {
		return nil, invalid("trigger_input", "must be produced by DecodeTriggerInput")
	}
	if len(i.DetectionOutcomes) == 0 || len(i.DetectionOutcomes) > MaxTriggerDecisionItemsV1 {
		return nil, invalid("trigger_decision_batch.decisions", "source input must contain between 1 and 500 outcomes")
	}
	if len(decisions) != len(i.DetectionOutcomes) {
		return nil, invalid("trigger_decision_batch.decisions", "must match input outcome count")
	}
	for index, decision := range decisions {
		source := i.DetectionOutcomes[index]
		if source == nil {
			return nil, invalid("trigger_decision_batch.decisions", "source outcome must be non-null")
		}
		if decision.InputID != source.InputID || decision.RecordID != source.Record.RecordID {
			return nil, invalid("trigger_decision_batch.decisions", "must preserve input order and identity")
		}
		if err := validateDecisionAgainstSource(i.StrategyIR, source, decision); err != nil {
			return nil, err
		}
	}
	batch := &TriggerDecisionBatch{
		Schema:               Schema{Name: triggerDecisionBatchSchema, Major: schemaMajor, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: i.PartitionHashVersion,
		BatchID:              i.DetectionOutcomes[0].BatchID,
		TenantID:             i.StrategyIR.TenantID,
		Purpose:              i.StrategyIR.Purpose,
		StrategyRef:          i.StrategyIR.StrategyRef,
		DecisionAlgorithm:    DecisionAlgorithmV1,
		Decisions:            cloneTriggerDecisions(decisions),
		partitionKey:         append([]byte(nil), i.partitionKey...),
	}
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	return batch, nil
}

func validateDecisionAgainstSource(strategy *TriggerStrategyIR, source *DetectionOutcome, decision TriggerDecision) error {
	if strategy.Purpose != PurposeDetect {
		if decision.Outcome != OutcomeUnsupported || decision.ReasonCode != "UNSUPPORTED_STRATEGY" {
			return invalid("trigger_decision_batch.decisions", "unsupported purpose requires UNSUPPORTED_STRATEGY")
		}
		return nil
	}
	switch source.Outcome {
	case OutcomeNormal:
		if decision.Outcome != DecisionOutcomeNoTrigger || decision.ReasonCode != DecisionReasonInputNormal {
			return invalid("trigger_decision_batch.decisions", "NORMAL input requires INPUT_NORMAL NO_TRIGGER")
		}
	case OutcomeAnomalous:
		if decision.Outcome == DecisionOutcomeTrigger && decision.ReasonCode == DecisionReasonTriggerConditionMet {
			return validateTriggerDecisionAgainstSource(strategy, source, decision)
		}
		if decision.Outcome != DecisionOutcomeNoTrigger || decision.ReasonCode != DecisionReasonTriggerConditionNotMet {
			return invalid("trigger_decision_batch.decisions", "ANOMALOUS input requires a trigger condition decision")
		}
	case OutcomeError, OutcomeUnsupported:
		var errorCode string
		if err := decodeJSONObject(source.ErrorCode, &errorCode); err != nil {
			return invalid("trigger_decision_batch.decisions", "source error_code is invalid")
		}
		if decision.Outcome != source.Outcome || decision.ReasonCode != errorCode {
			return invalid("trigger_decision_batch.decisions", "non-business input decision must preserve outcome and reason")
		}
	default:
		return invalid("trigger_decision_batch.decisions", "source outcome is unsupported")
	}
	return nil
}

func validateTriggerDecisionAgainstSource(strategy *TriggerStrategyIR, source *DetectionOutcome, decision TriggerDecision) error {
	if decision.Level == nil {
		return invalid("trigger_decision_batch.decisions.level", "TRIGGER requires level")
	}
	currentLevelIsAnomalous := false
	for _, evaluation := range source.Evaluations {
		if evaluation.Level == *decision.Level && evaluation.Result == EvaluationAnomalous {
			currentLevelIsAnomalous = true
			break
		}
	}
	if !currentLevelIsAnomalous {
		return invalid("trigger_decision_batch.decisions.level", "must match an anomalous evaluation in the source input")
	}
	var triggerConfig *TriggerConfig
	for index := range strategy.TriggerConfigs {
		if strategy.TriggerConfigs[index].Level == *decision.Level {
			triggerConfig = &strategy.TriggerConfigs[index]
			break
		}
	}
	if triggerConfig == nil {
		return invalid("trigger_decision_batch.decisions.level", "must have a TriggerConfig")
	}
	if len(decision.AnomalyTimestamps) < triggerConfig.TriggerCount {
		return invalid("trigger_decision_batch.decisions.anomaly_timestamps", "does not satisfy trigger count")
	}
	windowStart := source.Record.SourceTime - (int64(strategy.CheckWindowUnitSeconds)*int64(triggerConfig.CheckWindowSize) - 1)
	for _, timestamp := range decision.AnomalyTimestamps {
		if timestamp < windowStart || timestamp > source.Record.SourceTime {
			return invalid("trigger_decision_batch.decisions.anomaly_timestamps", "falls outside the selected trigger window")
		}
	}
	if decision.AnomalyTimestamps[len(decision.AnomalyTimestamps)-1] != source.Record.SourceTime {
		return invalid("trigger_decision_batch.decisions.anomaly_timestamps", "must include the current source time")
	}
	return nil
}

func cloneTriggerDecisions(decisions []TriggerDecision) []TriggerDecision {
	cloned := make([]TriggerDecision, len(decisions))
	for index, decision := range decisions {
		cloned[index] = decision
		if decision.Level != nil {
			level := *decision.Level
			cloned[index].Level = &level
		}
		cloned[index].AnomalyTimestamps = append([]int64{}, decision.AnomalyTimestamps...)
	}
	return cloned
}

func EncodeTriggerDecisionBatch(batch *TriggerDecisionBatch) ([]byte, error) {
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return nil, invalid("trigger_decision_batch", err.Error())
	}
	if len(payload) > MaxTriggerDecisionBytesV1 {
		return nil, invalid("trigger_decision_batch", "exceeds encoded byte limit")
	}
	return payload, nil
}

func DecodeTriggerDecisionBatch(payload []byte) (*TriggerDecisionBatch, error) {
	if len(payload) > MaxTriggerDecisionBytesV1 {
		return nil, invalid("trigger_decision_batch", "exceeds encoded byte limit")
	}
	schema, object, err := validateContractEnvelope(
		payload,
		"trigger_decision_batch",
		triggerDecisionBatchSchema,
		[]string{
			"schema", "required_features", "partition_hash_version", "batch_id", "tenant_id", "purpose",
			"strategy_ref", "decision_algorithm", "decisions",
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	allowUnknown := schema.Minor > 0
	if _, err := validateJSONObjectFields(
		object["strategy_ref"],
		"trigger_decision_batch.strategy_ref",
		[]string{"strategy_id", "item_id", "generation", "content_sha256"},
		nil,
		allowUnknown,
	); err != nil {
		return nil, err
	}
	var rawDecisions []json.RawMessage
	if err := decodeJSONObject(object["decisions"], &rawDecisions); err != nil {
		return nil, invalid("trigger_decision_batch.decisions", err.Error())
	}
	for _, rawDecision := range rawDecisions {
		decisionObject, err := validateJSONObjectFields(
			rawDecision,
			"trigger_decision_batch.decisions",
			[]string{"decision_id", "input_id", "record_id", "outcome", "reason_code", "anomaly_timestamps"},
			[]string{"level"},
			allowUnknown,
		)
		if err != nil {
			return nil, err
		}
		if level, ok := decisionObject["level"]; ok && bytes.Equal(bytes.TrimSpace(level), []byte("null")) {
			return nil, invalid("trigger_decision_batch.decisions.level", "must be omitted instead of null")
		}
	}
	var batch TriggerDecisionBatch
	if err := decodeJSONObject(payload, &batch); err != nil {
		return nil, err
	}
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	partitionKey, err := deriveTriggerPartitionKey(
		batch.PartitionHashVersion,
		batch.TenantID,
		batch.Purpose,
		batch.StrategyRef.StrategyID,
		batch.StrategyRef.ItemID,
	)
	if err != nil {
		return nil, err
	}
	batch.partitionKey = partitionKey
	return &batch, nil
}

func (b *TriggerDecisionBatch) Validate() error {
	if b == nil {
		return invalid("trigger_decision_batch", "must be non-null")
	}
	if b.RequiredFeatures == nil {
		return invalid("trigger_decision_batch.required_features", "must be an array")
	}
	if err := validateHeader(b.Schema, b.RequiredFeatures, triggerDecisionBatchSchema, map[string]struct{}{}); err != nil {
		return err
	}
	if b.PartitionHashVersion != PartitionHashVersionV1 {
		return invalid("trigger_decision_batch.partition_hash_version", "unsupported version")
	}
	if b.BatchID == "" || b.TenantID == "" {
		return invalid("trigger_decision_batch", "batch_id and tenant_id must be non-empty")
	}
	if err := validatePurpose(b.Purpose); err != nil {
		return err
	}
	if err := validateStrategyRef(b.StrategyRef); err != nil {
		return err
	}
	if b.DecisionAlgorithm != DecisionAlgorithmV1 {
		return invalid("trigger_decision_batch.decision_algorithm", "unsupported algorithm")
	}
	if len(b.Decisions) == 0 || len(b.Decisions) > MaxTriggerDecisionItemsV1 {
		return invalid("trigger_decision_batch.decisions", "must contain between 1 and 500 decisions")
	}
	inputIDs := make(map[string]struct{}, len(b.Decisions))
	decisionIDs := make(map[string]struct{}, len(b.Decisions))
	for index := range b.Decisions {
		decision := &b.Decisions[index]
		if err := decision.Validate(); err != nil {
			return err
		}
		expectedInputID, err := DeriveInputID(InputIdentity{
			TenantID:              b.TenantID,
			Purpose:               b.Purpose,
			StrategyID:            b.StrategyRef.StrategyID,
			ItemID:                b.StrategyRef.ItemID,
			StrategyContentSHA256: b.StrategyRef.ContentSHA256,
			RecordID:              decision.RecordID,
		})
		if err != nil {
			return err
		}
		if decision.InputID != expectedInputID {
			return invalid("trigger_decision_batch.decisions.input_id", "does not match batch identity and record_id")
		}
		if _, exists := inputIDs[decision.InputID]; exists {
			return invalid("trigger_decision_batch.decisions", "must not contain duplicate input_id")
		}
		if _, exists := decisionIDs[decision.DecisionID]; exists {
			return invalid("trigger_decision_batch.decisions", "must not contain duplicate decision_id")
		}
		inputIDs[decision.InputID] = struct{}{}
		decisionIDs[decision.DecisionID] = struct{}{}
		if b.Purpose != PurposeDetect && (decision.Outcome != OutcomeUnsupported || decision.ReasonCode != "UNSUPPORTED_STRATEGY") {
			return invalid("trigger_decision_batch.decisions", "unsupported purpose requires UNSUPPORTED_STRATEGY")
		}
	}
	return nil
}

func (b *TriggerDecisionBatch) PartitionKey() ([]byte, error) {
	if b == nil {
		return nil, invalid("trigger_decision_batch", "must be non-null")
	}
	if len(b.partitionKey) != 0 {
		return append([]byte(nil), b.partitionKey...), nil
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return deriveTriggerPartitionKey(
		b.PartitionHashVersion,
		b.TenantID,
		b.Purpose,
		b.StrategyRef.StrategyID,
		b.StrategyRef.ItemID,
	)
}

func (d *TriggerDecision) Validate() error {
	if d == nil {
		return invalid("trigger_decision", "must be non-null")
	}
	if !sha256Pattern.MatchString(d.DecisionID) {
		return invalid("trigger_decision.decision_id", "must be 64 lowercase hexadecimal characters")
	}
	if !sha256Pattern.MatchString(d.InputID) {
		return invalid("trigger_decision.input_id", "must be 64 lowercase hexadecimal characters")
	}
	expectedDecisionID, err := DeriveTriggerDecisionID(d.InputID)
	if err != nil {
		return err
	}
	if d.DecisionID != expectedDecisionID {
		return invalid("trigger_decision.decision_id", "does not match canonical tuple")
	}
	_, sourceTime, err := parseRecordID(d.RecordID)
	if err != nil {
		return err
	}
	if d.Level != nil && (*d.Level <= 0 || *d.Level > maxContractInt) {
		return invalid("trigger_decision.level", "must be a positive 32-bit signed integer")
	}
	if d.AnomalyTimestamps == nil {
		return invalid("trigger_decision.anomaly_timestamps", "must be an array")
	}
	for index := 1; index < len(d.AnomalyTimestamps); index++ {
		if d.AnomalyTimestamps[index] <= d.AnomalyTimestamps[index-1] {
			return invalid("trigger_decision.anomaly_timestamps", "must be strictly increasing")
		}
	}
	for _, timestamp := range d.AnomalyTimestamps {
		if timestamp < 0 || timestamp > sourceTime {
			return invalid("trigger_decision.anomaly_timestamps", "must be between zero and the current source time")
		}
	}
	switch d.Outcome {
	case DecisionOutcomeTrigger:
		if d.ReasonCode != DecisionReasonTriggerConditionMet || d.Level == nil || len(d.AnomalyTimestamps) == 0 {
			return invalid("trigger_decision", "TRIGGER requires condition-met reason, level and timestamps")
		}
		if d.AnomalyTimestamps[len(d.AnomalyTimestamps)-1] != sourceTime {
			return invalid("trigger_decision.anomaly_timestamps", "TRIGGER must include the current source time")
		}
	case DecisionOutcomeNoTrigger:
		switch d.ReasonCode {
		case DecisionReasonInputNormal:
			if d.Level != nil || len(d.AnomalyTimestamps) != 0 {
				return invalid("trigger_decision", "INPUT_NORMAL must not carry level or timestamps")
			}
		case DecisionReasonTriggerConditionNotMet:
			if d.Level != nil || len(d.AnomalyTimestamps) != 0 {
				return invalid("trigger_decision", "condition-not-met must not carry a selected level or timestamps")
			}
		default:
			return invalid("trigger_decision.reason_code", "unsupported NO_TRIGGER reason")
		}
	case OutcomeError, OutcomeUnsupported:
		if d.Level != nil || len(d.AnomalyTimestamps) != 0 {
			return invalid("trigger_decision", "non-business outcome must not carry level or timestamps")
		}
		if _, ok := errorCodes[d.Outcome][d.ReasonCode]; !ok {
			return invalid("trigger_decision.reason_code", "unsupported code for outcome")
		}
	default:
		return invalid("trigger_decision.outcome", "unsupported outcome")
	}
	return nil
}
