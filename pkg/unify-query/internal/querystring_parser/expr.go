// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring_parser

import (
	"fmt"
	"strings"
	"time"
)

// Expr .
type Expr interface {
}

// AndExpr .
type AndExpr struct {
	Left  Expr
	Right Expr
}

// NewAndExpr .
func NewAndExpr(left, right Expr) *AndExpr {
	return &AndExpr{Left: left, Right: right}
}

// OrExpr .
type OrExpr struct {
	Left  Expr
	Right Expr
}

// NewOrExpr .
func NewOrExpr(left, right Expr) *OrExpr {
	return &OrExpr{Left: left, Right: right}
}

// NotExpr .
type NotExpr struct {
	Expr Expr
}

// NewNotExpr .
func NewNotExpr(q Expr) *NotExpr {
	return &NotExpr{Expr: q}
}

// FieldableExpr .
type FieldableExpr interface {
	SetField(field string)
}

// MatchExpr .
type MatchExpr struct {
	Field string
	Value string
}

// ConditionMatchExpr .
type ConditionMatchExpr struct {
	Field string
	Value *ConditionExpr
}

// ConditionExpr .
type ConditionExpr struct {
	Values [][]string
}

// NewConditionExpr .
func NewConditionExpr(a interface{}) *ConditionExpr {
	switch v := a.(type) {
	case *ConditionExpr:
		return v
	case *MatchExpr:
		return &ConditionExpr{Values: [][]string{{v.Value}}}
	case string:
		return &ConditionExpr{Values: [][]string{{v}}}
	default:
		panic(fmt.Sprintf("unsupported type for NewConditionExpr: %T", a))
	}
}

// OrConditionExpr .
func OrConditionExpr(a, b Expr) *ConditionExpr {
	return &ConditionExpr{Values: append(a.(*ConditionExpr).Values, b.(*ConditionExpr).Values...)}
}

// AndConditionExpr .
func AndConditionExpr(a, b Expr) *ConditionExpr {
	var result [][]string
	for _, rowA := range a.(*ConditionExpr).Values {
		for _, rowB := range b.(*ConditionExpr).Values {
			merged := append(append([]string{}, rowA...), rowB...)
			result = append(result, merged)
		}
	}
	return &ConditionExpr{Values: result}
}

// NewConditionMatchExpr .
func NewConditionMatchExpr(value Expr) *ConditionMatchExpr {
	v, ok := value.(*ConditionExpr)
	if !ok {
		panic("value is not *ConditionExpr")
	}
	return &ConditionMatchExpr{
		Value: v,
	}
}

// SetField .
func (q *ConditionMatchExpr) SetField(expr Expr) {
	switch e := expr.(type) {
	case string:
		q.Field = e
	case *MatchExpr:
		q.Field = e.Value
	case *WildcardExpr:
		q.Field = e.Value
	default:
		panic(fmt.Sprintf("unsupported type for ConditionMatchExpr field : %T", e))
	}
}

// NewMatchExpr .
func NewMatchExpr(s string) *MatchExpr {
	return &MatchExpr{
		Value: s,
	}
}

// SetField .
func (q *MatchExpr) SetField(field string) {
	q.Field = field
}

// RegexpExpr .
type RegexpExpr struct {
	Field string
	Value string
}

// NewRegexpExpr .
func NewRegexpExpr(s string) *RegexpExpr {
	return &RegexpExpr{
		Value: s,
	}
}

// SetField .
func (q *RegexpExpr) SetField(field string) {
	q.Field = field
}

// WildcardExpr .
type WildcardExpr struct {
	Field string
	Value string
}

// NewWildcardExpr .
func NewWildcardExpr(s string) *WildcardExpr {
	return &WildcardExpr{
		Value: s,
	}
}

// SetField .
func (q *WildcardExpr) SetField(field string) {
	q.Field = field
}

// NumberRangeExpr .
type NumberRangeExpr struct {
	Field        string
	Start        *string
	End          *string
	IncludeStart bool
	IncludeEnd   bool
}

// NewNumberRangeExpr .
func NewNumberRangeExpr(start, end *string, includeStart, includeEnd bool) *NumberRangeExpr {
	return &NumberRangeExpr{
		Start:        start,
		End:          end,
		IncludeStart: includeStart,
		IncludeEnd:   includeEnd,
	}
}

// SetField .
func (q *NumberRangeExpr) SetField(field string) {
	q.Field = field
}

// TimeRangeExpr .
type TimeRangeExpr struct {
	Field        string
	Start        *string
	End          *string
	IncludeStart bool
	IncludeEnd   bool
}

// NewTimeRangeExpr .
func NewTimeRangeExpr(start, end *string, includeStart, includeEnd bool) *TimeRangeExpr {
	return &TimeRangeExpr{
		Start:        start,
		End:          end,
		IncludeStart: includeStart,
		IncludeEnd:   includeEnd,
	}
}

// SetField .
func (q *TimeRangeExpr) SetField(field string) {
	q.Field = field
}

func queryTimeFromString(t string) (time.Time, error) {
	return time.Parse(time.RFC3339, t)
}

func newStringExpr(str string) FieldableExpr {
	aliasStr := strings.ReplaceAll(str, `\*`, "")
	aliasStr = strings.ReplaceAll(aliasStr, `\?`, "")

	if strings.ContainsAny(aliasStr, "*?") {
		return NewWildcardExpr(str)
	}

	return NewMatchExpr(str)
}

type TermExpr struct {
	Field string
	Value string
}
