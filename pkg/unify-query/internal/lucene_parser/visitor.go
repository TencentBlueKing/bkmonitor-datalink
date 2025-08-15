// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"context"
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	SelectItem = "SELECT"
	TableItem  = "FROM"
	WhereItem  = "WHERE"
	OrderItem  = "ORDER BY"
	GroupItem  = "GROUP BY"
	LimitItem  = "LIMIT"

	AsItem = "AS"
)

type Encode func(string) (string, bool)

type Node interface {
	antlr.ParseTreeVisitor
	String() string
	Error() error

	WithEncode(Encode)
	WithSetAs(bool)
}

type baseNode struct {
	antlr.BaseParseTreeVisitor

	Encode Encode
	SetAs  bool
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

type Statement struct {
	baseNode

	nodes []Node

	nodeMap map[string]Node

	errNode []string
}

func (v *Statement) ItemString(name string) string {
	if n, ok := v.nodeMap[name]; ok {
		return nodeToString(n)
	}

	return ""
}

func (v *Statement) String() string {
	var s []string
	for _, n := range v.nodes {
		s = append(s, nodeToString(n))
	}

	return strings.Join(s, " ")
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
	case *gen.ConjQueryContext:
		node := &LogicNode{}
		v.nodes = append(v.nodes, node)
		next = node
		//case *gen.ModClauseContext:
		//	node := &ConditionNode{}
		//	v.nodes = append(v.nodes, node)
		//	next = node
	}

	return visitChildren(v.Encode, next, ctx)
}

type LogicNode struct {
	baseNode

	Left  Node
	Right Node
	Op    string
}

func (v *LogicNode) String() string {
	return fmt.Sprintf("%s %s %s", nodeToString(v.Left), v.Op, nodeToString(v.Right))
}

func (v *LogicNode) VisitTerminal(node antlr.TerminalNode) interface{} {
	result := strings.ToUpper(node.GetText())
	if v.Op == "" {
		v.Op = result
	}
	return nil
}

func (v *LogicNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ModClauseContext:
		node := &ConditionNode{}
		if v.Left == nil {
			v.Left = node
		} else if v.Right == nil {
			v.Right = node
		}
		next = node
	}

	return visitChildren(v.Encode, next, ctx)
}

type ConditionNode struct {
	baseNode

	field string
	value string
}

func (v *ConditionNode) String() string {
	return fmt.Sprintf("%s:%s", v.field, v.value)
}

func (v *ConditionNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.FieldNameContext:
		v.field = ctx.GetText()
	case *gen.TermContext:
		v.value = ctx.GetText()
	}

	return visitChildren(v.Encode, next, ctx)
}

func nodeToString(node Node) string {
	if node == nil {
		return ""
	}
	return node.String()
}

func visitChildren(encode Encode, next Node, node antlr.RuleNode) interface{} {
	next.WithEncode(encode)
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

	Table string
	Where string
}
