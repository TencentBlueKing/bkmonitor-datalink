// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"encoding/json"
	"fmt"

	"linkd/internal/jsonpath"
)

// EnrichAssignment 是运行时已求值且拥有独立值的补丁；JSON 编码仅用于持久化/接口边界。
type EnrichAssignment struct {
	RuleID      string
	OperationID string
	target      jsonpath.Target
	path        string
	value       any
	measure     jsonpath.Measurement
}

// NewEnrichAssignment 复用装配时已解析的路径，并在修改前冻结、校验目标值。
func NewEnrichAssignment(target jsonpath.Target, value any) (EnrichAssignment, error) {
	path := target.String()
	if err := validateEnrichParts(target.Parts()); err != nil {
		return EnrichAssignment{}, err
	}
	v, err := jsonpath.Native(value)
	if err != nil {
		return EnrichAssignment{}, err
	}
	m, err := jsonpath.Measure(v)
	if err != nil {
		return EnrichAssignment{}, err
	}
	parts := target.Parts()
	switch parts[0] {
	case "title", "content", "subject_name":
		text, ok := v.(string)
		limit := 256
		if parts[0] == "content" {
			limit = 1 << 20
		}
		if !ok || len(text) > limit {
			return EnrichAssignment{}, fmt.Errorf("invalid text patch")
		}
	case "labels":
		if _, err := ScalarFromValue(v); err != nil {
			return EnrichAssignment{}, err
		}
	}
	return EnrichAssignment{target: target, path: path, value: v, measure: m}, nil
}

// PrepareEnrichPatch 在恢复或旧类型化 Processor 输出边界解码一次。
func PrepareEnrichPatch(p EnrichPatch) (EnrichAssignment, error) {
	if p.Op != "set" || len(p.Value) == 0 {
		return EnrichAssignment{}, fmt.Errorf("invalid enrich patch")
	}
	t, err := jsonpath.ParseTarget(p.Path)
	if err != nil {
		return EnrichAssignment{}, err
	}
	a, err := NewEnrichAssignment(t, p.Value)
	a.path = p.Path
	a.RuleID, a.OperationID = p.RuleID, p.OperationID
	return a, err
}

// EncodedSize 计量持久化补丁的体积，不编码值；与 MarshalJSON 的字段保持一致。
func (a EnrichAssignment) EncodedSize() int {
	n := len(`{"op":"set","path":,"value":}`) + jsonpath.StringBytes(a.path) + a.measure.Bytes
	if a.RuleID != "" {
		n += len(`,"rule_id":`) + jsonpath.StringBytes(a.RuleID)
	}
	if a.OperationID != "" {
		n += len(`,"operation_id":`) + jsonpath.StringBytes(a.OperationID)
	}
	return n
}

// Patch 将运行时值转换为真实存储补丁，调用方不得将编码结果再作为执行输入。
func (a EnrichAssignment) Patch() (EnrichPatch, error) {
	data, err := json.Marshal(a.value)
	if err != nil {
		return EnrichPatch{}, err
	}
	return EnrichPatch{Op: "set", Path: a.path, Value: data, RuleID: a.RuleID, OperationID: a.OperationID}, nil
}

// ScalarFromValue 将已解码值转为领域 Scalar，保留现行数字为 float64 的契约。
func ScalarFromValue(value any) (Scalar, error) {
	switch v := value.(type) {
	case string:
		return NewStringScalar(v), nil
	case bool:
		return NewBoolScalar(v), nil
	case json.Number:
		n, err := v.Float64()
		if err != nil {
			return Scalar{}, err
		}
		return NewNumberScalar(n)
	case float64:
		return NewNumberScalar(v)
	case int:
		return NewNumberScalar(float64(v))
	case int64:
		return NewNumberScalar(float64(v))
	default:
		return Scalar{}, fmt.Errorf("scalar must be string, finite number, or boolean")
	}
}
