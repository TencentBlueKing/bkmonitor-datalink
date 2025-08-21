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

	antlr "github.com/antlr4-go/antlr/v4"
	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring_parser"
)

const (
	WildcardAsterisk    = "*"
	WildcardQuestion    = "?"
	DateSeparatorHyphen = "-"
	DateSeparatorT      = "T"
	DateSeparatorColon  = ":"
)

type Encode func(string) (string, bool)

type Node interface {
	antlr.ParseTreeVisitor
	ToSQL() querystring_parser.Expr
	ToES() elastic.Query
	Error() error
	WithEncode(Encode)
}

type baseNode struct {
	antlr.BaseParseTreeVisitor
	Encode Encode
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

type Statement struct {
	*gen.BaseLuceneParserVisitor

	root    Node
	errNode []string
	Encode  Encode
}

func NewQueryVisitor(ctx context.Context) *Statement {
	return &Statement{
		BaseLuceneParserVisitor: &gen.BaseLuceneParserVisitor{},
	}
}

func (s *Statement) WithEncode(encode Encode) {
	s.Encode = encode
}

func (s *Statement) ToSQL() querystring_parser.Expr {
	if s.root != nil {
		return s.root.ToSQL()
	}
	return nil
}

func (s *Statement) ToES() elastic.Query {
	if s.root != nil {
		return s.root.ToES()
	}
	return nil
}

func (s *Statement) Error() error {
	if len(s.errNode) > 0 {
		return fmt.Errorf("parse errors: %s", strings.Join(s.errNode, "; "))
	}
	return nil
}

func (s *Statement) shouldFilterLowercaseKeyword(node Node) bool {
	if fieldNode, ok := node.(*FieldNode); ok && fieldNode.field == "" {
		if valueNode, ok := fieldNode.value.(*ValueNode); ok {
			value := strings.ToLower(strings.TrimSpace(valueNode.value))
			return value == "and" || value == "or" || value == "not"
		}
	}
	return false
}

func (s *Statement) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
	s.errNode = append(s.errNode, ctx.GetText())
	return nil
}

// VisitTopLevelQuery 处理顶层查询规则
// 语法规则: topLevelQuery : query EOF
func (s *Statement) VisitTopLevelQuery(ctx *gen.TopLevelQueryContext) interface{} {
	topQuery := ctx.Query()
	if topQuery != nil {
		s.root = topQuery.Accept(s).(Node)
	}
	return s.root
}

// VisitQuery 处理查询规则
// 语法规则: query : disjQuery+
func (s *Statement) VisitQuery(ctx *gen.QueryContext) interface{} {
	disjQueries := ctx.AllDisjQuery()
	if len(disjQueries) == 1 {
		return disjQueries[0].Accept(s).(Node)
	}

	orNode := &OrNode{}
	for _, dq := range disjQueries {
		child := dq.Accept(s).(Node)
		if s.shouldFilterLowercaseKeyword(child) {
			continue
		}

		orNode.children = append(orNode.children, child)
	}

	if len(orNode.children) == 1 {
		return orNode.children[0]
	}

	return orNode
}

// VisitDisjQuery 处理析取查询规则
// 语法规则: disjQuery : conjQuery (OR conjQuery)*
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

// VisitConjQuery 处理合取查询规则
// 语法规则: conjQuery : modClause (AND modClause)*
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

// VisitModClause 处理修饰子句规则
// 语法规则: modClause : modifier? clause
func (s *Statement) VisitModClause(ctx *gen.ModClauseContext) interface{} {
	clause := ctx.Clause().Accept(s).(Node)

	if modifier := ctx.Modifier(); modifier != nil {
		modText := modifier.GetText()
		switch modText {
		case "+":
			// Must include - no change needed for SQL/ES
		case "-":
			// Must exclude - wrap in NOT
			return &NotNode{child: clause}
		case "NOT":
			return &NotNode{child: clause}
		}
	}

	return clause
}

// VisitClause 处理子句规则
// 语法规则: clause : fieldRangeExpr | (fieldName (OP_COLON | OP_EQUAL))? (term | groupingExpr)
func (s *Statement) VisitClause(ctx *gen.ClauseContext) interface{} {
	if ctx.FieldRangeExpr() != nil {
		return ctx.FieldRangeExpr().Accept(s).(Node)
	}

	fieldName := s.extractFieldName(ctx)
	value := s.extractFieldValue(ctx)

	return &FieldNode{
		field:  fieldName,
		value:  value,
		encode: s.Encode,
	}
}

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

func (s *Statement) extractFieldValue(ctx *gen.ClauseContext) Node {
	if term := ctx.Term(); term != nil {
		return s.processTermNode(term)
	}

	if groupingExpr := ctx.GroupingExpr(); groupingExpr != nil {
		return s.processGroupingNode(groupingExpr)
	}

	return &ValueNode{value: ""}
}

func (s *Statement) processTermNode(term antlr.ParseTree) Node {
	if result := term.Accept(s); result != nil {
		if node, ok := result.(Node); ok {
			return node
		}
	}
	return &ValueNode{value: ""}
}

func (s *Statement) processGroupingNode(groupingExpr antlr.ParseTree) Node {
	if result := groupingExpr.Accept(s); result != nil {
		if node, ok := result.(Node); ok {
			return node
		}
	}
	return &GroupNode{child: &ValueNode{value: ""}}
}

func (s *Statement) VisitFieldRangeExpr(ctx *gen.FieldRangeExprContext) interface{} {
	fieldName := ctx.FieldName().GetText()
	if s.Encode != nil {
		if encoded, ok := s.Encode(fieldName); ok {
			fieldName = encoded
		}
	}

	op := ctx.GetChild(1).(*antlr.TerminalNodeImpl).GetText()
	value := ctx.GetChild(2).(*antlr.TerminalNodeImpl).GetText()

	return &RangeNode{
		field:  fieldName,
		op:     op,
		value:  value,
		encode: s.Encode,
	}
}

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

// VisitTermRangeExpr 处理术语范围表达式规则
// 语法规则: termRangeExpr : (RANGEIN_START | RANGEEX_START) left=(RANGE_GOOP | RANGE_QUOTED | RANGE_TO) RANGE_TO right=(RANGE_GOOP | RANGE_QUOTED | RANGE_TO) (RANGEIN_END | RANGEEX_END)
func (s *Statement) VisitTermRangeExpr(ctx *gen.TermRangeExprContext) interface{} {
	children := ctx.GetChildren()
	if len(children) < 5 {
		return &ValueNode{value: ""}
	}

	return &RangeNode{
		value:  s.buildRangeText(s.parseRangeParams(children)),
		encode: s.Encode,
	}
}

type rangeParams struct {
	start          string
	end            string
	startInclusive bool
	endInclusive   bool
}

func (s *Statement) parseRangeParams(children []antlr.Tree) *rangeParams {
	params := &rangeParams{}
	for i, child := range children {
		if termNode, ok := child.(*antlr.TerminalNodeImpl); ok {
			text := termNode.GetSymbol().GetText()
			s.processRangeChild(params, i, len(children), text)
		}
	}
	params.start = s.cleanAndUnescapeValue(params.start)
	params.end = s.cleanAndUnescapeValue(params.end)

	return params
}

func (s *Statement) processRangeChild(params *rangeParams, index, totalChildren int, text string) {
	switch index {
	case 0:
		params.startInclusive = text == "["
	case 1:
		params.start = text
	case 3:
		params.end = text
	}

	if index == totalChildren-1 {
		params.endInclusive = text == "]"
	}
}

func (s *Statement) cleanAndUnescapeValue(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}

	return unescapeString(value)
}

func (s *Statement) buildRangeText(params *rangeParams) string {
	startBracket := s.getBracket(params.startInclusive, true)
	endBracket := s.getBracket(params.endInclusive, false)

	return fmt.Sprintf("%s%s TO %s%s", startBracket, params.start, params.end, endBracket)
}

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

// VisitGroupingExpr 处理分组表达式规则
// 语法规则: groupingExpr : LPAREN query RPAREN (CARAT NUMBER)?
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

// VisitQuotedTerm 处理引用项规则
// 语法规则: quotedTerm : QUOTED (CARAT NUMBER)?
func (s *Statement) VisitQuotedTerm(ctx *gen.QuotedTermContext) interface{} {
	if quoted := ctx.QUOTED(); quoted != nil {
		// Remove quotes and unescape the content
		text := quoted.GetText()
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			content := text[1 : len(text)-1]
			return &ValueNode{value: unescapeString(content), isQuoted: true}
		}
		return &ValueNode{value: unescapeString(text), isQuoted: true}
	}
	return &ValueNode{value: ""}
}

type FieldNode struct {
	baseNode
	field  string
	value  Node
	encode Encode
}

func (n *FieldNode) buildBraceRangeSQL(value string) string {
	content := strings.Trim(value, "{}")
	if !strings.Contains(content, " TO ") {
		return ""
	}

	parts := strings.Split(content, " TO ")
	if len(parts) != 2 {
		return ""
	}

	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	return fmt.Sprintf(`"%s" > '%s' AND "%s" < '%s'`, n.field, start, n.field, end)
}

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

func (n *FieldNode) handleValueNodeSQL(valNode *ValueNode) querystring_parser.Expr {
	if valNode.isRegex {
		return n.createRegexExpr(valNode)
	}

	if n.containsWildcards(valNode.ToSQLString()) {
		return n.createWildcardExpr(valNode)
	}

	return n.createMatchExpr(valNode)
}

func (n *FieldNode) handleRangeNodeSQL(rangeNode *RangeNode) querystring_parser.Expr {
	text := rangeNode.value

	if n.isInclusiveRange(text) {
		return n.parseRangeWithBrackets(text, true)
	}

	if n.isExclusiveRange(text) {
		return n.parseRangeWithBrackets(text, false)
	}

	return nil
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

func (n *FieldNode) isInclusiveRange(text string) bool {
	return strings.HasPrefix(text, "[") && strings.Contains(text, " TO ")
}

func (n *FieldNode) isExclusiveRange(text string) bool {
	return strings.HasPrefix(text, "{") && strings.Contains(text, " TO ")
}

func (n *FieldNode) parseRangeWithBrackets(text string, startInclusive bool) querystring_parser.Expr {
	content := text[1 : len(text)-1]
	parts := strings.Split(content, " TO ")
	if len(parts) != 2 {
		return nil
	}

	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])

	endInclusive := n.determineEndInclusive(text, startInclusive)

	if looksLikeDate(start) && looksLikeDate(end) {
		expr := querystring_parser.NewTimeRangeExpr(&start, &end, startInclusive, endInclusive)
		expr.SetField(n.field)
		return expr
	}

	expr := querystring_parser.NewNumberRangeExpr(&start, &end, startInclusive, endInclusive)
	expr.SetField(n.field)
	return expr
}

func (n *FieldNode) determineEndInclusive(text string, startInclusive bool) bool {
	if startInclusive {
		return strings.HasSuffix(text, "]")
	}
	return strings.HasSuffix(text, "}")
}

func (n *FieldNode) createMatchExprFromValue(node *ValueNode) querystring_parser.Expr {
	return n.createMatchExpr(node)
}

func (n *FieldNode) handleGroupNode(node *GroupNode) querystring_parser.Expr {
	childExpr := node.ToSQL()
	if childExpr == nil {
		return nil
	}

	if n.field == "" {
		return childExpr
	}

	if conditionExpr := n.convertToConditionExpr(childExpr); conditionExpr != nil {
		return &querystring_parser.ConditionMatchExpr{
			Field: n.field,
			Value: conditionExpr,
		}
	}

	if matchExpr, ok := childExpr.(*querystring_parser.MatchExpr); ok {
		newExpr := querystring_parser.NewMatchExpr(matchExpr.Value)
		newExpr.SetField(n.field)
		return newExpr
	}

	return childExpr
}

func (n *FieldNode) convertToConditionExpr(expr querystring_parser.Expr) *querystring_parser.ConditionExpr {
	return n.buildConditionExpr(expr)
}

func (n *FieldNode) buildConditionExpr(expr querystring_parser.Expr) *querystring_parser.ConditionExpr {
	switch e := expr.(type) {
	case *querystring_parser.MatchExpr:
		return nil
	case *querystring_parser.OrExpr:
		return n.buildOrCondition(e)
	case *querystring_parser.AndExpr:
		return n.buildAndCondition(e)
	default:
		return nil
	}
}

func (n *FieldNode) buildOrCondition(orExpr *querystring_parser.OrExpr) *querystring_parser.ConditionExpr {
	condition := &querystring_parser.ConditionExpr{Values: [][]string{}}
	condition.Values = n.decomposeOrExpression(orExpr)
	if len(condition.Values) == 0 {
		return nil
	}
	return condition
}

func (n *FieldNode) buildAndCondition(andExpr *querystring_parser.AndExpr) *querystring_parser.ConditionExpr {
	conditionGroups := n.calculateCartesianProduct(andExpr)
	if len(conditionGroups) == 0 {
		return nil
	}

	return &querystring_parser.ConditionExpr{Values: conditionGroups}
}

func (n *FieldNode) decomposeOrExpression(expr querystring_parser.Expr) [][]string {
	switch e := expr.(type) {
	case *querystring_parser.OrExpr:
		leftResults := n.decomposeOrExpression(e.Left)
		rightResults := n.decomposeOrExpression(e.Right)
		return append(leftResults, rightResults...)

	case *querystring_parser.AndExpr:
		return n.calculateCartesianProduct(e)

	case *querystring_parser.MatchExpr:
		return [][]string{{e.Value}}

	case *GroupNode:
		if childExpr := e.child.ToSQL(); childExpr != nil {
			return n.decomposeOrExpression(childExpr)
		}

	default:
		return [][]string{}
	}

	return [][]string{}
}

func (n *FieldNode) calculateCartesianProduct(andExpr *querystring_parser.AndExpr) [][]string {
	leftOptions := n.decomposeOrExpression(andExpr.Left)
	rightOptions := n.decomposeOrExpression(andExpr.Right)

	if len(leftOptions) == 0 || len(rightOptions) == 0 {
		return [][]string{}
	}

	var result [][]string
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

func (n *FieldNode) decomposeAndExpression(expr querystring_parser.Expr) []string {
	switch e := expr.(type) {
	case *querystring_parser.AndExpr:
		left := n.decomposeAndExpression(e.Left)
		right := n.decomposeAndExpression(e.Right)
		return append(left, right...)
	case *querystring_parser.MatchExpr:
		return []string{e.Value}
	default:
		return []string{}
	}
}

func looksLikeDate(s string) bool {
	if s == WildcardAsterisk {
		return false
	}
	return strings.Contains(s, DateSeparatorHyphen) ||
		strings.Contains(s, DateSeparatorT) ||
		strings.Contains(s, DateSeparatorColon)
}

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

func (n *FieldNode) buildValueQuery(valNode *ValueNode) elastic.Query {
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
	case valNode.isNumber:
		return n.buildNumericQuery(valNode)
	default:
		return elastic.NewTermQuery(n.field, valNode.value)
	}
}

func (n *FieldNode) buildGlobalQuery(valNode *ValueNode) elastic.Query {
	if valNode.isQuoted {
		cleaned := n.cleanQuotes(valNode.value)
		return elastic.NewMatchPhraseQuery("", cleaned)
	}
	return elastic.NewQueryStringQuery(valNode.value)
}

func (n *FieldNode) buildRegexQuery(valNode *ValueNode) elastic.Query {
	regexValue := strings.Trim(valNode.value, "/")
	return elastic.NewRegexpQuery(n.field, regexValue)
}

func (n *FieldNode) buildPhraseQuery(valNode *ValueNode) elastic.Query {
	cleaned := n.cleanQuotes(valNode.value)
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

func (n *FieldNode) buildGroupQuery(groupNode *GroupNode) elastic.Query {
	if groupNode.child != nil {
		if valNode, ok := groupNode.child.(*ValueNode); ok {
			return n.buildValueQuery(valNode)
		}
	}
	childQuery := groupNode.ToES()
	if childQuery == nil {
		return elastic.NewTermQuery(n.field, "")
	}

	return childQuery
}

type RangeNode struct {
	baseNode
	field  string
	op     string
	value  string
	encode Encode
}

func (n *RangeNode) ToSQL() querystring_parser.Expr {
	if n.op != "" {
		return n.buildComparisonExpr()
	}

	if strings.Contains(n.value, " TO ") {
		return n.buildRangeExpr()
	}

	return nil
}

func (n *RangeNode) buildComparisonExpr() querystring_parser.Expr {
	exprMap := map[string]*querystring_parser.NumberRangeExpr{
		">":  {Field: n.field, Start: &n.value},
		"<":  {Field: n.field, End: &n.value},
		">=": {Field: n.field, Start: &n.value, IncludeStart: true},
		"<=": {Field: n.field, End: &n.value, IncludeEnd: true},
	}
	return exprMap[n.op]
}

func (n *RangeNode) buildRangeExpr() querystring_parser.Expr {
	rp := n.parseRangeValue()
	if n.isDateRange(rp.start, rp.end) {
		return querystring_parser.NewTimeRangeExpr(
			&rp.start, &rp.end,
			rp.startInclusive, rp.endInclusive)
	}

	return querystring_parser.NewNumberRangeExpr(
		&rp.start, &rp.end,
		rp.startInclusive, rp.endInclusive)
}

func (n *RangeNode) parseRangeValue() *rangeParams {
	parts := strings.Split(n.extractRangeContent(n.value), " TO ")
	if len(parts) != 2 {
		return &rangeParams{}
	}

	return &rangeParams{
		start:          strings.TrimSpace(parts[0]),
		end:            strings.TrimSpace(parts[1]),
		startInclusive: strings.HasPrefix(n.value, "["),
		endInclusive:   strings.HasSuffix(n.value, "]"),
	}
}

func (n *RangeNode) extractRangeContent(text string) string {
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
		return text[1 : len(text)-1]
	}
	return text
}

func (n *RangeNode) isDateRange(start, end string) bool {
	return looksLikeDate(start) && looksLikeDate(end)
}

func (n *RangeNode) ToES() elastic.Query {
	if n.field != "" {
		return n.ToESForField(n.field)
	}
	return nil
}

func (n *RangeNode) ToESForField(field string) elastic.Query {
	// 尝试处理范围查询 [start TO end] 或 {start TO end}
	if rangeQuery := n.tryBuildRangeQuery(field); rangeQuery != nil {
		return rangeQuery
	}

	if compQuery := n.tryBuildComparisonQuery(field); compQuery != nil {
		return compQuery
	}

	return elastic.NewTermQuery(field, n.value)
}

func (n *RangeNode) tryBuildRangeQuery(field string) elastic.Query {
	text := n.value

	if n.isRangeFormat(text) {
		return n.buildBracketRange(field, text)
	}

	return nil
}

func (n *RangeNode) buildBracketRange(field, text string) elastic.Query {
	content := text[1 : len(text)-1]
	parts := strings.Split(content, " TO ")
	if len(parts) != 2 {
		return nil
	}

	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])

	if start == WildcardAsterisk && end == WildcardAsterisk {
		return elastic.NewMatchAllQuery()
	}

	if numQuery := n.buildOptimizedNumericRange(field, start, end, strings.HasPrefix(text, "["), strings.HasSuffix(text, "]")); numQuery != nil {
		return numQuery
	}

	return n.buildStringRange(field, start, end, strings.HasPrefix(text, "["), strings.HasSuffix(text, "]"))
}

func (n *RangeNode) buildOptimizedNumericRange(field, start, end string, startInc, endInc bool) elastic.Query {
	startNum, startIsNum := n.tryParseNumber(start)
	endNum, endIsNum := n.tryParseNumber(end)

	if !startIsNum && !endIsNum {
		return nil
	}

	query := elastic.NewRangeQuery(field)

	if start != WildcardAsterisk {
		if startIsNum {
			if startInc {
				query.Gte(startNum)
			} else {
				query.Gt(startNum)
			}
		} else {
			if startInc {
				query.Gte(start)
			} else {
				query.Gt(start)
			}
		}
	}

	if end != WildcardAsterisk {
		if endIsNum {
			if endInc {
				query.Lte(endNum)
			} else {
				query.Lt(endNum)
			}
		} else {
			if endInc {
				query.Lte(end)
			} else {
				query.Lt(end)
			}
		}
	}

	return query
}

func (n *RangeNode) buildStringRange(field, start, end string, startInc, endInc bool) elastic.Query {
	query := elastic.NewRangeQuery(field)

	if start != WildcardAsterisk {
		if startInc {
			query.Gte(start)
		} else {
			query.Gt(start)
		}
	}

	if end != WildcardAsterisk {
		if endInc {
			query.Lte(end)
		} else {
			query.Lt(end)
		}
	}

	return query
}

func (n *RangeNode) tryBuildComparisonQuery(field string) elastic.Query {
	if n.op == "" {
		return nil
	}

	if num, err := strconv.ParseFloat(n.value, 64); err == nil {
		return n.buildNumericComparison(field, num)
	}

	return n.buildStringComparison(field)
}

func (n *RangeNode) buildNumericComparison(field string, num float64) elastic.Query {
	return n.applyComparisonOperator(elastic.NewRangeQuery(field), num, num)
}

func (n *RangeNode) buildStringComparison(field string) elastic.Query {
	return n.applyComparisonOperator(elastic.NewRangeQuery(field), n.value, n.value)
}

func (n *RangeNode) parseNumericValue(value string) (float64, error) {
	if value == WildcardAsterisk {
		return 0, fmt.Errorf("wildcard value")
	}
	return strconv.ParseFloat(value, 64)
}

func (n *RangeNode) tryParseNumber(value string) (float64, bool) {
	if value == WildcardAsterisk {
		return 0, false
	}
	num, err := strconv.ParseFloat(value, 64)
	return num, err == nil
}

func (n *RangeNode) isRangeFormat(text string) bool {
	return (strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{")) &&
		strings.Contains(text, " TO ")
}

func (n *RangeNode) applyComparisonOperator(query *elastic.RangeQuery, gtValue, gteValue interface{}) elastic.Query {
	switch n.op {
	case ">":
		return query.Gt(gtValue)
	case "<":
		return query.Lt(gtValue)
	case ">=":
		return query.Gte(gteValue)
	case "<=":
		return query.Lte(gteValue)
	default:
		return query
	}
}

type ValueNode struct {
	baseNode
	value    string
	isQuoted bool
	isRegex  bool
	isNumber bool
}

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

func (n *ValueNode) ToSQLString() string {
	if n.isQuoted {
		return strings.Trim(n.value, `"'`)
	}

	// 检查是否为范围表达式
	if strings.HasPrefix(n.value, "[") && strings.HasSuffix(n.value, "]") {
		// 包含范围：[start TO end]
		content := strings.Trim(n.value, "[]")
		if strings.Contains(content, " TO ") {
			parts := strings.Split(content, " TO ")
			if len(parts) == 2 {
				start := strings.TrimSpace(parts[0])
				end := strings.TrimSpace(parts[1])
				return fmt.Sprintf("%s TO %s", start, end)
			}
		}
	} else if strings.HasPrefix(n.value, "{") && strings.HasSuffix(n.value, "}") {
		// 排除范围：{start TO end}
		content := strings.Trim(n.value, "{}")
		if strings.Contains(content, " TO ") {
			parts := strings.Split(content, " TO ")
			if len(parts) == 2 {
				start := strings.TrimSpace(parts[0])
				end := strings.TrimSpace(parts[1])
				return fmt.Sprintf("%s TO %s", start, end)
			}
		}
	}

	return n.value
}

func (n *ValueNode) ToES() elastic.Query {
	if n.isRegex {
		return nil
	}

	if n.isQuoted {
		cleaned := strings.Trim(n.value, `"'`)
		return elastic.NewQueryStringQuery(fmt.Sprintf("\"%s\"", cleaned))
	}

	if strings.Contains(n.value, WildcardAsterisk) || strings.Contains(n.value, WildcardQuestion) {
		return elastic.NewQueryStringQuery(n.value)
	}

	return elastic.NewQueryStringQuery(n.value)
}

type AndNode struct {
	baseNode
	children []Node
}

func (n *AndNode) ToSQL() querystring_parser.Expr {
	if len(n.children) == 0 {
		return nil
	}
	if len(n.children) == 1 {
		return n.children[0].ToSQL()
	}

	result := n.children[0].ToSQL()
	for i := 1; i < len(n.children); i++ {
		child := n.children[i].ToSQL()
		if child != nil {
			result = querystring_parser.NewAndExpr(result, child)
		}
	}
	return result
}

func (n *AndNode) ToES() elastic.Query {
	var queries []elastic.Query
	for _, child := range n.children {
		childES := child.ToES()
		if childES != nil {
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

type OrNode struct {
	baseNode
	children []Node
}

func (n *OrNode) ToSQL() querystring_parser.Expr {
	if len(n.children) == 0 {
		return nil
	}
	if len(n.children) == 1 {
		return n.children[0].ToSQL()
	}

	result := n.children[0].ToSQL()
	for i := 1; i < len(n.children); i++ {
		child := n.children[i].ToSQL()
		if child != nil {
			result = querystring_parser.NewOrExpr(result, child)
		}
	}
	return result
}

func (n *OrNode) ToES() elastic.Query {
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

type NotNode struct {
	baseNode
	child Node
}

func (n *NotNode) ToSQL() querystring_parser.Expr {
	child := n.child.ToSQL()
	if child == nil {
		return nil
	}
	return querystring_parser.NewNotExpr(child)
}

func (n *NotNode) ToES() elastic.Query {
	childES := n.child.ToES()
	if childES == nil {
		return nil
	}

	return elastic.NewBoolQuery().MustNot(childES)
}

type GroupNode struct {
	baseNode
	child Node
}

func (n *GroupNode) ToSQL() querystring_parser.Expr {
	return n.child.ToSQL()
}

func (n *GroupNode) ToES() elastic.Query {
	return n.child.ToES()
}

func unescapeString(s string) string {
	if s == "" {
		return s
	}

	result := make([]rune, 0, len(s))
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			// Skip the backslash and use the next character literally
			i++
			result = append(result, runes[i])
		} else {
			result = append(result, runes[i])
		}
	}

	return string(result)
}
