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
	"github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
)

type Parser struct {
	EsSchemas []FieldSchema
}

type ParserOption struct {
	EsSchema []FieldSchema
}

func WithEsSchema(esSchema FieldSchema) func(*Parser) {
	return func(p *Parser) {
		p.EsSchemas = append(p.EsSchemas, esSchema)
	}
}

type Option func(*Parser)

func NewParser(opts ...Option) *Parser {
	p := &Parser{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type ParseResult struct {
	Expr Expr
	ES   elastic.Query
	SQL  string
}

func (p *Parser) Do(q string) (rt ParseResult, err error) {
	expr, err := buildExpr(q)
	if err != nil {
		return
	}
	return ParseResult{
		Expr: expr,
		ES:   es(expr, p.EsSchemas...),
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
