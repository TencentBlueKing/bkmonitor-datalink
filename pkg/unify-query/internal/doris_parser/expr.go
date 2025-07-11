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

type Select struct {
	defaultExpr
	Field         *Field
	FieldListExpr []*Field
}

func (e *Select) String() string {
	var s []string
	for _, expr := range e.FieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return fmt.Sprintf("SELECT %s", strings.Join(s, ","))
}

func (e *Select) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.Field = &Field{}
	}
}

func (e *Select) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
		e.Field.FuncName = v.GetText()
	case *gen.IdentifierOrTextContext:
		e.Field.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		// 多层重叠的情况下忽略，避免覆盖
		if e.Field.Name == "" {
			e.Field.Name = v.GetText()
		}
	case *gen.NamedExpressionContext:
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
	}

	return
}

type Table struct {
	defaultExpr
	name string
}

func (e *Table) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.TableNameContext:
		e.name = v.GetText()
	}
}

func (e *Table) String() string {
	if e.name == "" {
		return ""
	}

	return fmt.Sprintf("FROM %s", e.name)
}

type Where struct {
	defaultExpr

	cur *Condition

	conditions []Expr
}

func (e *Where) addCondition(condition Expr) {
	if condition == nil {
		return
	}
	e.conditions = append(e.conditions, condition)
}

func (e *Where) String() string {
	var str []string
	for _, c := range e.conditions {
		str = append(str, c.String())
	}

	if len(str) == 0 {
		return ""
	}

	return fmt.Sprintf("WHERE %s", strings.Join(str, " "))
}

func (e *Where) LoggerEnable() bool {
	return true
}

func (e *Where) getOp(s string) string {
	// 删除掉值结尾的就是操作符
	// TODO: 找到可以直接获取 op 的方式
	return strings.TrimSuffix(s, e.cur.Value)
}

func (e *Where) Enter(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		e.addCondition(&Paren{
			Left: true,
		})
	case *gen.LogicalBinaryContext:
		e.addCondition(&Logical{
			Name: strings.ToUpper(v.GetOperator().GetText()),
		})
	case *gen.PredicatedContext:
		e.cur = &Condition{
			Field: &Field{},
		}
	case *gen.ColumnReferenceContext:
		e.cur.Field.Name = ctx.GetText()
	case *gen.ComparisonOperatorContext:
		e.cur.Op = ctx.GetText()
	case *gen.ConstantDefaultContext:
		e.cur.Value = ctx.GetText()
	case *gen.PredicateContext:
		e.cur.Op = e.getOp(ctx.GetText())
	}
}

func (e *Where) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		e.addCondition(&Paren{
			Right: true,
		})
	case *gen.PredicatedContext:
		e.addCondition(e.cur)
		e.cur = nil
	}
}

type Field struct {
	defaultExpr
	Name     string
	As       string
	FuncName string
}

func (e *Field) String() string {
	s := e.Name
	if e.FuncName != "" {
		s = fmt.Sprintf("%s(%s)", e.FuncName, s)
	}
	if e.As != "" {
		s = fmt.Sprintf("%s AS %s", s, e.As)
	}
	return s
}

type Condition struct {
	defaultExpr
	Field *Field
	Op    string
	Value string
}

func (e *Condition) String() string {
	if e == nil || e.Field == nil {
		return ""
	}
	s := fmt.Sprintf("%s %s %s", e.Field.String(), e.Op, e.Value)
	return s
}

type Logical struct {
	defaultExpr
	Name string
}

func (e *Logical) String() string {
	return e.Name
}

type Paren struct {
	defaultExpr
	Left  bool
	Right bool
}

func (e *Paren) String() string {
	if e.Left {
		return "("
	} else {
		return ")"
	}
}
