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

	for i, c := range n.Nodes {
		q := MergeQuery(c.DSL())
		// 只有为显性的使用 AND 和 OR 才需要进行拼接
		logic := ""
		if i == 0 {
			// 第一个根据后面的来判断
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

		allShould = append(allShould, q)
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
		case "NOT", "-":
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
			sql = fmt.Sprintf("(%s)", sql)
			if n.reverseOp {
				sql = fmt.Sprintf("NOT %s", sql)
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
		op = "LIKE"
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
	case "LIKE":
		if n.reverseOp {
			op = "NOT LIKE"
		}
		value = n.likeValue(value)
	}

	return fmt.Sprintf("%s %s '%s'", field, op, value)
}

func (n *ConditionNode) DSL() (allMust []elastic.Query, allShould []elastic.Query, allMustNot []elastic.Query) {
	var result elastic.Query
	defer func() {
		if n.reverseOp {
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
		cq := elastic.NewWildcardQuery(field, value)
		if cv.Boost != "" {
			cq.Boost(cast.ToFloat64(cv.Boost))
		}
		result = cq
	case *RegexpNode:
		cq := elastic.NewRegexpQuery(field, value)
		if cv.Boost != "" {
			cq.Boost(cast.ToFloat64(cv.Boost))
		}
		result = cq
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
		default:
			if n.fuzziness != "" {
				result = elastic.NewFuzzyQuery(field, value).Fuzziness(n.fuzziness)
				break
			}

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
		}
	}

	originField := n.Option.FieldsMap.Field(strings.Split(field, ".")[0])
	if strings.ToUpper(originField.FieldType) == "NESTED" {
		result = elastic.NewNestedQuery(fieldOption.OriginField, result)
	}

	return allMust, allShould, allMustNot
}

func (n *ConditionNode) VisitTerminal(ctx antlr.TerminalNode) any {
	switch ctx.GetText() {
	case ">", "<", ">=", "<=":
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
		case "NOT", "-":
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
