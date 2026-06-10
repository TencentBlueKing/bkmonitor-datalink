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
	"regexp"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type BaseNode struct {
	antlr.BaseParseTreeVisitor

	Option Option
}

func (n *BaseNode) MakeInitNode(node Node) Node {
	node.WithOption(n.Option)
	return node
}

func (n *BaseNode) WithOption(opt Option) Node {
	n.Option = opt
	return n
}

func (n *BaseNode) SetField(Node) {
}

func (n *BaseNode) String() string {
	return ""
}

func (n *BaseNode) DSL() ([]elastic.Query, []elastic.Query, []elastic.Query) {
	return nil, nil, nil
}

func (n *BaseNode) Error() error {
	return nil
}

func (n *BaseNode) Next(next Node, ctx antlr.RuleNode) {
	next.WithOption(n.Option)
	for _, child := range ctx.GetChildren() {
		switch tree := child.(type) {
		case antlr.ParseTree:
			// log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(next)
			// log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}
}

func MergeQuery(must []elastic.Query, should []elastic.Query, mustNot []elastic.Query) elastic.Query {
	must, should, mustNot = filterQuery(must, should, mustNot)
	if len(mustNot) == 0 && len(should) == 0 {
		if len(must) == 1 {
			return must[0]
		} else if len(must) == 0 {
			return nil
		}
	}

	return elastic.NewBoolQuery().Must(must...).Should(should...).MustNot(mustNot...)
}

func getErrorNode(s string) Node {
	return &ErrorNode{value: s}
}

func extractBoost(s string) (baseValue string, boost string) {
	boostPattern := regexp.MustCompile(`^(.+)\^([\d\.]+)$`) // boostValue^boost
	matches := boostPattern.FindStringSubmatch(s)

	if len(matches) == 3 {
		baseValue = matches[1]
		boost = matches[2]
	} else {
		baseValue = s
		boost = ""
	}
	return baseValue, boost
}

func extractRange(s string, boost string) (node Node, matched bool) {
	rangePattern := regexp.MustCompile(`^([\[{])(.+)TO(.+)([\]}])$`)
	matches := rangePattern.FindStringSubmatch(s)

	// 完整匹配, 开始括号, : 起始值, 结束值, 结束括号
	matched = len(matches) == 5
	if matched {
		startBracket := matches[1] // [ 或 {
		startValue := matches[2]   // 起始值
		endValue := matches[3]     // 结束值
		endBracket := matches[4]   // ] 或 }

		node = &RangeNode{
			IsIncludeStart: startBracket == "[",
			IsIncludeEnd:   endBracket == "]",
			Boost:          boost,
		}

		startValue = strings.Trim(startValue, `"`)
		if startValue != "*" {
			rangeNode := node.(*RangeNode)
			rangeNode.Start = &StringNode{
				Value: startValue,
			}
		}

		endValue = strings.Trim(endValue, `"`)
		if endValue != "*" {
			rangeNode := node.(*RangeNode)
			rangeNode.End = &StringNode{
				Value: endValue,
			}
		}
	}
	return node, matched
}

func extractRegexp(s string, boost string) (node Node, matched bool) {
	regexpPattern := regexp.MustCompile(`^/(.+)/$`)
	matches := regexpPattern.FindStringSubmatch(s)

	// matches[0]: 完整匹配, matches[1]: 正则表达式内容
	matched = len(matches) == 2
	if matched {
		pattern := matches[1]

		node = &RegexpNode{
			Value: pattern,
			Boost: boost,
		}
	}
	return node, matched
}

// containsUnescapedStar 检查字符串是否包含未转义的 * 通配符
func containsUnescapedStar(s string) bool {
	unescaped := strings.ReplaceAll(s, `\*`, "")
	return strings.Contains(unescaped, "*")
}

func extractWildCard(s string, boost string) (node Node, matched bool) {
	// 检查通配符: 移除转义后的通配符
	unescapeStr := strings.ReplaceAll(s, `\*`, "")
	unescapeStr = strings.ReplaceAll(unescapeStr, `\?`, "")

	matched = strings.ContainsAny(unescapeStr, "*?")
	if matched {
		node = &WildCardNode{
			Value: s,
			Boost: boost,
		}
	}
	return node, matched
}

func parseTerm(s string) Node {
	baseValue, boost := extractBoost(s)

	rangeNode, rangeMatched := extractRange(baseValue, boost)
	if rangeMatched {
		return rangeNode
	}

	regexpNode, regexpMatched := extractRegexp(baseValue, boost)
	if regexpMatched {
		return regexpNode
	}

	wildCardNode, wildCardMatched := extractWildCard(baseValue, boost)
	if wildCardMatched {
		return wildCardNode
	}

	return &StringNode{
		Value: baseValue,
		Boost: boost,
	}
}

func filterQuery(must []elastic.Query, should []elastic.Query, mustNot []elastic.Query) ([]elastic.Query, []elastic.Query, []elastic.Query) {
	if len(should) == 1 && len(must) == 0 && len(mustNot) == 0 {
		must = append(must, should...)
		should = nil
	}

	return must, should, mustNot
}

func realValue(node Node) any {
	var res any
	// 判断是否是数字，如果是则返回数字
	res, err := cast.ToFloat64E(node.String())
	if err != nil {
		value := node.String()
		res = value
	}

	return res
}

func ConditionNodeWalk(node Node, fn func(key string, operator string, isWildcard bool, values ...string)) {
	conditionNodeWalk(node, false, fn)
}

func conditionNodeWalk(node Node, reversed bool, fn func(key string, operator string, isWildcard bool, values ...string)) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ConditionNode:
		conditionNodeWalkCondition(n, reversed, fn)
	case *LogicNode:
		conditionNodeWalkLogic(n, reversed, fn)
	}
}

func conditionNodeWalkCondition(n *ConditionNode, reversed bool, fn func(key string, operator string, isWildcard bool, values ...string)) {
	if n.value == nil {
		return
	}

	reversed = toggleReverse(reversed, n.reverseOp)
	if v, ok := n.value.(*LogicNode); ok {
		v.SetField(n.field)
		conditionNodeWalkLogic(v, reversed, fn)
		return
	}

	label, ok := conditionNodeLabel(n, reversed)
	if !ok {
		return
	}
	fn(label.field, label.operator, label.isWildcard, label.values...)
}

func conditionNodeWalkLogic(n *LogicNode, reversed bool, fn func(key string, operator string, isWildcard bool, values ...string)) {
	reversed = toggleReverse(reversed, n.reverseOp)
	if reversed {
		// NOT over a composite expression may turn inner positive leaves into optional
		// matches, for example NOT(a AND NOT b) => NOT a OR b.
		// Only a single child can be safely reduced by propagating the NOT.
		if len(n.Nodes) == 1 {
			conditionNodeWalk(n.Nodes[0], reversed, fn)
		}
		return
	}

	if logicNodeIsConjunction(n) {
		for _, ln := range n.Nodes {
			conditionNodeWalk(ln, reversed, fn)
		}
		return
	}

	if labels, ok := sameFieldDisjunctionLabels(n); ok {
		for _, label := range labels {
			fn(label.field, label.operator, label.isWildcard, label.values...)
		}
	}
}

type conditionWalkLabel struct {
	field      string
	operator   string
	isWildcard bool
	values     []string
}

func logicNodeIsConjunction(n *LogicNode) bool {
	for i := 1; i < len(n.Nodes); i++ {
		if logicNodeOperator(n, i) != logicAnd {
			return false
		}
	}
	return true
}

func logicNodeIsDisjunction(n *LogicNode) bool {
	if len(n.Nodes) <= 1 {
		return false
	}

	for i := 1; i < len(n.Nodes); i++ {
		if logicNodeOperator(n, i) != logicOR {
			return false
		}
	}
	return true
}

func logicNodeOperator(n *LogicNode, index int) string {
	if index <= 0 || index-1 >= len(n.logics) {
		return ""
	}
	return n.logics[index-1]
}

func sameFieldDisjunctionLabels(n *LogicNode) ([]conditionWalkLabel, bool) {
	if !logicNodeIsDisjunction(n) {
		return nil, false
	}

	labels := make([]conditionWalkLabel, 0, len(n.Nodes))
	var (
		field    string
		hasField bool
	)
	for _, node := range n.Nodes {
		label, ok := positiveLeafLabel(node)
		if !ok {
			return nil, false
		}
		if !hasField {
			field = label.field
			hasField = true
		} else if field != label.field {
			return nil, false
		}
		labels = append(labels, label)
	}

	return labels, true
}

func positiveLeafLabel(n *ConditionNode) (conditionWalkLabel, bool) {
	if n.value == nil || n.reverseOp {
		return conditionWalkLabel{}, false
	}

	if v, ok := n.value.(*LogicNode); ok {
		v.SetField(n.field)
		if len(v.Nodes) != 1 || v.reverseOp {
			return conditionWalkLabel{}, false
		}
		return positiveLeafLabel(v.Nodes[0])
	}

	return conditionNodeLabel(n, false)
}

func conditionNodeLabel(n *ConditionNode, reversed bool) (conditionWalkLabel, bool) {
	var (
		field      string
		op         string
		isWildcard bool
	)
	if n.field != nil {
		field = n.field.String()
	}
	if n.op != nil {
		op = n.op.String()
	}

	value := n.value.String()

	switch n.value.(type) {
	case *WildCardNode:
		op = metadata.ConditionContains
		isWildcard = true
		value = normalizeWildcardConditionValue(value)
	case *RegexpNode:
		op = metadata.ConditionRegEqual
	case *StringNode:
		op = metadata.ConditionEqual
		// 转义
		value = strings.ReplaceAll(value, `\`, ``)
		if n.isQuoted {
			value = strings.Trim(value, `"`)
		}
	default:
		return conditionWalkLabel{}, false
	}

	if reversed {
		op = reverseConditionOperator(op)
	}

	return conditionWalkLabel{
		field:      field,
		operator:   op,
		isWildcard: isWildcard,
		values:     []string{value},
	}, true
}

func toggleReverse(reversed, current bool) bool {
	return reversed != current
}

func reverseConditionOperator(op string) string {
	switch op {
	case metadata.ConditionEqual:
		return metadata.ConditionNotEqual
	case metadata.ConditionNotEqual:
		return metadata.ConditionEqual
	case metadata.ConditionContains:
		return metadata.ConditionNotContains
	case metadata.ConditionNotContains:
		return metadata.ConditionContains
	case metadata.ConditionRegEqual:
		return metadata.ConditionNotRegEqual
	case metadata.ConditionNotRegEqual:
		return metadata.ConditionRegEqual
	default:
		return op
	}
}

func normalizeWildcardConditionValue(value string) string {
	chars := []rune(value)
	var builder strings.Builder
	for i := 0; i < len(chars); i++ {
		if chars[i] == '\\' && i+1 < len(chars) {
			next := chars[i+1]
			switch next {
			case '*', '?', '\\':
				builder.WriteRune('\\')
				builder.WriteRune(next)
			default:
				builder.WriteRune(next)
			}
			i++
			continue
		}
		builder.WriteRune(chars[i])
	}
	return builder.String()
}
