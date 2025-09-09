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
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/promql_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func ParseMetricSelector(selector string) ([]*labels.Matcher, error) {
	if selector == "" {
		return nil, fmt.Errorf("empty selector")
	}

	selector = strings.TrimSpace(selector)
	return parseWithANTLR(selector)
}

func parseWithANTLR(selector string) ([]*labels.Matcher, error) {
	inputStream := antlr.NewInputStream(selector)
	lexer := gen.NewPromQLLexer(inputStream)

	errorListener := &promqlErrorListener{}
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errorListener)
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewPromQLParser(tokens)
	parser.RemoveErrorListeners()
	parser.AddErrorListener(errorListener)
	tree := parser.InstantSelector()
	if errorListener.err != nil {
		return nil, errorListener.err
	}
	tokenStream := parser.GetTokenStream().(*antlr.CommonTokenStream)
	if tokenStream.LA(1) != antlr.TokenEOF {
		return nil, fmt.Errorf("unexpected tokens after selector: %s", selector)
	}

	statement := NewStatement()
	tree.Accept(statement)
	if statement.Error() != nil {
		return nil, statement.Error()
	}

	return statement.Matchers(), nil
}

type promqlErrorListener struct {
	err error
}

func (l *promqlErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol any, line, column int, msg string, e antlr.RecognitionException) {
	if l.err == nil {
		l.err = fmt.Errorf("syntax error at line %d:%d: %s", line, column, msg)
	} else {
		l.err = fmt.Errorf("%w; syntax error at line %d:%d: %s", l.err, line, column, msg)
	}
}

func (l *promqlErrorListener) ReportAmbiguity(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex int, exact bool, ambigAlts *antlr.BitSet, configs *antlr.ATNConfigSet) {
}

func (l *promqlErrorListener) ReportAttemptingFullContext(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex int, conflictingAlts *antlr.BitSet, configs *antlr.ATNConfigSet) {
}

func (l *promqlErrorListener) ReportContextSensitivity(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex, prediction int, configs *antlr.ATNConfigSet) {
}

type Node interface {
	antlr.ParseTreeVisitor
	Error() error
	Matchers() []*labels.Matcher
}

type baseNode struct {
	antlr.BaseParseTreeVisitor
	err      error
	matchers []*labels.Matcher
}

func (n *baseNode) Error() error {
	return n.err
}

func (n *baseNode) Matchers() []*labels.Matcher {
	return n.matchers
}

func (n *baseNode) VisitErrorNode(ctx antlr.ErrorNode) any {
	n.err = errors.Wrapf(n.err, "parse error at: %s", ctx.GetText())
	return nil
}

type Statement struct {
	baseNode
	node Node
}

func NewStatement() *Statement {
	return &Statement{}
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

func (s *Statement) Matchers() []*labels.Matcher {
	if s.node != nil {
		return s.node.Matchers()
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
	case *gen.LabelMatcherListContext:
		next = &LabelListNode{parent: g}
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

func (g *GroupNode) Matchers() []*labels.Matcher {
	var result []*labels.Matcher

	if g.metricName != "" {
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, g.metricName)
		if err != nil {
			log.Errorf(context.TODO(), "failed to create metric name matcher: %v", err)
		} else {
			result = append(result, matcher)
		}
	}

	for _, labelNode := range g.nodes {
		result = append(result, labelNode.Matchers()...)
	}

	return result
}

type LabelListNode struct {
	baseNode
	parent *GroupNode
}

func (l *LabelListNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node = l

	switch ctx.(type) {
	case *gen.LabelMatcherContext:
		m := &MatcherNode{}
		l.parent.nodes = append(l.parent.nodes, m)
		next = m
	}

	return visitChildren(next, ctx)
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
		next = &LabelNameNode{parent: m}
	case *gen.LabelMatcherOperatorContext:
		next = &OperatorNode{parent: m}
	}

	return visitChildren(next, ctx)
}

func (m *MatcherNode) VisitTerminal(ctx antlr.TerminalNode) any {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerSTRING:
		if len(text) >= 2 && ((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
			m.value = text[1 : len(text)-1]
		} else {
			m.value = text
		}
	}
	return nil
}

func (m *MatcherNode) Matchers() []*labels.Matcher {
	if m.labelName != "" && m.value != "" {
		matcher, err := labels.NewMatcher(operatorFromString(m.operator), m.labelName, m.value)
		if err != nil {
			log.Errorf(context.TODO(), "failed to create label matcher: %v", err)
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

type LabelNameNode struct {
	baseNode
	parent *MatcherNode
}

func (l *LabelNameNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node = l

	switch ctx.(type) {
	case *gen.KeywordContext:
		next = &KeywordNode{labelParent: l.parent}
	}

	return visitChildren(next, ctx)
}

func (l *LabelNameNode) VisitTerminal(ctx antlr.TerminalNode) any {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerMETRIC_NAME, gen.PromQLLexerLABEL_NAME:
		l.parent.labelName = text
		log.Debugf(context.TODO(), `"LABEL_NAME","%s"`, text)
	}
	return nil
}

type OperatorNode struct {
	baseNode
	parent *MatcherNode
}

func (o *OperatorNode) VisitTerminal(ctx antlr.TerminalNode) any {
	tokenType := ctx.GetSymbol().GetTokenType()

	switch tokenType {
	case gen.PromQLLexerEQ:
		o.parent.operator = OperatorEqual
	case gen.PromQLLexerNE:
		o.parent.operator = OperatorNotEqual
	case gen.PromQLLexerRE:
		o.parent.operator = OperatorRegexp
	case gen.PromQLLexerNRE:
		o.parent.operator = OperatorNotRegexp
	default:
		o.parent.operator = OperatorEqual
	}
	return nil
}

type KeywordNode struct {
	baseNode
	labelParent *MatcherNode
}

func (n *KeywordNode) VisitTerminal(ctx antlr.TerminalNode) any {
	text := ctx.GetText()
	n.labelParent.labelName = text
	return nil
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
