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
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewPromQLParser(tokens)

	errorListener := &promqlErrorListener{}
	parser.RemoveErrorListeners()
	parser.AddErrorListener(errorListener)
	tree := parser.InstantSelector()
	if errorListener.err != nil {
		return nil, errorListener.err
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

func (l *promqlErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.err = errors.Wrapf(l.err, "parse error at line %d:%d: %s", line, column, msg)
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

func (n *baseNode) VisitErrorNode(ctx antlr.ErrorNode) interface{} {
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

func (s *Statement) VisitChildren(ctx antlr.RuleNode) interface{} {
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

func (g *GroupNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node = g
	switch ctx.(type) {
	case *gen.LabelMatcherListContext:
		next = &LabelListNode{parent: g}
	}

	return visitChildren(next, ctx)
}

func (n *GroupNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerMETRIC_NAME:
		n.metricName = text
	}
	return nil
}

func (n *GroupNode) Matchers() []*labels.Matcher {
	var result []*labels.Matcher

	if n.metricName != "" {
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, n.metricName)
		if err != nil {
			log.Errorf(context.TODO(), "failed to create metric name matcher: %v", err)
		} else {
			result = append(result, matcher)
		}
	}

	for _, labelNode := range n.nodes {
		result = append(result, labelNode.Matchers()...)
	}

	return result
}

type LabelListNode struct {
	baseNode
	parent *GroupNode
}

func (n *LabelListNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node = n

	switch ctx.(type) {
	case *gen.LabelMatcherContext:
		m := &MatcherNode{}
		n.parent.nodes = append(n.parent.nodes, m)
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

func (n *MatcherNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node = n

	switch ctx.(type) {
	case *gen.LabelNameContext:
		next = &LabelNameNode{parent: n}
	case *gen.LabelMatcherOperatorContext:
		next = &OperatorNode{parent: n}
	}

	return visitChildren(next, ctx)
}

func (n *MatcherNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerSTRING:
		if len(text) >= 2 && ((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
			n.value = text[1 : len(text)-1]
		} else {
			n.value = text
		}
	}
	return nil
}

func (n *MatcherNode) Matchers() []*labels.Matcher {
	if n.labelName != "" && n.value != "" {
		matcher, err := labels.NewMatcher(operatorFromString(n.operator), n.labelName, n.value)
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

func (n *LabelNameNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next Node = n

	switch ctx.(type) {
	case *gen.KeywordContext:
		next = &KeywordNode{labelParent: n.parent}
	}

	return visitChildren(next, ctx)
}

func (n *LabelNameNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	tokenType := ctx.GetSymbol().GetTokenType()
	text := ctx.GetText()

	switch tokenType {
	case gen.PromQLLexerMETRIC_NAME, gen.PromQLLexerLABEL_NAME:
		n.parent.labelName = text
		log.Debugf(context.TODO(), `"LABEL_NAME","%s"`, text)
	}
	return nil
}

type OperatorNode struct {
	baseNode
	parent *MatcherNode
}

func (n *OperatorNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	tokenType := ctx.GetSymbol().GetTokenType()

	switch tokenType {
	case gen.PromQLLexerEQ:
		n.parent.operator = OperatorEqual
		log.Debugf(context.TODO(), `"OPERATOR","="`)
	case gen.PromQLLexerNE:
		n.parent.operator = OperatorNotEqual
		log.Debugf(context.TODO(), `"OPERATOR","!="`)
	case gen.PromQLLexerRE:
		n.parent.operator = OperatorRegexp
		log.Debugf(context.TODO(), `"OPERATOR","=~"`)
	case gen.PromQLLexerNRE:
		n.parent.operator = OperatorNotRegexp
		log.Debugf(context.TODO(), `"OPERATOR","!~"`)
	default:
		n.parent.operator = OperatorEqual
	}
	return nil
}

// KeywordNode handles keyword as label names
type KeywordNode struct {
	baseNode
	labelParent *MatcherNode
}

func (n *KeywordNode) VisitTerminal(ctx antlr.TerminalNode) interface{} {
	text := ctx.GetText()
	n.labelParent.labelName = text
	log.Debugf(context.TODO(), `"KEYWORD_AS_LABEL","%s"`, text)
	return nil
}

// visitChildren traverses child nodes
func visitChildren(next Node, node antlr.RuleNode) interface{} {
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
