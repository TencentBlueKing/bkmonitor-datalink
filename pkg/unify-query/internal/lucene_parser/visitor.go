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

func (v *Statement) VisitTopLevelQuery(ctx *gen.TopLevelQueryContext) interface{} {
	if ctx.Query() != nil {
		return ctx.Query().Accept(v)
	}
	return ""
}

func (v *Statement) VisitQuery(ctx *gen.QueryContext) interface{} {
	var parts []string
	for _, disj := range ctx.AllDisjQuery() {
		part := disj.Accept(v)
		if part != nil {
			parts = append(parts, part.(string))
		}
	}
	return strings.Join(parts, " ")
}

func (v *Statement) VisitDisjQuery(ctx *gen.DisjQueryContext) interface{} {
	var parts []string

	conjQueries := ctx.AllConjQuery()
	if len(conjQueries) == 0 {
		return ""
	}

	result := conjQueries[0].Accept(v)
	if result != nil {
		parts = append(parts, result.(string))
	}

	for i := 1; i < len(conjQueries); i++ {
		op := "OR"
		if ctx.OR(i-1) != nil {
			op = strings.ToUpper(ctx.OR(i - 1).GetText())
		}
		part := conjQueries[i].Accept(v)
		if part != nil {
			parts = append(parts, op, part.(string))
		}
	}
	return strings.Join(parts, " ")
}

func (v *Statement) VisitConjQuery(ctx *gen.ConjQueryContext) interface{} {
	var parts []string

	modClauses := ctx.AllModClause()
	if len(modClauses) == 0 {
		return ""
	}

	result := modClauses[0].Accept(v)
	if result != nil {
		parts = append(parts, result.(string))
	}

	for i := 1; i < len(modClauses); i++ {
		op := "AND"
		if ctx.AND(i-1) != nil {
			op = strings.ToUpper(ctx.AND(i - 1).GetText())
		}
		part := modClauses[i].Accept(v)
		if part != nil {
			parts = append(parts, op, part.(string))
		}
	}
	return strings.Join(parts, " ")
}

func (v *Statement) VisitModClause(ctx *gen.ModClauseContext) interface{} {
	var prefix string
	if ctx.Modifier() != nil {
		prefix = ctx.Modifier().GetText()
	}

	clause := ctx.Clause().Accept(v).(string)

	if prefix != "" {
		return prefix + clause
	}
	return clause
}

func (v *Statement) VisitClause(ctx *gen.ClauseContext) interface{} {
	if ctx.FieldRangeExpr() != nil {
		result := ctx.FieldRangeExpr().Accept(v)
		if result != nil {
			return result.(string)
		}
		return ""
	}

	var fieldPart string
	if ctx.FieldName() != nil {
		fieldNameResult := ctx.FieldName().Accept(v)
		if fieldNameResult != nil {
			fieldName := fieldNameResult.(string)
			var op string
			if ctx.OP_COLON() != nil {
				op = ":"
			} else if ctx.OP_EQUAL() != nil {
				op = "="
			}
			fieldPart = fieldName + op
		}
	}

	if ctx.Term() != nil {
		termResult := ctx.Term().Accept(v)
		if termResult != nil {
			return fieldPart + termResult.(string)
		}
	}
	if ctx.GroupingExpr() != nil {
		groupResult := ctx.GroupingExpr().Accept(v)
		if groupResult != nil {
			return fieldPart + groupResult.(string)
		}
	}

	return ""
}

func (v *Statement) VisitFieldRangeExpr(ctx *gen.FieldRangeExprContext) interface{} {
	fieldName := ctx.FieldName().Accept(v).(string)

	var op string
	switch {
	case ctx.OP_LESSTHAN() != nil:
		op = "<"
	case ctx.OP_LESSTHANEQ() != nil:
		op = "<="
	case ctx.OP_MORETHAN() != nil:
		op = ">"
	case ctx.OP_MORETHANEQ() != nil:
		op = ">="
	}

	var value string
	if ctx.TERM() != nil {
		value = ctx.TERM().GetText()
	} else if ctx.QUOTED() != nil {
		value = ctx.QUOTED().GetText()
	} else if ctx.NUMBER() != nil {
		value = ctx.NUMBER().GetText()
	}

	return fieldName + op + value
}

func (v *Statement) VisitTerm(ctx *gen.TermContext) interface{} {
	var result string

	switch {
	case ctx.TERM() != nil:
		result = ctx.TERM().GetText()
	case len(ctx.AllNUMBER()) > 0:
		result = ctx.NUMBER(0).GetText()
	case ctx.QuotedTerm() != nil:
		quotedResult := ctx.QuotedTerm().Accept(v)
		if quotedResult != nil {
			result = quotedResult.(string)
		}
	case ctx.REGEXPTERM() != nil:
		result = ctx.REGEXPTERM().GetText()
	case ctx.TermRangeExpr() != nil:
		rangeResult := ctx.TermRangeExpr().Accept(v)
		if rangeResult != nil {
			result = rangeResult.(string)
		}
	}

	if ctx.Fuzzy() != nil {
		fuzzy := ctx.Fuzzy().Accept(v)
		if fuzzy != nil {
			result += fuzzy.(string)
		}
	}

	if ctx.CARAT() != nil && len(ctx.AllNUMBER()) > 0 {
		boost := "^" + ctx.AllNUMBER()[len(ctx.AllNUMBER())-1].GetText()
		result += boost
	}

	return result
}

func (v *Statement) VisitQuotedTerm(ctx *gen.QuotedTermContext) interface{} {
	result := ctx.QUOTED().GetText()

	if ctx.CARAT() != nil && ctx.NUMBER() != nil {
		result += "^" + ctx.NUMBER().GetText()
	}

	return result
}

func (v *Statement) VisitGroupingExpr(ctx *gen.GroupingExprContext) interface{} {
	result := "(" + ctx.Query().Accept(v).(string) + ")"

	if ctx.CARAT() != nil && ctx.NUMBER() != nil {
		result += "^" + ctx.NUMBER().GetText()
	}

	return result
}

func (v *Statement) VisitTermRangeExpr(ctx *gen.TermRangeExprContext) interface{} {
	var start, end string
	var inclusive bool

	if ctx.RANGEIN_START() != nil {
		inclusive = true
	} else {
		inclusive = false
	}

	if len(ctx.AllRANGE_GOOP()) > 0 {
		start = ctx.AllRANGE_GOOP()[0].GetText()
	} else if len(ctx.AllRANGE_QUOTED()) > 0 {
		start = ctx.AllRANGE_QUOTED()[0].GetText()
	}

	if len(ctx.AllRANGE_GOOP()) > 1 {
		end = ctx.AllRANGE_GOOP()[1].GetText()
	} else if len(ctx.AllRANGE_QUOTED()) > 0 {
		end = ctx.AllRANGE_QUOTED()[0].GetText()
	}

	if inclusive {
		return fmt.Sprintf("[%s TO %s]", start, end)
	}
	return fmt.Sprintf("{%s TO %s}", start, end)
}

func (v *Statement) VisitFieldName(ctx *gen.FieldNameContext) interface{} {
	if ctx.TERM() != nil {
		return ctx.TERM().GetText()
	}
	return ""
}

func (v *Statement) VisitFuzzy(ctx *gen.FuzzyContext) interface{} {
	if ctx.NUMBER() != nil {
		return "~" + ctx.NUMBER().GetText()
	}
	return "~"
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

func (v *Statement) VisitChildren(node antlr.RuleNode) interface{} {
	return nil // 使用直接的Visit方法，而不是通用的VisitChildren
}

type LogicNode struct {
	baseNode

	Left  Node
	Right Node
	Op    string
}

func (v *LogicNode) String() string {
	var left, right string

	left = nodeToString(v.Left)
	right = nodeToString(v.Right)

	// Handle grouping for complex expressions
	if v.Op != "" && right != "" {
		return fmt.Sprintf("%s %s %s", left, v.Op, right)
	}
	return left
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

	switch ctx := ctx.(type) {
	case *gen.ModClauseContext:
		node := &ConditionNode{}
		if v.Left == nil {
			v.Left = node
		} else if v.Right == nil {
			v.Right = node
		}
		next = node
	case *gen.ConjQueryContext:
		// Handle conjunctive queries
		return visitChildren(v.Encode, next, ctx)
	case *gen.DisjQueryContext:
		// Handle disjunctive queries
		return visitChildren(v.Encode, next, ctx)
	}

	return visitChildren(v.Encode, next, ctx)
}

type ConditionNode struct {
	baseNode

	field      string
	value      string
	boost      string
	fuzzy      string
	regex      string
	rangeValue string
	op         string
	modifier   string
}

func (v *ConditionNode) String() string {
	var parts []string

	if v.modifier != "" {
		parts = append(parts, v.modifier)
	}

	if v.field != "" && v.value != "" {
		parts = append(parts, fmt.Sprintf("%s:%s", v.field, v.value))
	} else if v.field != "" && v.rangeValue != "" {
		parts = append(parts, fmt.Sprintf("%s:%s", v.field, v.rangeValue))
	} else if v.field != "" && v.regex != "" {
		parts = append(parts, fmt.Sprintf("%s:%s", v.field, v.regex))
	} else if v.value != "" {
		parts = append(parts, v.value)
	} else if v.regex != "" {
		parts = append(parts, v.regex)
	} else if v.rangeValue != "" {
		parts = append(parts, v.rangeValue)
	}

	if v.fuzzy != "" {
		parts[len(parts)-1] += v.fuzzy
	}

	if v.boost != "" {
		parts[len(parts)-1] += "^" + v.boost
	}

	if v.op != "" {
		return strings.Join(parts, " "+v.op+" ")
	}

	return strings.Join(parts, "")
}

func (v *ConditionNode) VisitTerminal(node antlr.TerminalNode) interface{} {
	text := node.GetText()

	switch strings.ToUpper(text) {
	case "AND", "OR":
		v.op = strings.ToUpper(text)
	case "+", "-", "NOT":
		v.modifier = text
	case "~":
		if v.fuzzy == "" {
			v.fuzzy = "~"
		}
	case "^":
		// boost value will be handled in context
	}
	return nil
}

func (v *ConditionNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = v

	switch ctx := ctx.(type) {
	case *gen.FieldNameContext:
		v.field = ctx.GetText()
	case *gen.TermContext:
		v.handleTerm(ctx)
	case *gen.QuotedTermContext:
		v.handleQuotedTerm(ctx)
	case *gen.GroupingExprContext:
		v.handleGrouping(ctx)
	case *gen.FieldRangeExprContext:
		v.handleFieldRange(ctx)
	case *gen.TermRangeExprContext:
		v.handleTermRange(ctx)
	case *gen.ModifierContext:
		// handled in VisitTerminal
	}

	return visitChildren(v.Encode, next, ctx)
}

func (v *ConditionNode) handleTerm(ctx *gen.TermContext) {
	if ctx.TERM() != nil {
		v.value = ctx.TERM().GetText()
	} else if len(ctx.AllNUMBER()) > 0 {
		v.value = ctx.AllNUMBER()[0].GetText()
	} else if ctx.QuotedTerm() != nil {
		quotedCtx := ctx.QuotedTerm().(*gen.QuotedTermContext)
		if quotedCtx.QUOTED() != nil {
			v.value = quotedCtx.QUOTED().GetText()
		}
	}

	// Handle fuzzy
	if ctx.Fuzzy() != nil {
		fuzzyCtx := ctx.Fuzzy().(*gen.FuzzyContext)
		if fuzzyCtx.NUMBER() != nil {
			v.fuzzy = "~" + fuzzyCtx.NUMBER().GetText()
		} else {
			v.fuzzy = "~"
		}
	}

	// Handle boost
	if ctx.CARAT() != nil && len(ctx.AllNUMBER()) > 0 {
		v.boost = ctx.AllNUMBER()[len(ctx.AllNUMBER())-1].GetText()
	}
}

func (v *ConditionNode) handleQuotedTerm(ctx *gen.QuotedTermContext) {
	v.value = ctx.QUOTED().GetText()

	// Handle boost
	if ctx.CARAT() != nil && ctx.NUMBER() != nil {
		v.boost = ctx.NUMBER().GetText()
	}
}

func (v *ConditionNode) handleGrouping(ctx *gen.GroupingExprContext) {
	// For grouping expressions, we'll use the string representation
	v.value = "(" + ctx.Query().GetText() + ")"

	// Handle boost
	if ctx.CARAT() != nil && ctx.NUMBER() != nil {
		v.boost = ctx.NUMBER().GetText()
	}
}

func (v *ConditionNode) handleFieldRange(ctx *gen.FieldRangeExprContext) {
	var op string
	if ctx.OP_LESSTHAN() != nil {
		op = "<"
	} else if ctx.OP_MORETHAN() != nil {
		op = ">"
	} else if ctx.OP_LESSTHANEQ() != nil {
		op = "<="
	} else if ctx.OP_MORETHANEQ() != nil {
		op = ">="
	}

	var value string
	if ctx.TERM() != nil {
		value = ctx.TERM().GetText()
	} else if ctx.QUOTED() != nil {
		value = ctx.QUOTED().GetText()
	} else if ctx.NUMBER() != nil {
		value = ctx.NUMBER().GetText()
	}

	v.rangeValue = fmt.Sprintf("%s%s%s", v.field, op, value)
}

func (v *ConditionNode) handleTermRange(ctx *gen.TermRangeExprContext) {
	var start, end string
	var inclusive bool

	if ctx.RANGEIN_START() != nil {
		inclusive = true
	} else {
		inclusive = false
	}

	// Get left boundary
	if len(ctx.AllRANGE_GOOP()) > 0 {
		start = ctx.AllRANGE_GOOP()[0].GetText()
	} else if len(ctx.AllRANGE_QUOTED()) > 0 {
		start = ctx.AllRANGE_QUOTED()[0].GetText()
	}

	// Get right boundary
	if len(ctx.AllRANGE_GOOP()) > 1 {
		end = ctx.AllRANGE_GOOP()[1].GetText()
	} else if len(ctx.AllRANGE_QUOTED()) > 0 {
		end = ctx.AllRANGE_QUOTED()[0].GetText()
	}

	if inclusive {
		v.rangeValue = fmt.Sprintf("[%s TO %s]", start, end)
	} else {
		v.rangeValue = fmt.Sprintf("{%s TO %s}", start, end)
	}
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
