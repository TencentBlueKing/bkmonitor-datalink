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
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/promql_parser/gen"
)

func ParseMetricSelector(selector string) ([]*labels.Matcher, error) {
	if selector == "" {
		return nil, nil
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
