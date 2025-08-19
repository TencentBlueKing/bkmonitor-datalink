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
)

type Encode func(string) (string, bool)

type Node interface {
	antlr.ParseTreeVisitor
	ToSQL() string
	ToES() elastic.Query
	Error() error
	WithEncode(Encode)
}

type baseNode struct {
	antlr.BaseParseTreeVisitor
	Encode Encode
}

func (n *baseNode) ToSQL() string {
	return ""
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

type QueryVisitor struct {
	*gen.BaseLuceneParserVisitor

	root    Node
	errNode []string
	Encode  Encode
}

func NewQueryVisitor(ctx context.Context) *QueryVisitor {
	return &QueryVisitor{
		BaseLuceneParserVisitor: &gen.BaseLuceneParserVisitor{},
	}
}

func (v *QueryVisitor) WithEncode(encode Encode) {
	v.Encode = encode
}

func (v *QueryVisitor) ToSQL() string {
	if v.root != nil {
		return v.root.ToSQL()
	}
	return ""
}

func (v *QueryVisitor) ToES() elastic.Query {
	if v.root != nil {
		return v.root.ToES()
	}
	return nil
}

func (v *QueryVisitor) Error() error {
	if len(v.errNode) > 0 {
		return fmt.Errorf("parse errors: %s", strings.Join(v.errNode, "; "))
	}
	return nil
}

func (v *QueryVisitor) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
	v.errNode = append(v.errNode, ctx.GetText())
	return nil
}

func (v *QueryVisitor) VisitTopLevelQuery(ctx *gen.TopLevelQueryContext) interface{} {
	topQuery := ctx.Query()
	if topQuery != nil {
		v.root = topQuery.Accept(v).(Node)
	}
	return v.root
}

func (v *QueryVisitor) VisitQuery(ctx *gen.QueryContext) interface{} {
	disjQueries := ctx.AllDisjQuery()
	if len(disjQueries) == 1 {
		return disjQueries[0].Accept(v).(Node)
	}

	// Multiple disjunctive queries should be treated as AND
	andNode := &AndNode{}
	for _, dq := range disjQueries {
		child := dq.Accept(v).(Node)
		andNode.children = append(andNode.children, child)
	}
	return andNode
}

func (v *QueryVisitor) VisitDisjQuery(ctx *gen.DisjQueryContext) interface{} {
	conjQueries := ctx.AllConjQuery()
	if len(conjQueries) == 1 {
		return conjQueries[0].Accept(v).(Node)
	}

	orNode := &OrNode{}
	for _, cq := range conjQueries {
		child := cq.Accept(v).(Node)
		orNode.children = append(orNode.children, child)
	}
	return orNode
}

func (v *QueryVisitor) VisitConjQuery(ctx *gen.ConjQueryContext) interface{} {
	modClauses := ctx.AllModClause()
	if len(modClauses) == 1 {
		return modClauses[0].Accept(v).(Node)
	}

	andNode := &AndNode{}
	for _, mc := range modClauses {
		child := mc.Accept(v).(Node)
		andNode.children = append(andNode.children, child)
	}
	return andNode
}

func (v *QueryVisitor) VisitModClause(ctx *gen.ModClauseContext) interface{} {
	clause := ctx.Clause().Accept(v).(Node)

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

func (v *QueryVisitor) VisitClause(ctx *gen.ClauseContext) interface{} {
	// 处理范围查询
	if ctx.FieldRangeExpr() != nil {
		return ctx.FieldRangeExpr().Accept(v).(Node)
	}

	fieldName := "_all"
	if ctx.FieldName() != nil {
		fieldName = ctx.FieldName().GetText()
		if v.Encode != nil {
			if encoded, ok := v.Encode(fieldName); ok {
				fieldName = encoded
			}
		}
	}

	var value Node
	if term := ctx.Term(); term != nil {
		if result := term.Accept(v); result != nil {
			if node, ok := result.(Node); ok {
				value = node
			} else {
				value = &ValueNode{value: ""}
			}
		} else {
			value = &ValueNode{value: ""}
		}
	} else if groupingExpr := ctx.GroupingExpr(); groupingExpr != nil {
		if result := groupingExpr.Accept(v); result != nil {
			if node, ok := result.(Node); ok {
				return node
			}
		}
		return &GroupNode{child: &ValueNode{value: ""}}
	} else {
		value = &ValueNode{value: ""}
	}

	return &FieldNode{
		field:  fieldName,
		value:  value,
		encode: v.Encode,
	}
}

func (v *QueryVisitor) VisitFieldRangeExpr(ctx *gen.FieldRangeExprContext) interface{} {
	fieldName := ctx.FieldName().GetText()
	if v.Encode != nil {
		if encoded, ok := v.Encode(fieldName); ok {
			fieldName = encoded
		}
	}

	op := ctx.GetChild(1).(*antlr.TerminalNodeImpl).GetText()
	value := ctx.GetChild(2).(*antlr.TerminalNodeImpl).GetText()

	return &RangeNode{
		field:  fieldName,
		op:     op,
		value:  value,
		encode: v.Encode,
	}
}

func (v *QueryVisitor) VisitTerm(ctx *gen.TermContext) interface{} {
	if quoted := ctx.QuotedTerm(); quoted != nil {
		return quoted.Accept(v).(Node)
	}
	if regex := ctx.REGEXPTERM(); regex != nil {
		return &ValueNode{value: regex.GetText(), isRegex: true}
	}
	if termRange := ctx.TermRangeExpr(); termRange != nil {
		return v.VisitTermRangeExpr(termRange.(*gen.TermRangeExprContext))
	}
	if number := ctx.NUMBER(0); number != nil {
		return &ValueNode{value: number.GetText(), isNumber: true}
	}

	if term := ctx.TERM(); term != nil {
		return &ValueNode{value: term.GetText()}
	}
	return &ValueNode{value: ""}
}

func (v *QueryVisitor) VisitTermRangeExpr(ctx *gen.TermRangeExprContext) interface{} {
	// 直接提取范围表达式的各个部分
	startToken := ctx.GetLeft()
	endToken := ctx.GetRight()

	if startToken == nil || endToken == nil {
		return &ValueNode{value: ""}
	}

	start := startToken.GetText()
	end := endToken.GetText()

	// 判断是否为包含范围
	isInclusive := ctx.RANGEIN_START() != nil

	// 构建范围表达式文本
	var rangeText string
	if isInclusive {
		rangeText = fmt.Sprintf("[%s TO %s]", start, end)
	} else {
		rangeText = fmt.Sprintf("{%s TO %s}", start, end)
	}

	// 构建范围节点
	return &RangeNode{
		value:  rangeText,
		encode: v.Encode,
	}
}

func (v *QueryVisitor) VisitGroupingExpr(ctx *gen.GroupingExprContext) interface{} {
	query := ctx.Query()
	if query != nil {
		child := query.Accept(v).(Node)
		return &GroupNode{child: child}
	}
	return &GroupNode{child: &ValueNode{value: ""}}
}

func (v *QueryVisitor) VisitQuotedTerm(ctx *gen.QuotedTermContext) interface{} {
	if quoted := ctx.QUOTED(); quoted != nil {
		return &ValueNode{value: quoted.GetText(), isQuoted: true}
	}
	return &ValueNode{value: ""}
}

func getRangeNodeValue(node Node) (*RangeNode, bool) {
	if rangeNode, ok := node.(*RangeNode); ok {
		return rangeNode, true
	}
	return nil, false
}

func getValueNodeValue(node Node) (*ValueNode, bool) {
	if value, ok := node.(*ValueNode); ok {
		return value, true
	}
	return nil, false
}

type FieldNode struct {
	baseNode
	field  string
	value  Node
	encode Encode
}

func (n *FieldNode) ToSQL() string {
	builder := NewFieldSQLBuilder(n.field, n.encode)

	value := n.value.ToSQL()
	if value == "" {
		return ""
	}

	if rangeNode, ok := getRangeNodeValue(n.value); ok {
		return rangeNode.ToSQLForField(n.field)
	}

	if valNode, ok := getValueNodeValue(n.value); ok {
		// 处理正则查询
		if valNode.isRegex {
			builder.SetOp("REGEXP")
			cleaned := strings.Trim(valNode.value, "/")
			builder.AddValue(cleaned)
			return builder.Build()
		}

		// 处理引号查询（精确匹配）
		if valNode.isQuoted {
			cleaned := strings.Trim(valNode.value, `"'`)
			builder.SetOp("=")
			builder.AddValue(cleaned)
			return builder.Build()
		}

		// 处理范围查询
		if strings.HasPrefix(valNode.value, "[") && strings.HasSuffix(valNode.value, "]") {
			content := strings.Trim(valNode.value, "[]")
			if strings.Contains(content, " TO ") {
				parts := strings.Split(content, " TO ")
				if len(parts) == 2 {
					start := strings.TrimSpace(parts[0])
					end := strings.TrimSpace(parts[1])
					builder.SetOp("BETWEEN")
					builder.AddValue(start)
					builder.AddValue(end)
					return builder.Build()
				}
			}
		} else if strings.HasPrefix(valNode.value, "{") && strings.HasSuffix(valNode.value, "}") {
			content := strings.Trim(valNode.value, "{}")
			if strings.Contains(content, " TO ") {
				parts := strings.Split(content, " TO ")
				if len(parts) == 2 {
					start := strings.TrimSpace(parts[0])
					end := strings.TrimSpace(parts[1])
					// 排除边界使用两个条件
					return fmt.Sprintf(`"%s" > '%s' AND "%s" < '%s'`, n.field, start, n.field, end)
				}
			}
		}

		// 处理无字段名查询（_all字段）
		if n.field == "_all" {
			return fmt.Sprintf(`"%s" like '%%%s%%'`, n.field, value)
		}

		// 默认情况 - 词项查询
		builder.SetOp("=")
		builder.AddValue(value)
		return builder.Build()
	}

	// 默认情况 - 词项查询
	builder.SetOp("=")
	builder.AddValue(value)
	return builder.Build()
}

func (n *FieldNode) ToES() elastic.Query {
	// Handle special cases
	if rangeNode, ok := n.value.(*RangeNode); ok {
		return rangeNode.ToESForField(n.field)
	}

	// Handle _all field for text search without field name
	if n.field == "_all" {
		if valNode, ok := n.value.(*ValueNode); ok {
			// Remove quotes for phrase queries
			if valNode.isQuoted {
				cleaned := strings.Trim(valNode.value, `"'`)
				return elastic.NewMatchPhraseQuery("", cleaned) // Empty field means _all
			}
			return elastic.NewQueryStringQuery(valNode.value)
		}
		return elastic.NewQueryStringQuery("")
	}

	// Handle regex queries
	if valNode, ok := n.value.(*ValueNode); ok {
		if valNode.isRegex {
			// Remove regex delimiters
			regexValue := strings.Trim(valNode.value, "/")
			return elastic.NewRegexpQuery(n.field, regexValue)
		}

		// Handle phrase queries
		if valNode.isQuoted {
			cleaned := strings.Trim(valNode.value, `"'`)
			return elastic.NewMatchPhraseQuery(n.field, cleaned)
		}

		// Handle numeric values
		if valNode.isNumber {
			if num, err := strconv.ParseFloat(valNode.value, 64); err == nil {
				return elastic.NewTermQuery(n.field, num)
			}
		}

		// Default term query
		return elastic.NewTermQuery(n.field, valNode.value)
	}

	// Fallback
	return elastic.NewTermQuery(n.field, "")
}

type RangeNode struct {
	baseNode
	field  string
	op     string
	value  string
	encode Encode
}

func (n *RangeNode) ToSQL() string {
	return n.value
}

func (n *RangeNode) ToSQLForField(field string) string {
	// 使用RangeSQLBuilder进行map-based构建
	builder := NewRangeSQLBuilder(field, n.encode)

	text := n.value

	// 检查方括号表示法
	if strings.HasPrefix(text, "[") && strings.Contains(text, " TO ") {
		// 包含范围：[start TO end]
		parts := strings.Split(text[1:len(text)-1], " TO ")
		if len(parts) == 2 {
			start := strings.TrimSpace(parts[0])
			end := strings.TrimSpace(parts[1])
			builder.SetRange(start, end, true, true)
			return builder.Build()
		}
	} else if strings.HasPrefix(text, "{") && strings.Contains(text, " TO ") {
		// 排除范围：{start TO end}
		parts := strings.Split(text[1:len(text)-1], " TO ")
		if len(parts) == 2 {
			start := strings.TrimSpace(parts[0])
			end := strings.TrimSpace(parts[1])
			builder.SetRange(start, end, false, false)
			return builder.Build()
		}
	}

	// 处理比较操作符
	switch n.op {
	case ">":
		// 检查是否为数字
		if _, err := strconv.ParseFloat(n.value, 64); err == nil {
			return fmt.Sprintf(`"%s" > %s`, field, n.value)
		}
		return fmt.Sprintf(`"%s" > '%s'`, field, n.value)
	case "<":
		if _, err := strconv.ParseFloat(n.value, 64); err == nil {
			return fmt.Sprintf(`"%s" < %s`, field, n.value)
		}
		return fmt.Sprintf(`"%s" < '%s'`, field, n.value)
	case ">=":
		if _, err := strconv.ParseFloat(n.value, 64); err == nil {
			return fmt.Sprintf(`"%s" >= %s`, field, n.value)
		}
		return fmt.Sprintf(`"%s" >= '%s'`, field, n.value)
	case "<=":
		if _, err := strconv.ParseFloat(n.value, 64); err == nil {
			return fmt.Sprintf(`"%s" <= %s`, field, n.value)
		}
		return fmt.Sprintf(`"%s" <= '%s'`, field, n.value)
	}

	// 默认情况
	builder.SetRange(n.value, n.value, true, true)
	return builder.Build()
}

func (n *RangeNode) ToES() elastic.Query {
	return nil
}

func (n *RangeNode) ToESForField(field string) elastic.Query {
	text := n.value

	// Handle bracket notation for range queries
	if strings.HasPrefix(text, "[") && strings.Contains(text, " TO ") {
		// Inclusive range: [start TO end]
		content := text[1 : len(text)-1] // Remove brackets
		parts := strings.Split(content, " TO ")
		if len(parts) == 2 {
			start := strings.TrimSpace(parts[0])
			end := strings.TrimSpace(parts[1])

			// Handle wildcard values
			if start == "*" && end == "*" {
				return elastic.NewMatchAllQuery()
			}

			// Try to parse as numbers
			if startNum, err1 := strconv.ParseFloat(start, 64); err1 == nil {
				if endNum, err2 := strconv.ParseFloat(end, 64); err2 == nil {
					return elastic.NewRangeQuery(field).Gte(startNum).Lte(endNum)
				}
			}

			// Handle string values
			query := elastic.NewRangeQuery(field)
			if start != "*" {
				query.Gte(start)
			}
			if end != "*" {
				query.Lte(end)
			}
			return query
		}
	} else if strings.HasPrefix(text, "{") && strings.Contains(text, " TO ") {
		// Exclusive range: {start TO end}
		content := text[1 : len(text)-1] // Remove braces
		parts := strings.Split(content, " TO ")
		if len(parts) == 2 {
			start := strings.TrimSpace(parts[0])
			end := strings.TrimSpace(parts[1])

			// Try to parse as numbers
			if startNum, err1 := strconv.ParseFloat(start, 64); err1 == nil {
				if endNum, err2 := strconv.ParseFloat(end, 64); err2 == nil {
					return elastic.NewRangeQuery(field).Gt(startNum).Lt(endNum)
				}
			}

			// Handle string values
			query := elastic.NewRangeQuery(field)
			if start != "*" {
				query.Gt(start)
			}
			if end != "*" {
				query.Lt(end)
			}
			return query
		}
	}

	// Handle comparison operators for direct range queries
	if num, err := strconv.ParseFloat(n.value, 64); err == nil {
		switch n.op {
		case ">":
			return elastic.NewRangeQuery(field).Gt(num)
		case "<":
			return elastic.NewRangeQuery(field).Lt(num)
		case ">=":
			return elastic.NewRangeQuery(field).Gte(num)
		case "<=":
			return elastic.NewRangeQuery(field).Lte(num)
		}
	}

	// String comparison
	switch n.op {
	case ">":
		return elastic.NewRangeQuery(field).Gt(n.value)
	case "<":
		return elastic.NewRangeQuery(field).Lt(n.value)
	case ">=":
		return elastic.NewRangeQuery(field).Gte(n.value)
	case "<=":
		return elastic.NewRangeQuery(field).Lte(n.value)
	}

	return elastic.NewTermQuery(field, n.value)
}

type ValueNode struct {
	baseNode
	value    string
	isQuoted bool
	isRegex  bool
	isNumber bool
}

func (n *ValueNode) ToSQL() string {
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
	// ValueNode不应该直接生成ES查询，应该由FieldNode处理
	// 这里返回nil，让调用者处理
	return nil
}

type AndNode struct {
	baseNode
	children []Node
}

func (n *AndNode) ToSQL() string {
	// 使用LogicSQLBuilder进行map-based构建
	builder := NewLogicSQLBuilder()

	for _, child := range n.children {
		childSQL := child.ToSQL()
		if childSQL != "" {
			builder.AddCondition(childSQL)
		}
	}

	// 设置操作符为AND
	for i := 0; i < len(n.children)-1; i++ {
		builder.AddOperator("AND")
	}

	return builder.Build()
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

func (n *OrNode) ToSQL() string {
	// 使用LogicSQLBuilder进行map-based构建
	builder := NewLogicSQLBuilder()

	for _, child := range n.children {
		childSQL := child.ToSQL()
		if childSQL != "" {
			builder.AddCondition(childSQL)
		}
	}

	// 设置操作符为OR
	for i := 0; i < len(n.children)-1; i++ {
		builder.AddOperator("OR")
	}

	return builder.Build()
}

func (n *OrNode) ToES() elastic.Query {
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
	return elastic.NewBoolQuery().Should(queries...)
}

type NotNode struct {
	baseNode
	child Node
}

func (n *NotNode) ToSQL() string {
	childSQL := n.child.ToSQL()
	if childSQL == "" {
		return ""
	}

	// Handle field-specific NOT
	if fieldNode, ok := n.child.(*FieldNode); ok {
		// 使用FieldSQLBuilder处理字段NOT
		builder := NewFieldSQLBuilder(fieldNode.field, nil)
		builder.SetOp("!=")
		builder.AddValue(fieldNode.value.ToSQL())
		return builder.Build()
	}

	// 使用LogicSQLBuilder处理通用NOT
	builder := NewLogicSQLBuilder()
	builder.AddCondition(childSQL)
	result := builder.Build()
	if result != "" {
		return "NOT (" + result + ")"
	}
	return ""
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

func (n *GroupNode) ToSQL() string {
	return n.child.ToSQL()
}

func (n *GroupNode) ToES() elastic.Query {
	return n.child.ToES()
}
