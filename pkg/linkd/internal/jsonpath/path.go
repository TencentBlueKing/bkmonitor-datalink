// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package jsonpath 为丰富提供有界 RFC 9535 读取和确定路径写入，不执行用户代码。
package jsonpath

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	jp "github.com/theory/jsonpath"
	"github.com/theory/jsonpath/spec"
)

// Query 是发布阶段编译、调用之间只读共享的 JSONPath。
type Query struct{ path *jp.Path }

// Compile 编译路径并限制递归及过滤组合，避免本地求值脱离请求预算。
func Compile(expression string) (*Query, error) {
	if len(expression) == 0 || len(expression) > 2048 {
		return nil, fmt.Errorf("JSONPath length must be 1..2048")
	}
	// RFC 完整选择器仍可使用；限制组合复杂度，不启动无法回收的后台求值 goroutine。
	if strings.Count(expression, "..") > 1 || strings.Count(expression, "?") > 4 || strings.Count(expression, "[") > 64 {
		return nil, fmt.Errorf("JSONPath complexity limit exceeded")
	}
	p, err := jp.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid JSONPath: %w", err)
	}
	for _, segment := range p.Query().Segments() {
		if len(segment.Selectors()) > 8 {
			return nil, fmt.Errorf("JSONPath selector fanout limit exceeded")
		}
		for _, selector := range segment.Selectors() {
			if filter, ok := selector.(*spec.FilterSelector); ok {
				if err := validateFilterCost(filter.String()); err != nil {
					return nil, err
				}
			}
		}
	}
	return &Query{path: p}, nil
}

// Select 返回保留类型的节点；缺失返回空列表，显式 null 返回包含 nil 的列表。
func (q *Query) Select(ctx context.Context, input any) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateTree(input); err != nil {
		return nil, err
	}
	nodes := []any{input}
	work := 0
	for _, segment := range q.path.Query().Segments() {
		next := []any{}
		for _, node := range nodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			selected := segment.Select(node, input)
			work += len(selected) + 1
			if work > 65536 || len(next)+len(selected) > 16384 {
				return nil, fmt.Errorf("JSONPath evaluation budget exceeded")
			}
			next = append(next, selected...)
		}
		nodes = next
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(nodes) > 1024 {
		return nil, fmt.Errorf("JSONPath selected too many nodes")
	}
	return nodes, nil
}

// Target 是仅包含属性名和非负下标的确定 JSONPath。
type Target struct{ parts []any }

// ParseTarget 拒绝过滤、多选和递归写入，字段名中的点通过 bracket notation 保留。
func ParseTarget(path string) (Target, error) {
	q, err := Compile(path)
	if err != nil {
		return Target{}, err
	}
	parts := []any{}
	for _, segment := range q.path.Query().Segments() {
		if segment.IsDescendant() || len(segment.Selectors()) != 1 {
			return Target{}, fmt.Errorf("target must be a singular path")
		}
		switch selector := segment.Selectors()[0].(type) {
		case spec.Name:
			parts = append(parts, string(selector))
		case spec.Index:
			if selector < 0 {
				return Target{}, fmt.Errorf("negative target index")
			}
			parts = append(parts, int(selector))
		default:
			return Target{}, fmt.Errorf("target only supports names and nonnegative indexes")
		}
	}
	if len(parts) == 0 {
		return Target{}, fmt.Errorf("target must not replace root")
	}
	return Target{parts: parts}, nil
}

// Parts 返回路径的隔离副本，用于字段权限检查。
func (t Target) Parts() []any { return append([]any(nil), t.parts...) }

// String 返回唯一的 bracket 路径表示，用于检测冲突及补丁审计。
func (t Target) String() string {
	var b strings.Builder
	b.WriteByte('$')
	for _, part := range t.parts {
		switch x := part.(type) {
		case string:
			s, _ := json.Marshal(x)
			b.WriteByte('[')
			b.Write(s)
			b.WriteByte(']')
		case int:
			fmt.Fprintf(&b, "[%d]", x)
		}
	}
	return b.String()
}

// Overlaps 检查两个目标是否相同或具有祖先关系。
func (t Target) Overlaps(other Target) bool {
	n := min(len(t.parts), len(other.parts))
	for i := range n {
		if t.parts[i] != other.parts[i] {
			return false
		}
	}
	return true
}

// Get 读取确定路径，缺失与 null 分开返回。
func (t Target) Get(input any) (any, bool) {
	v := input
	for _, p := range t.parts {
		switch x := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok = m[x]
			if !ok {
				return nil, false
			}
		case int:
			a, ok := v.([]any)
			if !ok || x >= len(a) {
				return nil, false
			}
			v = a[x]
		}
	}
	return v, true
}

// Set 只创建缺失对象，不创建数组或改变已有父节点的类型；调用方负责在隔离副本上原子执行。
func (t Target) Set(input map[string]any, value any) error {
	var parent any = input
	for i, p := range t.parts {
		last := i == len(t.parts)-1
		switch x := p.(type) {
		case string:
			m, ok := parent.(map[string]any)
			if !ok {
				return fmt.Errorf("target parent is not an object")
			}
			if last {
				m[x] = value
				return nil
			}
			next, exists := m[x]
			if !exists {
				if _, array := t.parts[i+1].(int); array {
					return fmt.Errorf("target array must already exist")
				}
				next = map[string]any{}
				m[x] = next
			}
			parent = next
		case int:
			a, ok := parent.([]any)
			if !ok || x >= len(a) {
				return fmt.Errorf("target array index does not exist")
			}
			if last {
				a[x] = value
				return nil
			}
			parent = a[x]
		}
	}
	return nil
}

// ValidateTree 限制单次处理树的深度、节点数和编码体积。
func ValidateTree(v any) error {
	nodes := 0
	var visit func(any, int) error
	visit = func(v any, depth int) error {
		nodes++
		if depth > 64 || nodes > 16384 {
			return fmt.Errorf("JSON tree exceeds depth or node budget")
		}
		switch x := v.(type) {
		case map[string]any:
			for _, item := range x {
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(v, 0); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("JSON exceeds 1 MiB")
	}
	return nil
}

// Clone 深拷贝 JSON 值，保留原始数值表示。
func Clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		r := make(map[string]any, len(x))
		for k, item := range x {
			r[k] = Clone(item)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, item := range x {
			r[i] = Clone(item)
		}
		return r
	default:
		return v
	}
}

// validateFilterCost 只允许过滤表达式内部读取确定路径。外层路径仍支持通配、切片和递归；
// 这样每个候选节点的谓词成本只与表达式长度有关，不能在谓词内部再次放大集合。
func validateFilterCost(expression string) error {
	quote := rune(0)
	escaped := false
	brackets := 0
	for i, ch := range expression {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		switch ch {
		case '[':
			brackets++
		case ']':
			brackets--
		case '*', ':':
			return fmt.Errorf("JSONPath filter queries must be singular")
		case '?':
			if i != 0 {
				return fmt.Errorf("nested JSONPath filters exceed budget")
			}
		case ',':
			if brackets > 0 {
				return fmt.Errorf("JSONPath filter unions exceed budget")
			}
		case '.':
			if i+1 < len(expression) && expression[i+1] == '.' {
				return fmt.Errorf("JSONPath filter recursion exceeds budget")
			}
		}
	}
	return nil
}
