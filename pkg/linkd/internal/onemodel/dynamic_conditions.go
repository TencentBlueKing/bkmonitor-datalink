// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
)

type dynamicRule struct {
	Field     string        `json:"field"`
	Operator  string        `json:"operator"`
	Value     any           `json:"value"`
	Condition string        `json:"condition"`
	Rules     []dynamicRule `json:"rules"`
}

// CompileDynamicConditions 按实时模型目录的显式类型将 Kingeye 条件转换为 OneModel 查询。
// KAC strict fetcher 从 condition_list/根对象取 rules 后重建 AND；嵌套分组保留原 AND/OR。
// 不使用告警表达式的 wildcard/match_phrase 语义，动态分组 contains 是转义后的字面量包含。
func CompileDynamicConditions(raw json.RawMessage, model ModelDefinition) (Filter, error) {
	if len(raw) == 0 || len(raw) > 128<<10 {
		return Filter{}, fmt.Errorf("%w: dynamic condition size", ErrInvalidQuery)
	}
	normalized, err := (domain.JSONObject{"conditions": raw}).Normalize()
	if err != nil {
		return Filter{}, fmt.Errorf("%w: invalid dynamic condition JSON", ErrInvalidQuery)
	}
	raw = normalized["conditions"]
	var root dynamicRule
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if len(raw) > 0 && raw[0] == '[' {
		err = decoder.Decode(&root.Rules)
	} else {
		err = decoder.Decode(&root)
	}
	if err != nil || root.Field != "" || root.Operator != "" || root.Value != nil {
		return Filter{}, fmt.Errorf("%w: invalid dynamic condition group", ErrInvalidQuery)
	}
	root.Condition = "AND"
	nodes := 0
	return compileDynamicGroup(root, model, 0, &nodes)
}

func compileDynamicGroup(rule dynamicRule, model ModelDefinition, depth int, nodes *int) (Filter, error) {
	*nodes++
	if depth > 16 || *nodes > 256 || len(rule.Rules) > 64 {
		return Filter{}, fmt.Errorf("%w: dynamic filter budget exceeded", ErrResultLimit)
	}
	if rule.Field != "" || rule.Operator != "" || rule.Value != nil {
		return Filter{}, fmt.Errorf("%w: mixed dynamic group and leaf", ErrInvalidQuery)
	}
	condition := strings.ToUpper(rule.Condition)
	if condition == "" {
		condition = "AND"
	}
	if condition != "AND" && condition != "OR" {
		return Filter{}, fmt.Errorf("%w: invalid dynamic conjunction", ErrInvalidQuery)
	}
	clauses := []Filter{}
	for _, child := range rule.Rules {
		var filter Filter
		var err error
		if child.Rules != nil {
			filter, err = compileDynamicGroup(child, model, depth+1, nodes)
		} else {
			*nodes++
			filter, err = compileDynamicLeaf(child, model)
		}
		if err != nil {
			return Filter{}, err
		}
		if *nodes > 256 {
			return Filter{}, ErrResultLimit
		}
		if !filter.Empty() {
			clauses = append(clauses, filter)
		}
	}
	if len(clauses) == 0 {
		return Filter{}, nil
	}
	if condition == "OR" {
		return Filter{Any: clauses}, nil
	}
	return Filter{All: clauses}, nil
}

func compileDynamicLeaf(rule dynamicRule, model ModelDefinition) (Filter, error) {
	if rule.Field == "" || rule.Condition != "" || len(rule.Field) > 128 {
		return Filter{}, fmt.Errorf("%w: invalid dynamic condition field", ErrInvalidQuery)
	}
	field := rule.Field
	kind := model.AttributeTypes[field]
	switch field {
	case "model_inst_id", "entity_uid", "display_name", "source":
		kind = InstanceAttributeKeyword
	case "bk_biz_ids":
		kind = InstanceAttributeLong
	case "bk_biz_id":
		kind = InstanceAttributeLong
		field = "attributes.bk_biz_id"
	default:
		field = "attributes." + field
	}
	if kind == "" {
		return Filter{}, fmt.Errorf("%w: dynamic field lacks declared type", ErrTargetUnavailable)
	}
	operators := map[string]string{"": "eq", "equal": "eq", "not_equal": "ne", "in": "in", "not_in": "not_in", "contains": "contains", "not_contains": "not_contains", "less": "lt", "less_or_equal": "lte", "greater": "gt", "greater_or_equal": "gte", "exists": "exists", "not exists": "exists"}
	op, ok := operators[strings.ToLower(strings.TrimSpace(rule.Operator))]
	if !ok {
		return Filter{}, fmt.Errorf("%w: unsupported dynamic operator", ErrInvalidQuery)
	}
	filter := Filter{Field: field, Type: kind, Operator: op}
	switch op {
	case "exists":
		// KAC exists 默认 true，not exists 固定 false；false 必须在 nested 结构外取反。
		exists := true
		if rule.Value != nil {
			v, ok := rule.Value.(bool)
			if !ok {
				return Filter{}, fmt.Errorf("%w: exists requires boolean", ErrInvalidQuery)
			}
			exists = v
		}
		if strings.ToLower(strings.TrimSpace(rule.Operator)) == "not exists" {
			exists = false
		}
		if !exists {
			leaf := filter
			filter = Filter{Not: &leaf}
		}
	case "in", "not_in":
		values, ok := rule.Value.([]any)
		if !ok || len(values) == 0 || len(values) > 1024 {
			return Filter{}, fmt.Errorf("%w: dynamic in requires 1..1024 values", ErrInvalidQuery)
		}
		converted := make([]any, 0, len(values))
		for _, value := range values {
			v, err := dynamicScalar(value, kind)
			if err != nil {
				return Filter{}, err
			}
			converted = append(converted, v)
		}
		filter.Value = converted
	default:
		value := rule.Value
		if text, ok := value.(string); ok && (op == "contains" || op == "not_contains") {
			value = strings.TrimSuffix(strings.TrimPrefix(text, ".*"), ".*")
		}
		var err error
		filter.Value, err = dynamicScalar(value, kind)
		if err != nil {
			return Filter{}, err
		}
	}
	if _, err := filter.Compile(); err != nil {
		return Filter{}, err
	}
	return filter, nil
}

func dynamicScalar(value any, kind InstanceAttributeType) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("%w: null dynamic comparison value", ErrInvalidQuery)
	}
	switch kind {
	case InstanceAttributeBoolean:
		if b, ok := value.(bool); ok {
			return b, nil
		}
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		switch text {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		default:
			return nil, fmt.Errorf("%w: invalid boolean condition", ErrInvalidQuery)
		}
	case InstanceAttributeDatetime:
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: datetime condition requires string", ErrInvalidQuery)
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if at, err := time.Parse(layout, text); err == nil {
				return at.UTC().Format(time.RFC3339Nano), nil
			}
		}
		return nil, fmt.Errorf("%w: invalid datetime condition", ErrInvalidQuery)
	case InstanceAttributeKeyword:
		switch v := value.(type) {
		case string:
			return v, nil
		case json.Number:
			return v.String(), nil
		case bool:
			if v {
				return "True", nil
			}
			return "False", nil
		default:
			return nil, fmt.Errorf("%w: keyword condition requires scalar", ErrInvalidQuery)
		}
	case InstanceAttributeLong:
		if b, ok := value.(bool); ok {
			if b {
				return int64(1), nil
			}
			return int64(0), nil
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		number, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid integer condition", ErrInvalidQuery)
		}
		return number, nil
	default:
		return typedFilterValue(kind, value)
	}
}

// NormalizeAttributeType 对齐 OneModel 字段目录类型，不依据本次取值猜测 mapping。
func NormalizeAttributeType(raw string) InstanceAttributeType {
	switch strings.ToLower(raw) {
	case "keyword", "string", "text", "singlechar", "longchar", "enum", "objuser", "organization", "timezone":
		return InstanceAttributeKeyword
	case "int", "integer", "long", "foreignkey":
		return InstanceAttributeLong
	case "float", "double", "number":
		return InstanceAttributeDouble
	case "bool", "boolean":
		return InstanceAttributeBoolean
	case "date", "datetime", "time", "timestamp":
		return InstanceAttributeDatetime
	case "ip":
		return InstanceAttributeIP
	default:
		return ""
	}
}
