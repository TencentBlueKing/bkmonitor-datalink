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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

type TriggerConfig struct {
	Level           int `json:"level"`
	CheckWindowSize int `json:"check_window_size"`
	TriggerCount    int `json:"trigger_count"`
}

type TriggerStrategyIR struct {
	Schema                 Schema          `json:"schema"`
	RequiredFeatures       []string        `json:"required_features"`
	TenantID               string          `json:"tenant_id"`
	Purpose                string          `json:"purpose"`
	StrategyRef            StrategyRef     `json:"strategy_ref"`
	RequiredLevels         []int           `json:"required_levels"`
	CheckWindowUnitSeconds int             `json:"check_window_unit_seconds"`
	TriggerConfigs         []TriggerConfig `json:"trigger_configs"`
	LegacyJSONBase64       string          `json:"legacy_json_b64"`
}

func DecodeTriggerStrategyIR(payload []byte) (*TriggerStrategyIR, error) {
	schema, object, err := validateContractEnvelope(
		payload,
		"strategy_ir",
		triggerStrategyIRSchema,
		[]string{
			"schema", "required_features", "tenant_id", "purpose", "strategy_ref", "required_levels",
			"check_window_unit_seconds", "trigger_configs", "legacy_json_b64",
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	allowUnknown := schema.Minor > 0
	if _, err := validateJSONObjectFields(
		object["strategy_ref"],
		"strategy_ir.strategy_ref",
		[]string{"strategy_id", "item_id", "generation", "content_sha256"},
		nil,
		allowUnknown,
	); err != nil {
		return nil, err
	}
	var configs []json.RawMessage
	if err := decodeJSONObject(object["trigger_configs"], &configs); err != nil {
		return nil, invalid("strategy_ir.trigger_configs", err.Error())
	}
	for _, config := range configs {
		if _, err := validateJSONObjectFields(
			config,
			"strategy_ir.trigger_configs",
			[]string{"level", "check_window_size", "trigger_count"},
			nil,
			allowUnknown,
		); err != nil {
			return nil, err
		}
	}
	var strategy TriggerStrategyIR
	if err := decodeJSONObject(payload, &strategy); err != nil {
		return nil, err
	}
	if err := strategy.Validate(); err != nil {
		return nil, err
	}
	return &strategy, nil
}

func (s *TriggerStrategyIR) Validate() error {
	if s == nil {
		return invalid("strategy_ir", "must be non-null")
	}
	if err := validateHeader(s.Schema, s.RequiredFeatures, triggerStrategyIRSchema, map[string]struct{}{
		featureRawStrategyBytes: {},
	}); err != nil {
		return err
	}
	if s.TenantID == "" {
		return invalid("tenant_id", "must be non-empty")
	}
	if err := validatePurpose(s.Purpose); err != nil {
		return err
	}
	if err := validateStrategyRef(s.StrategyRef); err != nil {
		return err
	}
	if s.CheckWindowUnitSeconds <= 0 || s.CheckWindowUnitSeconds > maxContractInt {
		return invalid("check_window_unit_seconds", "must be a positive 32-bit signed integer")
	}
	if len(s.RequiredLevels) == 0 {
		return invalid("required_levels", "must be non-empty")
	}
	previous := 0
	for _, level := range s.RequiredLevels {
		if level <= previous || level > maxContractInt {
			return invalid("required_levels", "must be positive, sorted and unique")
		}
		previous = level
	}
	if len(s.TriggerConfigs) != len(s.RequiredLevels) {
		return invalid("trigger_configs", "levels must equal required_levels")
	}
	for index, config := range s.TriggerConfigs {
		if config.Level != s.RequiredLevels[index] {
			return invalid("trigger_configs", "levels must equal required_levels")
		}
		if config.CheckWindowSize <= 0 || config.CheckWindowSize > maxContractInt || config.TriggerCount <= 0 || config.TriggerCount > maxContractInt {
			return invalid("trigger_configs", "window size and trigger count must be positive 32-bit signed integers")
		}
	}
	legacyJSON, err := s.LegacyJSON()
	if err != nil {
		return err
	}
	var legacyObject map[string]json.RawMessage
	if err := decodeJSONObject(legacyJSON, &legacyObject); err != nil {
		return invalid("legacy_json_b64", err.Error())
	}
	if legacyObject == nil {
		return invalid("legacy_json_b64", "must contain a JSON object")
	}
	digest := sha256.Sum256(legacyJSON)
	if hex.EncodeToString(digest[:]) != s.StrategyRef.ContentSHA256 {
		return invalid("legacy_json_b64", "content hash mismatch")
	}
	return nil
}

func (s *TriggerStrategyIR) LegacyJSON() ([]byte, error) {
	legacyJSON, err := base64.StdEncoding.Strict().DecodeString(s.LegacyJSONBase64)
	if err != nil || len(legacyJSON) == 0 {
		return nil, invalid("legacy_json_b64", "must contain non-empty canonical base64")
	}
	if base64.StdEncoding.EncodeToString(legacyJSON) != s.LegacyJSONBase64 {
		return nil, invalid("legacy_json_b64", "must use canonical base64 without ignored characters")
	}
	return legacyJSON, nil
}
