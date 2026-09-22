// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package custom 编译并执行有界的 CMDB 与字段规则，不写存储或触发生命周期。
package custom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// Value 描述一种取值及其顺序转换；Literal 的显式 null 与未配置不同。
type Value struct {
	Literal    json.RawMessage  `json:"literal,omitempty"`
	JSONPath   string           `json:"jsonpath,omitempty"`
	Template   *string          `json:"template,omitempty"`
	Variables  map[string]Value `json:"variables,omitempty"`
	Select     string           `json:"select,omitempty"`
	Default    *Value           `json:"default,omitempty"`
	Transforms []Transform      `json:"transforms,omitempty"`
	query      *jsonpath.Query
}

// Transform 是取值后的确定性转换或只读展示转换。
type Transform struct {
	Type      string         `json:"type"`
	Separator string         `json:"separator,omitempty"`
	Pattern   string         `json:"pattern,omitempty"`
	Group     int            `json:"group,omitempty"`
	Mapping   map[string]any `json:"mapping,omitempty"`
	ModelID   string         `json:"model_id,omitempty"`
	Field     string         `json:"field,omitempty"`
	regex     *regexp.Regexp
}

// Condition 是只在规则开始时求值一次的条件树。
type Condition struct {
	All      []Condition `json:"all,omitempty"`
	Any      []Condition `json:"any,omitempty"`
	Not      *Condition  `json:"not,omitempty"`
	Left     *Value      `json:"left,omitempty"`
	Operator string      `json:"operator,omitempty"`
	Right    *Value      `json:"right,omitempty"`
}

// Assignment 描述一个确定目标的赋值。
type Assignment struct {
	Target string `json:"target"`
	Value  Value  `json:"value"`
	target jsonpath.Target
}

// Replacement 是文字替换，不把搜索文本当作正则。
type Replacement struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Operation 是字段规则内的一个原子输出步骤。
type Operation struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Source       *Value        `json:"source,omitempty"`
	Pattern      string        `json:"pattern,omitempty"`
	Target       string        `json:"target,omitempty"`
	Replacements []Replacement `json:"replacements,omitempty"`
	Assignments  []Assignment  `json:"assignments,omitempty"`
	regex        *regexp.Regexp
	target       jsonpath.Target
}

// Predicate 是实例属性过滤，Value 可引用本次告警。
type Predicate struct {
	All      []Predicate                    `json:"all,omitempty"`
	Any      []Predicate                    `json:"any,omitempty"`
	Not      *Predicate                     `json:"not,omitempty"`
	Field    string                         `json:"field,omitempty"`
	Type     onemodel.InstanceAttributeType `json:"type,omitempty"`
	Operator string                         `json:"operator,omitempty"`
	Value    *Value                         `json:"value,omitempty"`
}

// Lookup 描述主实例或关联实例的查询目标。
type Lookup struct {
	ModelID string     `json:"model_id"`
	Expect  string     `json:"expect,omitempty"`
	Where   *Predicate `json:"where,omitempty"`
}

// Relation 是规则内有序的模型关系链节点。
type Relation struct {
	ID        string     `json:"id"`
	From      string     `json:"from,omitempty"`
	Relation  string     `json:"relation"`
	Direction string     `json:"direction"`
	ModelID   string     `json:"model_id"`
	Expect    string     `json:"expect,omitempty"`
	Where     *Predicate `json:"where,omitempty"`
}

// Rule 保留方案的一次匹配与内部操作顺序。
type Rule struct {
	ID          string       `json:"id"`
	When        *Condition   `json:"when,omitempty"`
	Operations  []Operation  `json:"operations,omitempty"`
	Lookup      *Lookup      `json:"lookup,omitempty"`
	Relations   []Relation   `json:"relations,omitempty"`
	Topology    bool         `json:"topology,omitempty"`
	Assignments []Assignment `json:"assignments,omitempty"`
}

// Config 对应一个 Processor 的多条配置。
type Config struct {
	Rules []Rule `json:"rules"`
}

// Program 是与某次来源发布绑定、可并发只读复用的编译结果。
type Program struct {
	kind    string
	config  Config
	display bool
}

// NeedsDisplay 判断该程序是否使用外部展示转换。
func (p *Program) NeedsDisplay() bool { return p.display }

// Compile 完成不依赖外部服务的严格校验，所有表达式在发布时编译。
func Compile(kind string, raw map[string]any) (*Program, error) {
	if kind != "cmdb" && kind != "fields" {
		return nil, fmt.Errorf("unknown rule processor %q", kind)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("rule config exceeds 64 KiB")
	}
	p := &Program{kind: kind}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p.config); err != nil {
		return nil, fmt.Errorf("rule config: %w", err)
	}
	if p.config.Rules == nil {
		return nil, fmt.Errorf("rules must be an array")
	}
	if len(p.config.Rules) > 128 {
		return nil, fmt.Errorf("at most 128 rules are allowed")
	}
	seen := map[string]bool{}
	for i := range p.config.Rules {
		r := &p.config.Rules[i]
		if err := checkID(r.ID, seen); err != nil {
			return nil, fmt.Errorf("rules[%d]: %w", i, err)
		}
		if err := p.compileRule(r); err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
	}
	return p, nil
}

func checkID(id string, seen map[string]bool) error {
	if id == "" || len(id) > 64 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" || seen[id] {
		return fmt.Errorf("invalid or duplicate id %q", id)
	}
	seen[id] = true
	return nil
}

func (p *Program) compileRule(r *Rule) error {
	if r.When != nil {
		if err := p.compileCondition(r.When, 0); err != nil {
			return err
		}
	}
	if p.kind == "cmdb" {
		if r.Lookup == nil || len(r.Operations) > 0 {
			return fmt.Errorf("cmdb rule requires lookup and no operations")
		}
		if err := p.compileLookup(r.Lookup); err != nil {
			return err
		}
		if r.Topology && (r.Lookup.ModelID != "cw-Host" || r.Lookup.Expect != "one") {
			return fmt.Errorf("topology requires expect one on cw-Host")
		}
		if len(r.Relations) > 8 {
			return fmt.Errorf("at most 8 relation steps")
		}
		names := map[string]bool{"root": true}
		expects := map[string]string{"root": r.Lookup.Expect}
		for i := range r.Relations {
			rel := &r.Relations[i]
			if rel.From == "" {
				rel.From = "root"
			}
			if !names[rel.From] {
				return fmt.Errorf("relation from must reference an earlier result")
			}
			if expects[rel.From] == "many" {
				return fmt.Errorf("intermediate relation must select one instance")
			}
			if err := checkID(rel.ID, names); err != nil {
				return err
			}
			if rel.Relation == "" || (rel.Direction != "in" && rel.Direction != "out" && rel.Direction != "both") {
				return fmt.Errorf("relation and direction required")
			}
			lookup := Lookup{ModelID: rel.ModelID, Expect: rel.Expect, Where: rel.Where}
			if lookup.Where == nil {
				lookup.Where = &Predicate{Field: "model_inst_id", Type: onemodel.InstanceAttributeKeyword, Operator: "exists"}
			}
			if err := p.compileLookup(&lookup); err != nil {
				return err
			}
			rel.Expect = lookup.Expect
			expects[rel.ID] = rel.Expect
		}
		return p.compileAssignments(r.Assignments)
	}
	if r.Lookup != nil || len(r.Relations) > 0 || len(r.Assignments) > 0 || r.Topology {
		return fmt.Errorf("fields rule only supports operations")
	}
	if len(r.Operations) == 0 || len(r.Operations) > 64 {
		return fmt.Errorf("fields rule requires 1..64 operations")
	}
	seen := map[string]bool{}
	for i := range r.Operations {
		o := &r.Operations[i]
		if err := checkID(o.ID, seen); err != nil {
			return err
		}
		switch o.Type {
		case "assign":
			if o.Source != nil || o.Pattern != "" || o.Target != "" || len(o.Replacements) > 0 {
				return fmt.Errorf("assign has unsupported fields")
			}
			if err := p.compileAssignments(o.Assignments); err != nil {
				return err
			}
		case "extract":
			if o.Source == nil || o.Pattern == "" || len(o.Pattern) > 2048 || o.Target != "" || len(o.Replacements) > 0 {
				return fmt.Errorf("extract requires source and pattern")
			}
			if err := p.compileValue(o.Source, 0); err != nil {
				return err
			}
			re, err := regexp.Compile(o.Pattern)
			if err != nil {
				return fmt.Errorf("operation %s regexp: %w", o.ID, err)
			}
			o.regex = re
			if err := p.compileAssignments(o.Assignments); err != nil {
				return err
			}
		case "replace":
			if o.Source != nil || o.Pattern != "" || len(o.Assignments) > 0 {
				return fmt.Errorf("replace has unsupported fields")
			}
			if err := validateTarget(o.Target); err != nil {
				return err
			}
			t, err := jsonpath.ParseTarget(o.Target)
			if err != nil {
				return err
			}
			o.target = t
			if len(o.Replacements) == 0 || len(o.Replacements) > 64 {
				return fmt.Errorf("replace requires 1..64 replacements")
			}
			for _, rep := range o.Replacements {
				if rep.From == "" {
					return fmt.Errorf("replace from must not be empty")
				}
			}
		default:
			return fmt.Errorf("unknown operation type %q", o.Type)
		}
	}
	return nil
}

func (p *Program) compileAssignments(a []Assignment) error {
	if len(a) == 0 || len(a) > 64 {
		return fmt.Errorf("requires 1..64 assignments")
	}
	for i := range a {
		if err := validateTarget(a[i].Target); err != nil {
			return err
		}
		t, err := jsonpath.ParseTarget(a[i].Target)
		if err != nil {
			return err
		}
		a[i].target = t
		for j := 0; j < i; j++ {
			if t.Overlaps(a[j].target) {
				return fmt.Errorf("assignment targets overlap")
			}
		}
		if err := p.compileValue(&a[i].Value, 0); err != nil {
			return fmt.Errorf("assignment %s: %w", a[i].Target, err)
		}
	}
	return nil
}

func (p *Program) compileValue(v *Value, depth int) error {
	if depth > 16 {
		return fmt.Errorf("value expression depth exceeded")
	}
	n := 0
	if len(v.Literal) > 0 {
		n++
	}
	if v.JSONPath != "" {
		n++
	}
	if v.Template != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("value requires exactly one literal/jsonpath/template")
	}
	if v.Select != "" && v.Select != "one" && v.Select != "all" {
		return fmt.Errorf("select must be one or all")
	}
	if v.JSONPath != "" {
		q, err := jsonpath.Compile(v.JSONPath)
		if err != nil {
			return err
		}
		v.query = q
	}
	if v.Template != nil {
		if len(*v.Template) > 65536 {
			return fmt.Errorf("template too large")
		}
		for name, variable := range v.Variables {
			if err := p.compileValue(&variable, depth+1); err != nil {
				return err
			}
			v.Variables[name] = variable
		}
		for _, part := range variablePattern.FindAllStringSubmatch(*v.Template, -1) {
			if _, ok := v.Variables[part[1]]; !ok {
				return fmt.Errorf("template variable %q not declared", part[1])
			}
		}
	}
	if v.Default != nil {
		if err := p.compileValue(v.Default, depth+1); err != nil {
			return err
		}
	}
	if len(v.Transforms) > 16 {
		return fmt.Errorf("too many transforms")
	}
	for i := range v.Transforms {
		t := &v.Transforms[i]
		switch t.Type {
		case "string", "number", "bool", "join", "map":
		case "regex_extract":
			if len(t.Pattern) > 2048 {
				return fmt.Errorf("pattern too long")
			}
			r, err := regexp.Compile(t.Pattern)
			if err != nil {
				return err
			}
			if t.Group < 0 || t.Group > r.NumSubexp() {
				return fmt.Errorf("invalid capture group")
			}
			t.regex = r
		case "display":
			if t.ModelID == "" || t.Field == "" {
				return fmt.Errorf("display transform requires model_id and field")
			}
			p.display = true
		default:
			return fmt.Errorf("unsupported transform %q", t.Type)
		}
	}
	return nil
}

func (p *Program) compileCondition(c *Condition, depth int) error {
	if depth > 16 {
		return fmt.Errorf("condition depth exceeded")
	}
	n := 0
	if len(c.All) > 0 {
		n++
	}
	if len(c.Any) > 0 {
		n++
	}
	if c.Not != nil {
		n++
	}
	if c.Left != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("condition requires exactly one leaf/all/any/not")
	}
	if len(c.All)+len(c.Any) > 64 {
		return fmt.Errorf("too many conditions")
	}
	for i := range c.All {
		if err := p.compileCondition(&c.All[i], depth+1); err != nil {
			return err
		}
	}
	for i := range c.Any {
		if err := p.compileCondition(&c.Any[i], depth+1); err != nil {
			return err
		}
	}
	if c.Not != nil {
		return p.compileCondition(c.Not, depth+1)
	}
	if c.Left != nil {
		if err := p.compileValue(c.Left, 0); err != nil {
			return err
		}
		switch c.Operator {
		case "exists":
			if c.Right != nil {
				return fmt.Errorf("exists has no right value")
			}
			return nil
		case "eq", "ne", "contains", "not_contains", "regex", "not_regex", "in", "not_in":
		default:
			return fmt.Errorf("invalid condition operator")
		}
		if c.Right == nil {
			return fmt.Errorf("condition right value required")
		}
		if err := p.compileValue(c.Right, 0); err != nil {
			return err
		}
		if (c.Operator == "regex" || c.Operator == "not_regex") && len(c.Right.Literal) > 0 {
			var pattern string
			if err := json.Unmarshal(c.Right.Literal, &pattern); err != nil {
				return fmt.Errorf("regex requires string")
			}
			if len(pattern) > 2048 {
				return fmt.Errorf("pattern too long")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (p *Program) compileLookup(l *Lookup) error {
	if l.ModelID == "" || len(l.ModelID) > 128 {
		return fmt.Errorf("model_id required")
	}
	if l.Expect == "" {
		l.Expect = "one"
	}
	if l.Expect != "one" && l.Expect != "many" {
		return fmt.Errorf("expect must be one or many")
	}
	if l.Where == nil {
		return fmt.Errorf("bounded lookup requires where")
	}
	return p.compilePredicate(l.Where, 0)
}

func (p *Program) compilePredicate(f *Predicate, depth int) error {
	if depth > 16 {
		return fmt.Errorf("query condition depth exceeded")
	}
	n := 0
	if f.Field != "" {
		n++
	}
	if len(f.All) > 0 {
		n++
	}
	if len(f.Any) > 0 {
		n++
	}
	if f.Not != nil {
		n++
	}
	if n != 1 || len(f.All)+len(f.Any) > 64 {
		return fmt.Errorf("invalid query condition group")
	}
	for i := range f.All {
		if err := p.compilePredicate(&f.All[i], depth+1); err != nil {
			return err
		}
	}
	for i := range f.Any {
		if err := p.compilePredicate(&f.Any[i], depth+1); err != nil {
			return err
		}
	}
	if f.Not != nil {
		return p.compilePredicate(f.Not, depth+1)
	}
	if f.Field != "" {
		if f.Operator != "exists" {
			if f.Value == nil {
				return fmt.Errorf("query value required")
			}
			if err := p.compileValue(f.Value, 0); err != nil {
				return err
			}
		}
		dummy := any("")
		switch f.Type {
		case onemodel.InstanceAttributeLong, onemodel.InstanceAttributeDouble:
			dummy = 0
		case onemodel.InstanceAttributeBoolean:
			dummy = false
		}
		if f.Operator == "in" || f.Operator == "not_in" {
			dummy = []any{dummy}
		}
		_, err := (onemodel.Filter{Field: f.Field, Type: f.Type, Operator: f.Operator, Value: dummy}).Compile()
		return err
	}
	return nil
}

var variablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// validateTarget 保护由受控资源处理器计算的权限与动态分组结果。
func validateTarget(path string) error {
	if err := domain.ValidateEnrichTarget(path); err != nil {
		return err
	}
	t, _ := jsonpath.ParseTarget(path)
	parts := t.Parts()
	if len(parts) > 1 && (parts[0] == "labels" || parts[0] == "extra_data") {
		if parts[1] == "cw_labels" || parts[1] == "dynamic_group_id" {
			return fmt.Errorf("system-derived field cannot be enriched by custom rules")
		}
	}
	return nil
}
