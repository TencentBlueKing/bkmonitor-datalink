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

type Expr interface {
	Log() bool
	String() string
	Enter(ctx antlr.ParserRuleContext)
	Exit(ctx antlr.ParserRuleContext)
	WithDimensionEncode(func(s string) string) Expr
}

type defaultExpr struct {
}

func (d *defaultExpr) Log() bool {
	return false
}

func (d *defaultExpr) Enter(_ antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) Exit(_ antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) String() string {
	return ""
}

func (d *defaultExpr) WithDimensionEncode(func(s string) string) Expr {
	return d
}

type Select struct {
	defaultExpr
	Field         *Field
	FieldListExpr []*Field
	encode        func(s string) string
}

func NewSelect() *Select {
	return &Select{}
}

func (e *Select) WithDimensionEncode(fn func(s string) string) Expr {
	e.encode = fn
	return e
}

func (e *Select) String() string {
	var s []string
	for _, expr := range e.FieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return fmt.Sprintf("SELECT %s", strings.Join(s, ", "))
}

func (e *Select) Enter(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.NamedExpressionContext:
		e.Field = NewField(e.encode)
	default:
		sf := e.Field
		if sf != nil {
			switch ctx.(type) {
			case *gen.CastContext:
				sf.SelectFunc = NewSelectFunc()
				sf.SetFuncName("CAST")
			case *gen.CastDataTypeContext:
				if sf.SelectFunc != nil {
					sf.SelectFunc.As = ctx.GetText()
				}
			case *gen.FunctionCallContext:
				sf.SelectFunc = NewSelectFunc()
			case *gen.FunctionNameIdentifierContext:
				sf.SetFuncName(ctx.GetText())
			case *gen.ColumnReferenceContext:
				sf.Name = ctx.GetText()
			case *gen.StarContext:
				sf.Name = "*"
			case *gen.ConstantDefaultContext:
				if sf.SelectFunc != nil {
					sf.SelectFunc.Arg = append(sf.SelectFunc.Arg, v.GetText())
				}
			case *gen.IdentifierOrTextContext:
				sf.As = v.GetText()
			}
		}
	}
}

func (e *Select) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
		e.Field = NewField(e.encode)
	default:
		sf := e.Field
		if sf != nil {
			switch ctx.(type) {
			case *gen.FunctionCallContext, *gen.CastContext:
				if sf.SelectFunc != nil {
					sf.SelectFunc.Name = sf.GetFuncName()
					sf.SelectFuncs = append(sf.SelectFuncs, sf.SelectFunc)
					sf.SelectFunc = NewSelectFunc()
				}
			}
		}
	}

	return
}

type Table struct {
	defaultExpr
	name string
}

func NewTable() *Table {
	return &Table{}
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

type logicListInc struct {
	list []*logicInc
}

type logicInc struct {
	name string
	inc  int
}

func (l *logicListInc) Append(name string) {
	if l.list == nil {
		l.list = make([]*logicInc, 0)
	}
	l.list = append(l.list, &logicInc{
		name: name,
	})
}

func (l *logicListInc) Name() (name string) {
	if len(l.list) == 0 {
		return name
	}

	last := l.list[len(l.list)-1]
	if last.inc == 0 {
		name = last.name
		l.list = l.list[:len(l.list)-1]
	}

	return name
}

func (l *logicListInc) Inc(e Expr) {
	if e == nil || len(l.list) == 0 {
		return
	}

	switch v := e.(type) {
	case *Paren:
		if v.Left {
			l.list[len(l.list)-1].inc++
		} else if v.Right {
			l.list[len(l.list)-1].inc--
		}
	}
}

type Where struct {
	defaultExpr

	encode func(s string) string
	cur    *Condition

	logic *logicListInc

	conditions []Expr
}

func NewWhere() *Where {
	return &Where{
		cur: &Condition{
			Field: NewField(nil),
		},
		logic: &logicListInc{},
	}
}

func (e *Where) WithDimensionEncode(fn func(s string) string) Expr {
	e.encode = fn
	if e.cur != nil && e.cur.Field != nil {
		e.cur.Field.WithDimensionEncode(fn)
	}
	return e
}

func (e *Where) addCondition(condition Expr) {
	e.conditions = append(e.conditions, condition)
}

func (e *Where) String() string {
	var list []string
	for _, c := range e.conditions {
		switch c.(type) {
		case *Logical:
			e.logic.Append(c.String())
		default:
			e.logic.Inc(c)
			item := c.String()
			if item != "" {
				list = append(list, item)
			}

			logicName := e.logic.Name()
			if logicName != "" {
				list = append(list, logicName)
			}
		}
	}

	if len(list) == 0 {
		return ""
	}

	return fmt.Sprintf("WHERE %s", strings.Join(list, " "))
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
	case *gen.ComparisonContext:
		e.cur = &Condition{
			Field: NewField(e.encode),
		}
	default:
		if e.cur != nil && e.cur.Field != nil {
			cur := e.cur
			sf := cur.Field
			switch ctx.(type) {
			case *gen.ColumnReferenceContext:
				sf.Name = ctx.GetText()
			case *gen.IdentifierOrTextContext:
				sf.As = v.GetText()
			case *gen.CastContext:
				sf.SelectFunc = NewSelectFunc()
				sf.SelectFunc.Name = "CAST"
			case *gen.CastDataTypeContext:
				if sf.SelectFunc != nil {
					sf.SelectFunc.As = ctx.GetText()
				}
			case *gen.ConstantDefaultContext:
				if sf.SelectFunc != nil {
					sf.SelectFunc.Arg = append(sf.SelectFunc.Arg, v.GetText())
				}
			case *gen.ComparisonOperatorContext, *gen.PredicateContext:
				cur.Op = ctx.GetText()
			case *gen.ValueExpressionDefaultContext:
				// 只有拿到 op 的 value 才是值
				if cur.Op != "" {
					cur.Value = ctx.GetText()
				}
			}
		}
	}
}

func (e *Where) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		e.addCondition(&Paren{
			Right: true,
		})
	case *gen.ComparisonContext:
		if e.cur != nil {
			e.addCondition(e.cur)
			e.cur = &Condition{
				Field: NewField(e.encode),
			}
		}
		// 兼容特殊查询操作符，op 操作符需要修改
	case *gen.PredicateContext:
		if e.cur != nil {
			e.cur.Op = e.getOp(ctx.GetText())
			e.addCondition(e.cur)
			e.cur = &Condition{
				Field: NewField(e.encode),
			}
		}
	default:
		cur := e.cur
		if cur != nil {
			sf := cur.Field
			switch ctx.(type) {
			case *gen.CastContext:
				sf.SelectFuncs = append(sf.SelectFuncs, sf.SelectFunc)
				sf.SelectFunc = NewSelectFunc()
			case *gen.FunctionCallContext:
				sf.SelectFuncs = append(sf.SelectFuncs, sf.SelectFunc)
				sf.SelectFunc = NewSelectFunc()
			}
		}
	}
}

type Agg struct {
	defaultExpr

	Field         *Field
	FieldListExpr []*Field

	encode func(s string) string
}

func NewAgg() *Agg {
	return &Agg{}
}

func (e *Agg) WithDimensionEncode(fn func(s string) string) Expr {
	e.encode = fn
	return e
}

func (e *Agg) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ExpressionContext:
		e.Field = NewField(e.encode)
	}
}

func (e *Agg) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.IdentifierOrTextContext:
		e.Field.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		// 多层重叠的情况下忽略，避免覆盖
		if e.Field.Name == "" {
			e.Field.Name = v.GetText()
		}
	case *gen.ExpressionContext:
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
		e.Field = NewField(e.encode)
	}
}

func (e *Agg) String() string {
	var s []string
	for _, expr := range e.FieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return fmt.Sprintf("GROUP BY %s", strings.Join(s, ", "))
}

type Sort struct {
	defaultExpr

	Field         *Field
	FieldListExpr []*Field

	encode func(s string) string
}

func NewSort() *Sort {
	return &Sort{}
}

func (e *Sort) WithDimensionEncode(fn func(s string) string) Expr {
	e.encode = fn
	return e
}

func (e *Sort) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ExpressionContext:
		e.Field = NewField(e.encode)
	}
}

func (e *Sort) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.IdentifierOrTextContext:
		e.Field.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		// 多层重叠的情况下忽略，避免覆盖
		if e.Field.Name == "" {
			e.Field.Name = v.GetText()
		}
	case *gen.ExpressionContext:
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
		e.Field = NewField(e.encode)
	}
}

func (e *Sort) String() string {
	var s []string
	for _, expr := range e.FieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return fmt.Sprintf("ORDER BY %s", strings.Join(s, ", "))
}

type Limit struct {
	defaultExpr

	offset string
	limit  string
}

func NewLimit() *Limit {
	return &Limit{}
}

func (e *Limit) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.LimitClauseContext:
		if v.GetLimit() != nil {
			e.limit = v.GetLimit().GetText()
		}
		if v.GetOffset() != nil {
			e.offset = v.GetOffset().GetText()
		}
	}
}

func (e *Limit) String() string {
	var s []string
	if e.limit != "" {
		s = append(s, fmt.Sprintf("LIMIT %s", e.limit))
	}
	if e.offset != "" {
		s = append(s, fmt.Sprintf("OFFSET %s", e.offset))
	}

	return strings.Join(s, " ")
}

type SelectFunc struct {
	Name string
	Arg  []string
	As   string
}

func NewSelectFunc() *SelectFunc {
	return &SelectFunc{
		Arg: make([]string, 0),
	}
}

type Field struct {
	defaultExpr
	Name string
	As   string

	encode func(s string) string

	FuncsName []string

	SelectFunc  *SelectFunc
	SelectFuncs []*SelectFunc
}

func NewField(fn func(s string) string) *Field {
	return (&Field{
		SelectFunc: NewSelectFunc(),
	}).WithDimensionEncode(fn)
}

func (e *Field) WithDimensionEncode(fn func(s string) string) *Field {
	e.encode = fn
	return e
}

func (e *Field) SetFuncName(name string) {
	e.FuncsName = append(e.FuncsName, name)
}

func (e *Field) GetFuncName() (name string) {
	if len(e.FuncsName) == 0 {
		return name
	}

	lastNum := len(e.FuncsName) - 1
	name = e.FuncsName[lastNum]
	e.FuncsName = e.FuncsName[0:lastNum]
	return name
}

func (e *Field) String() string {
	var s string
	if e.encode != nil {
		s = e.encode(e.Name)
	} else {
		s = e.Name
	}
	for _, sf := range e.SelectFuncs {
		var fieldName string
		if sf.Name == "CAST" {
			fieldName = s
			if len(sf.Arg) > 0 {
				fieldName = fmt.Sprintf("%s[%s]", fieldName, strings.Join(sf.Arg, "]["))
			}
			fieldName = fmt.Sprintf("%s AS %s", fieldName, sf.As)
		} else {
			fieldName = strings.Join(append([]string{s}, sf.Arg...), ", ")
		}
		s = fmt.Sprintf("%s(%s)", sf.Name, fieldName)
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
