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
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"unicode/utf8"
)

const (
	triggerInputSchema     = "trigger-input"
	PartitionHashVersionV1 = "trigger-input-partition-v1"
	MaxTriggerInputBytesV1 = 512 * 1024
	MaxTriggerInputItemsV1 = 500
)

type TriggerInput struct {
	Schema               Schema
	RequiredFeatures     []string
	PartitionHashVersion string
	StrategyIR           *TriggerStrategyIR
	DetectionOutcomes    []*DetectionOutcome
	partitionKey         []byte
}

func DecodeTriggerInput(payload []byte) (*TriggerInput, error) {
	if len(payload) > MaxTriggerInputBytesV1 {
		return nil, invalid("trigger_input", "exceeds encoded byte limit")
	}
	schema, object, err := validateContractEnvelope(
		payload,
		"trigger_input",
		triggerInputSchema,
		[]string{"schema", "required_features", "partition_hash_version", "strategy_ir", "detection_outcomes"},
		nil,
	)
	if err != nil {
		return nil, err
	}
	var header struct {
		RequiredFeatures     []string          `json:"required_features"`
		PartitionHashVersion string            `json:"partition_hash_version"`
		DetectionOutcomes    []json.RawMessage `json:"detection_outcomes"`
	}
	if err := decodeJSONObject(payload, &header); err != nil {
		return nil, err
	}
	if err := validateHeader(schema, header.RequiredFeatures, triggerInputSchema, map[string]struct{}{}); err != nil {
		return nil, err
	}
	if header.PartitionHashVersion != PartitionHashVersionV1 {
		return nil, invalid("trigger_input.partition_hash_version", "unsupported version")
	}
	strategyIR, err := DecodeTriggerStrategyIR(object["strategy_ir"])
	if err != nil {
		return nil, err
	}
	if len(header.DetectionOutcomes) == 0 || len(header.DetectionOutcomes) > MaxTriggerInputItemsV1 {
		return nil, invalid("trigger_input.detection_outcomes", "must contain between 1 and 500 outcomes")
	}
	outcomes := make([]*DetectionOutcome, 0, len(header.DetectionOutcomes))
	inputIDs := make(map[string]struct{}, len(header.DetectionOutcomes))
	batchID := ""
	for index, rawOutcome := range header.DetectionOutcomes {
		outcome, err := decodeDetectionOutcome(rawOutcome, strategyIR, false)
		if err != nil {
			return nil, invalid("trigger_input.detection_outcomes", err.Error())
		}
		if index == 0 {
			batchID = outcome.BatchID
		} else if outcome.BatchID != batchID {
			return nil, invalid("trigger_input.detection_outcomes", "must share one batch_id")
		}
		if _, exists := inputIDs[outcome.InputID]; exists {
			return nil, invalid("trigger_input.detection_outcomes", "must not contain duplicate input_id")
		}
		inputIDs[outcome.InputID] = struct{}{}
		outcomes = append(outcomes, outcome)
	}
	input := &TriggerInput{
		Schema:               schema,
		RequiredFeatures:     header.RequiredFeatures,
		PartitionHashVersion: header.PartitionHashVersion,
		StrategyIR:           strategyIR,
		DetectionOutcomes:    outcomes,
	}
	partitionKey, err := input.derivePartitionKey()
	if err != nil {
		return nil, err
	}
	input.partitionKey = partitionKey
	return input, nil
}

func (i *TriggerInput) PartitionKey() ([]byte, error) {
	if i == nil || i.StrategyIR == nil {
		return nil, invalid("trigger_input", "must contain StrategyIR")
	}
	if i.PartitionHashVersion != PartitionHashVersionV1 {
		return nil, invalid("trigger_input.partition_hash_version", "unsupported version")
	}
	if len(i.partitionKey) != 0 {
		return append([]byte(nil), i.partitionKey...), nil
	}
	if err := i.StrategyIR.Validate(); err != nil {
		return nil, err
	}
	return i.derivePartitionKey()
}

func (i *TriggerInput) derivePartitionKey() ([]byte, error) {
	return TriggerPartitionKey(
		i.PartitionHashVersion,
		i.StrategyIR,
	)
}

// TriggerPartitionKey derives the shared Detect, Trigger and Decision
// partition key from a validated strategy without requiring a wire envelope.
func TriggerPartitionKey(version string, strategy *TriggerStrategyIR) ([]byte, error) {
	if strategy == nil {
		return nil, invalid("trigger_input.partition_key", "strategy must be non-null")
	}
	if err := strategy.Validate(); err != nil {
		return nil, err
	}
	return deriveTriggerPartitionKey(
		version,
		strategy.TenantID,
		strategy.Purpose,
		strategy.StrategyRef.StrategyID,
		strategy.StrategyRef.ItemID,
	)
}

func deriveTriggerPartitionKey(version, tenantID, purpose, strategyID, itemID string) ([]byte, error) {
	fields := []string{version, tenantID, purpose, strategyID, itemID}
	digest := sha256.New()
	var prefix [4]byte
	for _, field := range fields {
		if !utf8.ValidString(field) {
			return nil, invalid("trigger_input.partition_key", "fields must contain valid UTF-8")
		}
		if uint64(len(field)) > math.MaxUint32 {
			return nil, invalid("trigger_input.partition_key", "field exceeds uint32 length")
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write([]byte(field))
	}
	return digest.Sum(nil), nil
}
