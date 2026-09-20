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
	"encoding/json"
)

const (
	detectInputSchema       = "detect-input"
	MaxDetectInputBytesV1   = 512 * 1024
	MaxDetectInputRecordsV1 = 500
)

type DetectInput struct {
	Schema               Schema             `json:"schema"`
	RequiredFeatures     []string           `json:"required_features"`
	PartitionHashVersion string             `json:"partition_hash_version"`
	StrategyIR           *TriggerStrategyIR `json:"strategy_ir"`
	BatchID              string             `json:"batch_id"`
	Records              []json.RawMessage  `json:"records"`
	partitionKey         []byte
}

func DecodeDetectInput(payload []byte) (*DetectInput, error) {
	if len(payload) > MaxDetectInputBytesV1 {
		return nil, invalid("detect_input", "exceeds encoded byte limit")
	}
	schema, object, err := validateContractEnvelope(
		payload,
		"detect_input",
		detectInputSchema,
		[]string{"schema", "required_features", "partition_hash_version", "strategy_ir", "batch_id", "records"},
		nil,
	)
	if err != nil {
		return nil, err
	}
	var header struct {
		RequiredFeatures     []string          `json:"required_features"`
		PartitionHashVersion string            `json:"partition_hash_version"`
		BatchID              string            `json:"batch_id"`
		Records              []json.RawMessage `json:"records"`
	}
	if err := decodeJSONObject(payload, &header); err != nil {
		return nil, err
	}
	if err := validateHeader(schema, header.RequiredFeatures, detectInputSchema, map[string]struct{}{}); err != nil {
		return nil, err
	}
	if header.PartitionHashVersion != PartitionHashVersionV1 {
		return nil, invalid("detect_input.partition_hash_version", "unsupported version")
	}
	if header.BatchID == "" {
		return nil, invalid("detect_input.batch_id", "must be non-empty")
	}
	strategyIR, err := DecodeTriggerStrategyIR(object["strategy_ir"])
	if err != nil {
		return nil, err
	}
	if len(header.Records) == 0 || len(header.Records) > MaxDetectInputRecordsV1 {
		return nil, invalid("detect_input.records", "must contain between 1 and 500 records")
	}
	recordIDs := make(map[string]struct{}, len(header.Records))
	for _, record := range header.Records {
		fields, err := validateJSONObjectFields(
			record,
			"detect_input.records",
			[]string{"record_id", "time"},
			[]string{"values"},
			true,
		)
		if err != nil {
			return nil, err
		}
		var recordID string
		if err := decodeJSONObject(fields["record_id"], &recordID); err != nil {
			return nil, invalid("detect_input.records.record_id", err.Error())
		}
		if _, exists := recordIDs[recordID]; exists {
			return nil, invalid("detect_input.records", "must not contain duplicate record_id")
		}
		recordIDs[recordID] = struct{}{}
		if _, _, err := parseRecordID(recordID); err != nil {
			return nil, err
		}
		var sourceTime int64
		if err := decodeJSONObject(fields["time"], &sourceTime); err != nil {
			return nil, invalid("detect_input.records.time", err.Error())
		}
		_, expectedTime, _ := parseRecordID(recordID)
		if sourceTime != expectedTime {
			return nil, invalid("detect_input.records.time", "must equal record source_time")
		}
	}
	input := &DetectInput{
		Schema:               schema,
		RequiredFeatures:     header.RequiredFeatures,
		PartitionHashVersion: header.PartitionHashVersion,
		StrategyIR:           strategyIR,
		BatchID:              header.BatchID,
		Records:              append([]json.RawMessage(nil), header.Records...),
	}
	partitionKey, err := input.derivePartitionKey()
	if err != nil {
		return nil, err
	}
	input.partitionKey = partitionKey
	return input, nil
}

func (i *DetectInput) PartitionKey() ([]byte, error) {
	if i == nil || i.StrategyIR == nil {
		return nil, invalid("detect_input", "must contain StrategyIR")
	}
	if i.PartitionHashVersion != PartitionHashVersionV1 {
		return nil, invalid("detect_input.partition_hash_version", "unsupported version")
	}
	if len(i.partitionKey) != 0 {
		return bytes.Clone(i.partitionKey), nil
	}
	if err := i.StrategyIR.Validate(); err != nil {
		return nil, err
	}
	return i.derivePartitionKey()
}

func (i *DetectInput) derivePartitionKey() ([]byte, error) {
	return deriveTriggerPartitionKey(
		i.PartitionHashVersion,
		i.StrategyIR.TenantID,
		i.StrategyIR.Purpose,
		i.StrategyIR.StrategyRef.StrategyID,
		i.StrategyIR.StrategyRef.ItemID,
	)
}
