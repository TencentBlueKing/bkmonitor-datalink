// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"linkd/internal/jsonpath"
)

const mergeTemplateBytes = 1 << 20

const mergeTemplateReadBytes = 32 << 20

// TemplateReader 仅访问冻结成员的有效字段；不可用字段必须返回错误，不能伪装成缺失。
type TemplateReader interface {
	Field(context.Context, string) (Value, error)
}

type mergeTemplatePart struct{ literal, variable string }

type mergeTemplateField struct {
	key     string
	kind    FieldKind
	target  jsonpath.Target
	literal any
	parts   []mergeTemplatePart
}

// MergeTemplate 是发布时编译的只读模板，不解释成员内容中的变量，不执行表达式或用户代码。
type MergeTemplate struct {
	fields   []mergeTemplateField
	maxChars int
}

func compileMergeTemplate(spec MergeSpec, catalog FieldCatalog) (*MergeTemplate, error) {
	if len(spec.Template) < 1 || len(spec.Template) > 64 || spec.MaxMergeFieldLength < 200 || spec.MaxMergeFieldLength > 65536 {
		return nil, fmt.Errorf("new_alarm_config requires 1..64 fields and max_merge_field_length requires 200..65536")
	}
	result := &MergeTemplate{maxChars: spec.MaxMergeFieldLength}
	seen := map[string]bool{}
	variables := map[string]bool{}
	for _, field := range spec.Template {
		if seen[field.Key] || len(field.Key) > 128 {
			return nil, fmt.Errorf("duplicate or oversized template field")
		}
		seen[field.Key] = true
		target, err := mergeTemplateTarget(field.Key, spec.FieldMappings)
		if err != nil {
			return nil, err
		}
		for _, previous := range result.fields {
			if target.Overlaps(previous.target) {
				return nil, fmt.Errorf("template output paths overlap")
			}
		}
		raw, err := json.Marshal(field.Value)
		if err != nil || field.Value == nil || len(raw) > 65536 || jsonpath.ValidateTree(field.Value) != nil {
			return nil, fmt.Errorf("invalid template literal for %s", field.Key)
		}
		compiled := mergeTemplateField{key: field.Key, kind: catalog[field.Key], target: target}
		if value, ok := field.Value.(string); ok {
			parts, err := parseMergeTemplate(value, catalog, spec.Fields)
			if err != nil {
				return nil, fmt.Errorf("template %s: %w", field.Key, err)
			}
			compiled.parts = parts
			dynamic := false
			for _, part := range parts {
				if part.variable != "" {
					variables[part.variable] = true
					dynamic = true
				}
			}
			if !dynamic {
				if _, err := compiled.outputValue(value); err != nil {
					return nil, err
				}
			}
		} else {
			value, err := compiled.outputValue(field.Value)
			if err != nil {
				return nil, err
			}
			compiled.literal = jsonpath.Clone(value)
		}
		result.fields = append(result.fields, compiled)
	}
	if !seen["name"] || !seen["level"] || len(variables) > 128 {
		return nil, fmt.Errorf("template requires name and level, with at most 128 variables")
	}
	return result, nil
}

// 系统字段由内部生产者写入；显式映射也不能覆盖其 labels/extra_data 别名。
func protectedMergeField(key string) bool {
	switch key {
	case "alarm_id", "event_id", "alarm_time", "action", "source_alarm_status", "source_id", "source_name", "bk_tenant_id", "entity_uid", "tag_info", "__kac_custom_fields":
		return true
	default:
		return false
	}
}

func mergeTemplateTarget(key string, mappings map[string]FieldMapping) (jsonpath.Target, error) {
	if protectedMergeField(key) {
		return jsonpath.Target{}, fmt.Errorf("template cannot override system field %s", key)
	}
	if mapping, ok := mappings[key]; ok {
		target, err := jsonpath.ParseTarget(mapping.Path)
		if err != nil {
			return target, err
		}
		parts := target.Parts()
		if len(parts) < 2 || (parts[0] != "labels" && parts[0] != "extra_data") {
			return target, fmt.Errorf("invalid template output path")
		}
		for _, p := range parts {
			if _, ok := p.(string); !ok {
				return target, fmt.Errorf("template output cannot create arrays")
			}
		}
		if protectedMergeField(parts[1].(string)) || (parts[0] == "labels" && len(parts) != 2) {
			return target, fmt.Errorf("template output path is protected or not a scalar label")
		}
		return target, nil
	}
	if KACFields()[key] == "" {
		return jsonpath.Target{}, fmt.Errorf("unknown template output field %s", key)
	}
	switch key {
	case "name":
		return pathFor("title"), nil
	case "content":
		return pathFor("content"), nil
	case "object":
		return pathFor("subject_name"), nil
	case "level":
		return pathFor("severity"), nil
	case "item":
		key = "display_name"
	case "strategy":
		key = "strategy_name"
	case "strategy_id":
		key = "monitor_template_id"
	case "dimension_info":
		key = "dimension_text"
	}
	return pathFor("extra_data", key), nil
}

func parseMergeTemplate(value string, catalog FieldCatalog, groups []string) ([]mergeTemplatePart, error) {
	parts := []mergeTemplatePart{}
	for {
		if len(parts) >= 256 {
			return nil, fmt.Errorf("template exceeds 256 segments")
		}
		start := strings.Index(value, "${")
		if start < 0 {
			parts = append(parts, mergeTemplatePart{literal: value})
			return parts, nil
		}
		parts = append(parts, mergeTemplatePart{literal: value[:start]})
		value = value[start+2:]
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return nil, fmt.Errorf("unterminated template variable")
		}
		name := value[:end]
		switch {
		case name == "alarm_num":
		case strings.HasPrefix(name, "cw_merged_"):
			if catalog[strings.TrimPrefix(name, "cw_merged_")] == "" {
				return nil, fmt.Errorf("unknown merged template variable")
			}
		default:
			if !slices.Contains(groups, name) {
				return nil, fmt.Errorf("template variable must name an aggregate field")
			}
		}
		parts = append(parts, mergeTemplatePart{variable: name})
		value = value[end+1:]
	}
}

func (f mergeTemplateField) outputValue(value any) (any, error) {
	text, isText := value.(string)
	switch f.key {
	case "name", "content", "object", "level", "model_id", "model_inst_id":
		if !isText {
			return nil, fmt.Errorf("template %s must render text", f.key)
		}
		maximum := mergeTemplateBytes
		switch f.key {
		case "name", "object":
			maximum = 256
		case "level":
			maximum = 32
		}
		if len(text) > maximum || ((f.key == "name" || f.key == "level") && strings.TrimSpace(text) == "") {
			return nil, fmt.Errorf("template %s has invalid text length", f.key)
		}
	}
	if f.kind == FieldDate {
		if _, err := queryScalar(FieldDate, value); err != nil {
			return nil, fmt.Errorf("template date requires RFC3339 or YYYY-MM-DD HH:MM:SS")
		}
	}
	if f.kind == FieldNumber {
		// 数值占位符先得到文本，再校验为 JSON 数字；不经过 float64，避免损失大整数。
		raw, err := json.Marshal(value)
		if isText {
			raw = []byte(text)
		}
		if err != nil || len(raw) == 0 || len(raw) > 256 {
			return nil, fmt.Errorf("template number invalid")
		}
		var number json.Number
		if json.Unmarshal(raw, &number) != nil || number == "" {
			return nil, fmt.Errorf("template %s requires a JSON number", f.key)
		}
		// Unmarshal 到 Number 也接受带引号的数字；这里只允许原始 JSON number。
		if raw[0] == '"' {
			return nil, fmt.Errorf("template number invalid")
		}
		value = number
	}
	if f.kind == FieldBoolean {
		if isText {
			var err error
			value, err = strconv.ParseBool(strings.ToLower(text))
			if err != nil || (text != "true" && text != "false" && text != "True" && text != "False") {
				return nil, fmt.Errorf("template boolean invalid")
			}
		}
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("template boolean invalid")
		}
	}
	if f.target.Parts()[0] == "labels" {
		switch v := value.(type) {
		case json.Number:
			number, err := v.Float64()
			before, beforeErr := queryScalar(FieldNumber, v)
			after, afterErr := queryScalar(FieldNumber, number)
			if err != nil || beforeErr != nil || afterErr != nil || before != after {
				return nil, fmt.Errorf("template numeric label loses precision; use extra_data or a string label")
			}
		case string, bool, float64:
		default:
			return nil, fmt.Errorf("template label must be scalar")
		}
	}
	return value, nil
}

// Render 返回新的 Event 字段树；members 必须是已去重、按 AlertID 排序的冻结成员。
// 只在成员值求解时应用 cw_merged 字符截断，完整输出超过 1 MiB 则失败，禁止静默截断合法 Event 字段。
func (t *MergeTemplate) Render(ctx context.Context, members []TemplateReader) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t == nil || len(members) < 2 || len(members) > 256 {
		return nil, fmt.Errorf("merge template requires 2..256 unique members")
	}
	document := map[string]any{}
	cache := map[string]string{}
	remaining := mergeTemplateReadBytes
	outputBytes := 0
	for _, field := range t.fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value := jsonpath.Clone(field.literal)
		if field.parts != nil {
			var builder strings.Builder
			for _, part := range field.parts {
				next := part.literal
				if part.variable != "" {
					var ok bool
					next, ok = cache[part.variable]
					if !ok {
						var err error
						next, err = t.variable(ctx, part.variable, members, &remaining)
						if err != nil {
							return nil, fmt.Errorf("template %s: %w", field.key, err)
						}
						cache[part.variable] = next
					}
				}
				if builder.Len()+len(next) > mergeTemplateBytes {
					return nil, fmt.Errorf("template output field exceeds 1 MiB")
				}
				builder.WriteString(next)
			}
			value = builder.String()
		}
		value, err := field.outputValue(value)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		outputBytes += len(encoded) + len(field.target.String())
		if outputBytes > mergeTemplateBytes {
			return nil, fmt.Errorf("merge template output exceeds 1 MiB")
		}
		if err := field.target.Set(document, value); err != nil {
			return nil, err
		}
	}
	if err := jsonpath.ValidateTree(document); err != nil {
		return nil, err
	}
	return document, ctx.Err()
}

func (t *MergeTemplate) variable(ctx context.Context, name string, members []TemplateReader, remaining *int) (string, error) {
	if name == "alarm_num" {
		return strconv.Itoa(len(members)), nil
	}
	merged := strings.HasPrefix(name, "cw_merged_")
	field := name
	if merged {
		field = strings.TrimPrefix(name, "cw_merged_")
	}
	values := []string{}
	groupKey := ""
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if member == nil {
			return "", ErrUnavailable
		}
		value, err := member.Field(ctx, field)
		if err != nil {
			return "", err
		}
		text := "--"
		if value.Present {
			text, err = templateText(value.Data)
			if err != nil {
				return "", err
			}
		}
		if !merged {
			key, err := GroupKey([]any{value.Data})
			if !value.Present || err != nil {
				return "", fmt.Errorf("aggregate template value missing or invalid")
			}
			if groupKey != "" && groupKey != key {
				return "", fmt.Errorf("aggregate template values differ")
			}
			groupKey = key
		}
		*remaining -= len(text)
		if *remaining < 0 {
			return "", fmt.Errorf("merge template input exceeds 32 MiB")
		}
		values = append(values, text)
	}
	if !merged {
		return values[0], nil
	}
	slices.Sort(values)
	values = slices.Compact(values)
	text := strings.Join(values, "###")
	count := 0
	for i := range text {
		if count == t.maxChars {
			return text[:i], nil
		}
		count++
	}
	return text, nil
}

func templateText(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	if err := jsonpath.ValidateTree(value); err != nil {
		return "", err
	}
	// 与 KAC 保持单值 bool/null 的展示；数组/对象改为键序稳定的 JSON 文本。
	switch value {
	case nil:
		return "None", nil
	case true:
		return "True", nil
	case false:
		return "False", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("template value is not JSON")
	}
	return string(raw), nil
}
