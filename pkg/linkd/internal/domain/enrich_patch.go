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
	"bytes"
	"encoding/json"
	"fmt"

	"linkd/internal/jsonpath"
)

// EnrichPatch 保存对 Alert 可丰富字段的已求值赋值，不包含可再次执行的表达式。
type EnrichPatch struct {
	Op          string          `json:"op"`
	Path        string          `json:"path"`
	Value       json.RawMessage `json:"value"`
	RuleID      string          `json:"rule_id,omitempty"`
	OperationID string          `json:"operation_id,omitempty"`
}

// NewEnrichPatch 编码输出值，显式 null 与遗漏 value 保持不同。
func NewEnrichPatch(path string, value any) (EnrichPatch, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return EnrichPatch{}, err
	}
	target, err := jsonpath.ParseTarget(path)
	if err != nil {
		return EnrichPatch{}, err
	}
	p := EnrichPatch{Op: "set", Path: target.String(), Value: data}
	return p, p.Validate()
}

// ValidateEnrichTarget 校验用户规则允许写入的路径；不允许父对象替换绕过字段保护。
func ValidateEnrichTarget(path string) error {
	t, err := jsonpath.ParseTarget(path)
	if err != nil {
		return err
	}
	parts := t.Parts()
	root, ok := parts[0].(string)
	if !ok {
		return fmt.Errorf("alert target must be an object field")
	}
	switch root {
	case "title", "content", "subject_name":
		if len(parts) != 1 {
			return fmt.Errorf("string field cannot have a child")
		}
	case "labels":
		if len(parts) != 2 {
			return fmt.Errorf("labels target must be a single scalar key")
		}
		if key, ok := parts[1].(string); !ok || key == "" {
			return fmt.Errorf("label key must be a string")
		}
	case "extra_data":
		if len(parts) < 2 {
			return fmt.Errorf("extra_data root cannot be replaced")
		}
	default:
		return fmt.Errorf("alert field %q cannot be enriched", root)
	}
	return nil
}

// Validate 检查补丁结构与目标值，不依赖 Alert 生命周期状态。
func (p EnrichPatch) Validate() error {
	if p.Op != "set" {
		return fmt.Errorf("unsupported enrich patch operation %q", p.Op)
	}
	if err := ValidateEnrichTarget(p.Path); err != nil {
		return err
	}
	if len(p.Value) == 0 || !json.Valid(p.Value) {
		return fmt.Errorf("patch value must be present JSON")
	}
	t, _ := jsonpath.ParseTarget(p.Path)
	parts := t.Parts()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(p.Value))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := jsonpath.ValidateTree(value); err != nil {
		return err
	}
	switch parts[0] {
	case "title", "content", "subject_name":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("text patch must be a string")
		}
		limit := 256
		if parts[0] == "content" {
			limit = 1 << 20
		}
		if len(text) > limit {
			return fmt.Errorf("text patch exceeds field limit")
		}
	case "labels":
		var scalar Scalar
		if err := json.Unmarshal(p.Value, &scalar); err != nil {
			return err
		}
		if err := (DimensionMap{parts[1].(string): scalar}).Validate(); err != nil {
			return err
		}
	}
	return nil
}

// AlertDocument 创建用于规则和预览的隔离 JSON 文档；历史丰富不作为新一轮输入。
func AlertDocument(alert Alert) (map[string]any, error) {
	data, err := json.Marshal(alert)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	delete(value, "enrich")
	delete(value, "enrich_status")
	for _, key := range []string{"labels", "extra_data"} {
		if value[key] == nil {
			value[key] = map[string]any{}
		}
	}
	return value, nil
}

// ApplyEnrichPatches 原子应用一组补丁并返回副本，不修改输入树。
func ApplyEnrichPatches(original map[string]any, patches []EnrichPatch) (map[string]any, error) {
	if len(patches) > 4096 {
		return nil, fmt.Errorf("enrich patch count exceeded")
	}
	result := jsonpath.Clone(original).(map[string]any)
	for _, p := range patches {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		t, _ := jsonpath.ParseTarget(p.Path)
		var v any
		d := json.NewDecoder(bytes.NewReader(p.Value))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		if err := t.Set(result, v); err != nil {
			return nil, err
		}
	}
	if err := jsonpath.ValidateTree(result); err != nil {
		return nil, err
	}
	return result, nil
}
