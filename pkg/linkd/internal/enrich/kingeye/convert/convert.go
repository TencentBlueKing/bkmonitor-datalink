// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package convert 将离线 Kingeye 丰富配置转为分组 Linkd 规则并报告不能自动保持的语义。
package convert

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"linkd/internal/config"
	"linkd/internal/enrich/custom"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// Model 提供不能从告警源规则推断的租户模型目录。
type Model struct {
	ModelID    string                                    `json:"model_id"`
	BKObjID    string                                    `json:"bk_obj_id"`
	Name       string                                    `json:"name"`
	Attributes map[string]onemodel.InstanceAttributeType `json:"attributes"`
}

// Input 使用配置导出数据，不建立 Kingeye 表或服务连接。
type Input struct {
	CMDBRules     []json.RawMessage `json:"cmdb_rules"`
	NormalRules   []json.RawMessage `json:"normal_rules"`
	Models        map[string]Model  `json:"models"`
	FieldMappings map[string]string `json:"field_mappings"`
}

// Item 保留旧配置位置、原文和转换状态，阻止静默漏掉一条旧规则。
type Item struct {
	Path     string          `json:"path"`
	Status   string          `json:"status"`
	Message  string          `json:"message,omitempty"`
	Original json.RawMessage `json:"original"`
	RuleID   string          `json:"rule_id,omitempty"`
}

// Result 最多产生两个处理器，绝不自动发布。
type Result struct {
	Enrich config.EnrichConfig `json:"enrich"`
	Report []Item              `json:"report"`
}

type field struct {
	Key              string   `json:"key"`
	Value            string   `json:"value"`
	ModelAssociation []string `json:"model_asst_info"`
}

type settingRule struct {
	Field      string `json:"field"`
	Value      string `json:"value"`
	Expression string `json:"expression"`
	Condition  string `json:"condition"`
}

type normal struct {
	Name     string                     `json:"name"`
	Match    map[string]json.RawMessage `json:"match_rules"`
	Settings []struct {
		Type   string        `json:"type"`
		Fields []field       `json:"fields"`
		Rules  []settingRule `json:"rules"`
	} `json:"enrich_settings"`
}

type cmdb struct {
	Name          string                     `json:"name"`
	Model         json.RawMessage            `json:"alarm_object_id"`
	ObjectRules   map[string]json.RawMessage `json:"obj_rules"`
	InstanceRules map[string]json.RawMessage `json:"inst_rules"`
	Fields        []field                    `json:"enrich_fields"`
	Multi         []json.RawMessage          `json:"multi_model_rules"`
}

type converter struct{ input Input }

// Convert 独立校验每条规则，失败规则保留在报告中且不产生半条候选规则。
func Convert(input Input) (Result, error) {
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > 1<<20 {
		return Result{}, fmt.Errorf("conversion input exceeds 1 MiB")
	}
	if len(input.CMDBRules) > 128 || len(input.NormalRules) > 128 {
		return Result{}, fmt.Errorf("rule limit exceeded")
	}
	c := converter{input}
	out := Result{Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{}}, Report: []Item{}}
	for _, kind := range []string{"cmdb", "fields"} {
		raws := input.NormalRules
		root := "normal_rules"
		if kind == "cmdb" {
			raws = input.CMDBRules
			root = "cmdb_rules"
		}
		rules := []custom.Rule{}
		for i, raw := range raws {
			item := Item{Path: fmt.Sprintf("$.%s[%d]", root, i), Original: raw, Status: "converted", RuleID: fmt.Sprintf("%s_%d", kind, i+1)}
			var rule custom.Rule
			var err error
			if kind == "fields" {
				rule, err = c.normal(raw, item.RuleID)
			} else {
				rule, err = c.cmdb(raw, item.RuleID)
				if err == nil {
					item.Status = "needs_review"
					item.Message = "核对已有实例身份分支、模型匹配顺序与多命中；Linkd 不自动选择首条实例。展示转换需配置 mysql/kingeye_display。"
				}
			}
			if err == nil {
				data, _ := json.Marshal(custom.Config{Rules: []custom.Rule{rule}})
				var config map[string]any
				err = json.Unmarshal(data, &config)
				if err == nil {
					_, err = custom.Compile(kind, config)
				}
			}
			if err != nil {
				item.Status = "unsupported"
				item.Message = err.Error()
				item.RuleID = ""
			} else {
				rules = append(rules, rule)
			}
			out.Report = append(out.Report, item)
		}
		if len(rules) > 0 {
			data, _ := json.Marshal(custom.Config{Rules: rules})
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Result{}, err
			}
			if _, err := custom.Compile(kind, cfg); err != nil {
				return Result{}, fmt.Errorf("combined %s configuration: %w", kind, err)
			}
			out.Enrich.Processors = append(out.Enrich.Processors, config.EnrichProcessorConfig{Type: kind, Config: cfg})
		}
	}
	return out, nil
}

func (c converter) path(field string) (string, error) {
	if path, ok := c.input.FieldMappings[field]; ok {
		if err := validateTarget(path); err != nil {
			return "", err
		}
		return path, nil
	}
	switch field {
	case "name":
		return "$.title", nil
	case "content":
		return "$.content", nil
	case "object":
		return "$.subject_name", nil
	default:
		return "", fmt.Errorf("field %q requires explicit field_mappings", field)
	}
}

func validateTarget(path string) error {
	_, err := custom.Compile("fields", map[string]any{"rules": []any{map[string]any{"id": "validate", "operations": []any{map[string]any{"id": "assign", "type": "assign", "assignments": []any{map[string]any{"target": path, "value": map[string]any{"literal": ""}}}}}}}})
	return err
}

func (c converter) source(field string) (custom.Value, error) {
	path, err := c.path(field)
	if err != nil {
		return custom.Value{}, err
	}
	return custom.Value{JSONPath: "$.alert" + strings.TrimPrefix(path, "$")}, nil
}

var variables = regexp.MustCompile(`\$\{([^}]+)\}|\$([0-9]+)`)

func (c converter) template(text string, captures int) (custom.Value, error) {
	vars := map[string]custom.Value{}
	var failure error
	converted := variables.ReplaceAllStringFunc(text, func(token string) string {
		parts := variables.FindStringSubmatch(token)
		key := fmt.Sprintf("v%d", len(vars))
		var v custom.Value
		if parts[1] != "" {
			var err error
			v, err = c.source(parts[1])
			if err != nil {
				failure = err
			}
		} else {
			index, _ := strconv.Atoi(parts[2])
			if captures < 0 || index < 1 {
				failure = fmt.Errorf("invalid extraction reference %q", token)
			}
			path := ""
			switch captures {
			case 0:
				path = fmt.Sprintf("$.extraction.matches[%d].text", index-1)
			case 1:
				path = fmt.Sprintf("$.extraction.matches[%d].groups[0]", index-1)
			default:
				path = fmt.Sprintf("$.extraction.matches[0].groups[%d]", index-1)
			}
			v = custom.Value{JSONPath: path}
		}
		vars[key] = v
		return "${" + key + "}"
	})
	if failure != nil {
		return custom.Value{}, failure
	}
	if len(vars) == 0 {
		return literal(text), nil
	}
	return custom.Value{Template: &converted, Variables: vars}, nil
}

func literal(v any) custom.Value { b, _ := json.Marshal(v); return custom.Value{Literal: b} }

func (c converter) normal(raw json.RawMessage, id string) (custom.Rule, error) {
	var old normal
	if err := json.Unmarshal(raw, &old); err != nil {
		return custom.Rule{}, err
	}
	rule := custom.Rule{ID: id}
	when, err := c.conditions(old.Match)
	if err != nil {
		return rule, err
	}
	rule.When = when
	for i, setting := range old.Settings {
		base := fmt.Sprintf("step_%d", i+1)
		switch setting.Type {
		case "replace":
			for j, f := range setting.Fields {
				target, err := c.path(f.Key)
				if err != nil {
					return rule, err
				}
				op := custom.Operation{ID: fmt.Sprintf("%s_%d", base, j+1), Type: "replace", Target: target}
				for _, rep := range setting.Rules {
					if strings.Contains(rep.Field, "@{") || strings.Contains(rep.Value, "@{") {
						return rule, fmt.Errorf("KAC replacement sentinel needs manual review")
					}
					op.Replacements = append(op.Replacements, custom.Replacement{From: rep.Field, To: rep.Value})
				}
				rule.Operations = append(rule.Operations, op)
			}
		case "field_adjust":
			for j, f := range setting.Fields {
				target, err := c.path(f.Key)
				if err != nil {
					return rule, err
				}
				value, err := c.template(f.Value, -1)
				if err != nil {
					return rule, err
				}
				rule.Operations = append(rule.Operations, custom.Operation{ID: fmt.Sprintf("%s_%d", base, j+1), Type: "assign", Assignments: []custom.Assignment{{Target: target, Value: value}}})
			}
		case "extract":
			if len(setting.Rules) != 1 {
				return rule, fmt.Errorf("extract needs exactly one regex")
			}
			r := setting.Rules[0]
			pattern, err := regexp.Compile(r.Value)
			if err != nil {
				return rule, fmt.Errorf("python regex unsupported by Go: %w", err)
			}
			source, err := c.source(r.Field)
			if err != nil {
				return rule, err
			}
			op := custom.Operation{ID: base, Type: "extract", Source: &source, Pattern: r.Value}
			for _, f := range setting.Fields {
				target, err := c.path(f.Key)
				if err != nil {
					return rule, err
				}
				value, err := c.template(f.Value, pattern.NumSubexp())
				if err != nil {
					return rule, err
				}
				for _, v := range value.Variables {
					if strings.HasPrefix(v.JSONPath, "$.alert") {
						return rule, fmt.Errorf("extract field references alert state; split dependent assignments manually")
					}
				}
				op.Assignments = append(op.Assignments, custom.Assignment{Target: target, Value: value})
			}
			rule.Operations = append(rule.Operations, op)
		default:
			return rule, fmt.Errorf("unsupported enrich setting %q", setting.Type)
		}
	}
	return rule, nil
}

func (c converter) conditions(old map[string]json.RawMessage) (*custom.Condition, error) {
	if len(old) == 0 {
		return nil, nil
	}
	return conditionExpression(old, func(raw json.RawMessage) (custom.Condition, error) {
		var leaf settingRule
		if err := json.Unmarshal(raw, &leaf); err != nil {
			return custom.Condition{}, err
		}
		value, err := c.source(leaf.Field)
		if err != nil {
			return custom.Condition{}, err
		}
		empty := literal("")
		value.Default = &empty
		value.Transforms = []custom.Transform{{Type: "string"}}
		op, err := operator(leaf.Condition)
		if err != nil {
			return custom.Condition{}, err
		}
		if leaf.Condition == "regexp_extract" {
			pattern, err := regexp.Compile(leaf.Expression)
			if err != nil {
				return custom.Condition{}, err
			}
			_ = pattern
			value.Transforms = append(value.Transforms, custom.Transform{Type: "regex_extract", Pattern: leaf.Expression, Group: 0})
		}
		if op == "regex" {
			if _, err := regexp.Compile(leaf.Value); err != nil {
				return custom.Condition{}, err
			}
		}
		right := literal(leaf.Value)
		return custom.Condition{Left: &value, Operator: op, Right: &right}, nil
	})
}

func operator(old string) (string, error) {
	switch old {
	case "term", "regexp_extract":
		return "eq", nil
	case "must_not_term":
		return "ne", nil
	case "wildcard":
		return "contains", nil
	case "must_not_wildcard":
		return "not_contains", nil
	case "regexp":
		return "regex", nil
	}
	return "", fmt.Errorf("unsupported condition %q", old)
}

func (c converter) cmdb(raw json.RawMessage, id string) (custom.Rule, error) {
	var old cmdb
	if err := json.Unmarshal(raw, &old); err != nil {
		return custom.Rule{}, err
	}
	rule := custom.Rule{ID: id}
	var modelKey any
	if err := json.Unmarshal(old.Model, &modelKey); err != nil {
		return rule, err
	}
	model, ok := c.input.Models[fmt.Sprint(modelKey)]
	if !ok || model.ModelID == "" {
		return rule, fmt.Errorf("alarm_object_id requires model directory")
	}
	if len(old.Multi) > 0 {
		return rule, fmt.Errorf("multi_model_rules requires explicit relation identifiers and directions; convert to relations after metadata review")
	}
	when, err := c.conditions(old.ObjectRules)
	if err != nil {
		return rule, err
	}
	rule.When = when
	condition, err := conditionExpression(old.InstanceRules, func(raw json.RawMessage) (custom.Condition, error) {
		var leaf settingRule
		if err := json.Unmarshal(raw, &leaf); err != nil {
			return custom.Condition{}, err
		}
		v, err := c.source(leaf.Field)
		if err != nil {
			return custom.Condition{}, err
		}
		op, err := operator(leaf.Condition)
		if err != nil {
			return custom.Condition{}, err
		}
		if leaf.Condition == "regexp_extract" {
			pattern, err := regexp.Compile(leaf.Expression)
			if err != nil {
				return custom.Condition{}, err
			}
			group := 0
			if pattern.NumSubexp() > 0 {
				group = 1
			}
			v.Transforms = []custom.Transform{{Type: "regex_extract", Pattern: leaf.Expression, Group: group}}
		}
		field := literal(leaf.Value)
		return custom.Condition{Left: &field, Operator: op, Right: &v}, nil
	})
	if err != nil {
		return rule, err
	}
	var predicate func(*custom.Condition) (*custom.Predicate, error)
	predicate = func(n *custom.Condition) (*custom.Predicate, error) {
		p := &custom.Predicate{}
		for i := range n.All {
			child, err := predicate(&n.All[i])
			if err != nil {
				return nil, err
			}
			p.All = append(p.All, *child)
		}
		for i := range n.Any {
			child, err := predicate(&n.Any[i])
			if err != nil {
				return nil, err
			}
			p.Any = append(p.Any, *child)
		}
		if n.Not != nil {
			child, err := predicate(n.Not)
			if err != nil {
				return nil, err
			}
			p.Not = child
		}
		if n.Left != nil {
			var attr string
			if err := json.Unmarshal(n.Left.Literal, &attr); err != nil {
				return nil, err
			}
			typ, ok := model.Attributes[attr]
			if !ok {
				return nil, fmt.Errorf("model attribute %q needs explicit type", attr)
			}
			p.Field = "attributes." + attr
			p.Type = typ
			p.Operator = n.Operator
			p.Value = n.Right
		}
		return p, nil
	}
	if condition == nil {
		return rule, fmt.Errorf("instance rule expression required; configure explicit identity lookup")
	}
	where, err := predicate(condition)
	if err != nil {
		return rule, err
	}
	rule.Lookup = &custom.Lookup{ModelID: model.ModelID, Expect: "one", Where: where}
	for _, f := range old.Fields {
		if len(f.ModelAssociation) > 0 {
			return rule, fmt.Errorf("association field requires reviewed relations mapping")
		}
		target, err := c.path(f.Key)
		if err != nil {
			return rule, err
		}
		path, _ := jsonpath.ParseTarget("$[" + strconv.Quote(f.Value) + "]")
		rule.Assignments = append(rule.Assignments, custom.Assignment{Target: target, Value: custom.Value{JSONPath: "$.lookup.attributes" + strings.TrimPrefix(path.String(), "$"), Transforms: []custom.Transform{{Type: "display", ModelID: model.ModelID, Field: f.Value}}}})
	}
	return rule, nil
}

// expressionParser 只接受符号、括号和 and/or/not，不执行 Python 或用户代码。
type expressionParser struct {
	tokens  []string
	index   int
	leaves  map[string]json.RawMessage
	convert func(json.RawMessage) (custom.Condition, error)
}

func conditionExpression(old map[string]json.RawMessage, convert func(json.RawMessage) (custom.Condition, error)) (*custom.Condition, error) {
	if len(old) == 0 {
		return nil, nil
	}
	var expression string
	if err := json.Unmarshal(old["expression"], &expression); err != nil {
		return nil, fmt.Errorf("expression is required")
	}
	if len(expression) > 2048 {
		return nil, fmt.Errorf("expression too long")
	}
	tokens := []string{}
	for i := 0; i < len(expression); {
		r := rune(expression[i])
		if unicode.IsSpace(r) {
			i++
			continue
		}
		if r == '(' || r == ')' {
			tokens = append(tokens, expression[i:i+1])
			i++
			continue
		}
		start := i
		for i < len(expression) && ((expression[i] >= 'A' && expression[i] <= 'Z') || (expression[i] >= 'a' && expression[i] <= 'z') || (expression[i] >= '0' && expression[i] <= '9') || expression[i] == '_') {
			i++
		}
		if i == start {
			return nil, fmt.Errorf("unsupported expression token")
		}
		tokens = append(tokens, expression[start:i])
	}
	p := expressionParser{tokens: tokens, leaves: old, convert: convert}
	result, err := p.parseOr(0)
	if err == nil && p.index != len(tokens) {
		err = fmt.Errorf("unexpected expression token")
	}
	return result, err
}

func (p *expressionParser) take(token string) bool {
	if p.index < len(p.tokens) && strings.EqualFold(p.tokens[p.index], token) {
		p.index++
		return true
	}
	return false
}

func (p *expressionParser) parseOr(depth int) (*custom.Condition, error) {
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for p.take("or") {
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		left = &custom.Condition{Any: []custom.Condition{*left, *right}}
	}
	return left, nil
}

func (p *expressionParser) parseAnd(depth int) (*custom.Condition, error) {
	left, err := p.atom(depth)
	if err != nil {
		return nil, err
	}
	for p.take("and") {
		right, err := p.atom(depth)
		if err != nil {
			return nil, err
		}
		left = &custom.Condition{All: []custom.Condition{*left, *right}}
	}
	return left, nil
}

func (p *expressionParser) atom(depth int) (*custom.Condition, error) {
	if depth > 16 {
		return nil, fmt.Errorf("expression depth exceeded")
	}
	if p.take("not") {
		child, err := p.atom(depth + 1)
		return &custom.Condition{Not: child}, err
	}
	if p.take("(") {
		child, err := p.parseOr(depth + 1)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("unbalanced expression")
		}
		return child, nil
	}
	if p.index >= len(p.tokens) {
		return nil, fmt.Errorf("missing expression operand")
	}
	symbol := p.tokens[p.index]
	p.index++
	raw, ok := p.leaves[symbol]
	if !ok || symbol == "expression" {
		return nil, fmt.Errorf("unknown expression symbol %q", symbol)
	}
	leaf, err := p.convert(raw)
	return &leaf, err
}
