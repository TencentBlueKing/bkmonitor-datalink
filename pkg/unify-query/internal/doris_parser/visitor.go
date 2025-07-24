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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type Node interface {
	antlr.ParseTreeVisitor
	String() string
	Error() error
}

type baseNode struct {
	antlr.BaseParseTreeVisitor
}

func (n *baseNode) String() string {
	return ""
}

func (n *baseNode) Error() error {
	return nil
}

type Statement struct {
	baseNode

	selectNode Node
	tableNode  Node
	whereNode  Node
	aggNode    Node
	sortNode   Node
	limitNode  Node

	errNode []string
}

func (v *Statement) SQL() (string, error) {
	var result []string
	for _, node := range []Node{v.selectNode, v.tableNode, v.whereNode, v.aggNode, v.sortNode, v.limitNode} {
		if node != nil {
			if node.Error() != nil {
				return "", node.Error()
			}
			res := node.String()
			if res != "" {
				result = append(result, node.String())
			}
		}
	}
	return strings.Join(result, " "), nil
}

func (v *Statement) Error() error {
	if len(v.errNode) > 0 {
		return fmt.Errorf("%s", strings.Join(v.errNode, " "))
	}
	return nil
}

func (v *Statement) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
	v.errNode = append(v.errNode, ctx.GetText())
	return nil
}

func (v *Statement) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SelectClauseContext:
		v.selectNode = &SelectNode{}
		next = v.selectNode
	case *gen.FromClauseContext:
		v.tableNode = &TableNode{}
		next = v.tableNode
	case *gen.WhereClauseContext:
		v.whereNode = &WhereNode{}
		next = v.whereNode
	case *gen.AggClauseContext:
		v.aggNode = &AggNode{}
		next = v.aggNode
	case *gen.SortClauseContext:
		v.sortNode = &SortNode{}
		next = v.sortNode
	case *gen.LimitClauseContext:
		v.limitNode = &LimitNode{}
		next = v.limitNode
	}
	return visitChildren(next, ctx)
}

type LimitNode struct {
	baseNode

	nodes []Node
}

func (v *LimitNode) String() string {
	var ns []string
	for _, fn := range v.nodes {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	if len(ns) > 0 {
		return fmt.Sprintf("%s", strings.Join(ns, " "))
	}

	return ""
}

func (v *LimitNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	result := strings.ToUpper(ctx.GetText())
	v.nodes = append(v.nodes, &StringNode{
		Name: result,
	})
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

	if len(ns) > 0 {
		return fmt.Sprintf("ORDER BY %s", strings.Join(ns, ", "))
	}

	return ""
}

func (v *SortNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SortItemContext:
		fn := &OrderNode{}
		next = fn
		v.nodes = append(v.nodes, fn)
	}
	return visitChildren(next, ctx)
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

func (v *OrderNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	result := strings.ToUpper(ctx.GetText())
	v.sort = &StringNode{
		Name: result,
	}
	return nil
}

func (v *OrderNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		v.node = &FieldNode{}
		next = v.node
	}
	return visitChildren(next, ctx)
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

	if len(ns) > 0 {
		return fmt.Sprintf("GROUP BY %s", strings.Join(ns, ", "))
	}

	return ""
}

func (v *AggNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		fn := &FieldNode{}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}
	return visitChildren(next, ctx)
}

type WhereNode struct {
	baseNode

	Node Node

	err error
}

func (v *WhereNode) Error() error {
	return v.err
}

func (v *WhereNode) String() string {
	where := nodeToString(v.Node)
	if where != "" {
		return fmt.Sprintf("WHERE %s", where)
	}
	return ""
}

func (v *WhereNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.LogicalBinaryContext:
		v.Node = &LogicNode{}
		next = v.Node
	case *gen.PredicatedContext:
		v.Node = &ConditionNode{}
		next = v.Node
	}
	return visitChildren(next, ctx)
}

type ParentNode struct {
	baseNode

	node Node
}

func (v *ParentNode) String() string {
	return fmt.Sprintf("(%s)", nodeToString(v.node))
}

func (v *ParentNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	v.node = &WhereNode{}
	next := v.node
	return visitChildren(next, ctx)
}

type LogicNode struct {
	baseNode

	Left  Node
	Right Node
	Op    Node
}

func (v *LogicNode) String() string {
	left := nodeToString(v.Left)
	op := nodeToString(v.Op)
	right := nodeToString(v.Right)
	return fmt.Sprintf("%s %s %s", left, op, right)
}

func (v *LogicNode) VisitTerminal(node antlr.TerminalNode) interface{} {
	v.Op = &StringNode{
		Name: strings.ToUpper(node.GetText()),
	}
	return nil
}

func (v *LogicNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.PredicatedContext:
		if v.Left == nil {
			v.Left = &ConditionNode{}
			next = v.Left
		} else if v.Right == nil {
			v.Right = &ConditionNode{}
			next = v.Right
		}
	}
	return visitChildren(next, ctx)
}

type ConditionNode struct {
	baseNode

	Key    Node
	Op     Node
	Values []Node
}

func (v *ConditionNode) String() string {
	key := nodeToString(v.Key)
	op := nodeToString(v.Op)

	var values []string
	for _, vn := range v.Values {
		vs := nodeToString(vn)
		if vs != "" {
			values = append(values, vs)
		}
	}

	var condition string
	if key != "" {
		condition = key
	}
	if op != "" {
		condition = fmt.Sprintf("%s %s", condition, op)
	}

	var value string
	if len(values) > 0 {
		if len(values) == 1 {
			value = values[0]
		} else {
			value = fmt.Sprintf("(%s)", strings.Join(values, ", "))
		}
	}
	if value != "" {
		condition = fmt.Sprintf("%s %s", condition, value)
	}

	return condition
}

func (v *ConditionNode) VisitTerminal(node antlr.TerminalNode) interface{} {
	if v.Op == nil {
		v.Op = &StringNode{
			Name: node.GetText(),
		}
	}
	return nil
}

func (v *ConditionNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ParenthesizedExpressionContext:
		v.Key = &ParentNode{}
		next = v.Key
	case *gen.ColumnReferenceContext:
		v.Key = &ColumnNode{
			Names: []Node{
				&StringNode{Name: ctx.GetText()},
			},
		}
		next = v.Key
	case *gen.ConstantDefaultContext:
		if v.Key != nil {
			switch n := v.Key.(type) {
			case *ColumnNode:
				n.Sep = "]["
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			case *FunctionNode:
				n.Args = append(n.Args, &StringNode{Name: ctx.GetText()})
			}
		} else {
			v.Key = &StringNode{
				Name: ctx.GetText(),
			}
		}
	case *gen.ExpressionContext:
		if v.Key == nil {
			v.Key = &FieldNode{}
			next = v.Key
		} else {
			fn := &FieldNode{}
			v.Values = append(v.Values, fn)
			next = fn
		}
	case *gen.ComparisonOperatorContext:
		v.Op = &StringNode{
			Name: ctx.GetText(),
		}
	}

	return visitChildren(next, ctx)
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
	if table == "" {
		return ""
	}

	return fmt.Sprintf("FROM %s", table)
}

func (v *TableNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.TableNameContext:
		v.Table = &StringNode{Name: ctx.GetText()}
	}
	return visitChildren(next, ctx)
}

type SelectNode struct {
	baseNode

	fieldsNode []Node
}

func (v *SelectNode) String() string {
	var ns []string
	for _, fn := range v.fieldsNode {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	if len(ns) > 0 {
		return fmt.Sprintf("SELECT %s", strings.Join(ns, ", "))
	}

	return ""
}

func (v *SelectNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		fn := &FieldNode{}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}
	return visitChildren(next, ctx)
}

type FieldNode struct {
	baseNode

	node Node
	as   Node

	sort Node

	args []Node
}

func (v *FieldNode) String() string {
	var result string
	result = nodeToString(v.node)

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
		result = fmt.Sprintf("%s AS %s", result, as)
	}

	sort := nodeToString(v.sort)
	if sort != "" {
		result = fmt.Sprintf("%s %s", result, sort)
	}

	return result
}

func (v *FieldNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	next := visitFieldNode(ctx, v)
	return visitChildren(next, ctx)
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

func (v *BinaryNode) VisitTerminal(node antlr.TerminalNode) interface{} {
	v.Op = &StringNode{
		Name: node.GetText(),
	}
	return nil
}

func (v *BinaryNode) VisitChildren(ctx antlr.RuleNode) interface{} {
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
	return visitChildren(next, ctx)
}

type FunctionNode struct {
	baseNode
	FuncName string
	Value    Node
	Args     []Node
}

func (v *FunctionNode) String() string {
	var result string
	result = nodeToString(v.Value)

	var cols []string
	for _, val := range v.Args {
		col := nodeToString(val)
		if col != "" {
			cols = append(cols, col)
		}
	}

	result = strings.Join(append([]string{result}, cols...), ", ")

	if v.FuncName != "" {
		result = fmt.Sprintf("%s(%s)", v.FuncName, result)
	}
	return result
}

func (v *FunctionNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ArithmeticBinaryContext:
		v.Value = &BinaryNode{}
		next = v.Value
	case *gen.CastContext:
		v.Value = &CastNode{}
		next = v.Value
	case *gen.FunctionCallContext:
		v.Value = &FunctionNode{}
		next = v.Value
	case *gen.FunctionIdentifierContext:
		v.FuncName = ctx.GetText()
	case *gen.ColumnReferenceContext:
		v.Value = &StringNode{Name: ctx.GetText()}
	case *gen.ConstantDefaultContext:
		v.Args = append(v.Args, &StringNode{Name: ctx.GetText()})
	case *gen.StarContext:
		v.Value = &StringNode{Name: ctx.GetText()}
		next = v.Value
	}
	return visitChildren(next, ctx)
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
		result = fmt.Sprintf("CAST(%s AS %s)", result, as)
	}
	return result
}

func (v *CastNode) VisitChildren(ctx antlr.RuleNode) interface{} {
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
				n.Args = append(n.Args, &StringNode{Name: ctx.GetText()})
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
	return visitChildren(next, ctx)
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

func (v *ColumnNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	return visitChildren(v, ctx)
}

type StringNode struct {
	baseNode
	Name string
}

func (v *StringNode) String() string {
	return v.Name
}

func (v *StringNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	return visitChildren(v, ctx)
}

func visitFieldNode(ctx antlr.RuleNode, node *FieldNode) Node {
	var next Node
	next = node

	switch ctx.(type) {
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
				n.Args = append(n.Args, &StringNode{Name: ctx.GetText()})
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

func visitChildren(visitor Node, node antlr.RuleNode) interface{} {
	for _, child := range node.GetChildren() {
		if tree, ok := child.(antlr.ParseTree); ok {
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(visitor)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}

	return nil
}

type DorisVisitorOption struct {
	DimensionTransform func(s string) (string, bool)
}
