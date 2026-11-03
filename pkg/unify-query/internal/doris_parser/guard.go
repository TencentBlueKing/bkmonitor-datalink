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

	"github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

// 访问器对未建模的语法节点只会穿过、不会报错，结果是静默改写：HAVING、JOIN、UNION、EXISTS 被丢弃，
// NOT、一元负号被吞掉。这里在渲染前按子句位置核对语法树，只放行访问器建模过的节点类型，其余一律拒绝，
// 让查询显式失败而不是返回错误的结果。

type syntaxErrorListener struct {
	*antlr.DefaultErrorListener
	errs []string
}

func (l *syntaxErrorListener) SyntaxError(_ antlr.Recognizer, _ any, line, column int, msg string, _ antlr.RecognitionException) {
	l.errs = append(l.errs, fmt.Sprintf("line %d:%d %s", line, column, msg))
}

func (l *syntaxErrorListener) Error() error {
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("doris sql syntax error: %s", strings.Join(l.errs, "; "))
}

// supportedRules lists, per clause, the parse-tree rules the visitor models, including the wrapper rules it
// passes through on purpose. Anything else in that clause is rejected.
var supportedRules = map[string]*set.Set[string]{
	"query": set.New[string](
		"Query", "QueryOrganization", "QueryPrimaryDefault", "QueryTermDefault", "RegularQuerySpecification",
	),
	"select": set.New[string](
		"ArithmeticBinary", "Cast", "CastDataType", "ColumnReference", "Comparison", "ComparisonOperator",
		"ConstantDefault", "DecimalLiteral", "Dereference", "ElementAt", "Expression", "FunctionCall",
		"FunctionCallExpression", "FunctionIdentifier", "FunctionNameIdentifier", "Identifier", "IdentifierOrText",
		"IntegerLiteral", "NamedExpression", "NamedExpressionSeq", "NonReserved", "NullLiteral", "NumericLiteral",
		"ParenthesizedExpression", "Predicate", "Predicated", "PrimitiveColType", "PrimitiveDataType",
		"QuotedIdentifier", "QuotedIdentifierAlternative", "SearchedCase", "SelectClause", "SelectColumnClause",
		"Star", "StringLiteral", "SubqueryExpression", "UnquotedIdentifier", "ValueExpressionDefault", "WhenClause",
	),
	"from": set.New[string](
		"AliasedQuery", "ErrorCapturingIdentifier", "FromClause", "Identifier", "MultipartIdentifier",
		"QuotedIdentifier", "QuotedIdentifierAlternative", "RealIdent", "Relation", "Relations", "TableAlias",
		"TableName", "UnquotedIdentifier",
	),
	"where": set.New[string](
		"ArithmeticBinary", "Cast", "CastDataType", "ColumnReference", "Comparison", "ComparisonOperator",
		"ConstantDefault", "Dereference", "ElementAt", "Expression", "FunctionCall", "FunctionCallExpression",
		"FunctionIdentifier", "FunctionNameIdentifier", "Identifier", "IntegerLiteral", "LogicalBinary",
		"NonReserved", "NullLiteral", "NumericLiteral", "ParenthesizedExpression", "Predicate", "Predicated",
		"PrimitiveColType", "PrimitiveDataType", "QuotedIdentifier", "QuotedIdentifierAlternative", "SearchedCase",
		"StringLiteral", "UnquotedIdentifier", "ValueExpressionDefault", "WhenClause", "WhereClause",
	),
	"group": set.New[string](
		"AggClause", "ArithmeticBinary", "Cast", "CastDataType", "ColumnReference", "ConstantDefault", "Dereference",
		"Expression", "FunctionCall", "FunctionCallExpression", "FunctionIdentifier", "FunctionNameIdentifier",
		"GroupingElement", "Identifier", "IntegerLiteral", "NonReserved", "NumericLiteral", "Predicated",
		"PrimitiveColType", "PrimitiveDataType", "QuotedIdentifier", "QuotedIdentifierAlternative",
		"UnquotedIdentifier", "ValueExpressionDefault",
	),
	"order": set.New[string](
		"ArithmeticBinary", "Cast", "CastDataType", "ColumnReference", "ConstantDefault", "Dereference",
		"Expression", "FunctionCall", "FunctionCallExpression", "FunctionIdentifier", "FunctionNameIdentifier",
		"Identifier", "IntegerLiteral", "NonReserved", "NumericLiteral", "Predicated", "PrimitiveColType",
		"PrimitiveDataType", "QuotedIdentifier", "QuotedIdentifierAlternative", "SortClause", "SortItem", "Star",
		"UnquotedIdentifier", "ValueExpressionDefault",
	),
	"limit": set.New[string](
		"IntegerLiteral", "LimitClause",
	),
}

func init() {
	// Rules modelled by the root-cause fixes that land before the guard. Each entry names the fix that models it.
	extend := func(rules []string, clauses ...string) {
		for _, c := range clauses {
			for _, r := range rules {
				supportedRules[c].Add(r)
			}
		}
	}
	// HAVING (E): its own clause, with the same conditions as WHERE
	supportedRules["having"] = set.New[string]("HavingClause", "Star")
	extend(supportedRules["where"].ToArray(), "having")
	// unary + / - (B)
	extend([]string{"ArithmeticUnary"}, "select", "where", "group", "order", "having")
	// NOT (A in WHERE; D as a value)
	extend([]string{"LogicalNot"}, "select", "where", "having")
	// AND / OR as a value (D)
	extend([]string{"LogicalBinary"}, "select")
	// CASE <value> WHEN (D)
	extend([]string{"SimpleCase", "WhenClause"}, "select", "where", "group", "order", "having")
	// Every literal kind wherever an expression stands. The visitor renders a literal as its own text in any clause, so a
	// clause that accepts expressions must accept all of them; listing them clause by clause let decimal, boolean and
	// typed literals (`a > 1.5`, `a = true`, DATE '...') fall out of some clauses while upstream renders them correctly.
	extend(literalRules, "select", "where", "group", "order", "having")
	extend([]string{"NullLiteral"}, "select", "where", "having")
	// GROUP BY / ORDER BY take the same value expressions the select list does (element access, searched CASE,
	// parentheses, COUNT(*)), rendered by the same nodes.
	extend([]string{"ElementAt", "SearchedCase", "WhenClause", "ParenthesizedExpression", "Star", "Comparison", "ComparisonOperator"}, "group", "order")
	extend([]string{"Star"}, "where", "having")
}

// literalRules are the grammar's literal alternatives, found by probing every literal kind through the rewrite.
var literalRules = []string{
	"ArrayLiteral", "BooleanLiteral", "BooleanValue", "ConstantDefault", "DecimalLiteral", "IntegerLiteral",
	"MapLiteral", "NumericLiteral", "StringLiteral", "TypeConstructor",
}

// NullLiteral stays out of GROUP BY and ORDER BY: the visitor drops a NULL grouping or sort key, so `GROUP BY NULL`
// would render without the clause.

// rejectedGroupingKeywords are GROUP BY modifiers that are tokens, not rules: WITH ROLLUP, CUBE, GROUPING SETS.
// truncateForError cuts quoted user text to 80 characters for the guard's own messages.
func truncateForError(text string) string {
	if r := []rune(text); len(r) > 80 {
		return string(r[:80]) + "..."
	}
	return text
}

// checkInputConsumed fails a parse that stopped before the end of the input. The query rule does not require EOF, so
// tokens it cannot place - `1.5e-3` lexes as 1.5, e, -, 3 and the rule ends after 1.5 - were silently left out of the
// rewritten SQL. Trailing semicolons are the only input allowed after the query.
func checkInputConsumed(tokens *antlr.CommonTokenStream) error {
	tokens.Fill()
	all := tokens.GetAllTokens()
	for i := tokens.Index(); i < len(all); i++ {
		tok := all[i]
		switch {
		case tok.GetTokenType() == antlr.TokenEOF:
			return nil
		case tok.GetChannel() != antlr.TokenDefaultChannel || tok.GetTokenType() == gen.DorisLexerSEMICOLON:
			continue
		default:
			return fmt.Errorf("doris sql syntax error: unexpected input after the query at %q", truncateForError(tok.GetText()))
		}
	}
	return nil
}

var rejectedGroupingKeywords = set.New[string]("ROLLUP", "CUBE", "GROUPING", "SETS")

// constructNames turns a rule into the name a user recognises in the error.
var constructNames = map[string]string{
	"HavingClause":     "HAVING",
	"JoinRelation":     "JOIN",
	"JoinCriteria":     "JOIN ... ON",
	"SetOperation":     "UNION / EXCEPT / INTERSECT",
	"LogicalNot":       "NOT",
	"ArithmeticUnary":  "unary + / -",
	"SimpleCase":       "CASE <value> WHEN",
	"Exist":            "EXISTS",
	"Cte":              "WITH (common table expression)",
	"AliasQuery":       "WITH (common table expression)",
	"QualifyClause":    "QUALIFY",
	"WindowSpec":       "window function (OVER)",
	"WindowClause":     "WINDOW",
	"LateralView":      "LATERAL VIEW",
	"IntervalLiteral":  "INTERVAL",
	"Interval":         "INTERVAL",
	"QueryTermDefault": "query",
	"LambdaExpression": "lambda (->)",
	"LogicalBinary":    "AND / OR",
}

// constructHints append a supported-rewrite suggestion to a rejected construct's message.
var constructHints = map[string]string{
	"Cte":        "; rewrite it as a FROM subquery",
	"AliasQuery": "; rewrite it as a FROM subquery",
}

func ruleName(n antlr.Tree) string {
	return strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", n), "*gen."), "Context")
}

// clausePosition is the clause a rule's children belong to.
func clausePosition(rule, current string) string {
	switch rule {
	case "SelectClause":
		return "select"
	case "WhereClause":
		return "where"
	case "FromClause":
		return "from"
	case "AggClause":
		return "group"
	case "HavingClause":
		return "having"
	case "SortClause":
		return "order"
	case "LimitClause":
		return "limit"
	case "AliasedQuery", "SubqueryExpression":
		// a subquery starts its own clauses
		return "query"
	}
	return current
}

// checkSupportedSyntax rejects the first construct the visitor does not model, naming it and its clause.
func checkSupportedSyntax(tree antlr.Tree) error {
	var walk func(n antlr.Tree, pos string) error
	walk = func(n antlr.Tree, pos string) error {
		if term, ok := n.(antlr.TerminalNode); ok {
			word := strings.ToUpper(term.GetText())
			if pos == "group" && rejectedGroupingKeywords.Existed(word) {
				return fmt.Errorf("unsupported Doris SQL: %s in GROUP BY", word)
			}
			return nil
		}
		rule := ruleName(n)
		next := clausePosition(rule, pos)
		// 子句节点按它开启的子句核对；子查询节点按它所在的子句核对，其内部从 query 重新开始。
		checkPos := next
		if rule == "AliasedQuery" || rule == "SubqueryExpression" {
			checkPos = pos
		}
		if allowed, ok := supportedRules[checkPos]; !ok || !allowed.Existed(rule) {
			return unsupported(n, rule, checkPos)
		}
		for i := 0; i < n.GetChildCount(); i++ {
			child := n.GetChild(i)
			childPos := next
			if rule == "AliasedQuery" && ruleName(child) != "Query" {
				// FROM (subquery) alias: the alias belongs to FROM
				childPos = pos
			}
			if err := walk(child, childPos); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(tree, "query")
}

func unsupported(n antlr.Tree, rule, pos string) error {
	name, known := constructNames[rule]
	if !known {
		name = rule
	}
	if rule == "Query" {
		// a nested query outside FROM, e.g. IN (SELECT ...)
		name = "subquery"
	}
	text := ""
	if rc, ok := n.(antlr.ParserRuleContext); ok && rc.GetStart() != nil && rc.GetStop() != nil {
		// the user's own text, not the token concatenation
		text = rc.GetStart().GetInputStream().GetTextFromInterval(
			antlr.NewInterval(rc.GetStart().GetStart(), rc.GetStop().GetStop()))
	}
	text = truncateForError(text)
	hint := constructHints[rule]
	if clausePosition(rule, pos) != pos || strings.EqualFold(name, pos) {
		// the rejected rule is a clause of its own, e.g. HAVING
		return fmt.Errorf("unsupported Doris SQL: %s: %s%s", name, text, hint)
	}
	return fmt.Errorf("unsupported Doris SQL: %s in %s clause: %s%s", name, strings.ToUpper(pos), text, hint)
}
