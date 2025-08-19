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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type Option struct {
	DimensionTransform Encode
}

func ParseLuceneWithVisitor(ctx context.Context, q string, opt *Option) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			// 处理异常
			log.Errorf(ctx, "parse lucene query error: %v", r)
		}
	}()

	// 创建输入流
	is := antlr.NewInputStream(q)

	// 创建词法分析器
	lexer := gen.NewLuceneLexer(is)

	// 创建Token流
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewLuceneParser(tokens)

	// 创建解析树
	visitor := NewQueryVisitor(ctx)
	if opt != nil {
		visitor.WithEncode(opt.DimensionTransform)
	}

	log.Debugf(ctx, `"action","type","text"`)

	// 开始解析
	query := parser.TopLevelQuery()
	if query == nil {
		return "", fmt.Errorf("parse lucene query (%s) error: query is nil", q)
	}

	result := query.Accept(visitor)
	if result != nil {
		if node, ok := result.(Node); ok {
			visitor.root = node
		}
	}

	err := visitor.Error()
	if err != nil {
		return "", fmt.Errorf("parse lucene query (%s) error: %v", q, err)
	}

	return visitor.ToSQL(), nil
}

func ParseLuceneToES(ctx context.Context, q string, opt *Option) (interface{}, error) {
	defer func() {
		if r := recover(); r != nil {
			// 处理异常
			log.Errorf(ctx, "parse lucene query error: %v", r)
		}
	}()

	// 创建输入流
	is := antlr.NewInputStream(q)

	// 创建词法分析器
	lexer := gen.NewLuceneLexer(is)

	// 创建Token流
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewLuceneParser(tokens)

	// 创建解析树
	visitor := NewQueryVisitor(ctx)
	if opt != nil {
		visitor.WithEncode(opt.DimensionTransform)
	}

	log.Debugf(ctx, `"action","type","text"`)

	// 开始解析
	query := parser.TopLevelQuery()
	if query == nil {
		return nil, fmt.Errorf("parse lucene query (%s) error: query is nil", q)
	}

	result := query.Accept(visitor)
	if result != nil {
		if node, ok := result.(Node); ok {
			visitor.root = node
		}
	}

	err := visitor.Error()
	if err != nil {
		return nil, fmt.Errorf("parse lucene query (%s) error: %v", q, err)
	}

	esQuery := visitor.ToES()
	return esQuery, nil
}
