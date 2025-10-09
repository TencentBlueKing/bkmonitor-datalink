// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

type Expr any

type AndExpr struct {
	Left  Expr
	Right Expr
}

type OrExpr struct {
	Left  Expr
	Right Expr
}

type NotExpr struct {
	Expr Expr
}

type GroupingExpr struct {
	Expr  Expr
	Boost float64
}

// OpType defines the operation type for OperatorExpr
type OpType string

const (
	OpMatch    OpType = "match"    // 精确匹配
	OpWildcard OpType = "wildcard" // 通配符匹配
	OpRegex    OpType = "regex"    // 正则表达式匹配
	OpRange    OpType = "range"    // 范围查询
	OpFuzzy    OpType = "fuzzy"    // 模糊匹配
)

// OperatorExpr represents a unified operator expression
type OperatorExpr struct {
	Field     Expr
	Op        OpType
	Value     Expr // 可以是StringExpr、NumberExpr或RangeExpr
	IsQuoted  bool
	Boost     float64
	Fuzziness string
	Slop      int
}

func (o *OperatorExpr) getField() string {
	return getString(o.Field)
}

func (o *OperatorExpr) setField(field string) {
	o.Field = &StringExpr{Value: field}
}

// RangeExpr represents range values used in OperatorExpr
type RangeExpr struct {
	Start        Expr
	End          Expr
	IncludeStart Expr
	IncludeEnd   Expr
}

type ConditionsExpr struct {
	Values [][]Expr
}

type ConditionMatchExpr struct {
	Field Expr
	Value *ConditionsExpr
}

func (cm *ConditionMatchExpr) getField() string {
	return getString(cm.Field)
}

func (cm *ConditionMatchExpr) setField(field string) {
	cm.Field = &StringExpr{Value: field}
}

func (o *OrExpr) setField(field string) {
	if fieldSetter, ok := o.Left.(FieldSetter); ok {
		fieldSetter.setField(field)
	}
	if fieldSetter, ok := o.Right.(FieldSetter); ok {
		fieldSetter.setField(field)
	}
}

func (a *AndExpr) setField(field string) {
	if fieldSetter, ok := a.Left.(FieldSetter); ok {
		fieldSetter.setField(field)
	}
	if fieldSetter, ok := a.Right.(FieldSetter); ok {
		fieldSetter.setField(field)
	}
}

type StringExpr struct {
	Value string
}

type BoolExpr struct {
	Value bool
}

type NumberExpr struct {
	Value float64
}

type FieldGettable interface {
	getField() string
}

func getField(e Expr) string {
	if fg, ok := e.(FieldGettable); ok {
		return fg.getField()
	}
	return Empty
}
