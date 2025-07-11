// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
)

const (
	logicalAnd = "AND"
	logicalOr  = "OR"
)

type Expr interface {
	LoggerEnable() bool
	String() string
	Enter(ctx antlr.ParserRuleContext)
	Exit(ctx antlr.ParserRuleContext)
}

type defaultExpr struct {
}

func (d *defaultExpr) LoggerEnable() bool {
	return false
}

func (d *defaultExpr) Enter(ctx antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) Exit(ctx antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) String() string {
	return ""
}

type SelectExpr struct {
	defaultExpr
	fieldExpr     *FieldExpr
	fieldListExpr []*FieldExpr
}

func (e *SelectExpr) String() string {
	var s []string
	for _, expr := range e.fieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return fmt.Sprintf("SELECT %s", strings.Join(s, ","))
}

func (e *SelectExpr) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.fieldExpr = &FieldExpr{}
	}
}

func (e *SelectExpr) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
		e.fieldExpr.FuncName = v.GetText()
	case *gen.IdentifierOrTextContext:
		e.fieldExpr.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		// 多层重叠的情况下忽略，避免覆盖
		if e.fieldExpr.Name == "" {
			e.fieldExpr.Name = v.GetText()
		}
	case *gen.NamedExpressionContext:
		e.fieldListExpr = append(e.fieldListExpr, e.fieldExpr)
	}

	return
}

type TableExpr struct {
	defaultExpr
	name string
}

func (e *TableExpr) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.TableNameContext:
		e.name = v.GetText()
	}
}

func (e *TableExpr) String() string {
	if e.name == "" {
		return ""
	}

	return fmt.Sprintf("FROM %s", e.name)
}

type LogicalExpr interface {
	String() string
	SetParen()
}

type WhereExpr struct {
	defaultExpr

	condition *ConditionExpr

	isParen bool
	logical LogicalExpr
}

func (e *WhereExpr) String() string {
	if e.logical == nil {
		return ""
	}

	logical := e.logical.String()
	if logical == "" {
		return ""
	}

	return fmt.Sprintf("WHERE %s", logical)
}

func (e *WhereExpr) LoggerEnable() bool {
	return true
}

func (e *WhereExpr) getOp(s string) string {
	// 删除掉值结尾的就是操作符
	// TODO: 找到可以直接获取 op 的方式
	return strings.TrimSuffix(s, e.condition.Value)
}

func (e *WhereExpr) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.PredicatedContext:
		e.condition = &ConditionExpr{
			Field: &FieldExpr{},
		}
	}
}

func (e *WhereExpr) Exit(ctx antlr.ParserRuleContext) {
	if e.condition == nil {
		e.condition = &ConditionExpr{
			Field: &FieldExpr{},
		}
	}

	switch v := ctx.(type) {
	case *gen.PredicatedContext:
		if e.logical == nil {
			e.logical = e.condition
		}
	case *gen.LogicalBinaryContext:
		op := strings.ToUpper(v.GetOperator().GetText())
		switch op {
		case logicalAnd:
			e.logical = &AndExpr{
				Left:  e.logical,
				Right: e.condition,
			}
		case logicalOr:
			e.logical = &OrExpr{
				Left:  e.logical,
				Right: e.condition,
			}
		}
	case *gen.ParenthesizedExpressionContext:
		e.logical.SetParen()
	case *gen.ColumnReferenceContext:
		e.condition.Field.Name = ctx.GetText()
	case *gen.ComparisonOperatorContext:
		e.condition.Op = ctx.GetText()
	case *gen.ConstantDefaultContext:
		e.condition.Value = ctx.GetText()
	case *gen.PredicateContext:
		e.condition.Op = e.getOp(ctx.GetText())
	}
}

type FieldExpr struct {
	defaultExpr
	Name     string
	As       string
	FuncName string
}

func (e *FieldExpr) String() string {
	s := e.Name
	if e.FuncName != "" {
		s = fmt.Sprintf("%s(%s)", e.FuncName, s)
	}
	if e.As != "" {
		s = fmt.Sprintf("%s AS %s", s, e.As)
	}
	return s
}

type ConditionExpr struct {
	IsParen bool
	Field   *FieldExpr
	Op      string
	Value   string
}

func (e *ConditionExpr) String() string {
	if e == nil || e.Field == nil {
		return ""
	}
	s := fmt.Sprintf("%s %s %s", e.Field.String(), e.Op, e.Value)
	if e.IsParen {
		s = fmt.Sprintf("(%s)", s)
	}
	return s
}

func (e *ConditionExpr) SetParen() {
	e.IsParen = true
}

type AndExpr struct {
	IsParen bool
	Left    LogicalExpr
	Right   LogicalExpr
}

func (e *AndExpr) String() string {
	return getLogicalString(logicalAnd, e.Left, e.Right, e.IsParen)
}

func (e *AndExpr) SetParen() {
	e.IsParen = true
}

type OrExpr struct {
	IsParen bool

	Left  LogicalExpr
	Right LogicalExpr
}

func (e *OrExpr) String() string {
	return getLogicalString(logicalOr, e.Left, e.Right, e.IsParen)
}

func getLogicalString(op string, left, right LogicalExpr, IsParen bool) string {
	var s string
	if left != nil && right != nil {
		s = fmt.Sprintf("%s OR %s", left.String(), right.String())
	} else if left != nil {
		s = left.String()
	} else {
		s = right.String()
	}
	if IsParen {
		s = fmt.Sprintf("(%s)", s)
	}
	return s
}

func (e *OrExpr) SetParen() {
	e.IsParen = true
}
