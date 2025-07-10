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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type DorisListener struct {
	gen.BaseDorisParserListener
	ctx context.Context

	sql string

	opt DorisListenerOption

	expr Expr

	Select    string
	Condition string
	Table     string
}

type DorisListenerOption struct {
	DimensionTransform func(s string) string
}

func (l *DorisListener) EnterEveryRule(ctx antlr.ParserRuleContext) {
	log.Infof(l.ctx, "enter %T %s", ctx, ctx.GetText())
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		l.expr = &SelectExpr{}
	case *gen.FromClauseContext:
		l.expr = &TableExpr{}
	}

	if l.expr != nil {
		l.expr.Enter(ctx)
	}
}

func (l *DorisListener) ExitEveryRule(ctx antlr.ParserRuleContext) {
	log.Infof(l.ctx, "exit %T %s", ctx, ctx.GetText())
	if l.expr != nil {
		l.expr.Exit(ctx)
	}
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		l.Select = l.expr.String()
	case *gen.FromClauseContext:
		l.Table = l.expr.String()
	}
}

func (l *DorisListener) WithOptions(opt DorisListenerOption) *DorisListener {
	l.opt = opt
	return l
}

func (l *DorisListener) SQL() (string, error) {
	if l.Select == "" {
		return "", fmt.Errorf("sql 解析异常: %s", l.sql)
	}

	return strings.Join([]string{
		l.Select, l.Table, l.Condition,
	}, " "), nil

}

// NewDorisListener 创建带Token流的Listener
func NewDorisListener(ctx context.Context, sql string) *DorisListener {
	return &DorisListener{
		ctx: ctx,
		sql: sql,
	}
}
