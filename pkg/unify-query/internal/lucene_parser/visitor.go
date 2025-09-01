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
	"strconv"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type Encode func(string) (string, bool)

type FieldSetter interface {
	SetField(string)
}

type Node interface {
	antlr.ParseTreeVisitor
	Error() error
	Expr() Expr
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

func (n *baseNode) Expr() Expr {
	return nil
}

type Statement struct {
	baseNode

	node Node
	err  error
}

func NewStatementVisitor() *Statement {
	return &Statement{}
}

func (s *Statement) Error() error {
	return s.err
}

func (s *Statement) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
	s.err = errors.Wrapf(s.err, "parse error at: %s", ctx.GetText())
	return nil
}

func (s *Statement) Expr() Expr {
	if s.node == nil {
		return nil
	}
	return s.node.Expr()
}

func (s *Statement) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = s

	switch ctx.(type) {
	case *gen.QueryContext:
		queryNode := &QueryNode{}
		s.node = queryNode
		next = queryNode
	}

	return visitChildren(next, ctx)
}

type QueryNode struct {
	baseNode
	nodes []Node
}

func (n *QueryNode) Expr() Expr {
	if len(n.nodes) == 0 {
		return nil
	}
	if len(n.nodes) == 1 {
		return n.nodes[0].Expr()
	}

	var mustClauses []Expr
	var mustNotClauses []Expr
	var shouldClauses []Expr

	for _, child := range n.nodes {
		switch node := child.(type) {
		case *OrNode:
			childMust, childMustNot, childShould := node.splitClause()
			mustClauses = append(mustClauses, childMust...)
			mustNotClauses = append(mustNotClauses, childMustNot...)
			shouldClauses = append(shouldClauses, childShould...)
		default:
			if expr := child.Expr(); expr != nil {
				shouldClauses = append(shouldClauses, expr)
			}
		}
	}

	return buildBooleanExpression(mustClauses, mustNotClauses, shouldClauses)
}

func (n *QueryNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.DisjQueryContext:
		node := &OrNode{}
		n.nodes = append(n.nodes, node)
		next = node
	}

	return visitChildren(next, ctx)
}

func buildBooleanExpression(mustClauses, mustNotClauses, shouldClauses []Expr) Expr {
	var result Expr
	var mustExpr Expr
	if len(mustClauses) == 1 {
		mustExpr = mustClauses[0]
	} else if len(mustClauses) > 1 {
		mustExpr = mustClauses[0]
		// 递归构建 mustExpr 子树
		for i := 1; i < len(mustClauses); i++ {
			mustExpr = &AndExpr{Left: mustExpr, Right: mustClauses[i]}
		}
	}

	var mustNotExpr Expr
	if len(mustNotClauses) > 0 {
		for _, clause := range mustNotClauses {
			notExpr := &NotExpr{Expr: clause}
			if mustNotExpr == nil {
				mustNotExpr = notExpr
			} else {
				mustNotExpr = &AndExpr{Left: mustNotExpr, Right: notExpr}
			}
		}
	}

	// 如果 must 和 should 同时存在,需要遍历 should, 构建 (must AND should1) OR (must AND should2) OR ... OR must
	if len(shouldClauses) > 0 && mustExpr != nil {
		var combinedExpr Expr
		for _, shouldClause := range shouldClauses {
			mustAndShould := &AndExpr{
				Left:  shouldClause,
				Right: mustExpr,
			}
			if combinedExpr == nil {
				combinedExpr = mustAndShould
			} else {
				combinedExpr = &OrExpr{
					Left:  combinedExpr,
					Right: mustAndShould,
				}
			}
		}

		if combinedExpr != nil {
			result = &OrExpr{
				Left:  combinedExpr,
				Right: mustExpr,
			}
		} else {
			result = mustExpr
		}
	} else {
		var shouldExpr Expr
		if len(shouldClauses) == 1 {
			shouldExpr = shouldClauses[0]
		} else if len(shouldClauses) > 1 {
			shouldExpr = shouldClauses[0]
			for i := 1; i < len(shouldClauses); i++ {
				shouldExpr = &OrExpr{Left: shouldExpr, Right: shouldClauses[i]}
			}
		}

		if mustExpr != nil {
			result = mustExpr
		}
		if shouldExpr != nil {
			if result != nil {
				result = &AndExpr{Left: result, Right: shouldExpr}
			} else {
				result = shouldExpr
			}
		}
	}

	// Add must_not constraints
	if mustNotExpr != nil {
		if result != nil {
			result = &AndExpr{Left: result, Right: mustNotExpr}
		} else {
			result = mustNotExpr
		}
	}

	return result
}

type OrNode struct {
	baseNode
	nodes []Node
}

func (n *OrNode) Expr() Expr {
	if len(n.nodes) == 0 {
		return nil
	}

	var buildRight func([]Node) Expr
	buildRight = func(nodes []Node) Expr {
		if len(nodes) == 1 {
			return nodes[0].Expr()
		}
		if len(nodes) > 1 {
			return &OrExpr{Left: nodes[0].Expr(), Right: buildRight(nodes[1:])}
		}
		return nil
	}
	return buildRight(n.nodes)
}

func (n *OrNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.ConjQueryContext:
		node := &AndNode{}
		n.nodes = append(n.nodes, node)
		next = node
	}

	return visitChildren(next, ctx)
}

func (n *OrNode) splitClause() (mustClauses, mustNotClauses, shouldClauses []Expr) {
	return splitClauseHelper(n.nodes, func(child Node) []Expr {
		if expr := child.Expr(); expr != nil {
			return []Expr{expr}
		}
		return nil
	})
}

type AndNode struct {
	baseNode
	nodes []Node
}

func (n *AndNode) Expr() Expr {
	if len(n.nodes) == 0 {
		return nil
	}
	if len(n.nodes) == 1 {
		return n.nodes[0].Expr()
	}

	result := n.nodes[0].Expr()
	for i := 1; i < len(n.nodes); i++ {
		if expr := n.nodes[i].Expr(); expr != nil {
			result = &AndExpr{Left: result, Right: expr}
		}
	}
	return result
}

func (n *AndNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.ModClauseContext:
		node := &ModClauseNode{}
		n.nodes = append(n.nodes, node)
		next = node
	}

	return visitChildren(next, ctx)
}

func (n *AndNode) splitClause() (mustClauses, mustNotClauses, shouldClauses []Expr) {
	return splitClauseHelper(n.nodes, func(child Node) []Expr {
		if modClause, ok := child.(*ModClauseNode); ok {
			if modClause.hasModifier() {
				return nil
			}
		}
		if expr := child.Expr(); expr != nil {
			return []Expr{expr}
		}
		return nil
	})
}

type ModClauseNode struct {
	baseNode
	modifier string
	node     Node
}

func (n *ModClauseNode) Expr() Expr {
	if n.node == nil {
		return nil
	}

	expr := n.node.Expr()
	if expr != nil && n.isNegative() {
		return &NotExpr{Expr: expr}
	}
	return expr
}

func (n *ModClauseNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	text := ctx.GetText()
	if text != "" {
		n.modifier = text
	}
	return nil
}

func (n *ModClauseNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.ClauseContext:
		n.node = &ClauseNode{}
		next = n.node
	}

	return visitChildren(next, ctx)
}

// hasModifier returns true if this node has a modifier (+, -, NOT)
func (n *ModClauseNode) hasModifier() bool {
	return n.modifier != ""
}

// isPositive returns true if this node has a positive modifier (+)
func (n *ModClauseNode) isPositive() bool {
	return n.modifier == "+"
}

// isNegative returns true if this node has a negative modifier (-, NOT)
func (n *ModClauseNode) isNegative() bool {
	return n.modifier == "-" || strings.ToUpper(n.modifier) == "NOT"
}

type ClauseNode struct {
	baseNode
	field string // field name from fieldName context
	node  Node   // the term or grouping expression
}

func (n *ClauseNode) Expr() Expr {
	if n.node == nil {
		return nil
	}

	if n.field != "" {
		switch child := n.node.(type) {
		case *GroupNode:
			child.field = n.field
			return child.Expr()
		}
	}

	expr := n.node.Expr()
	if expr != nil && n.field != "" {
		switch fieldSetter := expr.(type) {
		case FieldSetter:
			fieldSetter.SetField(n.field)
		}
	}

	return expr
}

func (n *ClauseNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.FieldRangeExprContext:
		n.node = &RangeNode{}
		next = n.node
	case *gen.FieldNameContext:
		// Store the field name for this node
		n.field = ctx.GetText()
	case *gen.TermContext:
		// Check if this term context contains a range expression
		termText := ctx.GetText()
		if strings.Contains(termText, "TO") {
			// This is a range expression, create a RangeNode instead
			n.node = &RangeNode{
				field: n.field,
			}
			next = n.node
		} else {
			n.node = &TermNode{}
			next = n.node
		}
	case *gen.GroupingExprContext:
		n.node = &GroupNode{}
		next = n.node
	}

	return visitChildren(next, ctx)
}

type TermNode struct {
	baseNode
	field      string
	value      string
	isQuoted   bool
	isRegex    bool
	isWildcard bool
}

func (n *TermNode) Expr() Expr {
	var expr *OperatorExpr
	if n.isRegex {
		regexValue := strings.Trim(n.value, "/")
		expr = &OperatorExpr{
			Op:    OpRegex,
			Value: &StringExpr{Value: regexValue},
		}
	} else if n.isWildcard {
		expr = &OperatorExpr{
			Op:    OpWildcard,
			Value: &StringExpr{Value: n.value},
		}
	} else {
		expr = &OperatorExpr{
			Op:       OpMatch,
			Value:    n.createValueExpr(),
			IsQuoted: n.isQuoted,
		}
	}

	if n.field != "" {
		expr.SetField(n.field)
	}

	return expr
}

func (n *TermNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch c := ctx.(type) {
	case *gen.QuotedTermContext:
		cleanValue, isWildcard, isQuoted := parseAndClassifyString(c.GetText())
		n.value = cleanValue
		n.isWildcard = isWildcard
		n.isQuoted = isQuoted
		return nil
	}

	return visitChildren(next, ctx)
}

func (n *TermNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	text := ctx.GetText()
	tokenType := ctx.GetSymbol().GetTokenType()

	switch tokenType {
	case gen.LuceneLexerOP_COLON, gen.LuceneLexerOP_EQUAL:
		// Skip operators
		return nil
	case gen.LuceneLexerTERM:
		// Apply escape processing
		unescapedText := unescapeString(text)

		// Check if this is a wildcard term (after unescaping)
		if strings.Contains(unescapedText, "*") || strings.Contains(unescapedText, "?") {
			n.isWildcard = true
		}
		// Determine if this is field or node based on context
		if n.field == "" && n.value == "" {
			n.value = unescapedText
		} else if n.field == "" && n.value != "" {
			n.field = n.value
			n.value = unescapedText
		} else if n.value == "" {
			n.value = unescapedText
		}
		return nil
	case gen.LuceneLexerQUOTED:
		n.isQuoted = true
		n.value = removeQuotesAndUnescape(text)
		return nil
	case gen.LuceneLexerREGEXPTERM:
		n.isRegex = true
		n.value = text
		return nil
	case gen.LuceneLexerNUMBER:
		n.value = text
		return nil
	}
	return nil
}

type RangeNode struct {
	baseNode
	field        string
	start        *string
	end          *string
	includeStart bool
	includeEnd   bool
	op           string // For comparison operators like >, <, >=, <=
	value        string // For comparison values
}

func (n *RangeNode) Expr() Expr {
	if n.op != "" {
		// Handle comparison operators
		var rangeExpr *RangeExpr
		switch n.op {
		case ">":
			rangeExpr = &RangeExpr{
				Start:        n.createValueExpr(n.value),
				IncludeStart: &BoolExpr{Value: false},
			}
		case "<":
			rangeExpr = &RangeExpr{
				End:        n.createValueExpr(n.value),
				IncludeEnd: &BoolExpr{Value: false},
			}
		case ">=":
			rangeExpr = &RangeExpr{
				Start:        n.createValueExpr(n.value),
				IncludeStart: &BoolExpr{Value: true},
			}
		case "<=":
			rangeExpr = &RangeExpr{
				End:        n.createValueExpr(n.value),
				IncludeEnd: &BoolExpr{Value: true},
			}
		}

		expr := &OperatorExpr{
			Op:    OpRange,
			Value: rangeExpr,
		}
		if n.field != "" {
			expr.SetField(n.field)
		}
		return expr
	}

	// Handle range expressions
	startPtr := n.start
	endPtr := n.end

	if startPtr == nil {
		wildcard := "*"
		startPtr = &wildcard
	}
	if endPtr == nil {
		wildcard := "*"
		endPtr = &wildcard
	}

	rangeExpr := &RangeExpr{
		Start:        n.createValueExpr(*startPtr),
		End:          n.createValueExpr(*endPtr),
		IncludeStart: &BoolExpr{Value: n.includeStart},
		IncludeEnd:   &BoolExpr{Value: n.includeEnd},
	}

	expr := &OperatorExpr{
		Op:    OpRange,
		Value: rangeExpr,
	}
	if n.field != "" {
		expr.SetField(n.field)
	}
	return expr
}

func (n *RangeNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	switch c := ctx.(type) {
	case *gen.FieldNameContext:
		n.field = c.GetText()
		return nil
	}

	return visitChildren(n, ctx)
}

func (n *RangeNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	text := ctx.GetText()
	tokenType := ctx.GetSymbol().GetTokenType()

	switch tokenType {
	case gen.LuceneLexerOP_MORETHAN, gen.LuceneLexerOP_LESSTHAN,
		gen.LuceneLexerOP_MORETHANEQ, gen.LuceneLexerOP_LESSTHANEQ:
		n.op = text
		return nil
	case gen.LuceneLexerRANGEIN_START:
		n.includeStart = true
		return nil
	case gen.LuceneLexerRANGEEX_START:
		n.includeStart = false
		return nil
	case gen.LuceneLexerRANGEIN_END:
		n.includeEnd = true
		return nil
	case gen.LuceneLexerRANGEEX_END:
		n.includeEnd = false
		return nil
	case gen.LuceneLexerRANGE_GOOP, gen.LuceneLexerRANGE_QUOTED, gen.LuceneLexerTERM, gen.LuceneLexerQUOTED, gen.LuceneLexerNUMBER:
		cleanValue := n.cleanRangeValue(text, tokenType)
		n.setRangeValue(cleanValue)
		return nil
	}
	return nil
}

type GroupNode struct {
	baseNode
	node  Node
	field string // Set when this is a field:(grouped expression)
}

func (n *GroupNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.QueryContext:
		queryNode := &QueryNode{}
		n.node = queryNode
		next = queryNode
	}

	return visitChildren(next, ctx)
}

func (n *GroupNode) Expr() Expr {
	if n.node == nil {
		return nil
	}

	// Try the ConditionExpr optimization in one pass
	if n.field != "" {
		if conditionExpr, ok := n.buildConditionExpr(n.node); ok {
			return &ConditionMatchExpr{
				Field: &StringExpr{Value: n.field},
				Value: conditionExpr,
			}
		}
	}

	innerExpr := n.node.Expr()
	if innerExpr == nil {
		return nil
	}

	// Always create GroupingExpr to represent parentheses structure
	expr := &GroupingExpr{Expr: innerExpr}

	// Apply field to the inner expression if needed
	if n.field != "" {
		if fieldSetter, ok := innerExpr.(FieldSetter); ok {
			fieldSetter.SetField(n.field)
		}
	}

	return expr
}

// buildConditionExpr recursively builds a ConditionExpr and reports success
func (n *GroupNode) buildConditionExpr(node Node) (*ConditionsExpr, bool) {
	var result [][]Expr
	switch v := node.(type) {
	case *QueryNode:
		if len(v.nodes) == 1 {
			return n.buildConditionExpr(v.nodes[0])
		}
		// Multiple nodes in QueryNode means OR at top level
		for _, child := range v.nodes {
			if childCondition, ok := n.buildConditionExpr(child); ok {
				if childCondition != nil {
					result = append(result, childCondition.Values...)
				}
			} else {
				// If any child fails, the whole group fails
				return nil, false
			}
		}
		return &ConditionsExpr{Values: result}, true

	case *OrNode:
		// OR node - combine all values as separate arrays
		for _, child := range v.nodes {
			if childCondition, ok := n.buildConditionExpr(child); ok {
				if childCondition != nil {
					result = append(result, childCondition.Values...)
				}
			} else {
				return nil, false
			}
		}
		return &ConditionsExpr{Values: result}, true

	case *AndNode:
		// AND node - cartesian product of all node values
		for _, child := range v.nodes {
			if childCondition, ok := n.buildConditionExpr(child); ok {
				if childCondition != nil {
					if len(result) == 0 {
						result = childCondition.Values
					} else {
						var newResult [][]Expr
						for _, existing := range result {
							for _, newValues := range childCondition.Values {
								combined := make([]Expr, 0, len(existing)+len(newValues))
								combined = append(combined, existing...)
								combined = append(combined, newValues...)
								newResult = append(newResult, combined)
							}
						}
						result = newResult
					}
				}
			} else {
				return nil, false
			}
		}
		return &ConditionsExpr{Values: result}, true

	case *ModClauseNode:
		if v.node != nil && v.modifier == "" {
			return n.buildConditionExpr(v.node)
		}

	case *ClauseNode:
		if v.node != nil && v.field == "" {
			return n.buildConditionExpr(v.node)
		}

	case *GroupNode:
		if v.node != nil {
			return n.buildConditionExpr(v.node)
		}

	case *TermNode:
		if v.isQuoted {
			cleanValue := strings.Trim(v.value, `"'`)
			return &ConditionsExpr{
				Values: [][]Expr{{&StringExpr{Value: cleanValue}}},
			}, true
		}
	}

	return nil, false
}

func splitClauseHelper(nodes []Node, defaultHandler func(Node) []Expr) (mustClauses, mustNotClauses, shouldClauses []Expr) {
	for _, child := range nodes {
		switch node := child.(type) {
		case interface {
			splitClause() ([]Expr, []Expr, []Expr)
		}:
			childMust, childMustNot, childShould := node.splitClause()
			mustClauses = append(mustClauses, childMust...)
			mustNotClauses = append(mustNotClauses, childMustNot...)
			shouldClauses = append(shouldClauses, childShould...)
		case *ModClauseNode:
			if node.hasModifier() {
				if expr := node.node.Expr(); expr != nil {
					if node.isPositive() {
						mustClauses = append(mustClauses, expr)
					} else if node.isNegative() {
						mustNotClauses = append(mustNotClauses, expr)
					}
				}
			} else if defaultHandler != nil {
				if exprs := defaultHandler(child); exprs != nil {
					shouldClauses = append(shouldClauses, exprs...)
				}
			}
		default:
			if defaultHandler != nil {
				if exprs := defaultHandler(child); exprs != nil {
					shouldClauses = append(shouldClauses, exprs...)
				}
			}
		}
	}
	return
}

func visitChildren(next Node, node antlr.RuleNode) interface{} {
	for _, child := range node.GetChildren() {
		switch tree := child.(type) {
		case antlr.ParseTree:
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(next)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}
	return nil
}

func looksLikeDate(s string) bool {
	if s == "*" {
		return false
	}
	return strings.Contains(s, "-") || strings.Contains(s, "T") || strings.Contains(s, ":")
}

func unescapeString(s string) string {
	if s == "" {
		return s
	}

	result := make([]rune, 0, len(s))
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++
			result = append(result, runes[i])
		} else {
			result = append(result, runes[i])
		}
	}

	return string(result)
}

func (n *RangeNode) cleanRangeValue(text string, tokenType int) string {
	if tokenType == gen.LuceneLexerRANGE_QUOTED || tokenType == gen.LuceneLexerQUOTED {
		return removeQuotesAndUnescape(text)
	}
	return unescapeString(text)
}

func (n *RangeNode) setRangeValue(cleanValue string) {
	if n.op != "" {
		n.value = cleanValue
	} else {
		if n.start == nil {
			n.start = &cleanValue
		} else if n.end == nil {
			n.end = &cleanValue
		}
	}
}

func removeQuotesAndUnescape(text string) string {
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		return unescapeString(text[1 : len(text)-1])
	}
	return unescapeString(text)
}

func parseAndClassifyString(text string) (value string, isWildcard bool, isQuoted bool) {
	value = removeQuotesAndUnescape(text)

	if strings.Contains(value, "*") || strings.Contains(value, "?") {
		isWildcard = true
	} else {
		isQuoted = true
	}
	return
}

// createValueExpr creates a NumberExpr for numeric values, StringExpr otherwise
func (n *RangeNode) createValueExpr(value string) Expr {
	if value == "*" {
		return &StringExpr{Value: value}
	}

	// Try to parse as number
	if numValue, err := strconv.ParseFloat(value, 64); err == nil {
		return &NumberExpr{Value: numValue}
	}

	// If not a number, treat as string
	return &StringExpr{Value: value}
}

// createValueExpr creates appropriate Expr based on value type
func (n *TermNode) createValueExpr() Expr {
	// Try to parse as number regardless of whether it's quoted
	if numValue, err := strconv.ParseFloat(n.value, 64); err == nil {
		return &NumberExpr{Value: numValue}
	}

	// If not a number, treat as string
	return &StringExpr{Value: n.value}
}
