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
	hasExplicitAnd := false
	hasExplicitOr := false

	for _, logic := range n.logics {
		switch logic {
		case logicAnd:
			hasExplicitAnd = true
		case logicOR:
			hasExplicitOr = true
		}
	}

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

	if (hasExplicitAnd && !hasExplicitOr && len(allMust) > 0) || (len(implicitShould) == 1 && len(allMust) > 1) {
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

// likeValue 将 Lucene 通配符转换为 SQL LIKE 通配符，并保留转义后的字面量。
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

// SetField 把分组外层字段下推给内部条件，例如 log:(a OR b)。
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
				if field, ok := n.emptyStringExistsGroupSQLField(sql); ok {
					return nonEmptyFieldSQL(field)
				}
				if field, ok := n.explicitExistsGroupSQLField(sql); ok {
					return fmt.Sprintf("%s IS NULL", field)
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

// nonEmptyFieldSQL 渲染 SQL/Doris 路径的“字段存在且不为空字符串”条件，用于 NOT field:"" 兼容语义。
func nonEmptyFieldSQL(field string) string {
	// Doris SQL 路径直接使用原字段比较空串：field IS NOT NULL AND field != ''。
	return fmt.Sprintf("%s IS NOT NULL AND %s != ''", field, field)
}

func (n *ConditionNode) DSL() (allMust []elastic.Query, allShould []elastic.Query, allMustNot []elastic.Query) {
	var (
		result       elastic.Query
		notEqual     bool
		outerMustNot = n.reverseOp
	)
	defer func() {
		if result == nil {
			return
		}
		if outerMustNot || notEqual {
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
					if field, ok := n.emptyStringExistsGroupQueryField(must[0]); ok {
						// NOT ((field:"")) 的 NOT 作用在分组上，需要在这里兼容为空串取反语义。
						// 重建后的非空 bool query 仍要保持原字段的 nested scope；普通字段会原样返回。
						result = wrapNestedFieldQuery(field, n.Option.FieldsMap, nonEmptyFieldQuery(field, n.Option.FieldsMap))
						// 取反已经表达为“字段存在且非空”，收尾阶段不再追加外层 must_not。
						outerMustNot = false
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
			// 大小写不敏感的 text 字段倒排索引为小写，wildcard 不经分词器，需手动小写化 pattern。
			if fieldOption.IsAnalyzed && !fieldOption.IsCaseSensitive {
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
			// 正向不包含前缀形式必须要求字段存在；单独 must_not regexp 会误匹配缺失字段。
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
					result = nonEmptyFieldQuery(field, n.Option.FieldsMap)
					// 取反已经表达为“字段存在且非空”，收尾阶段不再追加外层 must_not。
					outerMustNot = false
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
		result = wrapNestedFieldQuery(field, n.Option.FieldsMap, result)
	}

	return allMust, allShould, allMustNot
}

// nonEmptyFieldQuery 构造“字段存在且不为空字符串”的 ES 查询；text 字段优先用 keyword/raw 子字段判断空串。
func nonEmptyFieldQuery(field string, fieldsMap metadata.FieldsMap) elastic.Query {
	q := elastic.NewBoolQuery().Must(elastic.NewExistsQuery(field))
	if exactField, ok := exactSubfieldForEmptyValue(field, fieldsMap); ok {
		q.MustNot(elastic.NewTermQuery(exactField, ""))
	}
	return q
}

// wrapNestedFieldQuery 在字段属于 nested mapping 时，用正确 path 包装已有字段查询。
func wrapNestedFieldQuery(field string, fieldsMap metadata.FieldsMap, query elastic.Query) elastic.Query {
	if fieldsMap == nil {
		return query
	}
	originField := fieldsMap.Field(strings.Split(field, ".")[0])
	if strings.ToUpper(originField.FieldType) != "NESTED" {
		return query
	}

	fieldOption := fieldsMap.Field(field)
	nestedPath := fieldOption.OriginField
	if nestedPath == "" {
		// 部分 mapping 只有顶层 nested 字段声明，没有在叶子字段上回填 OriginField。
		// 这类字段仍按 dotted path 的首段作为 nested path，例如 nested.key -> nested。
		nestedPath = strings.Split(field, ".")[0]
	}
	return elastic.NewNestedQuery(nestedPath, query)
}

// exactSubfieldForEmptyValue 返回 ES DSL 路径可用于精确判断空字符串的字段或精确值子字段。
func exactSubfieldForEmptyValue(field string, fieldsMap metadata.FieldsMap) (string, bool) {
	if fieldsMap == nil {
		return field, true
	}

	fieldOption := fieldsMap.Field(field)
	if !fieldOption.IsAnalyzed {
		return field, true
	}

	// ES text/analyzed 字段会经过 analysis，term "" 不会分析查询词，不能稳定表达“值不等于空串”。
	// ES DSL 路径优先选择 mapping 中的 keyword/raw multi-fields 子字段做精确空串判断；没有精确子字段时只保留 exists。
	for _, candidate := range []string{field + ".keyword", field + ".raw"} {
		// field.keyword/field.raw 是 ES multi-fields 中常见的精确值子字段命名。
		// 参考：https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/multi-fields
		// 这里按 FieldsMap 中已知字段选择可用于 term 查询的非 analyzed 子字段。
		option := fieldsMap.Field(candidate)
		if option.Existed() && !option.IsAnalyzed {
			return candidate, true
		}
	}
	return "", false
}

// negativeLookaheadQuery 用 exists + must_not regexp 表达固定“不包含前缀形式”的兼容语义。
func negativeLookaheadQuery(field string, regexp elastic.Query) elastic.Query {
	// ES regexp 不支持不包含前缀形式，用字段存在 + 反向 regexp 保留“字段值不包含”的语义。
	return elastic.NewBoolQuery().
		Must(elastic.NewExistsQuery(field)).
		MustNot(regexp)
}

// existsSQLField 从简单的 SQL exists 表达式中取出字段名，并拒绝包含 AND/OR 的复合表达式。
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

// emptyStringExistsGroupSQLField 判断当前分组是否源自 field:""，并从 SQL 表达式中取真实字段名。
func (n *ConditionNode) emptyStringExistsGroupSQLField(sql string) (string, bool) {
	// field:"" 与 _exists_:field 都会先渲染成 field IS NOT NULL。
	// 分组取反时必须回看 AST 来源，只允许 field:"" 走“字段存在且非空”的兼容语义。
	if !n.isEmptyStringExistsGroupCondition() {
		return "", false
	}
	return existsSQLField(sql)
}

// emptyStringExistsGroupQueryField 判断当前分组是否源自 field:""，并从已生成 DSL 中取真实字段名。
func (n *ConditionNode) emptyStringExistsGroupQueryField(query elastic.Query) (string, bool) {
	// DSL 路径同样会把 field:"" 与 _exists_:field 都生成为 exists query。
	// 这里先确认分组源自 field:""，再从 query 中取经过别名转换后的真实字段名。
	if !n.isEmptyStringExistsGroupCondition() {
		return "", false
	}
	return existsQueryField(query)
}

// explicitExistsGroupSQLField 判断当前分组是否源自 _exists_:field，并从 SQL 表达式中取真实字段名。
func (n *ConditionNode) explicitExistsGroupSQLField(sql string) (string, bool) {
	// 显式 _exists_ 分组取反应保持存在性取反，避免被上面的空字符串兼容逻辑误改成非空字符串检查。
	if !n.isExplicitExistsGroupCondition() {
		return "", false
	}
	return existsSQLField(sql)
}

// isEmptyStringExistsGroupCondition 判断分组是否只包含一个 field:"" 条件。
func (n *ConditionNode) isEmptyStringExistsGroupCondition() bool {
	if !n.isGroup {
		return false
	}
	child, ok := n.singleGroupChild()
	if !ok {
		return false
	}
	return child.isEmptyStringExistsCondition()
}

// isEmptyStringExistsCondition 判断节点是否为正向 field:"" 条件；该条件在当前语义中表示字段存在。
func (n *ConditionNode) isEmptyStringExistsCondition() bool {
	if n == nil || n.reverseOp {
		return false
	}
	if n.isGroup {
		child, ok := n.singleGroupChild()
		return ok && child.isEmptyStringExistsCondition()
	}
	field, ok := conditionFieldName(n)
	if !ok || field == "_exists_" {
		return false
	}
	value, ok := n.value.(*StringNode)
	return ok && strings.Trim(value.Value, `"`) == ""
}

// isExplicitExistsGroupCondition 判断分组是否只包含一个显式 _exists_:field 条件。
func (n *ConditionNode) isExplicitExistsGroupCondition() bool {
	if !n.isGroup {
		return false
	}
	child, ok := n.singleGroupChild()
	if !ok {
		return false
	}
	return child.isExplicitExistsCondition()
}

// isExplicitExistsCondition 判断节点是否为正向 _exists_:field 条件。
func (n *ConditionNode) isExplicitExistsCondition() bool {
	if n == nil || n.reverseOp {
		return false
	}
	if n.isGroup {
		child, ok := n.singleGroupChild()
		return ok && child.isExplicitExistsCondition()
	}
	field, ok := conditionFieldName(n)
	return ok && field == "_exists_"
}

// singleGroupChild 返回单条件分组的唯一子节点；多条件分组不能套用 field:"" 或 _exists_ 的特殊取反语义。
func (n *ConditionNode) singleGroupChild() (*ConditionNode, bool) {
	logic, ok := n.value.(*LogicNode)
	if !ok || len(logic.Nodes) != 1 {
		return nil, false
	}
	return logic.Nodes[0], logic.Nodes[0] != nil
}

// conditionFieldName 读取条件节点的原始字段名；这里只接受普通字符串字段。
func conditionFieldName(n *ConditionNode) (string, bool) {
	if n == nil || n.field == nil {
		return "", false
	}
	field, ok := n.field.(*StringNode)
	if !ok || field.Value == "" {
		return "", false
	}
	return field.Value, true
}

// isWrappedExpression 判断 SQL 表达式是否被一对覆盖全表达式的括号包裹。
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

// balancedParentheses 判断 SQL 片段括号是否平衡，用于避免从异常表达式中误提字段名。
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

// existsQueryField 从 exists 或 nested exists DSL 中反查字段名。
func existsQueryField(query elastic.Query) (string, bool) {
	// 分组反向场景只能拿到已生成的 query，这里从 exists/nested exists DSL 中反查字段名。
	source, err := query.Source()
	if err != nil {
		return "", false
	}

	body, ok := source.(map[string]any)
	if !ok {
		return "", false
	}
	if nested, ok := body["nested"].(map[string]any); ok {
		return existsQueryFieldFromSource(nested["query"])
	}
	return existsQueryFieldFromSource(body)
}

// existsQueryFieldFromSource 从 olivere/elastic 生成的 exists query source 中读取 field。
func existsQueryFieldFromSource(source any) (string, bool) {
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
