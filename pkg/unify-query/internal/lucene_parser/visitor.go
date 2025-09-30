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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	DefaultLogField = "log"

	opTypeNone = iota
	opTypeOr
	opTypeAnd
)

const (
	eq = "="
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
	SQL() string
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
}

func (n *StringNode) SQL() string {
	return n.Value
}

type WildCardNode struct {
	BaseNode
	Value string
}

func (n *WildCardNode) SQL() string {
	return n.Value
}

type RegexpNode struct {
	BaseNode
	Value string
}

func (n *RegexpNode) SQL() string {
	return n.Value
}

type BoostNode struct {
	BaseNode
	Value string
	Boost string
}

func (n *BoostNode) SQL() string {
	return n.Value
}

type RangeNode struct {
	BaseNode

	Start          Node
	End            Node
	IsIncludeStart bool
	IsIncludeEnd   bool
}

type LogicNode struct {
	BaseNode

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

func (n *LogicNode) SQL() string {
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
				firstGroup = append(firstGroup, c.SQL())
				continue
			}
		}

		if logic == logicAnd {
			mustGroup = append(mustGroup, c.SQL())
		} else {
			shouldGroup = append(shouldGroup, c.SQL())
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
		sql.WriteString(node.SQL())
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
		if i > 0 {
			logic = n.logics[i-1]
		}

		if logic == logicAnd {
			allMust = append(allMust, q)
			continue
		}

		// 判断一下后面的是 and 的话，依然要加入到 must 分组
		if len(n.logics) > i {
			nextLogic := n.logics[i]
			if nextLogic == logicAnd {
				allMust = append(allMust, q)
				continue
			}
		}

		allShould = append(allShould, q)
	}

	if len(allShould) == 1 {
		allMust = append(allMust, allShould...)
		allShould = nil
	}

	return allMust, allShould, allMustNot
}

func (n *LogicNode) VisitTerminal(ctx antlr.TerminalNode) any {
	v := strings.ToUpper(ctx.GetText())
	switch v {
	case logicOR, logicAnd:
		n.logics = append(n.logics, v)
	case "&&":
		n.logics = append(n.logics, "AND")
	case "||":
		n.logics = append(n.logics, "OR")
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

	mustOp     bool
	reverseOp  bool
	isQuoted   bool
	isGroup    bool
	isWildcard bool

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

func (n *ConditionNode) SQL() string {
	// 如果是分组则，直接返回 value
	if n.isGroup {
		if n.value != nil {
			if n.field != nil {
				n.value.SetField(n.field)
			}
			sql := n.value.SQL()
			return fmt.Sprintf("(%s)", sql)
		}
	}

	// 根据类型重写操作符
	var (
		field string
		op    string
		value string
	)

	if n.field != nil {
		field = n.field.SQL()
	}
	if field == "" {
		field = DefaultLogField
	}

	var fieldOption metadata.FieldOption
	if n.Option.FieldsMap != nil {
		fieldOption = n.Option.FieldsMap.Field(field)
	}

	if n.op != nil {
		op = n.op.SQL()
	}
	if op == "" {
		op = "="
	}

	// 别名替换
	if nf, ok := n.Option.ReverseFieldAlias[field]; ok {
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

			s = append(s, fmt.Sprintf("%s %s '%s'", field, o, v.Start.SQL()))
		}
		if v.End != nil {
			o := "<"
			if v.IsIncludeEnd {
				o += "="
			}

			s = append(s, fmt.Sprintf("%s %s '%s'", field, o, v.End.SQL()))
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
		value = n.value.SQL()
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
	var q elastic.Query
	defer func() {
		if n.reverseOp {
			allMustNot = append(allMustNot, q)
		} else {
			allMust = append(allMust, q)
		}
	}()

	if n.isGroup {
		if n.value != nil {
			if n.field != nil {
				n.value.SetField(n.field)
			}
			must, should, mustNot := n.value.DSL()
			q = elastic.NewBoolQuery().Must(must...).Should(should...).MustNot(mustNot...)
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
		value = n.value.SQL()
	}
	if n.isQuoted {
		value = strings.ReplaceAll(value, `\`, ``)
		value = strings.Trim(value, `"`)
	}

	if n.field != nil {
		field = n.field.SQL()
	}

	if field == "" {
		q = elastic.NewQueryStringQuery(value).
			AnalyzeWildcard(true).
			Field("*").
			Field("__*").
			Lenient(true)
		return allMust, allShould, allMustNot
	}

	// 别名替换
	if nf, ok := n.Option.ReverseFieldAlias[field]; ok {
		field = nf
	}

	var fieldOption metadata.FieldOption
	if n.Option.FieldsMap != nil {
		fieldOption = n.Option.FieldsMap.Field(field)
	}

	if n.op != nil {
		op = n.op.SQL()
	}
	if op == "" {
		op = "="
	}
	if n.Option.FieldEncodeFunc != nil {
		field = n.Option.FieldEncodeFunc(field)
	}

	switch v := n.value.(type) {
	case *RangeNode:
		rn := elastic.NewRangeQuery(field)
		if v.Start != nil {
			if v.IsIncludeStart {
				rn.Gte(v.Start.SQL())
			} else {
				rn.Gt(v.Start.SQL())
			}
		}

		if v.End != nil {
			if v.IsIncludeEnd {
				rn.Lte(v.End.SQL())
			} else {
				rn.Lt(v.End.SQL())
			}
		}
		q = rn
	case *WildCardNode:
		q = elastic.NewWildcardQuery(field, value)
	case *RegexpNode:
		q = elastic.NewRegexpQuery(field, value)
	case *StringNode:
		if fieldOption.IsAnalyzed {
			q = elastic.NewMatchPhraseQuery(field, value)
		} else {
			q = elastic.NewTermQuery(field, value)
		}
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
		node := n.MakeInitNode(&LogicNode{})
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
	case *gen.TermContext:
		n.value = n.MakeInitNode(parseTerm(ctx.GetText()))
	}

	n.Next(next, ctx)
	return nil
}
