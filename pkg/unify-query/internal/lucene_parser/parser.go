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
	"context"
	"fmt"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type Option struct {
	reverseFieldAlias map[string]string

	FieldsMap       metadata.FieldsMap
	FieldEncodeFunc func(string) string
}

// ParseLuceneWithVisitor 解析
func ParseLuceneWithVisitor(ctx context.Context, q string, opt Option) Node {
	defer func() {
		if r := recover(); r != nil {
			_ = metadata.Sprintf(
				metadata.MsgParserLucene,
				"Lucene 语法解析异常",
			).Error(ctx, fmt.Errorf("%v", r))
		}
	}()

	opt.reverseFieldAlias = make(map[string]string)
	for k, v := range opt.FieldsMap {
		if v.AliasName != "" {
			opt.reverseFieldAlias[v.AliasName] = k
		}
	}

	if q == "" || q == "*" {
		return &StringNode{
			Value: "",
		}
	}

	is := antlr.NewInputStream(q)
	lexer := gen.NewLuceneLexer(is)

	lexerErrorListener := NewCustomErrorListener()

	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(lexerErrorListener)

	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewLuceneParser(tokens)
	parserErrorListener := NewCustomErrorListener()
	parser.RemoveErrorListeners()
	parser.AddErrorListener(parserErrorListener)

	visitor := &LogicNode{}
	visitor.WithOption(opt)
	query := parser.TopLevelQuery()
	if lexerErrorListener.HasErrors() {
		return getErrorNode(lexerErrorListener.GetFirstError())
	}
	if parserErrorListener.HasErrors() {
		return getErrorNode(parserErrorListener.GetFirstError())
	}

	if query == nil {
		return getErrorNode(fmt.Sprintf("parse lucene query (%s) error: query is nil", q))
	}

	query.Accept(visitor)
	return visitor
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

func (c *CustomErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol any, line, column int, msg string, e antlr.RecognitionException) {
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
