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
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	elastic "github.com/olivere/elastic/v7"
	"github.com/pkg/errors"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/esregexpcompat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	DefaultLogField = "log"
)

const (
	logicAnd = "AND"
	logicOR  = "OR"
)

type Encode func(string) (string, bool)

type Node interface {
	antlr.ParseTreeVisitor
	WithOption(opt Option) Node
	SetField(Node)
	Error() error
	String() string
	DSL() ([]elastic.Query, []elastic.Query, []elastic.Query)
}

type ErrorNode struct {
	BaseNode
	value string
}

func (s *ErrorNode) Error() error {
	return errors.New(s.value)
}

type StringNode struct {
	BaseNode
	Value string

	Boost string
}

func (n *StringNode) String() string {
	return n.Value
}

type WildCardNode struct {
	BaseNode
	Value string
	Boost string
}

func (n *WildCardNode) String() string {
	return n.Value
}

type RegexpNode struct {
	BaseNode
	Value string
	Boost string
}

func (n *RegexpNode) String() string {
	return n.Value
}

type RangeNode struct {
	BaseNode

	Start          Node
	End            Node
	IsIncludeStart bool
	IsIncludeEnd   bool
	Boost          string
}

type LogicNode struct {
	BaseNode

	boost string

	reverseOp bool
	mustOp    bool

	Nodes  []*ConditionNode
	logics []string

	err error
}

func (n *LogicNode) Error() error {
	return n.err
}

func (n *LogicNode) SetField(field Node) {
	for _, node := range n.Nodes {
		node.SetField(field)
	}
}

func (n *LogicNode) VisitErrorNode(ctx antlr.ErrorNode) any {
	n.err = errors.Wrapf(n.err, "parse error at: %s", ctx.GetText())
	return nil
}

func (n *LogicNode) String() string {
	firstGroup := make([]string, 0)
	shouldGroup := make([]string, 0)
	mustGroup := make([]string, 0)

	for i, c := range n.Nodes {
		// 只有为显性的使用 AND 和 OR 才需要进行拼接
		logic := ""
		if i > 0 {
			logic = n.logics[i-1]
		}

		if logic == "" {
			if c.mustOp || c.reverseOp {
				firstGroup = append(firstGroup, c.String())
				continue
			}
		}

		if logic == logicAnd {
			mustGroup = append(mustGroup, c.String())
		} else {
			shouldGroup = append(shouldGroup, c.String())
		}
	}

	if len(firstGroup) > 0 {
		mustGroup = append(mustGroup, firstGroup...)
		mustString := strings.Join(mustGroup, fmt.Sprintf(" %s ", logicAnd))

		orList := make([]string, 0)
		for _, g := range shouldGroup {
			g = fmt.Sprintf("%s %s %s", g, logicAnd, mustString)
			orList = append(orList, g)
		}

		orList = append(orList, mustString)

		return strings.Join(orList, fmt.Sprintf(" %s ", logicOR))
	}

	sql := strings.Builder{}
	for i, node := range n.Nodes {
		if i > 0 {
			logic := n.logics[i-1]
			if logic == "" {
				logic = logicOR
			}
			sql.WriteString(fmt.Sprintf(" %s ", logic))
		}
		sql.WriteString(node.String())
	}

	return sql.String()
}

func (n *LogicNode) DSL() ([]elastic.Query, []elastic.Query, []elastic.Query) {
	allMust := make([]elastic.Query, 0)
	allShould := make([]elastic.Query, 0)
	allMustNot := make([]elastic.Query, 0)
	implicitShould := make([]elastic.Query, 0)

	for i, c := range n.Nodes {
		q := MergeQuery(c.DSL())
		logic := ""
		if i == 0 {
			if len(n.logics) > 0 {
				logic = n.logics[i]
			}
		} else {
			logic = n.logics[i-1]
		}

		if logic == logicAnd || (logic == "" && (c.reverseOp || c.mustOp)) {
			allMust = append(allMust, q)
			continue
		}

		if logic == logicOR {
			allShould = append(allShould, q)
		} else {
			implicitShould = append(implicitShould, q)
		}
	}

	if len(implicitShould) == 1 && len(allMust) > 1 {
		allMust = append(allMust, implicitShould...)
	} else {
		allShould = append(allShould, implicitShould...)
	}

	return filterQuery(allMust, allShould, allMustNot)
}

func (n *LogicNode) VisitTerminal(ctx antlr.TerminalNode) any {
	v := strings.ToUpper(ctx.GetText())
	switch v {
	case logicOR, logicAnd:
		n.logics = append(n.logics, v)
	case "&&":
		n.logics = append(n.logics, logicAnd)
	case "||":
		n.logics = append(n.logics, logicOR)
	}

	return nil
}

func (n *LogicNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.ModClauseContext:
		node := n.MakeInitNode(&ConditionNode{})
		n.Nodes = append(n.Nodes, node.(*ConditionNode))
		if len(n.logics) < len(n.Nodes)-1 {
			n.logics = append(n.logics, "")
		}
		next = node
	case *gen.ModifierContext:
		switch strings.ToUpper(ctx.GetText()) {
		case "NOT", "-", "!":
			n.reverseOp = true
		case "+":
			n.mustOp = true
		}
	}

	n.Next(next, ctx)
	return nil
}

type ConditionNode struct {
	BaseNode

	mustOp    bool
	reverseOp bool
	isQuoted  bool
	isGroup   bool
	fuzziness string

	field Node
	op    Node
	value Node
}

func (n *ConditionNode) likeValue(s string) string {
	if s == "" {
		return ""
	}

	charChange := func(cur, last rune) rune {
		if last == '\\' {
			return cur
		}

		if cur == '*' {
			return '%'
		}

		if cur == '?' {
			return '_'
		}

		return cur
	}

	var (
		ns       []rune
		lastChar rune
	)
	for _, char := range s {
		ns = append(ns, charChange(char, lastChar))
		lastChar = char
	}

	return string(ns)
}

func (n *ConditionNode) SetField(field Node) {
	if field != nil {
		n.field = field
	}
}

func (n *ConditionNode) String() string {
	// 如果是分组则，直接返回 value
	if n.isGroup {
		if n.value != nil {
			if n.field != nil {
				n.value.SetField(n.field)
			}
			sql := n.value.String()
			if n.reverseOp {
				// NOT ((field:"")) 的 SQL 字符串语义也要和 DSL 一样兼容为空值判断。
				if field, ok := existsSQLField(sql); ok {
					return nonEmptyFieldSQL(field)
				}
				sql = fmt.Sprintf("(%s)", sql)
				sql = fmt.Sprintf("NOT %s", sql)
			} else {
				sql = fmt.Sprintf("(%s)", sql)
			}
			return sql
		}
	}

	// 根据类型重写操作符
	var (
		field string
		op    string
		value string
	)

	if n.field != nil {
		field = n.field.String()
	}
	if field == "" {
		field = DefaultLogField
	}

	if field == "_exists_" {
		fieldName := ""
		if n.value != nil {
			fieldName = n.value.String()
		}
		if nf, ok := n.Option.reverseFieldAlias[fieldName]; ok {
			fieldName = nf
		}
		if n.Option.FieldEncodeFunc != nil {
			fieldName = n.Option.FieldEncodeFunc(fieldName)
		}
		if n.reverseOp {
			return fmt.Sprintf("%s IS NULL", fieldName)
		}
		return fmt.Sprintf("%s IS NOT NULL", fieldName)
	}

	var fieldOption metadata.FieldOption
	if n.Option.FieldsMap != nil {
		fieldOption = n.Option.FieldsMap.Field(field)
	}

	if n.op != nil {
		op = n.op.String()
	}
	if op == "" {
		op = "="
	}

	// 别名替换
	if nf, ok := n.Option.reverseFieldAlias[field]; ok {
		field = nf
	}

	// 外部注入字段转换函数
	if n.Option.FieldEncodeFunc != nil {
		field = n.Option.FieldEncodeFunc(field)
	}

	switch v := n.value.(type) {
	case *RangeNode:
		s := make([]string, 0)
		if v.Start != nil {
			o := ">"
			if v.IsIncludeStart {
				o += "="
			}

			s = append(s, fmt.Sprintf("%s %s '%s'", field, o, v.Start.String()))
		}
		if v.End != nil {
			o := "<"
			if v.IsIncludeEnd {
				o += "="
			}

			s = append(s, fmt.Sprintf("%s %s '%s'", field, o, v.End.String()))
		}
		return strings.Join(s, fmt.Sprintf(" %s ", logicAnd))
	case *WildCardNode:
		if n.isQuoted {
			// 引号内的通配符应视为字面字符，与 ES query_string 语义一致
		} else {
			op = "LIKE"
		}
	case *RegexpNode:
		op = "REGEXP"
	case *StringNode:
		if op == "" {
			op = "="
		}
	}

	if n.value != nil {
		value = n.value.String()
	}
	if n.isQuoted {
		value = strings.ReplaceAll(value, `\`, ``)
		value = strings.Trim(value, `"`)
	}

	switch op {
	case "=":
		if value == "" && n.field != nil {
			// field:"" 语义为字段存在，与分词无关
			if n.reverseOp {
				return nonEmptyFieldSQL(field)
			}
			return fmt.Sprintf("%s IS NOT NULL", field)
		}
		if fieldOption.IsAnalyzed {
			if n.reverseOp {
				op = "NOT MATCH_PHRASE"
			} else {
				op = "MATCH_PHRASE"
			}
		} else {
			if n.reverseOp {
				op = "!="
			}
		}
	case "!=":
		if fieldOption.IsAnalyzed {
			op = "NOT MATCH_PHRASE"
		}
	case "LIKE":
		if n.reverseOp {
			op = "NOT LIKE"
		}
		value = n.likeValue(value)
	}

	return fmt.Sprintf("%s %s '%s'", field, op, value)
}

func nonEmptyFieldSQL(field string) string {
	return fmt.Sprintf("%s IS NOT NULL AND %s != ''", field, field)
}

func (n *ConditionNode) DSL() (allMust []elastic.Query, allShould []elastic.Query, allMustNot []elastic.Query) {
	var (
		result          elastic.Query
		notEqual        bool
		inlineReverseOp bool
	)
	defer func() {
		if result == nil {
			return
		}
		if (n.reverseOp && !inlineReverseOp) || notEqual {
			allMustNot = append(allMustNot, result)
		} else {
			allMust = append(allMust, result)
		}
	}()

	if n.isGroup {
		if n.value != nil {
			if n.field != nil {
				n.value.SetField(n.field)
			}

			must, should, mustNot := n.value.DSL()
			if b, ok := n.value.(*LogicNode); ok {
				if n.reverseOp && len(must) == 1 && len(should) == 0 && len(mustNot) == 0 {
					if field, ok := existsQueryField(must[0]); ok {
						// NOT ((field:"")) 的 NOT 作用在分组上，需要在这里兼容为空串取反语义。
						result = nonEmptyFieldQuery(field)
						inlineReverseOp = true
						return allMust, allShould, allMustNot
					}
				}
				if b.boost != "" {
					result = elastic.NewBoolQuery().Must(must...).Should(should...).MustNot(mustNot...).Boost(cast.ToFloat64(b.boost))
				} else {
					result = MergeQuery(must, should, mustNot)
				}
			}
			return allMust, allShould, allMustNot
		}
	}

	// 根据类型重写操作符
	var (
		field string
		op    string
		value string
	)

	if n.value != nil {
		value = n.value.String()
	}

	if n.field != nil {
		field = n.field.String()
	}

	if field == "" {
		var boost string
		switch v := n.value.(type) {
		case *RegexpNode:
			value = fmt.Sprintf(`/%s/`, value)
		case *StringNode:
			boost = v.Boost
		case *WildCardNode:
			boost = v.Boost
		}

		if n.fuzziness != "" {
			value = fmt.Sprintf("%s~", value)
			if n.fuzziness != "AUTO" {
				fuzziness := cast.ToFloat64(n.fuzziness)
				value = fmt.Sprintf("%s%v", value, fuzziness)
			}
		}
		cq := elastic.NewQueryStringQuery(value).
			AnalyzeWildcard(true).
			Field("*").
			Field("__*").
			Lenient(true)
		if boost != "" {
			cq.Boost(cast.ToFloat64(boost))
		}
		result = cq
		return allMust, allShould, allMustNot
	}

	if n.isQuoted {
		value = strings.ReplaceAll(value, `\`, ``)
		value = strings.Trim(value, `"`)
	}

	if field == "_exists_" {
		existsField := value
		if nf, ok := n.Option.reverseFieldAlias[existsField]; ok {
			existsField = nf
		}
		result = elastic.NewExistsQuery(existsField)
		return allMust, allShould, allMustNot
	}

	// 别名替换
	if nf, ok := n.Option.reverseFieldAlias[field]; ok {
		field = nf
	}

	var fieldOption metadata.FieldOption
	if n.Option.FieldsMap != nil {
		fieldOption = n.Option.FieldsMap.Field(field)
	}

	if n.op != nil {
		op = n.op.String()
	}
	if op == "" {
		op = "="
	}
	if n.Option.FieldEncodeFunc != nil {
		field = n.Option.FieldEncodeFunc(field)
	}

	switch cv := n.value.(type) {
	case *RangeNode:
		cq := elastic.NewRangeQuery(field)
		if cv.Start != nil {
			cq.From(realValue(cv.Start))
		}
		cq.IncludeLower(cv.IsIncludeStart)

		if cv.End != nil {
			cq.To(realValue(cv.End))
		}
		cq.IncludeUpper(cv.IsIncludeEnd)
		if cv.Boost != "" {
			cq.Boost(cast.ToFloat64(cv.Boost))
		}
		result = cq
	case *WildCardNode:
		if n.isQuoted {
			// 引号内的通配符应视为字面字符，与 ES query_string 语义一致
			if fieldOption.IsAnalyzed {
				cq := elastic.NewMatchPhraseQuery(field, value)
				if cv.Boost != "" {
					cq.Boost(cast.ToFloat64(cv.Boost))
				}
				result = cq
			} else {
				cq := elastic.NewTermQuery(field, value)
				if cv.Boost != "" {
					cq.Boost(cast.ToFloat64(cv.Boost))
				}
				result = cq
			}
		} else {
			// text 字段倒排索引为小写，wildcard 不经分词器，需手动小写化 pattern
			if fieldOption.IsAnalyzed {
				value = strings.ToLower(value)
			}
			cq := elastic.NewWildcardQuery(field, value)
			if cv.Boost != "" {
				cq.Boost(cast.ToFloat64(cv.Boost))
			}
			result = cq
		}
	case *RegexpNode:
		rewrite := esregexpcompat.Rewrite(value)
		value = rewrite.Pattern
		cq := elastic.NewRegexpQuery(field, value)
		if cv.Boost != "" {
			cq.Boost(cast.ToFloat64(cv.Boost))
		}
		if rewrite.Negative {
			// 正向负前瞻必须要求字段存在；单独 must_not regexp 会误匹配缺失字段。
			result = negativeLookaheadQuery(field, cq)
		} else {
			result = cq
		}
	case *StringNode:
		switch op {
		case ">":
			result = elastic.NewRangeQuery(field).Gt(realValue(n.value))
		case ">=":
			result = elastic.NewRangeQuery(field).Gte(realValue(n.value))
		case "<":
			result = elastic.NewRangeQuery(field).Lt(realValue(n.value))
		case "<=":
			result = elastic.NewRangeQuery(field).Lte(realValue(n.value))
		case "!=":
			notEqual = true
			if fieldOption.IsAnalyzed {
				result = elastic.NewMatchPhraseQuery(field, value)
			} else {
				result = elastic.NewTermQuery(field, value)
			}
		default:
			if n.fuzziness != "" {
				result = elastic.NewFuzzyQuery(field, value).Fuzziness(n.fuzziness)
				break
			}

			if value == "" && n.field != nil {
				// field:"" 语义为字段存在；NOT field:"" 兼容为字段存在且不等于空串。
				if n.reverseOp {
					result = nonEmptyFieldQuery(field)
					inlineReverseOp = true
				} else {
					result = elastic.NewExistsQuery(field)
				}
			} else if fieldOption.IsAnalyzed {
				cq := elastic.NewMatchPhraseQuery(field, value)
				if cv.Boost != "" {
					cq.Boost(cast.ToFloat64(cv.Boost))
				}
				result = cq
			} else {
				cq := elastic.NewTermQuery(field, value)
				if cv.Boost != "" {
					cq.Boost(cast.ToFloat64(cv.Boost))
				}
				result = cq
			}
		}
	}

	originField := n.Option.FieldsMap.Field(strings.Split(field, ".")[0])
	if strings.ToUpper(originField.FieldType) == "NESTED" {
		result = elastic.NewNestedQuery(fieldOption.OriginField, result)
	}

	return allMust, allShould, allMustNot
}

func nonEmptyFieldQuery(field string) elastic.Query {
	return elastic.NewBoolQuery().
		Must(elastic.NewExistsQuery(field)).
		MustNot(elastic.NewTermQuery(field, ""))
}

func negativeLookaheadQuery(field string, regexp elastic.Query) elastic.Query {
	// ES regexp 不支持负向前瞻，用字段存在 + 反向 regexp 保留“字段值不包含”的语义。
	return elastic.NewBoolQuery().
		Must(elastic.NewExistsQuery(field)).
		MustNot(regexp)
}

func existsSQLField(sql string) (string, bool) {
	for isWrappedExpression(sql) {
		sql = strings.TrimSpace(sql[1 : len(sql)-1])
	}
	field, ok := strings.CutSuffix(sql, " IS NOT NULL")
	if !ok || field == "" {
		return "", false
	}
	upperField := strings.ToUpper(field)
	if strings.Contains(upperField, " OR ") || strings.Contains(upperField, " AND ") {
		return "", false
	}
	return field, balancedParentheses(field)
}

func isWrappedExpression(sql string) bool {
	sql = strings.TrimSpace(sql)
	if len(sql) < 2 || sql[0] != '(' || sql[len(sql)-1] != ')' {
		return false
	}

	depth := 0
	for i, r := range sql {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(sql)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func balancedParentheses(sql string) bool {
	depth := 0
	for _, r := range sql {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func existsQueryField(query elastic.Query) (string, bool) {
	// 分组反向场景只能拿到已生成的 query，这里从 exists DSL 中反查字段名。
	source, err := query.Source()
	if err != nil {
		return "", false
	}

	body, ok := source.(map[string]any)
	if !ok {
		return "", false
	}
	exists, ok := body["exists"].(map[string]any)
	if !ok {
		return "", false
	}
	field, ok := exists["field"].(string)
	return field, ok && field != ""
}

func (n *ConditionNode) VisitTerminal(ctx antlr.TerminalNode) any {
	switch ctx.GetText() {
	case ">", "<", ">=", "<=", "!=":
		n.op = n.MakeInitNode(&StringNode{
			Value: ctx.GetText(),
		})
	default:
		if n.op != nil {
			n.value = n.MakeInitNode(&StringNode{
				Value: ctx.GetText(),
			})
		}
	}
	return nil
}

func (n *ConditionNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = n

	switch ctx.(type) {
	case *gen.GroupingExprContext:
		// checkBoost
		logicNode := &LogicNode{}
		termNode := parseTerm(ctx.GetText())
		if v, ok := termNode.(*StringNode); ok {
			logicNode.boost = v.Boost
		}
		node := n.MakeInitNode(logicNode)
		n.isGroup = true
		n.value = node
		next = node
	case *gen.QuotedTermContext:
		n.isQuoted = true
	case *gen.ModifierContext:
		switch strings.ToUpper(ctx.GetText()) {
		case "NOT", "-", "!":
			n.reverseOp = true
		case "+":
			n.mustOp = true
		}
	case *gen.FieldNameContext:
		// Store the field name for this node
		n.field = n.MakeInitNode(&StringNode{
			Value: ctx.GetText(),
		})
	case *gen.FuzzyContext:
		s := ctx.GetText()
		if s != "" {
			n.fuzziness = s[1:]
			if n.fuzziness == "" {
				n.fuzziness = "AUTO"
			}
		}
	case *gen.TermContext:
		node := parseTerm(ctx.GetText())
		n.value = n.MakeInitNode(node)
	}

	n.Next(next, ctx)
	return nil
}
