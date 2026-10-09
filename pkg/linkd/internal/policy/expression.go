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
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"linkd/internal/domain"
)

// FieldKind 是 KAC 索引字段语义，不从某次值的 Go 类型猜测 ES mapping。
type FieldKind string

const (
	FieldText    FieldKind = "text"
	FieldKeyword FieldKind = "keyword"
	FieldNumber  FieldKind = "number"
	FieldBoolean FieldKind = "boolean"
	FieldDate    FieldKind = "date"
)

// FieldCatalog 提供已知字段及明确注册的自定义字段，未知字段必须在发布时拒绝。
type FieldCatalog map[string]FieldKind

// KACFields 返回内置字段目录的独立副本。依赖 KAC 处置态/标签目录的字段不在默认目录中。
func KACFields() FieldCatalog {
	result := FieldCatalog{}
	for _, field := range strings.Fields("name content object item bk_set_name bk_module_name bk_cloud_name meta_info strategy strategy_id dimension_info model_id model_inst_id model_name metric_unique_id") {
		result[field] = FieldText
	}
	for _, field := range strings.Fields("source_id source_name level action bk_biz_name bk_set_id bk_module_id bk_obj_id bk_inst_id source_alarm_status bk_tenant_id cw_labels dynamic_group_id entity_uid") {
		result[field] = FieldKeyword
	}
	for _, field := range strings.Fields("bk_biz_id bk_cloud_id bk_service_id strategy_config_version duration") {
		result[field] = FieldNumber
	}
	result["alarm_time"] = FieldDate
	return result
}

// Condition 保留 KAC 条件名。TargetValue 必须出现，0/false 不视为遗漏。
type Condition struct {
	Operator  string          `json:"condition"`
	Field     string          `json:"target_key"`
	Value     json.RawMessage `json:"target_value"`
	Relation  string          `json:"bk_obj_asst_id,omitempty"`
	Code      string          `json:"code,omitempty"`
	IsEmpty   bool            `json:"isEmpty,omitempty"`
	ShowAdd   bool            `json:"show_add,omitempty"`
	IsMonitor bool            `json:"is_monitor,omitempty"`
	// Referenced 仅用于依赖屏蔽子条件，Value 是读取选定主告警字段的字符串模板。
	Referenced bool `json:"is_alarm_field_referenced,omitempty"`
}

// Value 区分字段确实缺失和查询失败；不可评估必须由 Reader 返回错误。
type Value struct {
	Data    any
	Present bool
}

// Reader 是匹配器消费的只读字段和关系端口；关系查询须由调用者约束租户和 canonical 模型。
type Reader interface {
	Field(context.Context, string) (Value, error)
	Related(context.Context, Condition) (bool, error)
}

// Fields 用于无外部依赖的事实匹配；Unavailable 可标识丰富或依赖失败，不能被否定条件误命中。
type Fields struct {
	Values      map[string]any
	Unavailable map[string]bool
}

func (f Fields) Field(ctx context.Context, name string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if f.Unavailable[name] {
		return Value{}, ErrUnavailable
	}
	value, ok := f.Values[name]
	return Value{Data: value, Present: ok}, nil
}

func (Fields) Related(context.Context, Condition) (bool, error) { return false, ErrUnavailable }

// ErrUnavailable 表示本次条件不能可靠求值，调用方应跳过受影响策略并记录诊断。
var ErrUnavailable = errors.New("policy condition unavailable")

// ConditionResult 只携带条件位置与结果，不回显字段值或外部响应。
type ConditionResult struct {
	ID        string `json:"id"`
	Matched   bool   `json:"matched"`
	Evaluated bool   `json:"evaluated"`
	Reason    string `json:"reason,omitempty"`
}

// MatchResult 包含完整逐条件解释；任何必需读取失败时 Evaluated 为 false。
type MatchResult struct {
	Matched    bool              `json:"matched"`
	Evaluated  bool              `json:"evaluated"`
	Conditions []ConditionResult `json:"conditions"`
}

type booleanNode struct {
	id          string
	op          string
	left, right *booleanNode
}

// Expression 是发布时编译的不可变表达式，可跨 goroutine 复用。
type Expression struct {
	root    *booleanNode
	clauses map[string]compiledCondition
	order   []string
}

// CompileExpression 校验 KAC 的条件对象并解析 AND 优先于 OR 的有界表达式。
func CompileExpression(raw json.RawMessage, fields FieldCatalog) (*Expression, error) {
	return compileExpression(raw, fields, false)
}

func compileExpression(raw json.RawMessage, fields FieldCatalog, references bool) (*Expression, error) {
	if len(raw) == 0 || len(raw) > 65536 {
		return nil, fmt.Errorf("policy: expression object must be 1..65536 bytes")
	}
	if _, err := (domain.JSONObject{"policy": raw}).Normalize(); err != nil {
		return nil, fmt.Errorf("policy: invalid or duplicate JSON field")
	}
	var object domain.JSONObject
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("policy: expected expression object")
	}
	// Normalize 同时检查嵌套重复 JSON key，防止发布和执行读取不同值。
	normalized, err := object.Normalize()
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	var expression string
	if err := json.Unmarshal(normalized["expression"], &expression); err != nil || expression == "" || len(expression) > 4096 {
		return nil, fmt.Errorf("policy.expression: required string up to 4096 bytes")
	}
	delete(normalized, "expression")
	if len(normalized) == 0 || len(normalized) > 64 {
		return nil, fmt.Errorf("policy: requires 1..64 conditions")
	}
	result := &Expression{clauses: map[string]compiledCondition{}}
	for id, value := range normalized {
		if !validVariable(id) {
			return nil, fmt.Errorf("policy: invalid condition identifier")
		}
		var condition Condition
		if err := strictDecode(value, &condition); err != nil {
			return nil, fmt.Errorf("policy.%s: %w", id, err)
		}
		var clause compiledCondition
		if condition.Referenced {
			if !references {
				return nil, fmt.Errorf("policy.%s: field references require dependency rely_policy", id)
			}
			clause, err = compileReference(condition, fields)
		} else {
			clause, err = compileCondition(condition, fields)
		}
		if err != nil {
			return nil, fmt.Errorf("policy.%s: %w", id, err)
		}
		result.clauses[id] = clause
		result.order = append(result.order, id)
	}
	sort.Strings(result.order)
	parser := expressionParser{text: expression, clauses: result.clauses, used: map[string]bool{}}
	result.root, err = parser.or(0)
	if err != nil {
		return nil, fmt.Errorf("policy.expression: %w", err)
	}
	parser.space()
	if parser.offset != len(expression) || len(parser.used) != len(result.clauses) {
		return nil, fmt.Errorf("policy.expression: unexpected token or unused condition")
	}
	return result, nil
}

// Match 全量评估条件以保留调试证据；外部错误不伪装为 false，也不能经 must_not 放大范围。
func (e *Expression) Match(ctx context.Context, reader Reader) (MatchResult, error) {
	return e.match(ctx, reader, nil)
}

func (e *Expression) match(ctx context.Context, reader Reader, origin Reader) (MatchResult, error) {
	result := MatchResult{Evaluated: true, Conditions: make([]ConditionResult, 0, len(e.order))}
	values := map[string]bool{}
	var failure bool
	for _, id := range e.order {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		clause, err := e.clauses[id].resolveReference(ctx, origin)
		var matched bool
		if err == nil {
			matched, err = clause.match(ctx, reader)
		}
		// 明确的租户/授权错误不得降级为普通不可求值，避免策略跳过掩盖错误作用域。
		if errors.Is(err, ErrAccess) {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		row := ConditionResult{ID: id, Matched: matched, Evaluated: err == nil}
		if err != nil {
			failure = true
			row.Reason = "condition_unavailable"
		}
		result.Conditions = append(result.Conditions, row)
		values[id] = matched
	}
	if failure {
		result.Evaluated = false
		return result, ErrUnavailable
	}
	result.Matched = e.root.evaluate(values)
	return result, nil
}

func (n *booleanNode) evaluate(values map[string]bool) bool {
	switch n.op {
	case "and":
		return n.left.evaluate(values) && n.right.evaluate(values)
	case "or":
		return n.left.evaluate(values) || n.right.evaluate(values)
	default:
		return values[n.id]
	}
}

func validVariable(s string) bool {
	if len(s) == 0 || len(s) > 32 || s == "AND" || s == "OR" {
		return false
	}
	for i, r := range s {
		if (r < 'A' || r > 'Z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

type expressionParser struct {
	text          string
	offset, nodes int
	clauses       map[string]compiledCondition
	used          map[string]bool
}

func (p *expressionParser) space() {
	for p.offset < len(p.text) && unicode.IsSpace(rune(p.text[p.offset])) {
		p.offset++
	}
}

func (p *expressionParser) word(w string) bool {
	p.space()
	if len(p.text)-p.offset < len(w) || !strings.EqualFold(p.text[p.offset:p.offset+len(w)], w) {
		return false
	}
	end := p.offset + len(w)
	if end < len(p.text) && ((p.text[end] >= 'A' && p.text[end] <= 'Z') || (p.text[end] >= 'a' && p.text[end] <= 'z') || (p.text[end] >= '0' && p.text[end] <= '9')) {
		return false
	}
	p.offset = end
	return true
}

func (p *expressionParser) or(depth int) (*booleanNode, error) {
	node, err := p.and(depth)
	if err != nil {
		return nil, err
	}
	for p.word("or") {
		right, err := p.and(depth)
		if err != nil {
			return nil, err
		}
		node = &booleanNode{op: "or", left: node, right: right}
	}
	return node, nil
}

func (p *expressionParser) and(depth int) (*booleanNode, error) {
	node, err := p.atom(depth)
	if err != nil {
		return nil, err
	}
	for p.word("and") {
		right, err := p.atom(depth)
		if err != nil {
			return nil, err
		}
		node = &booleanNode{op: "and", left: node, right: right}
	}
	return node, nil
}

func (p *expressionParser) atom(depth int) (*booleanNode, error) {
	p.nodes++
	if depth > 32 || p.nodes > 256 {
		return nil, fmt.Errorf("expression budget exceeded")
	}
	p.space()
	if p.offset >= len(p.text) {
		return nil, fmt.Errorf("expected condition")
	}
	if p.text[p.offset] == '(' {
		p.offset++
		node, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		p.space()
		if p.offset >= len(p.text) || p.text[p.offset] != ')' {
			return nil, fmt.Errorf("unbalanced parentheses")
		}
		p.offset++
		return node, nil
	}
	start := p.offset
	for p.offset < len(p.text) {
		c := p.text[p.offset]
		if c < 'A' || c > 'Z' {
			if c < '0' || c > '9' {
				break
			}
		}
		p.offset++
	}
	id := p.text[start:p.offset]
	if _, ok := p.clauses[id]; !ok {
		return nil, fmt.Errorf("unknown condition at byte %d", start)
	}
	p.used[id] = true
	return &booleanNode{id: id}, nil
}
