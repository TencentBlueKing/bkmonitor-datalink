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

type Schema struct {
	mapping map[string]string
	encode  func(k string) string
	decode  func(k string) string
}

type Parser struct {
	schema   Schema
	IsPrefix bool
}

func (s *Schema) GetActualFieldName(field string) string {
	if actual, ok := s.mapping[field]; ok {
		if s.decode != nil {
			return s.decode(field)
		}
		return actual
	}
	return field
}

func NewParser(mapping map[string]string, encode, decode func(k string) string) *Parser {
	p := &Parser{
		schema: Schema{
			mapping: mapping,
			encode:  encode,
			decode:  decode,
		},
		IsPrefix: false,
	}
	return p
}

type ParseResult struct {
	Expr Expr
	ES   elastic.Query
	SQL  string
}

func (p *Parser) Do(q string, isPrefix bool) (rt ParseResult, err error) {
	if q == "" || q == "*" {
		return ParseResult{
			Expr: nil,
			ES:   nil,
			SQL:  "",
		}, nil
	}

	expr, err := buildExpr(q)
	if err != nil {
		return
	}
	return ParseResult{
		Expr: expr,
		ES:   walkESWithSchema(expr, p.schema, isPrefix, true),
		SQL:  toSql(expr),
	}, nil
}

func buildExpr(queryString string) (Expr, error) {
	is := antlr.NewInputStream(queryString)
	lexer := gen.NewLuceneLexer(is)
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewLuceneParser(tokens)
	visitor := NewStatementVisitor()
	query := parser.TopLevelQuery()
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
