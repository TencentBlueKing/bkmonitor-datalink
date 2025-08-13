// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring_parser

//go:generate goyacc -o querystring.y.go querystring.y

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Parse querystring and return Expr
func Parse(query string) (Expr, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(context.TODO(), "parse querystring panic: %v", r)
		}
	}()
	if query == "" || query == "*" {
		return nil, nil
	}

	query = regexp.MustCompile(`(?i)(\s+\b(?:AND|OR)\b\s*)+$`).ReplaceAllString(query, "")

	lex := newLexerWrapper(newExprStringLex(strings.NewReader(query)))
	doParse(lex)

	if len(lex.errs) > 0 {
		return nil, fmt.Errorf(strings.Join(lex.errs, "\n"))
	}
	return lex.expr, nil
}

type walkParse struct {
	fieldAlias map[string]string
}

func (w *walkParse) alias(k string) string {
	if alias, ok := w.fieldAlias[k]; ok {
		return alias
	}
	return k
}

func (w *walkParse) do(e Expr) Expr {
	switch c := e.(type) {
	case *NotExpr:
		return &NotExpr{
			Expr: w.do(c.Expr),
		}
	case *AndExpr:
		return &AndExpr{
			Left:  w.do(c.Left),
			Right: w.do(c.Right),
		}
	case *OrExpr:
		return &OrExpr{
			Left:  w.do(c.Left),
			Right: w.do(c.Right),
		}
	case *NumberRangeExpr:
		if c.Field != "" {
			c.Field = w.alias(c.Field)
		}
	case *MatchExpr:
		if c.Field != "" {
			c.Field = w.alias(c.Field)
		}
	case *WildcardExpr:
		if c.Field != "" {
			c.Field = w.alias(c.Field)
		}
	case *RegexpExpr:
		c.Field = w.alias(c.Field)
	}
	return e
}

func ParseWithFieldAlias(query string, fieldAlias map[string]string) (Expr, error) {
	expr, err := Parse(query)
	if err != nil {
		return nil, err
	}
	wp := &walkParse{fieldAlias: fieldAlias}
	return wp.do(expr), nil
}

func doParse(lex *lexerWrapper) {
	yyParse(lex)
}

const (
	queryShould = iota
	queryMust
	queryMustNot
)

type lexerWrapper struct {
	lex  yyLexer
	errs []string
	expr Expr
}

func newLexerWrapper(lex yyLexer) *lexerWrapper {
	return &lexerWrapper{
		lex:  lex,
		expr: nil,
	}
}

func (l *lexerWrapper) Lex(lval *yySymType) int {
	return l.lex.Lex(lval)
}

func (l *lexerWrapper) Error(s string) {
	l.errs = append(l.errs, s)
}

func init() {
	yyErrorVerbose = true
}
