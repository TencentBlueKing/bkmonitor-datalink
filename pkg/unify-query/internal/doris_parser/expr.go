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
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
)

type Expr interface {
	String() string
	Enter(ctx antlr.ParserRuleContext)
	Exit(ctx antlr.ParserRuleContext)
}

type defaultExpr struct {
}

func (d *defaultExpr) Enter(ctx antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) Exit(ctx antlr.ParserRuleContext) {
	return
}

func (d *defaultExpr) String() string {
	return ""
}

type SelectExpr struct {
	defaultExpr
	fieldExpr     *FieldExpr
	fieldListExpr []*FieldExpr
}

func (e *SelectExpr) String() string {
	var s []string
	for _, expr := range e.fieldListExpr {
		s = append(s, expr.String())
	}

	if len(s) > 0 {
		return fmt.Sprintf("SELECT %s", strings.Join(s, ","))
	}

	return ""
}

func (e *SelectExpr) Enter(ctx antlr.ParserRuleContext) {
	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		e.fieldExpr = &FieldExpr{}
	}
}

func (e *SelectExpr) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.FunctionNameIdentifierContext:
		e.fieldExpr.FuncName = v.GetText()
	case *gen.IdentifierOrTextContext:
		e.fieldExpr.As = v.GetText()
	case *gen.ValueExpressionDefaultContext:
		e.fieldExpr.Name = v.GetText()
	case *gen.NamedExpressionContext:
		e.fieldListExpr = append(e.fieldListExpr, e.fieldExpr)
	}

	return
}

type TableExpr struct {
	defaultExpr
	name string
}

func (e *TableExpr) Exit(ctx antlr.ParserRuleContext) {
	switch v := ctx.(type) {
	case *gen.TableNameContext:
		e.name = v.GetText()
	}
}

func (e *TableExpr) String() string {
	if e.name != "" {
		return fmt.Sprintf("FROM %s", e.name)
	}

	return ""
}

type FieldExpr struct {
	defaultExpr
	Name     string
	As       string
	FuncName string
}

func (e *FieldExpr) String() string {
	s := e.Name
	if e.FuncName != "" {
		s = fmt.Sprintf("%s(%s)", e.FuncName, s)
	}
	if e.As != "" {
		s = fmt.Sprintf("%s AS %s", s, e.As)
	}
	return s
}

type ConditionExpr struct {
	defaultExpr
	Field *FieldExpr
	Op    string
	Value string
}

func (e *ConditionExpr) String() string {
	return fmt.Sprintf("%s %s '%s'", e.Field.String(), e.Op, e.Value)
}

type AndExpr struct {
	defaultExpr
	IsLeftInclude  bool
	IsRightInclude bool
	Left           Expr
	Right          Expr
}

func (e *AndExpr) String() string {
	var s string
	if e.Left != nil && e.Right != nil {
		s = fmt.Sprintf("%s AND %s", e.Left.String(), e.Right.String())
	} else if e.Left != nil {
		s = e.Left.String()
	} else {
		s = e.Right.String()
	}

	if e.IsLeftInclude {
		s = fmt.Sprintf("(%s", s)
	}
	if e.IsRightInclude {
		s = fmt.Sprintf("%s)", s)
	}
	return s
}

type OrExpr struct {
	defaultExpr
	IsLeftInclude  bool
	IsRightInclude bool
	Left           Expr
	Right          Expr
}

func (e *OrExpr) String() string {
	var s string
	if e.Left != nil && e.Right != nil {
		s = fmt.Sprintf("%s OR %s", e.Left.String(), e.Right.String())
	} else if e.Left != nil {
		s = e.Left.String()
	} else {
		s = e.Right.String()
	}
	if e.IsLeftInclude {
		s = fmt.Sprintf("(%s", s)
	}
	if e.IsRightInclude {
		s = fmt.Sprintf("%s)", s)
	}
	return s
}
