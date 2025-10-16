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

	WithAliasEncode(Encode) Expr
}

type defaultExpr struct{}

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

func (d *defaultExpr) WithAliasEncode(_ Encode) Expr {
	return d
}

type Select struct {
	defaultExpr
	Field         *Field
	FieldListExpr []*Field

	setAs  bool
	encode Encode
}

func NewSelect() *Select {
	return &Select{}
}

func (e *Select) WithAliasEncode(fn Encode) Expr {
	e.setAs = true
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

	return strings.Join(s, ", ")
}

func (e *Select) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.Field = NewField().WithAliasEncode(e.encode).WithSetAs(true)
	default:
		if e.Field != nil {
			e.Field.Enter(ctx)
		}
	}
}

func (e *Select) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
		e.Field = NewField().WithAliasEncode(e.encode).WithSetAs(true)
	default:
		if e.Field != nil {
			e.Field.Exit(ctx)
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

	return e.name
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

	switch e.(type) {
	case *LeftParen:
		l.list[len(l.list)-1].inc++
	case *RightParen:
		l.list[len(l.list)-1].inc--
	}
}

type Where struct {
	defaultExpr

	encode Encode
	cur    *Condition

	logic *logicListInc

	conditions []Expr
}

func NewWhere() *Where {
	return &Where{
		cur: &Condition{
			Field: NewField(),
		},
		logic: &logicListInc{},
	}
}

func (e *Where) WithAliasEncode(fn Encode) Expr {
	e.encode = fn
	if e.cur != nil && e.cur.Field != nil {
		e.cur.Field.WithAliasEncode(fn)
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

	return strings.Join(list, " ")
}

func (e *Where) getOp(s string) string {
	// 删除掉值结尾的就是操作符
	// TODO: 找到可以直接获取 op 的方式
	return strings.TrimSuffix(s, e.cur.GetValue())
}

func (e *Where) Enter(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		e.addCondition(NewLeftParen())
	case *gen.LogicalBinaryContext:
		e.addCondition(&Logical{
			Name: strings.ToUpper(v.GetOperator().GetText()),
		})
	case *gen.ComparisonContext:
		e.cur = &Condition{
			Field: NewField().WithAliasEncode(e.encode),
		}
	default:
		if e.cur != nil && e.cur.Field != nil {
			cur := e.cur
			switch ctx.(type) {
			case *gen.ComparisonOperatorContext, *gen.PredicateContext:
				cur.Op = ctx.GetText()
			case *gen.ValueExpressionDefaultContext:
				// 只有拿到 op 的 value 才是值
				if cur.Op != "" {
					cur.Values = append(cur.Values, ctx.GetText())
				}
			default:
				cur.Field.Enter(ctx)
			}
		}
	}
}

func (e *Where) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		e.addCondition(NewRightParen())
	case *gen.ComparisonContext:
		if e.cur != nil {
			e.addCondition(e.cur)
			e.cur = &Condition{
				Field: NewField().WithAliasEncode(e.encode),
			}
		}
		// 兼容特殊查询操作符，op 操作符需要修改
	case *gen.PredicateContext:
		if e.cur != nil {
			e.cur.Op = e.getOp(ctx.GetText())
			e.addCondition(e.cur)
			e.cur = &Condition{
				Field: NewField().WithAliasEncode(e.encode),
			}
		}
	default:
		if e.cur != nil && e.cur.Field != nil {
			e.cur.Field.Exit(ctx)
		}
	}
}

type Agg struct {
	defaultExpr

	Field         *Field
	FieldListExpr []*Field

	encode Encode
}

func NewAgg() *Agg {
	return &Agg{}
}

func (e *Agg) WithAliasEncode(fn Encode) Expr {
	e.encode = fn
	return e
}

func (e *Agg) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ExpressionContext:
		e.Field = NewField().WithAliasEncode(e.encode)
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
		e.Field = NewField().WithAliasEncode(e.encode)
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

	return strings.Join(s, ", ")
}

type Sort struct {
	defaultExpr

	Field         *Field
	FieldListExpr []*Field

	encode Encode
}

func NewSort() *Sort {
	return &Sort{}
}

func (e *Sort) WithAliasEncode(fn Encode) Expr {
	e.encode = fn
	return e
}

func (e *Sort) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.ExpressionContext:
		e.Field = NewField().WithAliasEncode(e.encode)
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
	case *gen.SortItemContext:
		e.Field.Sort = strings.ToUpper(strings.TrimPrefix(v.GetText(), e.Field.Name))
		e.FieldListExpr = append(e.FieldListExpr, e.Field)
		e.Field = NewField().WithAliasEncode(e.encode)
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

	return strings.Join(s, ", ")
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
	Name       string
	ExtraNames []string

	Sort string

	As string

	encode Encode
	setAs  bool

	FuncsName []string

	SelectFunc  *SelectFunc
	SelectFuncs []*SelectFunc
}

func NewField() *Field {
	return &Field{
		SelectFunc: NewSelectFunc(),
	}
}

func (e *Field) WithAliasEncode(fn Encode) *Field {
	e.encode = fn
	return e
}

func (e *Field) WithSetAs(setAs bool) *Field {
	e.setAs = setAs
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

func (e *Field) Enter(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.CastContext:
		e.SelectFunc = NewSelectFunc()
		e.SetFuncName("CAST")
	case *gen.CastDataTypeContext:
		if e.SelectFunc != nil {
			e.SelectFunc.As = v.GetText()
		}
	case *gen.FunctionCallContext:
		e.SelectFunc = NewSelectFunc()
	case *gen.FunctionNameIdentifierContext:
		e.SetFuncName(ctx.GetText())
	case *gen.ColumnReferenceContext:
		e.Name = v.GetText()
	case *gen.StarContext:
		e.Name = "*"
	case *gen.IdentifierContext:
		if e.As == "" && e.Name != "" {
			aliasName := v.GetText()
			if aliasName != e.Name {
				e.ExtraNames = append(e.ExtraNames, aliasName)
			}
		}
	case *gen.ConstantDefaultContext:
		if e.SelectFunc != nil {
			e.SelectFunc.Arg = append(e.SelectFunc.Arg, v.GetText())
		}
	case *gen.IdentifierOrTextContext:
		e.As = v.GetText()
	}
}

func (e *Field) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.FunctionCallContext, *gen.CastContext:
		e.SelectFunc.Name = e.GetFuncName()
		e.SelectFuncs = append(e.SelectFuncs, e.SelectFunc)
		e.SelectFunc = NewSelectFunc()
	}
}

func (e *Field) String() string {
	var (
		originName string
		as         string
	)
	aliasName := strings.Join(append([]string{e.Name}, e.ExtraNames...), ".")
	if e.encode != nil {
		originName, as = e.encode(aliasName)
		if e.setAs && as != "" && e.As == "" {
			e.As = as
		}
	} else {
		originName = aliasName
	}

	for _, sf := range e.SelectFuncs {
		var fieldName string
		if sf.Name == "CAST" {
			fieldName = originName
			if len(sf.Arg) > 0 {
				fieldName = fmt.Sprintf("%s[%s]", fieldName, strings.Join(sf.Arg, "]["))
			}
			fieldName = fmt.Sprintf("%s AS %s", fieldName, sf.As)
		} else {
			fieldName = strings.Join(append([]string{originName}, sf.Arg...), ", ")
		}
		originName = fmt.Sprintf("%s(%s)", sf.Name, fieldName)
	}

	if e.As != "" {
		originName = fmt.Sprintf("%s AS %s", originName, e.As)
	}
	if e.Sort != "" {
		originName = fmt.Sprintf("%s %s", originName, e.Sort)
	}
	return originName
}

type Condition struct {
	defaultExpr
	Field  *Field
	Op     string
	Values []string
}

func (e *Condition) GetValue() string {
	var val string
	if len(e.Values) > 0 {
		if len(e.Values) > 1 {
			val = fmt.Sprintf("(%s)", strings.Join(e.Values, ","))
		} else {
			val = e.Values[0]
		}
	}

	return val
}

func (e *Condition) String() string {
	if e == nil || e.Field == nil {
		return ""
	}

	s := fmt.Sprintf("%s %s %s", e.Field.String(), e.Op, e.GetValue())
	return s
}

type Logical struct {
	defaultExpr
	Name string
}

func (e *Logical) String() string {
	return e.Name
}

type LeftParen struct {
	defaultExpr
}

func NewLeftParen() *LeftParen {
	return &LeftParen{}
}

func (e *LeftParen) String() string {
	return "("
}

type RightParen struct {
	defaultExpr
}

func NewRightParen() *RightParen {
	return &RightParen{}
}

func (e *RightParen) String() string {
	return ")"
}

type As struct {
	defaultExpr
	name string
}

func NewAs(s string) *As {
	return &As{
		name: s,
	}
}

func (e *As) String() string {
	return fmt.Sprintf("AS %s", e.name)
}

type Str struct {
	defaultExpr
	name string
}

func NewStr(s string) *Str {
	return &Str{
		name: s,
	}
}

func (e *Str) String() string {
	return e.name
}

type SF struct {
	defaultExpr
	name  Expr
	value Expr
	as    Expr
}

func NewSF() *SF {
	return &SF{}
}

func (e *SF) String() string {
	s := fmt.Sprintf("%s(%s)", e.name.String(), e.value.String())
	if e.as != nil {
		s += fmt.Sprintf(" AS %s", e.as.String())
	}
	return s
}

func (e *SF) SetName(exp Expr) *SF {
	e.name = exp
	return e
}

func (e *SF) SetValue(exp Expr) *SF {
	e.value = exp
	return e
}

func (e *SF) SetAs(exp Expr) *SF {
	e.as = exp
	return e
}

type Binary struct {
	defaultExpr
	Left  Expr
	Right Expr
	Op    string
}

func NewBinary(left, right Expr, op string) *Binary {
	return &Binary{
		Left:  left,
		Right: right,
		Op:    op,
	}
}

func (e *Binary) String() string {
	return fmt.Sprintf("%s %s %s", e.Left.String(), e.Op, e.Right.String())
}

type Item struct {
	defaultExpr
	items []Expr
}

func NewItem() *Item {
	return &Item{}
}

func (e *Item) String() string {
	var s strings.Builder
	for _, item := range e.items {
		if item == nil {
			continue
		}
		if s.Len() > 0 {
			s.WriteString(", ")
		}
		s.WriteString(item.String())
	}
	return s.String()
}

func (e *Item) add(exp Expr) {
	if exp == nil {
		return
	}
	if e.items == nil {
		e.items = make([]Expr, 0)
	}
	e.items = append(e.items, exp)
}

func (e *Item) getSF() (*SF, bool) {
	if len(e.items) == 0 {
		e.items = append(e.items, NewSF())
	}
	v, ok := e.items[0].(*SF)
	return v, ok
}

func (e *Item) setFuncName(exp Expr) {
	if sf, ok := e.getSF(); ok {
		sf.SetName(exp)
	}
}

func (e *Item) setFuncValue(exp Expr) {
	if sf, ok := e.getSF(); ok {
		sf.SetValue(exp)
	}
}

func (e *Item) setAs(exp Expr) {
	if sf, ok := e.getSF(); ok {
		sf.SetAs(exp)
	}
}

func (e *Item) Enter(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
		e.items = append(e.items, NewSF())
		if sf, ok := e.getSF(); ok {
			sf.SetName(NewStr(v.GetText()))
		}
	case *gen.CastContext:
		if sf, ok := e.getSF(); ok {
			sf.SetName(NewStr("CAST"))
		}
	case *gen.StarContext:
		if sf, ok := e.getSF(); ok {
			sf.SetValue(NewStr("*"))
		}
	}
}

func (e *Item) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
	case *gen.CastDataTypeContext:
		if sf, ok := e.getSF(); ok {
			sf.SetAs(NewStr(v.GetText()))
		}
	case *gen.CastContext:
	case *gen.FunctionCallContext:
	case *gen.NamedExpressionContext:
	}
}

type SelectV2 struct {
	defaultExpr
	Item *Item

	Items []*Item
}

func (e *SelectV2) String() string {
	var s []string
	for _, expr := range e.Items {
		s = append(s, expr.String())
	}

	if len(s) == 0 {
		return ""
	}

	return strings.Join(s, ", ")
}

func (e *SelectV2) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.Item = NewItem()
	default:
		if e.Item != nil {
			e.Item.Enter(ctx)
		}
	}
}

func (e *SelectV2) Exit(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.Items = append(e.Items, e.Item)
	default:
		if e.Item != nil {
			e.Item.Exit(ctx)
		}
	}

	return
}
