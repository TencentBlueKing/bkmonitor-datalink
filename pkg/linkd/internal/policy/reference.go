// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"linkd/internal/jsonpath"
)

type referencePart struct{ literal, field string }

func compileReference(condition Condition, fields FieldCatalog) (compiledCondition, error) {
	var text string
	if condition.Relation != "" || json.Unmarshal(condition.Value, &text) != nil || len(text) > 65536 {
		return compiledCondition{}, fmt.Errorf("field reference requires a string template without CMDB relation")
	}
	parts := []referencePart{}
	for {
		if len(parts) >= 128 {
			return compiledCondition{}, fmt.Errorf("field reference exceeds 128 segments")
		}
		start := strings.Index(text, "${")
		if start < 0 {
			parts = append(parts, referencePart{literal: text})
			break
		}
		parts = append(parts, referencePart{literal: text[:start]})
		text = text[start+2:]
		end := strings.IndexByte(text, '}')
		if end < 0 || fields[text[:end]] == "" {
			return compiledCondition{}, fmt.Errorf("field reference requires a registered field")
		}
		parts = append(parts, referencePart{field: text[:end]})
		text = text[end+1:]
	}
	if len(parts) == 1 {
		return compiledCondition{}, fmt.Errorf("field reference requires at least one placeholder")
	}
	// 发布时仍校验操作符和目标类型；实际查询值须等主告警确定后再独立编译。
	value := "x"
	switch fields[condition.Field] {
	case FieldNumber:
		value = "0"
	case FieldBoolean:
		value = "true"
	case FieldDate:
		value = "2026-01-01T00:00:00Z"
	}
	probe := condition
	probe.Referenced = false
	probe.Value, _ = json.Marshal(value)
	if strings.TrimPrefix(condition.Operator, "must_not_") == "terms" {
		probe.Value, _ = json.Marshal([]string{value})
	}
	result, err := compileCondition(probe, fields)
	result.condition = condition
	result.reference = parts
	return result, err
}

func (c compiledCondition) resolveReference(ctx context.Context, origin Reader) (compiledCondition, error) {
	if len(c.reference) == 0 {
		return c, nil
	}
	if origin == nil {
		return compiledCondition{}, ErrUnavailable
	}
	var output strings.Builder
	for _, part := range c.reference {
		if err := ctx.Err(); err != nil {
			return compiledCondition{}, err
		}
		text := part.literal
		if part.field != "" {
			value, err := origin.Field(ctx, part.field)
			if err != nil {
				return compiledCondition{}, err
			}
			text = ""
			if value.Present {
				encoded, err := json.Marshal(value.Data)
				if err != nil || len(encoded) > 65536 || jsonpath.ValidateTree(value.Data) != nil {
					return compiledCondition{}, ErrUnavailable
				}
				text, err = referenceText(value.Data, false, 0)
				if err != nil {
					return compiledCondition{}, err
				}
			}
		}
		if output.Len()+len(text) > 65536 {
			return compiledCondition{}, ErrUnavailable
		}
		output.WriteString(text)
	}
	condition := c.condition
	condition.Referenced = false
	resolved := output.String()
	if c.kind == FieldBoolean && (resolved == "True" || resolved == "False") {
		resolved = strings.ToLower(resolved)
	}
	condition.Value, _ = json.Marshal(resolved)
	// 旧 membership 引用是一个替换后的字符串，不拆 CSV，也不展开主告警的数组。
	if c.operator == "terms" {
		condition.Value, _ = json.Marshal([]string{resolved})
	}
	return compileCondition(condition, FieldCatalog{condition.Field: c.kind})
}

// referenceText 保留 Python str 的空值、布尔和容器表示；对象键排序确保重放稳定。
// 输入只允许有界 JSON，不读取 payload，也不会递归解释字段值中的模板。
func referenceText(value any, nested bool, depth int) (string, error) {
	if depth > 16 {
		return "", ErrUnavailable
	}
	switch v := value.(type) {
	case nil:
		return "None", nil
	case string:
		if !nested {
			return v, nil
		}
		quoted := strconv.Quote(v)
		if strings.Contains(v, "'") && !strings.Contains(v, `"`) {
			return quoted, nil
		}
		return "'" + strings.ReplaceAll(strings.ReplaceAll(quoted[1:len(quoted)-1], `\"`, `"`), "'", `\'`) + "'", nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			text, err := referenceText(item, true, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			name, err := referenceText(key, true, depth+1)
			if err != nil {
				return "", err
			}
			text, err := referenceText(v[key], true, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, name+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		if number, ok := numberText(value); ok {
			return number, nil
		}
		return "", ErrUnavailable
	}
}
