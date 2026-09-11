// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
)

// DiagnosticCode 是可持久化且不包含依赖错误详情的稳定原因码。
type DiagnosticCode string

const (
	DiagnosticCodeMissingField         DiagnosticCode = "missing_field"
	DiagnosticCodeInvalidField         DiagnosticCode = "invalid_field"
	DiagnosticCodeDependencyInvalid    DiagnosticCode = "dependency_invalid"
	DiagnosticCodeClassificationFailed DiagnosticCode = "classification_failed"
)

// Diagnostic 描述单个 Processor 的输入或依赖问题。
type Diagnostic struct {
	Code       DiagnosticCode `json:"code"`
	Dependency string         `json:"dependency,omitempty"`
	Fields     []string       `json:"fields,omitempty"`
}

// ProcessorResult 是 Processor 执行后的类型化结果。
type ProcessorResult struct {
	Status      domain.EnrichStatus
	Value       domain.JSONObject
	Diagnostics []Diagnostic
}

// ProcessorEnvelope 是 Alert.enrich 中一个 Processor 的结果信封。
type ProcessorEnvelope struct {
	Status      domain.EnrichStatus `json:"status"`
	Value       domain.JSONObject   `json:"value"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
}

// ProcessorEntry 保证 processors 数组的每项以 Processor 名称作为唯一 key。
type ProcessorEntry map[string]ProcessorEnvelope

// Payload 是 Alert.enrich 的固定顶层协议。
type Payload struct {
	Processors []ProcessorEntry `json:"processors"`
}

// JSONObject 校验 Payload 并转换为领域 JSON object。
func (p Payload) JSONObject() (domain.JSONObject, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal enrich payload: %w", err)
	}
	var object domain.JSONObject
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("decode enrich payload object: %w", err)
	}
	return object.Normalize()
}

// Validate 校验状态、单 key entry 和 JSON value。
func (p Payload) Validate() error {
	if p.Processors == nil {
		return fmt.Errorf("enrich payload processors must be an array")
	}
	for index, entry := range p.Processors {
		if len(entry) != 1 {
			return fmt.Errorf("enrich payload processors[%d] must contain exactly one entry", index)
		}
		for name, envelope := range entry {
			if name == "" {
				return fmt.Errorf("enrich payload processors[%d] name is required", index)
			}
			if !processorStatusValid(envelope.Status) {
				return fmt.Errorf("enrich payload processor %q status is invalid: %q", name, envelope.Status)
			}
			if envelope.Value == nil {
				return fmt.Errorf("enrich payload processor %q value must be an object", name)
			}
			if _, err := envelope.Value.Normalize(); err != nil {
				return fmt.Errorf("enrich payload processor %q value: %w", name, err)
			}
			for diagnosticIndex, diagnostic := range envelope.Diagnostics {
				if !diagnostic.Code.Valid() {
					return fmt.Errorf("enrich payload processor %q diagnostics[%d] code is invalid: %q", name, diagnosticIndex, diagnostic.Code)
				}
			}
		}
	}
	return nil
}

// DecodePayload 从领域 JSON object 解码并严格校验丰富协议。
func DecodePayload(object domain.JSONObject) (Payload, error) {
	data, err := json.Marshal(object)
	if err != nil {
		return Payload{}, fmt.Errorf("marshal enrich object: %w", err)
	}
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		return Payload{}, fmt.Errorf("decode enrich payload: %w", err)
	}
	if err := payload.Validate(); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func (c DiagnosticCode) Valid() bool {
	switch c {
	case DiagnosticCodeMissingField, DiagnosticCodeInvalidField,
		DiagnosticCodeDependencyInvalid, DiagnosticCodeClassificationFailed:
		return true
	default:
		return false
	}
}

func processorStatusValid(status domain.EnrichStatus) bool {
	return status == domain.EnrichStatusSucceeded || status == domain.EnrichStatusPartial ||
		status == domain.EnrichStatusFailed || status == domain.EnrichStatusSkipped
}
