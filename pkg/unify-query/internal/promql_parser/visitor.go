// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql_parser

import (
	"context"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/promql_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type Node interface {
	antlr.ParseTreeVisitor
	Error() error
	Matchers(ctx context.Context) []*labels.Matcher
}

type baseNode struct {
	antlr.BaseParseTreeVisitor
	err      error
	matchers []*labels.Matcher
}

func (n *baseNode) Error() error {
	return n.err
}

func (n *baseNode) Matchers(ctx context.Context) []*labels.Matcher {
	return n.matchers
}

func (n *baseNode) VisitErrorNode(ctx antlr.ErrorNode) any {
	n.err = errors.Wrapf(n.err, "parse error at: %s", ctx.GetText())
	return nil
}

type Statement struct {
	baseNode

	ctx  context.Context
	node Node
}

func NewStatement(ctx context.Context) *Statement {
	return &Statement{
		ctx: ctx,
	}
}

func (s *Statement) VisitChildren(ctx antlr.RuleNode) any {
	var next Node = s

	switch ctx.(type) {
	case *gen.InstantSelectorContext:
		s.node = &GroupNode{}
		next = s.node
	}

	return visitChildren(next, ctx)
}

func (s *Statement) Matchers(ctx context.Context) []*labels.Matcher {
	if s.node != nil {
		return s.node.Matchers(s.ctx)
	}
	return s.matchers
}

type GroupNode struct {
	baseNode
	metricName string
	nodes      []Node
}

func (g *GroupNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node = g
	switch ctx.(type) {
	case *gen.LabelMatcherContext:
		next = &MatcherNode{}
		g.nodes = append(g.nodes, next)
	}

	return visitChildren(next, ctx)
}

func (g *GroupNode) VisitTerminal(ctx antlr.TerminalNode) any {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerMETRIC_NAME:
		g.metricName = text
	}
	return nil
}

func (g *GroupNode) Matchers(ctx context.Context) []*labels.Matcher {
	var result []*labels.Matcher

	if g.metricName != "" {
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, g.metricName)
		if err != nil {
			_ = metadata.Sprintf(
				metadata.MsgParserDoris,
				"promql matcher metric 解析失败",
			).Error(ctx, err)
		} else {
			result = append(result, matcher)
		}
	}

	for _, labelNode := range g.nodes {
		result = append(result, labelNode.Matchers(ctx)...)
	}

	return result
}

type MatcherNode struct {
	baseNode
	labelName string
	operator  string
	value     string
}

func (m *MatcherNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node = m

	switch ctx.(type) {
	case *gen.LabelNameContext:
		m.labelName = ctx.GetText()
	case *gen.LabelMatcherOperatorContext:
		m.operator = ctx.GetText()
	}

	return visitChildren(next, ctx)
}

func (m *MatcherNode) VisitTerminal(ctx antlr.TerminalNode) any {
	text := ctx.GetText()

	text = strings.Trim(text, `"`)
	text = strings.Trim(text, `'`)
	if m.operator != "" {
		m.value = text
	}

	return nil
}

func (m *MatcherNode) Matchers(ctx context.Context) []*labels.Matcher {
	if m.labelName != "" && m.value != "" {
		matcher, err := labels.NewMatcher(operatorFromString(m.operator), m.labelName, m.value)
		if err != nil {
			err = metadata.Sprintf(
				metadata.MsgParserDoris,
				"promql matcher 解析",
			).Error(ctx, err)
			return nil
		}
		return []*labels.Matcher{matcher}
	}
	return nil
}

var (
	OperatorEqual     = "="
	OperatorNotEqual  = "!="
	OperatorRegexp    = "=~"
	OperatorNotRegexp = "!~"
)

func operatorFromString(op string) labels.MatchType {
	switch op {
	case OperatorEqual:
		return labels.MatchEqual
	case OperatorNotEqual:
		return labels.MatchNotEqual
	case OperatorRegexp:
		return labels.MatchRegexp
	case OperatorNotRegexp:
		return labels.MatchNotRegexp
	default:
		return labels.MatchEqual
	}
}

func visitChildren(next Node, node antlr.RuleNode) any {
	for _, child := range node.GetChildren() {
		switch tree := child.(type) {
		case antlr.ParseTree:
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(next)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}
	return nil
}
