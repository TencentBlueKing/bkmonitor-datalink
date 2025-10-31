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
	"context"
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	SelectItem = "SELECT"
	TableItem  = "FROM"
	WhereItem  = "WHERE"
	OrderItem  = "ORDER BY"
	GroupItem  = "GROUP BY"
	LimitItem  = "LIMIT"
	OffsetItem = "OFFSET"

	AsItem = "AS"

	defaultLimit = "100"
)

type Encode func(string) (string, string)

type Node interface {
	antlr.ParseTreeVisitor
	String() string
	Error() error

	WithEncode(Encode)
	WithSetAs(bool)
	WithAlias(map[string]string)
}

type baseNode struct {
	antlr.BaseParseTreeVisitor

	Encode Encode
	SetAs  bool
	alias  map[string]string
}

func (n *baseNode) String() string {
	return ""
}

func (n *baseNode) Error() error {
	return nil
}

func (n *baseNode) WithEncode(encode Encode) {
	n.Encode = encode
}

func (n *baseNode) WithSetAs(setAs bool) {
	n.SetAs = setAs
}

func (n *baseNode) WithAlias(aliases map[string]string) {
	n.alias = aliases
}

type Statement struct {
	baseNode

	isSubQuery bool

	nodeMap map[string]Node

	Tables  []string
	Where   string
	Offset  int
	Limit   int
	errNode []string
}

func (v *Statement) ItemString(name string) string {
	if n, ok := v.nodeMap[name]; ok {
		return nodeToString(n)
	}

	return ""
}

func (v *Statement) String() string {
	var result []string

	for _, name := range []string{SelectItem, TableItem, WhereItem, GroupItem, OrderItem, LimitItem} {
		res := v.ItemString(name)
		key := name

		switch name {
		case TableItem:
			if len(v.Tables) > 0 {
				if len(v.Tables) == 1 {
					res = v.Tables[0]
				} else {
					stmts := make([]string, 0, len(v.Tables))
					for _, t := range v.Tables {
						s := fmt.Sprintf("SELECT * FROM %s", t)
						if v.Where != "" {
							s = fmt.Sprintf("%s WHERE %s", s, v.Where)
						}
						stmts = append(stmts, s)
					}
					res = fmt.Sprintf("(%s) AS combined_data", strings.Join(stmts, " UNION ALL "))
					v.Where = ""
				}
			}
		case WhereItem:
			// 清空 where 条件
			if len(v.Tables) > 1 {
				res = ""
			}

			if v.Where != "" {
				if res == "" {
					res = v.Where
				} else {
					res = fmt.Sprintf("%s AND %s", res, v.Where)
				}
			}
		case LimitItem:
			key = ""
		}

		if res != "" {
			if key != "" {
				res = fmt.Sprintf("%s %s", key, res)
			}
			result = append(result, res)
		}
	}

	sql := strings.Join(result, " ")
	if v.isSubQuery {
		sql = fmt.Sprintf("(%s)", sql)
	}

	return sql
}

func (v *Statement) Error() error {
	if len(v.errNode) > 0 {
		return fmt.Errorf("%s", strings.Join(v.errNode, " "))
	}
	return nil
}

func (v *Statement) VisitErrorNode(ctx antlr.ErrorNode) any {
	v.errNode = append(v.errNode, ctx.GetText())
	return nil
}

func (v *Statement) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	if v.nodeMap == nil {
		v.nodeMap = map[string]Node{
			LimitItem: &LimitNode{
				ParentLimit:  v.Limit,
				ParentOffset: v.Offset,
			},
		}
	}

	var isSetAs bool
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		v.nodeMap[SelectItem] = &SelectNode{}
		isSetAs = true
		next = v.nodeMap[SelectItem]
	case *gen.FromClauseContext:
		v.nodeMap[TableItem] = &TableNode{}
		next = v.nodeMap[TableItem]
	case *gen.WhereClauseContext:
		v.nodeMap[WhereItem] = &WhereNode{
			LogicInc: &LogicNodesInc{},
		}
		next = v.nodeMap[WhereItem]
	case *gen.AggClauseContext:
		v.nodeMap[GroupItem] = &AggNode{}
		next = v.nodeMap[GroupItem]
	case *gen.SortClauseContext:
		v.nodeMap[OrderItem] = &SortNode{}
		next = v.nodeMap[OrderItem]
	case *gen.LimitClauseContext:
		v.nodeMap[LimitItem] = &LimitNode{}
		next = v.nodeMap[LimitItem]
	}

	return visitChildren(v.Encode, isSetAs, next, ctx)
}

type LimitNode struct {
	baseNode

	prefix string

	limit  int
	offset int

	ParentLimit  int
	ParentOffset int
}

func (v *LimitNode) getOffsetAndLimit() (string, string) {
	offset := v.offset + v.ParentOffset

	// 如果外层的 OFFSET 已经超出了内层的 LIMIT，则需要设置 LIMIT 为 0.代表没有数据
	if v.limit > 0 && offset >= v.limit {
		return "", "0"
	}

	limit := v.limit
	if v.ParentLimit > 0 {
		if v.limit <= 0 || v.limit > v.ParentLimit {
			limit = v.ParentLimit
		}
	}

	var resultOffset, resultLimit string
	if offset > 0 {
		resultOffset = cast.ToString(offset)
	}

	if limit > 0 {
		resultLimit = cast.ToString(limit)
	} else {
		resultLimit = defaultLimit
	}

	// 只有制定了 offset 的逻辑的才需要进行切割
	if v.ParentOffset > 0 && v.limit > 0 && (limit+offset) > v.limit {
		left := v.limit % limit
		if left != 0 {
			resultLimit = cast.ToString(left)
		}
	}

	return resultOffset, resultLimit
}

func (v *LimitNode) String() string {
	var s []string

	offset, limit := v.getOffsetAndLimit()
	if limit != "" {
		s = append(s, fmt.Sprintf("%s %s", LimitItem, limit))
	}
	if offset != "" {
		s = append(s, fmt.Sprintf("%s %s", OffsetItem, offset))
	}

	return strings.Join(s, " ")
}

func (v *LimitNode) VisitTerminal(ctx antlr.TerminalNode) any {
	result := strings.ToUpper(ctx.GetText())
	switch result {
	case LimitItem, OffsetItem:
		v.prefix = result
	case ",":
		v.offset = v.limit
	default:
		if v.prefix == LimitItem {
			v.limit = cast.ToInt(result)
		} else if v.prefix == OffsetItem {
			v.offset = cast.ToInt(result)
		}
	}

	return nil
}

type SortNode struct {
	nodes []Node

	baseNode
}

func (v *SortNode) String() string {
	var ns []string
	for _, fn := range v.nodes {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *SortNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SortItemContext:
		fn := &OrderNode{}
		next = fn
		v.nodes = append(v.nodes, fn)
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type OrderNode struct {
	node Node
	sort Node

	baseNode
}

func (v *OrderNode) String() string {
	var ns []string
	result := nodeToString(v.node)
	if result != "" {
		ns = append(ns, result)
	}
	sort := nodeToString(v.sort)
	if sort != "" {
		ns = append(ns, sort)
	}

	return strings.Join(ns, " ")
}

func (v *OrderNode) VisitTerminal(ctx antlr.TerminalNode) any {
	result := strings.ToUpper(ctx.GetText())
	v.sort = &StringNode{
		Name: result,
	}
	return nil
}

func (v *OrderNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		v.node = &FieldNode{}
		next = v.node
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type AggNode struct {
	fieldsNode []Node

	baseNode
}

func (v *AggNode) String() string {
	var ns []string
	for _, fn := range v.fieldsNode {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *AggNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		fn := &FieldNode{}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type WhereNode struct {
	baseNode

	nodes []Node

	LogicInc *LogicNodesInc

	err error
}

func (v *WhereNode) add(node Node) {
	v.nodes = append(v.nodes, node)
}

func (v *WhereNode) Error() error {
	return v.err
}

func (v *WhereNode) String() string {
	var list []string
	for _, n := range v.nodes {
		switch n.(type) {
		case *LogicNode:
			v.LogicInc.Append(nodeToString(n))
		default:
			v.LogicInc.Inc(n)
			item := nodeToString(n)
			if item != "" {
				list = append(list, item)
			}

			logicName := v.LogicInc.Name()
			if logicName != "" {
				list = append(list, logicName)
			}
		}
	}

	return strings.Join(list, " ")
}

func (v *WhereNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch n := ctx.(type) {
	case *gen.LogicalBinaryContext:
		v.add(&LogicNode{
			Op: &StringNode{
				Name: strings.ToUpper(n.GetOperator().GetText()),
			},
		})
	case *gen.ParenthesizedExpressionContext:
		v.add(&LeftParenNode{})
		defer func() {
			v.add(&RightParenNode{})
		}()
	case *gen.PredicatedContext:
		// 忽略带有括号的
		s := ctx.GetText()
		if s[0] == '(' && s[len(s)-1] == ')' {
			break
		}

		on := &OperatorNode{}
		v.add(on)
		next = on
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type LeftParenNode struct {
	baseNode
}

func (v *LeftParenNode) String() string {
	return fmt.Sprintf("(")
}

type RightParenNode struct {
	baseNode
}

func (v *RightParenNode) String() string {
	return fmt.Sprintf(")")
}

type ParentNode struct {
	baseNode

	node Node
}

func (v *ParentNode) String() string {
	return fmt.Sprintf("(%s)", nodeToString(v.node))
}

func (v *ParentNode) VisitChildren(ctx antlr.RuleNode) any {
	v.node = &ConditionNode{}
	next := v.node
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type LogicNode struct {
	baseNode

	Op Node
}

func (v *LogicNode) String() string {
	return nodeToString(v.Op)
}

type LogicNodesInc struct {
	list []*LogicNodeInc
}

type LogicNodeInc struct {
	name string
	inc  int
}

func (l *LogicNodesInc) Append(name string) {
	if l.list == nil {
		l.list = make([]*LogicNodeInc, 0)
	}
	l.list = append(l.list, &LogicNodeInc{
		name: name,
	})
}

func (l *LogicNodesInc) Name() (name string) {
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

func (l *LogicNodesInc) Inc(e Node) {
	if e == nil || len(l.list) == 0 {
		return
	}

	switch e.(type) {
	case *LeftParenNode:
		l.list[len(l.list)-1].inc++
	case *RightParenNode:
		l.list[len(l.list)-1].inc--
	}
}

type ConditionNode struct {
	baseNode

	node Node
}

func (v *ConditionNode) String() string {
	return nodeToString(v.node)
}

func (v *ConditionNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.PredicatedContext:
		v.node = &OperatorNode{}
		next = v.node
	case *gen.LogicalBinaryContext:
		v.node = &LogicNode{
			Op: &StringNode{
				Name: ctx.GetText(),
			},
		}
		next = v.node
	}

	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type OperatorNode struct {
	baseNode

	Left  Node
	Right Node
	Op    Node
}

func (v *OperatorNode) String() string {
	left := nodeToString(v.Left)
	op := nodeToString(v.Op)
	right := nodeToString(v.Right)

	result := fmt.Sprintf("%s %s %s", left, op, right)
	return result
}

func (v *OperatorNode) VisitTerminal(node antlr.TerminalNode) any {
	banTokens := []string{"(", ")", ","}
	token := node.GetText()

	for _, bt := range banTokens {
		if token == bt {
			return nil
		}
	}

	if v.Op == nil {
		v.Op = &StringsNode{}
	}
	v.Op.(*StringsNode).add(node.GetText())

	return nil
}

func (v *OperatorNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ValueExpressionDefaultContext:
		if v.Left == nil {
			v.Left = &FieldNode{}
			next = v.Left
		} else if v.Right == nil {
			v.Right = &ValueNode{}
			next = v.Right
		} else {
			next = v.Right
		}
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type TableNode struct {
	baseNode
	Table Node
}

func (v *TableNode) String() string {
	if v.Table == nil {
		return ""
	}

	table := v.Table.String()
	return table
}

func (v *TableNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.TableNameContext:
		v.Table = &StringNode{Name: ctx.GetText()}
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type SelectNode struct {
	baseNode

	DistinctIndex int
	Distinct      bool
	fieldsNode    []Node

	fieldsAlias map[string]string

	informalField string
	informalAlias string
}

func (v *SelectNode) VisitTerminal(ctx antlr.TerminalNode) any {
	name := ctx.GetText()
	switch name {
	case "DISTINCT":
		v.Distinct = true
		v.DistinctIndex = len(v.fieldsNode)
	}
	return nil
}

func (v *SelectNode) String() string {
	var ns []string
	for idx, fn := range v.fieldsNode {
		ss := nodeToString(fn)
		if ss != "" {
			if v.Distinct && idx == v.DistinctIndex {
				// 如果字段包含AS别名，则不添加外层括号
				if strings.Contains(ss, " AS ") {
					ss = fmt.Sprintf("DISTINCT %s", ss)
				} else {
					ss = fmt.Sprintf("DISTINCT(%s)", ss)
				}
			}
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *SelectNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		fn := &FieldNode{}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}

	return visitChildren(v.Encode, v.SetAs, next, ctx)
}

type FieldNode struct {
	baseNode

	isField bool

	node Node
	as   Node

	sort Node

	args          []Node
	informalAlias string
	informalField string
}

func (v *FieldNode) String() string {
	var result string
	result = nodeToString(v.node)

	if v.isField && v.Encode != nil {

		originField, as := v.Encode(result)
		if v.SetAs && as != "" && v.as == nil {
			if as == "null" {
				as = result
			}
			v.as = &StringNode{Name: as}
		}
		if originField == "null" {
			if _, ok := v.alias[result]; ok {
				originField = fmt.Sprintf("`%s`", result)
			}
		}

		result = originField
	}

	var cols []string
	for _, val := range v.args {
		col := nodeToString(val)
		if col != "" {
			cols = append(cols, col)
		}
	}
	if len(cols) > 0 {
		result = fmt.Sprintf("%s[%s]", result, strings.Join(cols, "]["))
	}

	as := nodeToString(v.as)
	if as != "" {
		result = fmt.Sprintf("%s %s %s", result, AsItem, as)
	}

	sort := nodeToString(v.sort)
	if sort != "" {
		result = fmt.Sprintf("%s %s", result, sort)
	}

	return result
}

func (v *FieldNode) VisitChildren(ctx antlr.RuleNode) any {
	next := visitFieldNode(ctx, v)
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type BinaryNode struct {
	baseNode
	Left  Node
	Right Node
	Op    Node
}

func (v *BinaryNode) String() string {
	return fmt.Sprintf("%s %s %s", nodeToString(v.Left), nodeToString(v.Op), nodeToString(v.Right))
}

func (v *BinaryNode) VisitTerminal(node antlr.TerminalNode) any {
	v.Op = &StringNode{
		Name: node.GetText(),
	}
	return nil
}

func (v *BinaryNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ArithmeticBinaryContext:
		if v.Op == nil {
			v.Left = &BinaryNode{}
			next = v.Left
		} else {
			v.Right = &BinaryNode{}
			next = v.Right
		}
	case *gen.ValueExpressionDefaultContext:
		if v.Op == nil {
			v.Left = &FieldNode{}
			next = v.Left
		} else {
			v.Right = &FieldNode{}
			next = v.Right
		}
	// 兼容类型识别异常情况
	case *antlr.BaseParserRuleContext:
		if v.Op == nil {
			v.Left = &StringNode{Name: ctx.GetText()}
		} else {
			v.Right = &StringNode{Name: ctx.GetText()}
		}
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type FunctionNode struct {
	baseNode

	Distinct bool
	FuncName string
	Values   []Node
}

func (v *FunctionNode) String() string {
	var result string

	var cols []string
	for _, val := range v.Values {
		col := nodeToString(val)
		if col != "" {
			cols = append(cols, col)
		}
	}

	result = strings.Join(cols, ", ")

	if v.Distinct {
		result = fmt.Sprintf("DISTINCT(%s)", result)
	}

	if v.FuncName != "" {
		result = fmt.Sprintf("%s(%s)", v.FuncName, result)
	}
	return result
}

func (v *FunctionNode) VisitTerminal(ctx antlr.TerminalNode) any {
	name := ctx.GetText()
	switch name {
	case "DISTINCT":
		v.Distinct = true
	}
	return nil
}

func (v *FunctionNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SearchedCaseContext:
		sn := &SearchCaseNode{}
		v.Values = append(v.Values, sn)
		next = sn
	case *gen.ArithmeticBinaryContext:
		bn := &BinaryNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.CastContext:
		bn := &CastNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.FunctionCallContext:
		bn := &FunctionNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.FunctionIdentifierContext:
		v.FuncName = ctx.GetText()
	case *gen.ColumnReferenceContext:
		col := ctx.GetText()
		if v.Encode != nil {
			col, _ = v.Encode(col)
		}
		v.Values = append(v.Values, &StringNode{Name: col})
	case *gen.ConstantDefaultContext:
		v.Values = append(v.Values, &StringNode{Name: ctx.GetText()})
	case *gen.StarContext:
		v.Values = append(v.Values, &StringNode{Name: ctx.GetText()})
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type SearchCaseNode struct {
	baseNode

	ops   []string
	nodes []Node
}

func (v *SearchCaseNode) String() string {
	s := strings.Builder{}
	if len(v.nodes) > 0 && len(v.ops) > len(v.nodes) {
		s.WriteString("CASE")
		for idx, n := range v.nodes {
			op := v.ops[idx+1]

			when := nodeToString(n)
			if when != "" {
				s.WriteString(fmt.Sprintf(" %s %s", op, nodeToString(n)))
			}
		}

		s.WriteString(" END")
	}

	return s.String()
}

func (v *SearchCaseNode) VisitTerminal(ctx antlr.TerminalNode) any {
	v.ops = append(v.ops, strings.ToUpper(ctx.GetText()))
	return nil
}

func (v *SearchCaseNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ArithmeticBinaryContext:
		bn := &BinaryNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.CastContext:
		bn := &CastNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.FunctionCallContext:
		bn := &FunctionNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.ColumnReferenceContext:
		col := ctx.GetText()
		if v.Encode != nil {
			col, _ = v.Encode(col)
		}
		cn := &StringNode{Name: col}
		v.nodes = append(v.nodes, cn)
		next = cn
	case *gen.ConstantDefaultContext, *gen.StarContext:
		sn := &StringNode{Name: ctx.GetText()}
		v.nodes = append(v.nodes, sn)
		next = sn
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type CastNode struct {
	baseNode
	Value Node
	As    Node
}

func (v *CastNode) String() string {
	var result string
	result = nodeToString(v.Value)

	as := nodeToString(v.As)
	if as != "" {
		result = fmt.Sprintf("CAST(%s %s %s)", result, AsItem, as)
	}
	return result
}

func (v *CastNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.CastDataTypeContext:
		v.As = &StringNode{
			Name: ctx.GetText(),
		}
		next = v.As
	case *gen.FunctionCallContext:
		v.Value = &FunctionNode{}
		next = v.Value
	case *gen.ColumnReferenceContext:
		v.Value = &ColumnNode{
			Names: []Node{
				&StringNode{Name: ctx.GetText()},
			},
		}
	case *gen.ConstantDefaultContext:
		if v.Value != nil {
			switch n := v.Value.(type) {
			case *ColumnNode:
				n.Sep = "]["
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			case *FunctionNode:
				n.Values = append(n.Values, &StringNode{Name: ctx.GetText()})
			}
		} else {
			v.Value = &StringNode{
				Name: ctx.GetText(),
			}
		}
	case *gen.StarContext:
		v.Value = &StringNode{Name: ctx.GetText()}
		next = v.Value
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type ColumnNode struct {
	baseNode

	Sep   string
	Names []Node
}

func (v *ColumnNode) String() string {
	var ns []string
	for _, name := range v.Names {
		s := nodeToString(name)
		if s != "" {
			ns = append(ns, s)
		}
	}
	if len(ns) == 0 {
		return ""
	}

	if v.Sep == "." {
		return strings.Join(ns, v.Sep)
	}

	s := ns[0]
	if len(ns) > 1 {
		s = fmt.Sprintf("%s[%s]", s, strings.Join(ns[1:], v.Sep))
	}
	return s
}

func (v *ColumnNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.alias, v.Encode, v.SetAs, v, ctx)
}

type ValueNode struct {
	baseNode

	nodes []Node
}

func (v *ValueNode) String() string {
	var names []string
	for _, n := range v.nodes {
		names = append(names, n.String())
	}
	if len(names) == 1 {
		return names[0]
	}

	return fmt.Sprintf("(%s)", strings.Join(names, ", "))
}

func (v *ValueNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.FunctionCallContext:
		node := &FunctionNode{}
		v.nodes = append(v.nodes, node)
		next = node
	case *gen.ConstantDefaultContext:
		v.nodes = append(v.nodes, &StringNode{Name: ctx.GetText()})
	}
	return visitChildren(v.alias, v.Encode, v.SetAs, next, ctx)
}

type StringsNode struct {
	baseNode
	Names []string
}

func (v *StringsNode) add(s string) {
	v.Names = append(v.Names, s)
}

func (v *StringsNode) String() string {
	return strings.Join(v.Names, " ")
}

func (v *StringsNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.alias, v.Encode, v.SetAs, v, ctx)
}

type StringNode struct {
	baseNode
	Name string
}

func (v *StringNode) String() string {
	return v.Name
}

func (v *StringNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.alias, v.Encode, v.SetAs, v, ctx)
}

func visitFieldNode(ctx antlr.RuleNode, node *FieldNode) Node {
	var next Node
	next = node

	switch ctx.(type) {
	case *gen.SubqueryExpressionContext:
		node.node = &Statement{
			isSubQuery: true,
		}
		next = node.node
	case *gen.SearchedCaseContext:
		node.node = &SearchCaseNode{}
		next = node.node
	case *gen.ArithmeticBinaryContext:
		node.node = &BinaryNode{}
		next = node.node
	case *gen.CastContext:
		node.node = &CastNode{}
		next = node.node
	case *gen.FunctionCallContext:
		node.node = &FunctionNode{}
		next = node.node
	case *gen.ColumnReferenceContext:
		node.node = &ColumnNode{}
		node.isField = true
		node.informalField = ctx.GetText()
		node.informalAlias = ""
	// 兼容 a.b.c 的字段情况
	case *gen.IdentifierContext:
		if node.node != nil {
			switch n := node.node.(type) {
			case *ColumnNode:
				n.Sep = "."
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			}
		}
	case *gen.ConstantDefaultContext:
		if node.node != nil {
			switch n := node.node.(type) {
			case *ColumnNode:
				n.Sep = "]["
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			case *FunctionNode:
				n.Values = append(n.Values, &StringNode{Name: ctx.GetText()})
			}
		} else {
			node.node = &StringNode{
				Name: ctx.GetText(),
			}
		}
	case *gen.IdentifierOrTextContext:
		node.as = &StringNode{
			Name: ctx.GetText(),
		}
		node.informalAlias = ctx.GetText()
		node.alias[node.informalAlias] = node.informalField
		node.informalAlias = ""
		next = node.as
	case *gen.StarContext:
		node.node = &StringNode{Name: ctx.GetText()}
	}

	return next
}

func nodeToString(node Node) string {
	if node == nil {
		return ""
	}
	return node.String()
}

func visitChildren(alias map[string]string, encode Encode, setAs bool, next Node, node antlr.RuleNode) any {
	next.WithEncode(encode)
	next.WithSetAs(setAs)
	next.WithAlias(alias)
	for _, child := range node.GetChildren() {
		if tree, ok := child.(antlr.ParseTree); ok {
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(next)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}

	return nil
}

type Option struct {
	DimensionTransform Encode

	Tables []string
	Where  string
	Offset int
	Limit  int
}
