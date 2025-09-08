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

	result := NewMatcherVisitor().VisitChildren(tree)
	matchers, ok := result.([]*labels.Matcher)
	if !ok {
		return nil, fmt.Errorf("unexpected result type")
	}

	return matchers, nil
}

type promqlErrorListener struct {
	err error
}

func (l *promqlErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.err = errors.Wrapf(l.err, "failed to parse %s", msg)
}

func (l *promqlErrorListener) ReportAmbiguity(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex int, exact bool, ambigAlts *antlr.BitSet, configs *antlr.ATNConfigSet) {
}

func (l *promqlErrorListener) ReportAttemptingFullContext(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex int, conflictingAlts *antlr.BitSet, configs *antlr.ATNConfigSet) {
}

func (l *promqlErrorListener) ReportContextSensitivity(recognizer antlr.Parser, dfa *antlr.DFA, startIndex, stopIndex, prediction int, configs *antlr.ATNConfigSet) {
}

type MatcherVisitor struct {
	*gen.BasePromQLParserVisitor
}

func NewMatcherVisitor() *MatcherVisitor {
	return &MatcherVisitor{
		BasePromQLParserVisitor: &gen.BasePromQLParserVisitor{
			BaseParseTreeVisitor: &antlr.BaseParseTreeVisitor{},
		},
	}
}

func (v *MatcherVisitor) VisitChildren(tree antlr.ParseTree) interface{} {
	if tree == nil {
		return nil
	}

	switch ctx := tree.(type) {
	case *gen.InstantSelectorContext:
		return v.VisitInstantSelector(ctx)
	case *gen.LabelMatcherListContext:
		return v.VisitLabelMatcherList(ctx)
	case *gen.LabelMatcherContext:
		return v.VisitLabelMatcher(ctx)
	case *gen.LabelMatcherOperatorContext:
		return v.VisitLabelMatcherOperator(ctx)
	case *gen.LabelNameContext:
		return v.VisitLabelName(ctx)
	case *gen.KeywordContext:
		return v.VisitKeyword(ctx)
	default:
		return nil
	}
}

func (v *MatcherVisitor) VisitInstantSelector(ctx *gen.InstantSelectorContext) interface{} {
	var matchers []*labels.Matcher

	if ctx.METRIC_NAME() != nil {
		metricName := ctx.METRIC_NAME().GetText()
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, metricName)
		if err != nil {
			log.Errorf(nil, "failed to create metric name matcher: %v", err)
			return nil
		}
		matchers = append(matchers, matcher)
	}

	if ctx.LabelMatcherList() != nil {
		labelMatchers := v.VisitChildren(ctx.LabelMatcherList())
		if labelMatchers != nil {
			if lm, ok := labelMatchers.([]*labels.Matcher); ok {
				matchers = append(matchers, lm...)
			}
		}
	}

	return matchers
}

func (v *MatcherVisitor) VisitLabelMatcherList(ctx *gen.LabelMatcherListContext) interface{} {
	var matchers []*labels.Matcher

	for _, labelMatcherCtx := range ctx.AllLabelMatcher() {
		result := v.VisitChildren(labelMatcherCtx)
		if result != nil {
			if matcher, ok := result.(*labels.Matcher); ok {
				matchers = append(matchers, matcher)
			}
		}
	}

	return matchers
}

func (v *MatcherVisitor) VisitLabelMatcher(ctx *gen.LabelMatcherContext) interface{} {
	labelNameResult := v.VisitChildren(ctx.LabelName())
	labelName, ok := labelNameResult.(string)
	if !ok {
		log.Errorf(nil, "failed to get label name")
		return nil
	}

	operatorResult := v.VisitChildren(ctx.LabelMatcherOperator())
	matchType, ok := operatorResult.(labels.MatchType)
	if !ok {
		log.Errorf(nil, "failed to get match type")
		return nil
	}

	value := ctx.STRING().GetText()
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}

	matcher, err := labels.NewMatcher(matchType, labelName, value)
	if err != nil {
		log.Errorf(nil, "failed to create matcher: %v", err)
		return nil
	}

	return matcher
}

func (v *MatcherVisitor) VisitLabelMatcherOperator(ctx *gen.LabelMatcherOperatorContext) interface{} {
	switch {
	case ctx.EQ() != nil:
		return labels.MatchEqual
	case ctx.NE() != nil:
		return labels.MatchNotEqual
	case ctx.RE() != nil:
		return labels.MatchRegexp
	case ctx.NRE() != nil:
		return labels.MatchNotRegexp
	default:
		// 默认是label equal
		return labels.MatchEqual
	}
}

func (v *MatcherVisitor) VisitLabelName(ctx *gen.LabelNameContext) interface{} {
	if ctx.METRIC_NAME() != nil {
		return ctx.METRIC_NAME().GetText()
	}
	if ctx.LABEL_NAME() != nil {
		return ctx.LABEL_NAME().GetText()
	}
	if ctx.Keyword() != nil {
		keywordResult := v.VisitChildren(ctx.Keyword())
		if keywordResult != nil {
			return keywordResult.(string)
		}
	}
	return ""
}

func (v *MatcherVisitor) VisitKeyword(ctx *gen.KeywordContext) interface{} {
	switch {
	case ctx.AND() != nil:
		return ctx.AND().GetText()
	case ctx.OR() != nil:
		return ctx.OR().GetText()
	case ctx.UNLESS() != nil:
		return ctx.UNLESS().GetText()
	case ctx.BY() != nil:
		return ctx.BY().GetText()
	case ctx.WITHOUT() != nil:
		return ctx.WITHOUT().GetText()
	case ctx.ON() != nil:
		return ctx.ON().GetText()
	case ctx.IGNORING() != nil:
		return ctx.IGNORING().GetText()
	case ctx.GROUP_LEFT() != nil:
		return ctx.GROUP_LEFT().GetText()
	case ctx.GROUP_RIGHT() != nil:
		return ctx.GROUP_RIGHT().GetText()
	case ctx.OFFSET() != nil:
		return ctx.OFFSET().GetText()
	case ctx.BOOL() != nil:
		return ctx.BOOL().GetText()
	case ctx.AGGREGATION_OPERATOR() != nil:
		return ctx.AGGREGATION_OPERATOR().GetText()
	case ctx.FUNCTION() != nil:
		return ctx.FUNCTION().GetText()
	default:
		return ""
	}
}
