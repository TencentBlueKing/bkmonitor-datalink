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

	originSQL string

	opt DorisListenerOption

	expr Expr

	depIndex int

	selectClause []string
	tableClause  string
	whereClause  []string
	groupClause  []string
	sortClause   []string
	limitClause  string
}

type DorisListenerOption struct {
	DimensionTransform Encode
	Table              string
	Where              string
}

func (l *DorisListener) EnterEveryRule(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		l.expr = NewSelect()
		// l.expr = &SelectV2{}
	case *gen.WhereClauseContext:
		l.expr = NewWhere()
	case *gen.FromClauseContext:
		l.expr = NewTable()
	case *gen.AggClauseContext:
		l.expr = NewAgg()
	case *gen.SortClauseContext:
		l.expr = NewSort()
	case *gen.LimitClauseContext:
		l.expr = NewLimit()
	}

	l.depIndex++
	log.Debugf(l.ctx, `"%d","ENTER","%T","%s"`, l.depIndex, ctx, ctx.GetText())
	if l.expr != nil {
		if l.opt.DimensionTransform != nil {
			l.expr.WithAliasEncode(l.opt.DimensionTransform)
		}
		l.expr.Enter(ctx)
	}
}

func (l *DorisListener) ExitEveryRule(ctx antlr.ParserRuleContext) {
	log.Debugf(l.ctx, `"%d","EXIT","%T","%s"`, l.depIndex, ctx, ctx.GetText())
	l.depIndex--

	if l.expr != nil {
		l.expr.Exit(ctx)
	}
	switch ctx.(type) {
	case *gen.SelectClauseContext:
		l.selectClause = append(l.selectClause, l.expr.String())
	case *gen.FromClauseContext:
		l.tableClause = l.expr.String()
	case *gen.WhereClauseContext:
		l.whereClause = append(l.whereClause, l.expr.String())
	case *gen.AggClauseContext:
		l.groupClause = append(l.groupClause, l.expr.String())
	case *gen.SortClauseContext:
		l.sortClause = append(l.sortClause, l.expr.String())
	case *gen.LimitClauseContext:
		l.limitClause = l.expr.String()
	}
}

func (l *DorisListener) WithOptions(opt DorisListenerOption) *DorisListener {
	l.opt = opt
	return l
}

func (l *DorisListener) SQL() (string, error) {
	if len(l.selectClause) == 0 {
		return "", fmt.Errorf("String 解析失败：%s", l.originSQL)
	}

	var sql strings.Builder
	sql.WriteString(fmt.Sprintf("SELECT %s", strings.Join(l.selectClause, ", ")))
	if l.opt.Table != "" {
		l.tableClause = l.opt.Table
	}
	if l.tableClause != "" {
		sql.WriteString(fmt.Sprintf(" FROM %s", l.tableClause))
	}
	if l.opt.Where != "" {
		l.whereClause = append(l.whereClause, l.opt.Where)
	}
	if len(l.whereClause) > 0 {
		sql.WriteString(fmt.Sprintf(" WHERE %s", strings.Join(l.whereClause, " AND ")))
	}
	if len(l.groupClause) > 0 {
		sql.WriteString(fmt.Sprintf(" GROUP BY %s", strings.Join(l.groupClause, ", ")))
	}
	if len(l.sortClause) > 0 {
		sql.WriteString(fmt.Sprintf(" ORDER BY %s", strings.Join(l.sortClause, ", ")))
	}
	if l.limitClause != "" {
		sql.WriteString(fmt.Sprintf(" %s", l.limitClause))
	}

	return sql.String(), nil
}

// NewDorisListener 创建带Token流的Listener
func NewDorisListener(ctx context.Context, sql string) *DorisListener {
	return &DorisListener{
		ctx:       ctx,
		originSQL: sql,
	}
}
