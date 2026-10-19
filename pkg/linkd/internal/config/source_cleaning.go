// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"linkd/internal/policy"
	"linkd/internal/textregexp"
)

// SourceCleaning 限定一租户的来源清洗与过滤；空租户表示来源公共规则。
type SourceCleaning struct {
	TenantID    string             `json:"bk_tenant_id,omitempty" yaml:"bk_tenant_id,omitempty"`
	Assignments []SourceAssignment `json:"assignments,omitempty" yaml:"assignments,omitempty"`
	Filter      json.RawMessage    `json:"filter,omitempty" yaml:"filter,omitempty"`
}

// SourceAssignment 从 standard 字段复制值或提取第一次完整正则匹配。
type SourceAssignment struct {
	Target string `json:"target" yaml:"target"`
	Field  string `json:"field" yaml:"field"`
	Rule   string `json:"rule" yaml:"rule"`
	Value  string `json:"value,omitempty" yaml:"value,omitempty"`
}

// CompileSourceFilter 复用布尔表达式，来源 wildcard 保持子串包含语义。
func CompileSourceFilter(raw json.RawMessage) (*policy.Expression, map[string]string, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, nil, nil
	}
	var rules map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, nil, err
	}
	converted := map[string]any{}
	var expression string
	if err := json.Unmarshal(rules["expression"], &expression); err != nil {
		return nil, nil, fmt.Errorf("source filter expression required")
	}
	converted["expression"] = expression
	fields := policy.FieldCatalog{}
	paths := map[string]string{}
	for name, rawRule := range rules {
		if name == "expression" {
			continue
		}
		var r struct {
			Field     string `json:"field"`
			Condition string `json:"condition"`
			Value     any    `json:"value"`
		}
		decoder := json.NewDecoder(bytes.NewReader(rawRule))
		decoder.UseNumber()
		if err := decoder.Decode(&r); err != nil {
			return nil, nil, err
		}
		if r.Field == "" {
			return nil, nil, fmt.Errorf("source filter %s field required", name)
		}
		// 每个条件独立类型，避免同字段上的数值比较影响文本匹配。
		field := name
		paths[field] = r.Field
		kind := policy.FieldKeyword
		switch r.Value.(type) {
		case json.Number:
			kind = policy.FieldNumber
		case bool:
			kind = policy.FieldBoolean
		}
		if strings.TrimPrefix(r.Condition, "must_not_") == "wildcard" {
			value, ok := r.Value.(string)
			if !ok {
				return nil, nil, fmt.Errorf("source wildcard value must be string")
			}
			r.Value = regexp.QuoteMeta(value)
			r.Condition = strings.Replace(r.Condition, "wildcard", "regexp", 1)
		}
		fields[field] = kind
		converted[name] = map[string]any{"target_key": field, "condition": r.Condition, "target_value": r.Value}
	}
	body, err := json.Marshal(converted)
	if err != nil {
		return nil, nil, err
	}
	compiled, err := policy.CompileExpression(body, fields)
	return compiled, paths, err
}

// ValidateCleaning 在配置发布时报告规则错误，避免启动后才发现非法表达式。
func (c CleanerConfig) ValidateCleaning() error {
	for i, r := range c.Rules {
		for j, a := range r.Assignments {
			target := strings.TrimPrefix(a.Target, "$.")
			root, _, _ := strings.Cut(target, ".")
			switch root {
			case "title", "content", "dimensions", "values", "labels", "subject", "occurred_at", "produced_at", "extra_data", "evaluations", "event_id":
			default:
				return fmt.Errorf("cleaner.rules[%d].assignments[%d]: unsupported target %q", i, j, a.Target)
			}
			if a.Field == "" {
				return fmt.Errorf("cleaner assignment field required")
			}
			switch a.Rule {
			case "term":
			case "regexp_extract":
				if _, err := textregexp.Compile(a.Value); err != nil {
					return fmt.Errorf("cleaner.rules[%d].assignments[%d]: %w", i, j, err)
				}
			default:
				return fmt.Errorf("unsupported source assignment rule %q", a.Rule)
			}
		}
		if _, _, err := CompileSourceFilter(r.Filter); err != nil {
			return fmt.Errorf("cleaner.rules[%d].filter: %w", i, err)
		}
	}
	return nil
}
