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

	fieldListExpr []*FieldExpr
	fieldExpr     *FieldExpr

	conditionExpr *ConditionExpr

	Selects    []*FieldExpr
	Conditions Expr
	Table      *FieldExpr
}

type DorisListenerOption struct {
	DimensionTransform func(s string) string
}

func (l *DorisListener) EnterEveryRule(ctx antlr.ParserRuleContext) {
	log.Infof(l.ctx, "enter %T %s", ctx, ctx.GetText())

	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
		l.fieldExpr.FuncName = v.GetText()
	case *gen.IdentifierOrTextContext:
		l.fieldExpr.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		l.fieldExpr.Name = v.GetText()
	case *gen.NamedExpressionContext:
		l.fieldExpr = &FieldExpr{}
	case *gen.TableAliasContext:
		l.fieldExpr.As = ctx.GetText()
	case *gen.TableNameContext:
		l.fieldExpr.Name = ctx.GetText()
	case *gen.PredicateContext:
		l.conditionExpr.Field = l.fieldExpr
	case *gen.ComparisonOperatorContext:
		l.conditionExpr.Op = v.GetText()
	}
}

func (l *DorisListener) ExitEveryRule(ctx antlr.ParserRuleContext) {
	log.Infof(l.ctx, "exit %T %s", ctx, ctx.GetText())

	switch v := ctx.(type) {
	case *gen.NamedExpressionContext:
		l.fieldListExpr = append(l.fieldListExpr, l.fieldExpr)
	case *gen.SelectClauseContext:
		l.Selects = l.fieldListExpr
		l.fieldListExpr = make([]*FieldExpr, 0)
		l.fieldExpr = &FieldExpr{}
	case *gen.TableNameContext:
		l.Table = l.fieldExpr
		l.fieldExpr = &FieldExpr{}
	case *gen.PredicateContext:
		l.conditionExpr = &ConditionExpr{
			Field: &FieldExpr{},
		}
	case *gen.LogicalBinaryContext:
		op := strings.ToLower(v.GetOperator().GetText())
		switch op {
		case "and":
			l.Conditions = &AndExpr{
				Left:  l.conditionExpr,
				Right: l.Conditions,
			}
		case "or":
			l.Conditions = &OrExpr{
				Left:  l.conditionExpr,
				Right: l.Conditions,
			}
		}
	}
}

func (l *DorisListener) WithOptions(opt DorisListenerOption) *DorisListener {
	l.opt = opt
	return l
}

func (l *DorisListener) SQL() (string, error) {
	selectsList := make([]string, 0, len(l.Selects))
	for _, selectExpr := range l.Selects {
		selectsList = append(selectsList, selectExpr.String())
	}
	if len(selectsList) == 0 {
		return "", fmt.Errorf("sql 解析异常: %s", l.sql)
	}

	var (
		sql strings.Builder
	)
	sql.WriteString("SELECT ")
	sql.WriteString(strings.Join(selectsList, ", "))

	if l.Table != nil {
		sql.WriteString(fmt.Sprintf(" FROM %s", l.Table.String()))
	}
	if l.Conditions != nil {
		sql.WriteString(fmt.Sprintf(" WHERE %s", l.Conditions.String()))
	}

	return sql.String(), nil
}

// NewDorisListener 创建带Token流的Listener
func NewDorisListener(ctx context.Context, sql string) *DorisListener {
	return &DorisListener{
		ctx:           ctx,
		sql:           sql,
		fieldListExpr: make([]*FieldExpr, 0),
		fieldExpr:     &FieldExpr{},
		conditionExpr: &ConditionExpr{},
	}
}
