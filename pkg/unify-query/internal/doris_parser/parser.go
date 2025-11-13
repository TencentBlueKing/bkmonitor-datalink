// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"fmt"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func ParseDorisSQLWithVisitor(ctx context.Context, q string, opt *Option) (string, error) {
	defer func() {
		if r := recover(); r != nil {
			_ = metadata.Sprintf(
				metadata.MsgParserDoris,
				"Doris 语法解析异常",
			).Error(ctx, fmt.Errorf("%v", r))
		}
	}()

	// 创建输入流
	is := antlr.NewInputStream(q)

	// 创建词法分析器
	lexer := gen.NewDorisLexer(is)

	// 创建Token流
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewDorisParserParser(tokens)

	// 创建解析树
	// visitor := NewDorisVisitor(ctx, q).WithOptions(opt)

	stmt := &Statement{}
	if opt != nil {
		stmt.WithEncode(opt.DimensionTransform)
		stmt.WithAddIgnoreField(opt.AddIgnoreField)
		stmt.Tables = opt.Tables
		stmt.Where = opt.Where

		stmt.Limit = opt.Limit
		stmt.Offset = opt.Offset
	}

	log.Debugf(ctx, `"action","type","text"`)

	// 开始解析
	parser.Query().Accept(stmt)

	err := stmt.Error()
	if err != nil {
		return "", fmt.Errorf("parse doris sql (%s) error: %v", q, err.Error())
	}

	return stmt.String(), nil
}

func ParseDorisSQLWithListener(ctx context.Context, q string, opt DorisListenerOption) *DorisListener {
	defer func() {
		if r := recover(); r != nil {
			_ = metadata.Sprintf(
				metadata.MsgParserDoris,
				"Doris 语法解析",
			).Error(ctx, fmt.Errorf("%v", r))
		}
	}()

	// 创建输入流
	is := antlr.NewInputStream(q)

	// 创建词法分析器
	lexer := gen.NewDorisLexer(is)

	// 创建Token流
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := gen.NewDorisParserParser(tokens)

	// 创建解析树
	listener := NewDorisListener(ctx, q).
		WithOptions(opt)

	antlr.ParseTreeWalkerDefault.Walk(listener, parser.Query())
	return listener
}
