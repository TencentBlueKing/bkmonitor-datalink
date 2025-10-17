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

func parseTerm(s string) Node {
	// 先提取boost后缀
	var boost string
	baseValue := s
	boostParent := regexp.MustCompile(`^(.+)\^([\d\.]+)$`)
	boostMatches := boostParent.FindStringSubmatch(s)
	if len(boostMatches) == 3 {
		baseValue = boostMatches[1]
		boost = boostMatches[2]
	}

	// 再判断baseValue是否是range
	rangeParent := regexp.MustCompile(`^([\[{])(.+)TO(.+)([\]}])$`)
	all := rangeParent.FindStringSubmatch(baseValue)
	if len(all) == 5 {
		node := &RangeNode{
			IsIncludeStart: all[1] == "[",
			IsIncludeEnd:   all[4] == "]",
			Boost:          boost,
		}

		all[2] = strings.Trim(all[2], `"`)
		if all[2] != "*" {
			node.Start = &StringNode{
				Value: all[2],
			}
		}
		all[3] = strings.Trim(all[3], `"`)
		if all[3] != "*" {
			node.End = &StringNode{
				Value: all[3],
			}
		}
		return node
	}

	// 如果有boost但不是range，返回带boost的StringNode
	if boost != "" {
		return &StringNode{
			Value: baseValue,
			Boost: boost,
		}
	}

	regexpParent := regexp.MustCompile(`^/(.+)/$`)
	all = regexpParent.FindStringSubmatch(s)
	if len(all) == 2 {
		return &RegexpNode{
			Value: all[1],
		}
	}

	aliasStr := strings.ReplaceAll(s, `\*`, "")
	aliasStr = strings.ReplaceAll(aliasStr, `\?`, "")

	if strings.ContainsAny(aliasStr, "*?") {
		return &WildCardNode{
			Value: s,
		}
	}

	return &StringNode{
		Value: s,
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

func ConditionNodeWalk(node Node, fn func(key string, operator string, values ...string)) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ConditionNode:
		if n.value == nil {
			return
		}

		var (
			field string
			op    string
		)
		if n.field != nil {
			field = n.field.String()
		}
		if n.op != nil {
			op = n.op.String()
		}

		value := n.value.String()

		switch v := n.value.(type) {
		case *WildCardNode:
			op = metadata.ConditionContains
			var (
				ns       []rune
				lastChar rune
			)
			for _, char := range value {
				if char != '*' && char != '?' && char != '\\' || lastChar == '\\' {
					ns = append(ns, char)
				}
				lastChar = char
			}
			value = string(ns)
		case *RegexpNode:
			op = metadata.ConditionRegEqual
		case *StringNode:
			op = metadata.ConditionEqual
			// 转义
			value = strings.ReplaceAll(value, `\`, ``)
			if n.isQuoted {
				value = strings.Trim(value, `"`)
			}
		case *LogicNode:
			v.SetField(n.field)
			ConditionNodeWalk(n.value, fn)
			return
		default:
			return
		}

		if n.reverseOp {
			switch op {
			case metadata.ConditionEqual:
				op = metadata.ConditionNotEqual
			case metadata.ConditionNotEqual:
				op = metadata.ConditionEqual
			case metadata.ConditionContains:
				op = metadata.ConditionNotContains
			case metadata.ConditionNotContains:
				op = metadata.ConditionContains
			case metadata.ConditionRegEqual:
				op = metadata.ConditionNotRegEqual
			case metadata.ConditionNotRegEqual:
				op = metadata.ConditionRegEqual
			}
		}

		fn(field, op, value)
	case *LogicNode:
		for _, ln := range n.Nodes {
			ConditionNodeWalk(ln, fn)
		}
	}
}
