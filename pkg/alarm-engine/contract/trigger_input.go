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
	"math"
	"unicode/utf8"
)

const (
	triggerInputSchema     = "trigger-input"
	PartitionHashVersionV1 = "trigger-input-partition-v1"
)

type TriggerInput struct {
	Schema               Schema
	RequiredFeatures     []string
	PartitionHashVersion string
	StrategyIR           *TriggerStrategyIR
	DetectionOutcome     *DetectionOutcome
}

func DecodeTriggerInput(payload []byte) (*TriggerInput, error) {
	schema, object, err := validateContractEnvelope(
		payload,
		"trigger_input",
		triggerInputSchema,
		[]string{"schema", "required_features", "partition_hash_version", "strategy_ir", "detection_outcome"},
		nil,
	)
	if err != nil {
		return nil, err
	}
	var header struct {
		RequiredFeatures     []string `json:"required_features"`
		PartitionHashVersion string   `json:"partition_hash_version"`
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
	outcome, err := DecodeDetectionOutcome(object["detection_outcome"], strategyIR)
	if err != nil {
		return nil, err
	}
	return &TriggerInput{
		Schema:               schema,
		RequiredFeatures:     header.RequiredFeatures,
		PartitionHashVersion: header.PartitionHashVersion,
		StrategyIR:           strategyIR,
		DetectionOutcome:     outcome,
	}, nil
}

func (i *TriggerInput) PartitionKey() ([]byte, error) {
	if i == nil || i.StrategyIR == nil {
		return nil, invalid("trigger_input", "must contain StrategyIR")
	}
	if i.PartitionHashVersion != PartitionHashVersionV1 {
		return nil, invalid("trigger_input.partition_hash_version", "unsupported version")
	}
	if err := i.StrategyIR.Validate(); err != nil {
		return nil, err
	}
	fields := []string{
		i.PartitionHashVersion,
		i.StrategyIR.TenantID,
		i.StrategyIR.Purpose,
		i.StrategyIR.StrategyRef.StrategyID,
		i.StrategyIR.StrategyRef.ItemID,
	}
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
