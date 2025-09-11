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

	antlr "github.com/antlr4-go/antlr/v4"
	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
)

type Parser struct {
	esSchema    esSchemaProvider
	dorisSchema dorisSchemaProvider
	IsPrefix    bool
}

type parserOption struct {
	Mapping          map[string]string
	Alias            map[string]string
	AliasFunc        func(k string) string
	GetFieldTypeFunc func(field string) (string, bool)
}

type OptionFunc func(*parserOption)

func WithMapping(mapping map[string]string) OptionFunc {
	return func(o *parserOption) {
		o.Mapping = mapping
	}
}

func WithAlias(alias map[string]string) OptionFunc {
	return func(o *parserOption) {
		o.Alias = alias
	}
}

func WithAliasFunc(f func(k string) string) OptionFunc {
	return func(o *parserOption) {
		o.Alias = nil
		o.AliasFunc = f
	}
}

func NewParser(opts ...OptionFunc) *Parser {
	option := &parserOption{}

	for _, o := range opts {
		o(option)
	}

	getFieldType := func(field string) (string, bool) {
		if option.GetFieldTypeFunc != nil {
			return option.GetFieldTypeFunc(field)
		}
		if option.Mapping != nil {
			if t, ok := option.Mapping[field]; ok {
				return t, true
			} else {
				return "", false
			}
		}
		return "", false
	}

	getAlias := func(k string) string {
		if option.AliasFunc != nil {
			return option.AliasFunc(k)
		}
		if option.Alias != nil {
			if v, ok := option.Alias[k]; ok {
				return v
			}
		}
		return k
	}

	esSchema := NewESSchema(getFieldType, getAlias, option.Mapping)
	dorisSchemaEntry := NewDorisSchema(getFieldType, getAlias)

	p := &Parser{
		esSchema:    esSchema,
		dorisSchema: dorisSchemaEntry,
		IsPrefix:    false,
	}
	return p
}

type ParseResult struct {
	Expr Expr
	ES   elastic.Query
	SQL  string
}

func (p *Parser) Parse(q string, isPrefix bool) (rt ParseResult, err error) {
	if q == "" || q == "*" {
		return ParseResult{
			Expr: nil,
			ES:   nil,
			SQL:  "",
		}, nil
	}
	expr, err := buildExpr(q)
	if err != nil {
		return rt, fmt.Errorf("parse lucene query (%s) error: %w", q, err)
	}
	rt.Expr = expr
	rt.SQL = p.toSql(expr)
	rt.ES = p.toES(expr, isPrefix)
	return rt, nil
}

// CustomErrorListener captures syntax errors during parsing
type CustomErrorListener struct {
	*antlr.DefaultErrorListener
	errors []string
}

func NewCustomErrorListener() *CustomErrorListener {
	return &CustomErrorListener{
		DefaultErrorListener: antlr.NewDefaultErrorListener(),
		errors:               make([]string, 0),
	}
}

func (c *CustomErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	c.errors = append(c.errors, fmt.Sprintf("syntax error: %s", msg))
}

func (c *CustomErrorListener) HasErrors() bool {
	return len(c.errors) > 0
}

func (c *CustomErrorListener) GetFirstError() string {
	if len(c.errors) > 0 {
		return c.errors[0]
	}
	return ""
}

func buildExpr(queryString string) (Expr, error) {
	is := antlr.NewInputStream(queryString)
	lexer := gen.NewLuceneLexer(is)

	lexerErrorListener := NewCustomErrorListener()
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(lexerErrorListener)

	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewLuceneParser(tokens)
	parserErrorListener := NewCustomErrorListener()
	parser.RemoveErrorListeners()
	parser.AddErrorListener(parserErrorListener)

	visitor := NewStatementVisitor()
	query := parser.TopLevelQuery()
	if lexerErrorListener.HasErrors() {
		return nil, fmt.Errorf(lexerErrorListener.GetFirstError())
	}
	if parserErrorListener.HasErrors() {
		return nil, fmt.Errorf(parserErrorListener.GetFirstError())
	}

	if query == nil {
		return nil, fmt.Errorf("parse lucene query (%s) error: query is nil", queryString)
	}

	result := query.Accept(visitor)
	if result != nil {
		if node, ok := result.(Node); ok {
			visitor.node = node
		}
	}

	err := visitor.Error()
	return visitor.Expr(), err
}
