// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// LegacyEvaluationInput intentionally exposes none of the v2 Query Group,
// completeness, Dataset Contract, or Plan Set model.
type LegacyEvaluationInput struct {
	input *contract.DetectInput
}

func DecodeLegacyGroupOfOne(ctx context.Context, payload []byte) (*LegacyEvaluationInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := contract.DecodeDetectInput(payload)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &LegacyEvaluationInput{input: input}, nil
}

func (input *LegacyEvaluationInput) Mode() string {
	if input == nil {
		return ""
	}
	return contract.CompatibilityModeLegacyGroupOfOne
}

func (input *LegacyEvaluationInput) BatchID() string {
	if input == nil || input.input == nil {
		return ""
	}
	return input.input.BatchID
}

func (input *LegacyEvaluationInput) RecordCount() int {
	if input == nil || input.input == nil {
		return 0
	}
	return len(input.input.Records)
}

func (input *LegacyEvaluationInput) Record(index int) (json.RawMessage, bool) {
	if input == nil || input.input == nil || index < 0 || index >= len(input.input.Records) {
		return nil, false
	}
	return bytes.Clone(input.input.Records[index]), true
}

func (input *LegacyEvaluationInput) StrategyIR() *contract.TriggerStrategyIR {
	if input == nil || input.input == nil || input.input.StrategyIR == nil {
		return nil
	}
	cloned := *input.input.StrategyIR
	cloned.RequiredFeatures = append([]string(nil), input.input.StrategyIR.RequiredFeatures...)
	cloned.RequiredLevels = append([]int(nil), input.input.StrategyIR.RequiredLevels...)
	cloned.TriggerConfigs = append([]contract.TriggerConfig(nil), input.input.StrategyIR.TriggerConfigs...)
	return &cloned
}
