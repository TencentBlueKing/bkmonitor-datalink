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
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring_parser"
)

// Global constants for special characters used in Lucene syntax.
const (
	WildcardAsterisk    = "*"
	WildcardQuestion    = "?"
	DateSeparatorHyphen = "-"
	DateSeparatorT      = "T"
	DateSeparatorColon  = ":"
)

// Encode defines a function type for optional field name encoding.
type Encode func(string) (string, bool)

const (
	TypeKeyword = "keyword"
	TypeText    = "text"
	TypeInteger = "integer"
	TypeLong    = "long"
	TypeDate    = "date"
)

type SchemaConfig struct {
	Mapping map[string]string `json:"mapping"` // Field name(flat) to Elasticsearch type mapping. E.g., {"status": "keyword", "message": "text","field1.field2": "keyword"}
}

// NewSchemaConfig creates a new, empty schema configuration.
func NewSchemaConfig() *SchemaConfig {
	return &SchemaConfig{
		Mapping: make(map[string]string),
	}
}

// WithMappings allows chaining to set the schema mappings.
func (sc *SchemaConfig) WithMappings(mappings map[string]string) *SchemaConfig {
	sc.Mapping = mappings
	return sc
}

// GetFieldType retrieves the Elasticsearch type for a given field.
func (sc *SchemaConfig) GetFieldType(field string) (string, bool) {
	fieldType, exists := sc.Mapping[field]
	return fieldType, exists
}

// Node is the interface for all nodes in our Abstract Syntax Tree (AST).
// Each node must be able to convert itself to a SQL expression and an Elasticsearch query.
type Node interface {
	antlr.ParseTreeVisitor
	ToSQL() querystring_parser.Expr
	ToES() elastic.Query
	Error() error
	WithEncode(Encode)
	WithSchema(*SchemaConfig)
}

// baseNode provides a default implementation for the Node interface.
type baseNode struct {
	antlr.BaseParseTreeVisitor
	Encode Encode
	Schema *SchemaConfig
}

func (n *baseNode) ToSQLString() string {
	return ""
}

func (n *baseNode) ToSQL() querystring_parser.Expr {
	return nil
}

func (n *baseNode) ToES() elastic.Query {
	return nil
}

func (n *baseNode) Error() error {
	return nil
}

func (n *baseNode) WithEncode(encode Encode) {
	n.Encode = encode
}

func (n *baseNode) WithSchema(schema *SchemaConfig) {
	n.Schema = schema
}

// Statement is the main visitor struct that walks the ANTLR parse tree
// and builds our custom AST. It holds the final root of the AST.
type Statement struct {
	*gen.BaseLuceneParserVisitor

	root    Node
	errNode []string
	Encode  Encode
	Schema  *SchemaConfig
}

// NewStatementVisitor creates a new visitor instance.
func NewStatementVisitor(ctx context.Context) *Statement {
	return &Statement{
		BaseLuceneParserVisitor: &gen.BaseLuceneParserVisitor{},
		Schema:                  NewSchemaConfig(),
	}
}

// WithEncode sets the field name encoder function.
func (s *Statement) WithEncode(encode Encode) {
	s.Encode = encode
}

// WithSchema sets the schema configuration.
func (s *Statement) WithSchema(schema *SchemaConfig) {
	s.Schema = schema
}

// ToSQL converts the entire AST to a SQL expression tree.
func (s *Statement) ToSQL() querystring_parser.Expr {
	if s.root != nil {
		return s.root.ToSQL()
	}
	return nil
}

// ToES converts the entire AST to an Elasticsearch query object.
func (s *Statement) ToES() elastic.Query {
	if s.root != nil {
		return s.root.ToES()
	}
	return nil
}

// Error returns any collected parsing errors.
func (s *Statement) Error() error {
	if len(s.errNode) > 0 {
		return fmt.Errorf("parse errors: %s", strings.Join(s.errNode, "; "))
	}
	return nil
}

// shouldFilterLowercaseKeyword checks for and filters out standalone keywords
// like 'and', 'or', 'not' that are not part of a larger expression.
func (s *Statement) shouldFilterLowercaseKeyword(node Node) bool {
	if fieldNode, ok := node.(*FieldNode); ok && fieldNode.field == "" {
		if valueNode, ok := fieldNode.value.(*ValueNode); ok {
			value := strings.ToLower(strings.TrimSpace(valueNode.value))
			return value == "and" || value == "or" || value == "not"
		}
	}
	return false
}

// hasRequiredModifier checks if a node is marked with a '+' (must).
func (s *Statement) hasRequiredModifier(node Node) bool {
	_, isRequiredNode := node.(*RequiredNode)
	return isRequiredNode
}

// hasNotModifier checks if a node is marked with a '-' or 'NOT' (must not).
func (s *Statement) hasNotModifier(node Node) bool {
	_, isNotNode := node.(*NotNode)
	return isNotNode
}

// unwrapModifier returns the child node from within a RequiredNode.
func (s *Statement) unwrapModifier(node Node) Node {
	if requiredNode, ok := node.(*RequiredNode); ok {
		return requiredNode.child
	}
	return node
}

// unwrapNotModifier returns the child node from within a NotNode.
func (s *Statement) unwrapNotModifier(node Node) Node {
	if notNode, ok := node.(*NotNode); ok {
		return notNode.child
	}
	return node
}

// buildMixedQuery constructs the appropriate boolean node (AndNode, OrNode, or BoolNode)
// based on the combination of must, must_not, and should clauses found.
func (s *Statement) buildMixedQuery(mustClauses, mustNotClauses, shouldClauses []Node) Node {
	// Simplify if only one type of clause exists.
	if len(mustClauses) > 0 && len(mustNotClauses) == 0 && len(shouldClauses) == 0 {
		if len(mustClauses) == 1 {
			return mustClauses[0]
		}
		return &AndNode{children: mustClauses}
	}
	if len(mustClauses) == 0 && len(mustNotClauses) > 0 && len(shouldClauses) == 0 {
		var nottedChildren []Node
		for _, child := range mustNotClauses {
			nottedChildren = append(nottedChildren, &NotNode{child: child})
		}
		if len(nottedChildren) == 1 {
			return nottedChildren[0]
		}
		return &AndNode{children: nottedChildren}
	}
	if len(mustClauses) == 0 && len(mustNotClauses) == 0 && len(shouldClauses) > 0 {
		if len(shouldClauses) == 1 {
			return shouldClauses[0]
		}
		return &OrNode{children: shouldClauses}
	}

	// For mixed types, use the comprehensive BoolNode.
	return &BoolNode{
		MustClauses:    mustClauses,
		MustNotClauses: mustNotClauses,
		ShouldClauses:  shouldClauses,
	}
}

// VisitErrorNode is called by ANTLR when a syntax error is encountered.
func (s *Statement) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
	s.errNode = append(s.errNode, ctx.GetText())
	return nil
}

// VisitTopLevelQuery is the entry point for visiting the parse tree.
// It simply traverses down to the main query rule.
func (s *Statement) VisitTopLevelQuery(ctx *gen.TopLevelQueryContext) interface{} {
	topQuery := ctx.Query()
	if topQuery != nil {
		s.root = topQuery.Accept(s).(Node)
	}
	return s.root
}

// VisitQuery handles the top-level logic of a query, which can consist of
// multiple sub-queries with different modifiers (+, -). This is where the
// logic for must, must_not, and should is primarily handled.
func (s *Statement) VisitQuery(ctx *gen.QueryContext) interface{} {
	disjQueries := ctx.AllDisjQuery()
	if len(disjQueries) == 1 {
		return disjQueries[0].Accept(s).(Node)
	}

	// Separate clauses based on their modifiers.
	var mustClauses []Node
	var mustNotClauses []Node
	var shouldClauses []Node

	for _, dq := range disjQueries {
		child := dq.Accept(s).(Node)
		if s.shouldFilterLowercaseKeyword(child) {
			continue
		}

		if s.hasRequiredModifier(child) {
			mustClauses = append(mustClauses, s.unwrapModifier(child))
		} else if s.hasNotModifier(child) {
			mustNotClauses = append(mustNotClauses, s.unwrapNotModifier(child))
		} else {
			shouldClauses = append(shouldClauses, child)
		}
	}

	return s.buildMixedQuery(mustClauses, mustNotClauses, shouldClauses)
}

// VisitDisjQuery handles OR logic. A DisjQuery is one or more ConjQuery nodes
// joined by the OR operator.
func (s *Statement) VisitDisjQuery(ctx *gen.DisjQueryContext) interface{} {
	conjQueries := ctx.AllConjQuery()
	if len(conjQueries) == 1 {
		return conjQueries[0].Accept(s).(Node)
	}

	orNode := &OrNode{}
	for _, cq := range conjQueries {
		child := cq.Accept(s).(Node)
		orNode.children = append(orNode.children, child)
	}
	return orNode
}

// VisitConjQuery handles AND logic. A ConjQuery is one or more ModClause nodes
// joined by the AND operator. This reflects the higher precedence of AND over OR.
func (s *Statement) VisitConjQuery(ctx *gen.ConjQueryContext) interface{} {
	modClauses := ctx.AllModClause()
	if len(modClauses) == 1 {
		return modClauses[0].Accept(s).(Node)
	}

	andNode := &AndNode{}
	for _, mc := range modClauses {
		child := mc.Accept(s).(Node)
		andNode.children = append(andNode.children, child)
	}
	return andNode
}

// VisitModClause handles modifiers (+, -, NOT) attached to a clause.
// It wraps the clause node in a special node (RequiredNode, NotNode) to preserve the modifier's intent.
func (s *Statement) VisitModClause(ctx *gen.ModClauseContext) interface{} {
	clause := ctx.Clause().Accept(s).(Node)

	if modifier := ctx.Modifier(); modifier != nil {
		modText := modifier.GetText()
		switch modText {
		case "+":
			return &RequiredNode{child: clause}
		case "-":
			return &NotNode{child: clause}
		case "NOT":
			return &NotNode{child: clause}
		}
	}

	return clause
}

// VisitClause handles the fundamental building block of a query: a field-value pair,
// a range expression, or a grouped expression.
func (s *Statement) VisitClause(ctx *gen.ClauseContext) interface{} {
	if ctx.FieldRangeExpr() != nil {
		return ctx.FieldRangeExpr().Accept(s).(Node)
	}

	fieldName := s.extractFieldName(ctx)

	// Special handling for complex grouped expressions like `field:(a AND (b OR c))`.
	// This requires transforming the boolean logic inside the group.
	groupingExpr := ctx.GroupingExpr()
	if fieldName != "" && groupingExpr != nil {
		childNode := groupingExpr.Accept(s).(Node)
		// convertNodeToConditionExpr will expand the boolean logic.
		if conditionExpr := s.convertNodeToConditionExpr(childNode); conditionExpr != nil {
			conditionFieldNode := &ConditionFieldNode{
				field:      fieldName,
				conditions: conditionExpr,
			}
			conditionFieldNode.WithSchema(s.Schema)
			return conditionFieldNode
		}
	}

	fieldNode := &FieldNode{
		field:  fieldName,
		value:  s.extractFieldValue(ctx),
		encode: s.Encode,
	}
	fieldNode.WithSchema(s.Schema)
	return fieldNode
}

// extractFieldName is a helper to get the field name text from a clause.
func (s *Statement) extractFieldName(ctx *gen.ClauseContext) string {
	if ctx.FieldName() == nil {
		return ""
	}

	fieldName := ctx.FieldName().GetText()
	if s.Encode != nil {
		if encoded, ok := s.Encode(fieldName); ok {
			return encoded
		}
	}
	return fieldName
}

// extractFieldValue is a helper to get the value node (term, group, etc.) from a clause.
func (s *Statement) extractFieldValue(ctx *gen.ClauseContext) Node {
	if term := ctx.Term(); term != nil {
		return s.processTermNode(term)
	}

	if groupingExpr := ctx.GroupingExpr(); groupingExpr != nil {
		return s.processGroupingNode(groupingExpr)
	}

	return &ValueNode{value: ""}
}

// processTermNode is a helper to visit a term node.
func (s *Statement) processTermNode(term antlr.ParseTree) Node {
	if result := term.Accept(s); result != nil {
		if node, ok := result.(Node); ok {
			return node
		}
	}
	return &ValueNode{value: ""}
}

// processGroupingNode is a helper to visit a grouping node.
func (s *Statement) processGroupingNode(groupingExpr antlr.ParseTree) Node {
	if result := groupingExpr.Accept(s); result != nil {
		if node, ok := result.(Node); ok {
			return node
		}
	}
	return &GroupNode{child: &ValueNode{value: ""}}
}

// VisitFieldRangeExpr handles simple range expressions like `field > 10`.
func (s *Statement) VisitFieldRangeExpr(ctx *gen.FieldRangeExprContext) interface{} {
	fieldName := ctx.FieldName().GetText()
	if s.Encode != nil {
		if encoded, ok := s.Encode(fieldName); ok {
			fieldName = encoded
		}
	}

	op := ctx.GetChild(1).(*antlr.TerminalNodeImpl).GetText()
	value := ctx.GetChild(2).(*antlr.TerminalNodeImpl).GetText()

	node := &RangeNode{
		field:  fieldName,
		op:     op,
		value:  value,
		encode: s.Encode,
	}
	node.WithSchema(s.Schema)
	return node
}

// VisitTerm handles a single term, which could be a simple word, a quoted phrase,
// a regex, a number, or a bracketed range expression.
func (s *Statement) VisitTerm(ctx *gen.TermContext) interface{} {
	if quoted := ctx.QuotedTerm(); quoted != nil {
		return quoted.Accept(s).(Node)
	}
	if regex := ctx.REGEXPTERM(); regex != nil {
		return &ValueNode{value: regex.GetText(), isRegex: true}
	}
	if termRange := ctx.TermRangeExpr(); termRange != nil {
		return s.VisitTermRangeExpr(termRange.(*gen.TermRangeExprContext))
	}
	if number := ctx.NUMBER(0); number != nil {
		return &ValueNode{value: number.GetText(), isNumber: true}
	}

	if term := ctx.TERM(); term != nil {
		return &ValueNode{value: unescapeString(term.GetText())}
	}
	return &ValueNode{value: ""}
}

// VisitTermRangeExpr handles bracketed range expressions like `[10 TO 20}`.
func (s *Statement) VisitTermRangeExpr(ctx *gen.TermRangeExprContext) interface{} {
	children := ctx.GetChildren()
	if len(children) < 5 {
		return &ValueNode{value: ""}
	}

	params := s.parseRangeParams(children)

	node := &RangeNode{
		startInclusive: params.startInclusive,
		endInclusive:   params.endInclusive,
		encode:         s.Encode,
	}

	// Only set start/end if they are not unbounded ('*').
	if params.start != WildcardAsterisk {
		node.start = &params.start
	}
	if params.end != WildcardAsterisk {
		node.end = &params.end
	} else {
		// Even if end is *, we still need to track the original inclusivity
		// for proper ES query generation
		node.hasUnboundedEnd = true
	}

	node.WithSchema(s.Schema)
	return node
}

// rangeParams is a temporary struct to hold parsed range information.
type rangeParams struct {
	start          string
	end            string
	startInclusive bool
	endInclusive   bool
}

// parseRangeParams extracts the start, end, and inclusivity from a range expression's tokens.
func (s *Statement) parseRangeParams(children []antlr.Tree) *rangeParams {
	params := &rangeParams{}
	for i, child := range children {
		if termNode, ok := child.(*antlr.TerminalNodeImpl); ok {
			text := termNode.GetSymbol().GetText()
			// Debug output - remove after debugging
			fmt.Printf("Child %d: '%s'\n", i, text)
			s.processRangeChild(params, i, len(children), text)
		}
	}
	params.start = s.cleanAndUnescapeValue(params.start)
	params.end = s.cleanAndUnescapeValue(params.end)

	return params
}

// processRangeChild sets the range parameters based on the token's text and position.
func (s *Statement) processRangeChild(params *rangeParams, index, totalChildren int, text string) {
	switch index {
	case 0:
		// Check if the opening bracket is '[' for inclusive.
		params.startInclusive = text == "["
	case 1:
		params.start = text
	case 3:
		params.end = text
	}

	// Check the last child to determine end inclusivity.
	if index == totalChildren-1 {
		// ']' means inclusive, '}' means exclusive
		params.endInclusive = text == "]"
		fmt.Printf("Setting endInclusive: text='%s', endInclusive=%t\n", text, params.endInclusive)
	}
}

// cleanAndUnescapeValue removes surrounding quotes and unescapes characters.
func (s *Statement) cleanAndUnescapeValue(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return unescapeString(value)
}

// buildRangeText is a helper function (currently unused) to reconstruct a range string.
func (s *Statement) buildRangeText(params *rangeParams) string {
	startBracket := s.getBracket(params.startInclusive, true)
	endBracket := s.getBracket(params.endInclusive, false)
	return fmt.Sprintf("%s%s TO %s%s", startBracket, params.start, params.end, endBracket)
}

// getBracket returns the correct bracket character based on inclusivity.
func (s *Statement) getBracket(inclusive, isStart bool) string {
	if inclusive {
		if isStart {
			return "["
		}
		return "]"
	}
	if isStart {
		return "{"
	}
	return "}"
}

// VisitGroupingExpr handles expressions inside parentheses.
func (s *Statement) VisitGroupingExpr(ctx *gen.GroupingExprContext) interface{} {
	query := ctx.Query()
	if query != nil {
		if result := query.Accept(s); result != nil {
			if child, ok := result.(Node); ok {
				return &GroupNode{child: child}
			}
		}
	}
	return &GroupNode{child: &ValueNode{value: ""}}
}

// convertNodeToConditionExpr is a key function that expands boolean logic
// for use in ConditionFieldNode.
func (s *Statement) convertNodeToConditionExpr(node Node) *querystring_parser.ConditionExpr {
	sqlExpr := node.ToSQL()
	if sqlExpr == nil {
		return nil
	}
	return s.buildConditionExprFromSQL(sqlExpr)
}

// buildConditionExprFromSQL recursively builds the condition expression from a SQL expression tree.
func (s *Statement) buildConditionExprFromSQL(expr querystring_parser.Expr) *querystring_parser.ConditionExpr {
	switch e := expr.(type) {
	case *querystring_parser.MatchExpr:
		return nil // Simple matches are handled by FieldNode.
	case *querystring_parser.OrExpr:
		return s.buildOrCondition(e)
	case *querystring_parser.AndExpr:
		return s.buildAndCondition(e)
	default:
		return nil
	}
}

// buildOrCondition handles OR logic in the condition expression tree.
func (s *Statement) buildOrCondition(orExpr *querystring_parser.OrExpr) *querystring_parser.ConditionExpr {
	condition := &querystring_parser.ConditionExpr{Values: [][]string{}}
	condition.Values = s.decomposeOrExpression(orExpr)
	if len(condition.Values) == 0 {
		return nil
	}
	return condition
}

// buildAndCondition handles AND logic by triggering the Cartesian product calculation.
func (s *Statement) buildAndCondition(andExpr *querystring_parser.AndExpr) *querystring_parser.ConditionExpr {
	conditionGroups := s.calculateCartesianProduct(andExpr)
	if len(conditionGroups) == 0 {
		return nil
	}
	return &querystring_parser.ConditionExpr{Values: conditionGroups}
}

// decomposeOrExpression breaks down a complex expression into a list of OR-groups.
func (s *Statement) decomposeOrExpression(expr querystring_parser.Expr) [][]string {
	switch e := expr.(type) {
	case *querystring_parser.OrExpr:
		// For OR, simply concatenate the results from both sides.
		return append(s.decomposeOrExpression(e.Left), s.decomposeOrExpression(e.Right)...)
	case *querystring_parser.AndExpr:
		// For AND, we must calculate the Cartesian product.
		return s.calculateCartesianProduct(e)
	case *querystring_parser.MatchExpr:
		// The base case: a single value is a group of one.
		return [][]string{{e.Value}}
	default:
		return [][]string{}
	}
}

// calculateCartesianProduct applies the distributive law to expand boolean expressions.
// For example, (a OR b) AND (c OR d) becomes (a AND c) OR (a AND d) OR (b AND c) OR (b AND d).
func (s *Statement) calculateCartesianProduct(andExpr *querystring_parser.AndExpr) [][]string {
	leftOptions := s.decomposeOrExpression(andExpr.Left)
	rightOptions := s.decomposeOrExpression(andExpr.Right)
	if len(leftOptions) == 0 || len(rightOptions) == 0 {
		return [][]string{}
	}

	var result [][]string
	// Create all possible combinations of left and right groups.
	for _, leftGroup := range leftOptions {
		for _, rightGroup := range rightOptions {
			combined := make([]string, 0, len(leftGroup)+len(rightGroup))
			combined = append(combined, leftGroup...)
			combined = append(combined, rightGroup...)
			result = append(result, combined)
		}
	}

	return result
}

// VisitQuotedTerm handles quoted strings, unescaping their content.
func (s *Statement) VisitQuotedTerm(ctx *gen.QuotedTermContext) interface{} {
	if quoted := ctx.QUOTED(); quoted != nil {
		text := quoted.GetText()
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			content := text[1 : len(text)-1]
			return &ValueNode{value: unescapeString(content), isQuoted: true}
		}
		return &ValueNode{value: unescapeString(text), isQuoted: true}
	}
	return &ValueNode{value: ""}
}

// FieldNode represents a field-value pair, e.g., `status:active`.
type FieldNode struct {
	baseNode
	field  string
	value  Node
	encode Encode
}

// ToSQL converts the FieldNode to a SQL expression.
func (n *FieldNode) ToSQL() querystring_parser.Expr {
	switch node := n.value.(type) {
	case *ValueNode:
		return n.handleValueNodeSQL(node)
	case *RangeNode:
		return n.handleRangeNodeSQL(node)
	case *GroupNode:
		return n.handleGroupNode(node)
	default:
		return nil
	}
}

// handleValueNodeSQL determines the correct SQL expression type for a value (match, regex, wildcard).
func (n *FieldNode) handleValueNodeSQL(valNode *ValueNode) querystring_parser.Expr {
	if valNode.isRegex {
		return n.createRegexExpr(valNode)
	}
	if n.containsWildcards(valNode.ToSQLString()) {
		return n.createWildcardExpr(valNode)
	}
	return n.createMatchExpr(valNode)
}

// handleRangeNodeSQL delegates SQL conversion to the RangeNode.
func (n *FieldNode) handleRangeNodeSQL(rangeNode *RangeNode) querystring_parser.Expr {
	rangeNode.field = n.field
	expr := rangeNode.ToSQL()
	if expr == nil {
		return nil
	}
	if fieldSetter, ok := expr.(interface{ SetField(string) }); ok && n.field != "" {
		fieldSetter.SetField(n.field)
	}
	return expr
}

func (n *FieldNode) createRegexExpr(valNode *ValueNode) querystring_parser.Expr {
	regexValue := strings.Trim(valNode.value, "/")
	expr := querystring_parser.NewRegexpExpr(regexValue)
	expr.SetField(n.field)
	return expr
}

func (n *FieldNode) createWildcardExpr(valNode *ValueNode) querystring_parser.Expr {
	value := valNode.ToSQLString()
	expr := querystring_parser.NewWildcardExpr(value)
	if n.field != "" {
		expr.SetField(n.field)
	}
	return expr
}

func (n *FieldNode) createMatchExpr(valNode *ValueNode) querystring_parser.Expr {
	value := valNode.ToSQLString()
	expr := querystring_parser.NewMatchExpr(value)
	if n.field != "" {
		expr.SetField(n.field)
	}
	return expr
}

func (n *FieldNode) containsWildcards(value string) bool {
	return strings.Contains(value, WildcardAsterisk) || strings.Contains(value, WildcardQuestion)
}

// handleGroupNode handles SQL conversion for a grouped expression, propagating the field name down.
func (n *FieldNode) handleGroupNode(node *GroupNode) querystring_parser.Expr {
	childExpr := node.ToSQL()
	if childExpr == nil {
		return nil
	}
	if n.field == "" {
		return childExpr
	}
	if matchExpr, ok := childExpr.(*querystring_parser.MatchExpr); ok {
		newExpr := querystring_parser.NewMatchExpr(matchExpr.Value)
		newExpr.SetField(n.field)
		return newExpr
	}
	if fieldSetter, ok := childExpr.(interface{ SetField(string) }); ok {
		fieldSetter.SetField(n.field)
	}
	return childExpr
}

// looksLikeDate is a simple heuristic to guess if a string might be a date.
func looksLikeDate(s string) bool {
	if s == WildcardAsterisk {
		return false
	}
	return strings.Contains(s, DateSeparatorHyphen) ||
		strings.Contains(s, DateSeparatorT) || strings.Contains(s, DateSeparatorColon)
}

// ToES converts the FieldNode to an Elasticsearch query.
func (n *FieldNode) ToES() elastic.Query {
	if rangeNode, ok := n.value.(*RangeNode); ok {
		return rangeNode.ToESForField(n.field)
	}
	if valNode, ok := n.value.(*ValueNode); ok {
		return n.buildValueQuery(valNode)
	}
	if groupNode, ok := n.value.(*GroupNode); ok {
		return n.buildGroupQuery(groupNode)
	}
	return elastic.NewTermQuery(n.field, "")
}

// buildValueQuery selects the appropriate ES query type based on the value's properties and the schema.
func (n *FieldNode) buildValueQuery(valNode *ValueNode) elastic.Query {
	// If no field is specified, use a general query_string query.
	if n.field == "" {
		return n.buildGlobalQuery(valNode)
	}

	switch {
	case valNode.isRegex:
		return n.buildRegexQuery(valNode)
	case valNode.isQuoted:
		return n.buildPhraseQuery(valNode)
	case n.isWildcardValue(valNode.value):
		return elastic.NewWildcardQuery(n.field, valNode.value)
	}

	// Use schema information to generate a more precise query.
	if fieldType, exist := n.Schema.GetFieldType(n.field); exist {
		return n.buildQueryByFieldType(fieldType, valNode)
	}

	return n.buildFallbackQuery(valNode)
}

// buildQueryByFieldType generates an ES query based on the field's mapped type.
func (n *FieldNode) buildQueryByFieldType(fieldType string, valNode *ValueNode) elastic.Query {
	switch fieldType {
	case TypeKeyword:
		return elastic.NewTermQuery(n.field, valNode.value)
	case TypeText:
		return elastic.NewMatchQuery(n.field, valNode.value)
	case TypeInteger, TypeLong:
		if num, err := strconv.ParseFloat(valNode.value, 64); err == nil {
			return elastic.NewTermQuery(n.field, num)
		}
		return elastic.NewTermQuery(n.field, valNode.value)
	case TypeDate:
		return elastic.NewTermQuery(n.field, valNode.value)
	default:
		return elastic.NewTermQuery(n.field, valNode.value)
	}
}

// fallback query when no schema mapping is available.
func (n *FieldNode) buildFallbackQuery(valNode *ValueNode) elastic.Query {
	if valNode.isNumber {
		return n.buildNumericQuery(valNode)
	}
	return elastic.NewTermQuery(n.field, valNode.value)
}

// buildGlobalQuery creates a query_string query for field-less searches.
func (n *FieldNode) buildGlobalQuery(valNode *ValueNode) elastic.Query {
	if valNode.isQuoted {
		cleaned := n.cleanQuotes(valNode.value)
		return elastic.NewQueryStringQuery(fmt.Sprintf("\"%s\"", cleaned))
	}
	return elastic.NewQueryStringQuery(valNode.value)
}

func (n *FieldNode) buildRegexQuery(valNode *ValueNode) elastic.Query {
	regexValue := strings.Trim(valNode.value, "/")
	return elastic.NewRegexpQuery(n.field, regexValue)
}

func (n *FieldNode) buildPhraseQuery(valNode *ValueNode) elastic.Query {
	cleaned := n.cleanQuotes(valNode.value)
	if n.isWildcardValue(cleaned) {
		return elastic.NewWildcardQuery(n.field, cleaned)
	}
	return elastic.NewMatchPhraseQuery(n.field, cleaned)
}

func (n *FieldNode) buildNumericQuery(valNode *ValueNode) elastic.Query {
	if num, err := strconv.ParseFloat(valNode.value, 64); err == nil {
		return elastic.NewTermQuery(n.field, num)
	}
	return elastic.NewTermQuery(n.field, valNode.value)
}

func (n *FieldNode) isWildcardValue(value string) bool {
	return strings.Contains(value, "*") || strings.Contains(value, "?")
}

func (n *FieldNode) cleanQuotes(value string) string {
	return strings.Trim(value, `"'`)
}

// buildGroupQuery handles ES conversion for a grouped expression.
func (n *FieldNode) buildGroupQuery(groupNode *GroupNode) elastic.Query {
	if groupNode.child == nil {
		return elastic.NewTermQuery(n.field, "")
	}
	return n.buildChildQueryWithField(groupNode.child)
}

// buildChildQueryWithField recursively builds the query for a child node,
// propagating the parent's field name if the child doesn't have its own.
func (n *FieldNode) buildChildQueryWithField(child Node) elastic.Query {
	if child == nil {
		return nil
	}

	switch node := child.(type) {
	case *FieldNode:
		if node.field == "" {
			childCopy := *node
			childCopy.field = n.field
			childCopy.Schema = n.Schema
			return childCopy.ToES()
		}
		return node.ToES()
	case *ValueNode:
		return n.buildValueQuery(node)
	case *GroupNode:
		return n.buildGroupQuery(node)
	default:
		return child.ToES()
	}
}

// ConditionFieldNode is a special node for fields with complex boolean conditions,
// resulting from the expansion done by calculateCartesianProduct.
type ConditionFieldNode struct {
	baseNode
	field      string
	conditions *querystring_parser.ConditionExpr
}

// ToSQL converts the ConditionFieldNode to its SQL representation.
func (n *ConditionFieldNode) ToSQL() querystring_parser.Expr {
	return &querystring_parser.ConditionMatchExpr{
		Field: n.field,
		Value: n.conditions,
	}
}

// ToES converts the expanded boolean logic into a nested ES bool query.
func (n *ConditionFieldNode) ToES() elastic.Query {
	if n.conditions == nil || len(n.conditions.Values) == 0 {
		return elastic.NewTermQuery(n.field, "")
	}

	// Optimization: If it's a simple list of ORs for a keyword field, use a `terms` query.
	canUseTerms := true
	values := make([]interface{}, 0, len(n.conditions.Values))
	for _, group := range n.conditions.Values {
		if len(group) != 1 {
			canUseTerms = false
			break
		}
		values = append(values, group[0])
	}
	if canUseTerms && len(values) > 1 && n.Schema != nil {
		if fieldType, exists := n.Schema.GetFieldType(n.field); exists && fieldType == TypeKeyword {
			return elastic.NewTermsQuery(n.field, values...)
		}
	}

	// Helper to create the right query type based on schema.
	createQueryForItem := func(value string) elastic.Query {
		if n.Schema != nil {
			if fieldType, exists := n.Schema.GetFieldType(n.field); exists && fieldType == TypeText {
				return elastic.NewMatchPhraseQuery(n.field, value)
			}
		}
		return elastic.NewTermQuery(n.field, value)
	}

	// Build the bool query from the expanded groups.
	shouldQueries := make([]elastic.Query, 0, len(n.conditions.Values))
	for _, group := range n.conditions.Values {
		if len(group) == 1 { // A single item is a direct should clause.
			shouldQueries = append(shouldQueries, createQueryForItem(group[0]))
		} else if len(group) > 1 { // A group of items represents an inner AND.
			mustQueries := make([]elastic.Query, len(group))
			for i, val := range group {
				mustQueries[i] = createQueryForItem(val)
			}
			shouldQueries = append(shouldQueries, elastic.NewBoolQuery().Must(mustQueries...))
		}
	}

	if len(shouldQueries) == 1 {
		return shouldQueries[0]
	}

	return elastic.NewBoolQuery().Should(shouldQueries...).MinimumShouldMatch(strconv.Itoa(1))
}

// RangeNode represents any range or comparison query.
type RangeNode struct {
	baseNode
	field           string
	start           *string
	end             *string
	startInclusive  bool
	endInclusive    bool
	hasUnboundedEnd bool // tracks if original end was '*' but we still need endInclusive
	op              string
	value           string
	encode          Encode
}

// ToSQL converts the RangeNode to a SQL range expression.
func (n *RangeNode) ToSQL() querystring_parser.Expr {
	if n.op != "" {
		return n.buildComparisonExpr()
	}

	if n.start != nil || n.end != nil {
		startPtr, endPtr := n.start, n.end

		if startPtr == nil {
			wildcard := WildcardAsterisk
			startPtr = &wildcard
		}
		if endPtr == nil {
			wildcard := WildcardAsterisk
			endPtr = &wildcard
		}

		if n.isDateRange() {
			expr := querystring_parser.NewTimeRangeExpr(
				startPtr, endPtr,
				n.startInclusive, n.endInclusive)
			if n.field != "" {
				expr.SetField(n.field)
			}
			return expr
		}
		expr := querystring_parser.NewNumberRangeExpr(
			startPtr, endPtr,
			n.startInclusive, n.endInclusive)
		if n.field != "" {
			expr.SetField(n.field)
		}
		return expr
	}

	return nil
}

// isDateRange checks if the range boundaries look like dates.
func (n *RangeNode) isDateRange() bool {
	if n.start != nil && looksLikeDate(*n.start) {
		return true
	}
	if n.end != nil && looksLikeDate(*n.end) {
		return true
	}
	return false
}

// buildComparisonExpr creates a SQL range expression from simple operators.
func (n *RangeNode) buildComparisonExpr() querystring_parser.Expr {
	switch n.op {
	case ">":
		return &querystring_parser.NumberRangeExpr{Field: n.field, Start: &n.value, IncludeStart: false}
	case "<":
		return &querystring_parser.NumberRangeExpr{Field: n.field, End: &n.value, IncludeEnd: false}
	case ">=":
		return &querystring_parser.NumberRangeExpr{Field: n.field, Start: &n.value, IncludeStart: true}
	case "<=":
		return &querystring_parser.NumberRangeExpr{Field: n.field, End: &n.value, IncludeEnd: true}
	}
	return nil
}

// ToES converts the RangeNode to an Elasticsearch query.
func (n *RangeNode) ToES() elastic.Query {
	if n.field != "" {
		return n.ToESForField(n.field)
	}
	return nil
}

// ToESForField builds the ES range query for a specific field.
func (n *RangeNode) ToESForField(field string) elastic.Query {
	if n.op != "" {
		return n.buildComparisonQueryES(field)
	}

	if n.start != nil || n.end != nil {
		query := elastic.NewRangeQuery(field)

		shouldUseNumeric := false
		if n.Schema != nil {
			if fieldType, exists := n.Schema.GetFieldType(field); exists && (fieldType == TypeLong || fieldType == TypeInteger) {
				shouldUseNumeric = true
			}
		}

		if n.start != nil {
			var startVal interface{} = *n.start
			if shouldUseNumeric && *n.start != WildcardAsterisk {
				if num, err := strconv.ParseFloat(*n.start, 64); err == nil {
					startVal = num
				}
			}
			if n.startInclusive {
				query.Gte(startVal)
			} else {
				query.Gt(startVal)
			}
		}
		if n.end != nil {
			var endVal interface{} = *n.end
			if shouldUseNumeric && *n.end != WildcardAsterisk {
				if num, err := strconv.ParseFloat(*n.end, 64); err == nil {
					endVal = num
				}
			}
			fmt.Printf("ES Query build: endInclusive=%t, endVal=%v\n", n.endInclusive, endVal)
			if n.endInclusive {
				query.Lte(endVal)
			} else {
				query.Lt(endVal)
			}
		} else if n.hasUnboundedEnd {
			if n.endInclusive {
				query.Lte(nil)
			} else {
				query.Lt(nil)
			}
		}
		return query
	}

	return elastic.NewTermQuery(field, n.value)
}

// buildComparisonQueryES builds an ES range query from simple operators.
func (n *RangeNode) buildComparisonQueryES(field string) elastic.Query {
	query := elastic.NewRangeQuery(field)

	var compVal interface{} = n.value
	if n.Schema != nil {
		if fieldType, exists := n.Schema.GetFieldType(field); exists && (fieldType == TypeLong || fieldType == TypeInteger) {
			if num, err := strconv.ParseFloat(n.value, 64); err == nil {
				compVal = num
			}
		}
	}

	switch n.op {
	case ">":
		return query.Gt(compVal)
	case "<":
		return query.Lt(compVal)
	case ">=":
		return query.Gte(compVal)
	case "<=":
		return query.Lte(compVal)
	default:
		return query
	}
}

// ValueNode represents a literal value (string, number, etc.).
type ValueNode struct {
	baseNode
	value    string
	isQuoted bool
	isRegex  bool
	isNumber bool
}

// ToSQL converts the value to its SQL expression representation.
func (n *ValueNode) ToSQL() querystring_parser.Expr {
	value := n.ToSQLString()
	if n.isRegex {
		return querystring_parser.NewRegexpExpr(strings.Trim(n.value, "/"))
	}
	if strings.Contains(value, WildcardAsterisk) || strings.Contains(value, WildcardQuestion) {
		return querystring_parser.NewWildcardExpr(value)
	}
	return querystring_parser.NewMatchExpr(value)
}

// ToSQLString returns the raw string value, with quotes removed.
func (n *ValueNode) ToSQLString() string {
	if n.isQuoted {
		return strings.Trim(n.value, `"'`)
	}
	return n.value
}

// ToES converts the ValueNode to an ES query.
func (n *ValueNode) ToES() elastic.Query {
	if n.isRegex {
		// Regex without a field is not directly supported in this context; handled by FieldNode.
		return nil
	}
	if n.isQuoted {
		cleaned := strings.Trim(n.value, `"'`)
		return elastic.NewQueryStringQuery(fmt.Sprintf("\"%s\"", cleaned))
	}
	return elastic.NewQueryStringQuery(n.value)
}

// AndNode represents a list of clauses joined by AND.
type AndNode struct {
	baseNode
	children []Node
}

// ToSQL combines child nodes with AND expressions.
func (n *AndNode) ToSQL() querystring_parser.Expr {
	if len(n.children) == 0 {
		return nil
	}
	if len(n.children) == 1 {
		return n.children[0].ToSQL()
	}

	result := n.children[0].ToSQL()
	for i := 1; i < len(n.children); i++ {
		if child := n.children[i].ToSQL(); child != nil {
			result = querystring_parser.NewAndExpr(result, child)
		}
	}
	return result
}

// ToES combines child nodes into an ES bool query with `must` clauses.
func (n *AndNode) ToES() elastic.Query {
	var queries []elastic.Query
	for _, child := range n.children {
		if childES := child.ToES(); childES != nil {
			queries = append(queries, childES)
		}
	}
	if len(queries) == 0 {
		return nil
	}
	if len(queries) == 1 {
		return queries[0]
	}
	return elastic.NewBoolQuery().Must(queries...)
}

// OrNode represents a list of clauses joined by OR.
type OrNode struct {
	baseNode
	children []Node
}

// ToSQL combines child nodes with OR expressions.
func (n *OrNode) ToSQL() querystring_parser.Expr {
	if len(n.children) == 0 {
		return nil
	}
	if len(n.children) == 1 {
		return n.children[0].ToSQL()
	}

	result := n.children[0].ToSQL()
	for i := 1; i < len(n.children); i++ {
		if child := n.children[i].ToSQL(); child != nil {
			result = querystring_parser.NewOrExpr(result, child)
		}
	}
	return result
}

// ToES combines child nodes into an ES bool query with `should` clauses.
func (n *OrNode) ToES() elastic.Query {
	if termsQuery := n.tryOptimizeToTermsQuery(); termsQuery != nil {
		return termsQuery
	}

	var queries []elastic.Query
	for _, child := range n.children {
		if childES := child.ToES(); childES != nil {
			queries = append(queries, childES)
		}
	}

	switch len(queries) {
	case 0:
		return nil
	case 1:
		return queries[0]
	default:
		return elastic.NewBoolQuery().Should(queries...)
	}
}

// tryOptimizeToTermsQuery attempts to convert a simple OR of terms on the same keyword field
// into a more efficient ES `terms` query.
func (n *OrNode) tryOptimizeToTermsQuery() elastic.Query {
	if len(n.children) < 2 {
		return nil
	}

	var (
		fieldName     string
		values        []interface{}
		schema        *SchemaConfig
		allFieldNodes = true
	)

	for i, child := range n.children {
		fieldNode, ok := child.(*FieldNode)
		if !ok {
			allFieldNodes = false
			break
		}
		valueNode, ok := fieldNode.value.(*ValueNode)
		if !ok {
			allFieldNodes = false
			break
		}
		if i == 0 {
			fieldName = fieldNode.field
			schema = fieldNode.Schema
		} else if fieldNode.field != fieldName {
			allFieldNodes = false
			break
		}

		// Optimization only applies to simple, non-wildcard, non-regex terms.
		if !valueNode.isRegex && !valueNode.isQuoted && !fieldNode.isWildcardValue(valueNode.value) {
			values = append(values, valueNode.value)
		} else {
			allFieldNodes = false
			break
		}
	}

	if allFieldNodes && fieldName != "" && len(values) > 1 {
		if fieldType, exist := schema.GetFieldType(fieldName); exist {
			if fieldType == TypeKeyword {
				return elastic.NewTermsQuery(fieldName, values...)
			}
		}
	}

	return nil
}

// NotNode represents a negated clause.
type NotNode struct {
	baseNode
	child Node
}

// BoolNode represents a complex boolean query with a mix of must, must_not, and should clauses.
type BoolNode struct {
	baseNode
	MustClauses    []Node
	MustNotClauses []Node
	ShouldClauses  []Node
}

// ToSQL translates the boolean logic into a SQL WHERE clause.
// Note: This translation is a compromise, as SQL lacks a native "should" for scoring.
// Here, `should` is combined with `must` using AND, prioritizing precision.
func (n *BoolNode) ToSQL() querystring_parser.Expr {
	var mustExprs []querystring_parser.Expr
	var finalExpr querystring_parser.Expr

	if len(n.MustClauses) > 0 {
		mustAndExpr := n.MustClauses[0].ToSQL()
		for i := 1; i < len(n.MustClauses); i++ {
			mustAndExpr = querystring_parser.NewAndExpr(mustAndExpr, n.MustClauses[i].ToSQL())
		}
		mustExprs = append(mustExprs, mustAndExpr)
	}

	for _, node := range n.MustNotClauses {
		mustExprs = append(mustExprs, querystring_parser.NewNotExpr(node.ToSQL()))
	}

	var shouldOrExpr querystring_parser.Expr
	if len(n.ShouldClauses) > 0 {
		shouldOrExpr = n.ShouldClauses[0].ToSQL()
		for i := 1; i < len(n.ShouldClauses); i++ {
			shouldOrExpr = querystring_parser.NewOrExpr(shouldOrExpr, n.ShouldClauses[i].ToSQL())
		}
	}

	if len(mustExprs) > 0 {
		finalExpr = mustExprs[0]
		for i := 1; i < len(mustExprs); i++ {
			finalExpr = querystring_parser.NewAndExpr(finalExpr, mustExprs[i])
		}
	}

	if shouldOrExpr != nil {
		if finalExpr != nil {
			finalExpr = querystring_parser.NewAndExpr(finalExpr, shouldOrExpr)
		} else {
			finalExpr = shouldOrExpr
		}
	}

	return finalExpr
}

// ToES perfectly translates the must, must_not, and should logic into an ES bool query.
func (n *BoolNode) ToES() elastic.Query {
	boolQuery := elastic.NewBoolQuery()
	hasClauses := false

	// Collect all 'must' clauses into a slice first. This ensures that even a single
	// clause is rendered as a JSON array, matching test expectations.
	if len(n.MustClauses) > 0 {
		hasClauses = true
		queries := make([]elastic.Query, 0, len(n.MustClauses))
		for _, child := range n.MustClauses {
			if esQuery := child.ToES(); esQuery != nil {
				queries = append(queries, esQuery)
			}
		}
		if len(queries) > 0 {
			boolQuery.Must(queries...)
		}
	}

	if len(n.MustNotClauses) > 0 {
		hasClauses = true
		queries := make([]elastic.Query, 0, len(n.MustNotClauses))
		for _, child := range n.MustNotClauses {
			if esQuery := child.ToES(); esQuery != nil {
				queries = append(queries, esQuery)
			}
		}
		if len(queries) > 0 {
			boolQuery.MustNot(queries...)
		}
	}

	if len(n.ShouldClauses) > 0 {
		hasClauses = true
		queries := make([]elastic.Query, 0, len(n.ShouldClauses))
		for _, child := range n.ShouldClauses {
			if esQuery := child.ToES(); esQuery != nil {
				queries = append(queries, esQuery)
			}
		}
		if len(queries) > 0 {
			boolQuery.Should(queries...)
		}
	}

	if !hasClauses {
		return nil
	}
	return boolQuery
}

// RequiredNode is a wrapper node indicating a `+` modifier.
type RequiredNode struct {
	baseNode
	child Node
}

// ToSQL for a NotNode.
func (n *NotNode) ToSQL() querystring_parser.Expr {
	if child := n.child.ToSQL(); child != nil {
		return querystring_parser.NewNotExpr(child)
	}
	return nil
}

// ToES for a NotNode.
func (n *NotNode) ToES() elastic.Query {
	if childES := n.child.ToES(); childES != nil {
		return elastic.NewBoolQuery().MustNot(childES)
	}
	return nil
}

// ToSQL for a RequiredNode just passes through to its child.
// The "must" logic is handled at a higher level in the boolean query construction.
func (n *RequiredNode) ToSQL() querystring_parser.Expr {
	return n.child.ToSQL()
}

// ToES for a RequiredNode also passes through to its child.
func (n *RequiredNode) ToES() elastic.Query {
	return n.child.ToES()
}

// GroupNode represents a parenthesized expression.
type GroupNode struct {
	baseNode
	child Node
}

// ToSQL for a GroupNode simply returns its child's SQL.
func (n *GroupNode) ToSQL() querystring_parser.Expr {
	return n.child.ToSQL()
}

// ToES for a GroupNode returns its child's ES query.
func (n *GroupNode) ToES() elastic.Query {
	return n.child.ToES()
}

// unescapeString handles backslash-escaped characters in Lucene strings.
func unescapeString(s string) string {
	if s == "" {
		return s
	}

	result := make([]rune, 0, len(s))
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			// Skip the backslash and take the next character literally.
			i++
			result = append(result, runes[i])
		} else {
			result = append(result, runes[i])
		}
	}

	return string(result)
}
