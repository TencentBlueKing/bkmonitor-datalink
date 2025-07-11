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
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
)

type DorisListener struct {
	gen.BaseDorisParserListener
	ctx context.Context

	originSQL string

	opt DorisListenerOption

	expr Expr

	depIndex int

	exprString []string
	err        error
}

type DorisListenerOption struct {
	DimensionTransform func(s string) string
}

func (l *DorisListener) writeSQL() {
	s := l.expr.String()
	if s == "" {
		return
	}
	l.exprString = append(l.exprString, s)
}

func (l *DorisListener) EnterEveryRule(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		l.expr = &SelectExpr{}
	case *gen.FromClauseContext:
		l.expr = &TableExpr{}
	case *gen.WhereClauseContext:
		l.expr = &WhereExpr{}
	}

	if l.expr != nil {
		if l.expr.LoggerEnable() {
			l.depIndex++
			fmt.Printf("%d,ENTER,%T,%s\n", l.depIndex, ctx, ctx.GetText())
		}
		l.expr.Enter(ctx)
	}
}

func (l *DorisListener) ExitEveryRule(ctx antlr.ParserRuleContext) {
	if l.expr != nil {
		if l.expr.LoggerEnable() {
			fmt.Printf("%d,EXIT,%T,%s\n", l.depIndex, ctx, ctx.GetText())
			l.depIndex--
		}
		l.expr.Exit(ctx)
	}
	switch ctx.(type) {
	case *gen.SelectClauseContext, *gen.FromClauseContext, *gen.WhereClauseContext:
		l.writeSQL()
	}
}

func (l *DorisListener) WithOptions(opt DorisListenerOption) *DorisListener {
	l.opt = opt
	return l
}

func (l *DorisListener) SQL() string {
	if len(l.exprString) == 0 {
		l.err = fmt.Errorf("SQL 解析失败：%s", l.originSQL)
	}
	return strings.Join(l.exprString, " ")
}

// NewDorisListener 创建带Token流的Listener
func NewDorisListener(ctx context.Context, sql string) *DorisListener {
	return &DorisListener{
		ctx:       ctx,
		originSQL: sql,
	}
}
