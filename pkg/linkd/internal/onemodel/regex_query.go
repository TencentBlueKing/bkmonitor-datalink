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
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"time"

	"linkd/internal/textregexp"
)

func hasTextRegex(f Filter) bool {
	if f.Operator == "regex" || f.Operator == "not_regex" {
		return true
	}
	if f.Not != nil && hasTextRegex(*f.Not) {
		return true
	}
	for _, children := range [][]Filter{f.All, f.Any} {
		for _, child := range children {
			if hasTextRegex(child) {
				return true
			}
		}
	}
	return false
}

// regexCandidates 只下推完整条件的必要条件；含正则的 NOT 和无约束 OR 保留为全候选。
func regexCandidates(f Filter) Filter {
	if !hasTextRegex(f) {
		return f
	}
	if f.Not != nil || f.Field != "" {
		return Filter{}
	}
	children := f.All
	if len(f.Any) > 0 {
		children = f.Any
	}
	var out []Filter
	for _, child := range children {
		candidate := regexCandidates(child)
		if candidate.Empty() {
			if len(f.Any) > 0 {
				return Filter{}
			}
			continue
		}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return Filter{}
	}
	if len(out) == 1 {
		return out[0]
	}
	if len(f.Any) > 0 {
		return Filter{Any: out}
	}
	return Filter{All: out}
}

// searchTextRegex 复用现有 PIT/keyset 分页，在统一正则语义下判断完整条件。
// 仍受 SDK 的 1024 实例读取预算约束，耗尽时返回超限而非假装无匹配。
func (c *Client) searchTextRegex(ctx context.Context, tenant string, q Query) ([]Instance, error) {
	patterns := map[string]*textregexp.Expression{}
	var compile func(Filter) error
	compile = func(f Filter) error {
		if f.Operator == "regex" || f.Operator == "not_regex" {
			pattern, ok := f.Value.(string)
			if !ok {
				return fmt.Errorf("regex requires string")
			}
			compiled, err := textregexp.Compile(pattern)
			if err != nil {
				return err
			}
			patterns[pattern] = compiled
		}
		if f.Not != nil {
			return compile(*f.Not)
		}
		for _, children := range [][]Filter{f.All, f.Any} {
			for _, child := range children {
				if err := compile(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := compile(q.Where); err != nil {
		return nil, err
	}
	pager, err := NewPager(c)
	if err != nil {
		return nil, err
	}
	cursor := ""
	defer func() {
		if cursor != "" {
			_ = pager.Close(ctx, tenant, cursor)
		}
	}()
	var out []Instance
	read := 0
	// 页大小参与游标签名，必须保持固定；128 整除既有的 1024 候选预算。
	for {
		page, err := pager.Search(ctx, tenant, PageQuery{ModelID: q.ModelID, Where: regexCandidates(q.Where), Limit: 128, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		cursor = page.NextCursor
		for _, instance := range page.Instances {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			matched, err := matchInstanceFilter(q.Where, instance, patterns)
			if err != nil {
				return nil, err
			}
			if matched {
				out = append(out, instance)
				if q.First {
					return out, nil
				}
				if len(out) > q.Limit {
					return nil, ErrResultLimit
				}
			}
		}
		read += len(page.Instances)
		if cursor == "" {
			return out, nil
		}
		if read >= 1024 {
			return nil, fmt.Errorf("%w: regex candidate budget exceeded", ErrResultLimit)
		}
	}
}

func matchInstanceFilter(f Filter, instance Instance, patterns map[string]*textregexp.Expression) (bool, error) {
	if f.Empty() {
		return true, nil
	}
	if f.Not != nil {
		match, err := matchInstanceFilter(*f.Not, instance, patterns)
		return !match, err
	}
	if len(f.All) > 0 || len(f.Any) > 0 {
		children, all := f.All, len(f.All) > 0
		if !all {
			children = f.Any
		}
		for _, child := range children {
			match, err := matchInstanceFilter(child, instance, patterns)
			if err != nil {
				return false, err
			}
			if match != all {
				return match, nil
			}
		}
		return all, nil
	}
	value, exists := instance.Document()[f.Field]
	if strings.HasPrefix(f.Field, "attributes.") {
		value, exists = instance.Attributes[strings.TrimPrefix(f.Field, "attributes.")]
	}
	if f.Operator == "exists" {
		if exists {
			for _, item := range filterValues(value) {
				if item != nil {
					return true, nil
				}
			}
		}
		return false, nil
	}
	op := f.Operator
	negated := false
	switch op {
	case "ne":
		op, negated = "eq", true
	case "not_in":
		op, negated = "in", true
	case "not_contains":
		op, negated = "contains", true
	case "not_regex":
		op, negated = "regex", true
	}
	values := filterValues(value)
	wanted := []any{f.Value}
	if op == "in" {
		wanted = filterValues(f.Value)
	}
	matched := false
	for _, actual := range values {
		if actual == nil {
			continue
		}
		for _, expected := range wanted {
			var match bool
			var err error
			switch op {
			case "regex":
				text, ok := actual.(string)
				if ok {
					match, err = patterns[expected.(string)].MatchString(text)
				}
			case "contains":
				text, ok := actual.(string)
				match = ok && strings.Contains(text, expected.(string))
			default:
				var comparison int
				comparison, err = compareFilterValues(f.Type, actual, expected)
				if err == nil {
					switch op {
					case "eq", "in":
						match = comparison == 0
					case "gt":
						match = comparison > 0
					case "gte":
						match = comparison >= 0
					case "lt":
						match = comparison < 0
					case "lte":
						match = comparison <= 0
					}
				}
			}
			if err != nil {
				return false, err
			}
			matched = matched || match
		}
	}
	if negated {
		matched = !matched
	}
	return matched, nil
}

// compareFilterValues 保留声明类型的比较语义；日期按时刻、IP 按地址比较，
// 不能因同一查询含有正则而退化为原始字符串的字典序。
func compareFilterValues(kind InstanceAttributeType, actual, expected any) (int, error) {
	a, err := typedFilterValue(kind, actual)
	if err != nil {
		return 0, err
	}
	b, err := typedFilterValue(kind, expected)
	if err != nil {
		return 0, err
	}
	switch kind {
	case InstanceAttributeLong, InstanceAttributeDouble:
		left, leftOK := new(big.Rat).SetString(fmt.Sprint(a))
		right, rightOK := new(big.Rat).SetString(fmt.Sprint(b))
		if !leftOK || !rightOK {
			return 0, fmt.Errorf("invalid numeric filter value")
		}
		return left.Cmp(right), nil
	case InstanceAttributeDatetime:
		left, err := filterTime(a.(string))
		if err != nil {
			return 0, err
		}
		right, err := filterTime(b.(string))
		if err != nil {
			return 0, err
		}
		return left.Compare(right), nil
	case InstanceAttributeIP:
		left, err := netip.ParseAddr(a.(string))
		if err != nil {
			return 0, fmt.Errorf("invalid IP filter value")
		}
		right, err := netip.ParseAddr(b.(string))
		if err != nil {
			return 0, fmt.Errorf("invalid IP filter value")
		}
		// IPv4 使用映射后的 16 字节表示，与等价的 IPv6 映射地址一致。
		l, r := left.As16(), right.As16()
		return bytes.Compare(l[:], r[:]), nil
	default:
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)), nil
	}
}

func filterTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid datetime filter value")
}

func filterValues(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	// SDK 的调用者也可传 []string/[]int64，统一保留 JSON 数值精度。
	if raw, err := json.Marshal(value); err == nil && len(raw) > 0 && raw[0] == '[' {
		var values []any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&values) == nil {
			return values
		}
	}
	return []any{value}
}
