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
	"strings"

	elastic "github.com/olivere/elastic/v7"
)

func mergeExpr(parentOpType int, items ...any) (string, error) {
	s := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case Expr:
			sql, err := v.SQL(parentOpType)
			if err != nil {
				return "", err
			}
			s = append(s, sql)
		case string:
			s = append(s, v)
		}
	}

	return strings.Join(s, " "), nil
}

func setField(field string, items ...Expr) {
	for _, item := range items {
		if f, ok := item.(FieldSetter); ok {
			f.SetField(field)
		}
	}
}

type FieldSetter interface {
	SetField(string)
}

type Expr interface {
	SQL(parentOpType int) (string, error)
	DSL() (elastic.Query, error)
}

type defaultExpr struct{}

func (e *defaultExpr) SetField(field string) {
}

func (e *defaultExpr) SQL(parentOpType int) (string, error) {
	return "", nil
}

func (e *defaultExpr) DSL() (elastic.Query, error) {
	return nil, nil
}

type AndExpr struct {
	defaultExpr
	Left  Expr
	Right Expr
}

func (e *AndExpr) SQL(_ int) (string, error) {
	return mergeExpr(opTypeAnd, e.Left, "AND", e.Right)
}

func (e *AndExpr) SetField(field string) {
	setField(field, e.Left, e.Right)
}

type OrExpr struct {
	defaultExpr
	Left  Expr
	Right Expr
}

func (e *OrExpr) SQL(parentOpType int) (string, error) {
	sql, err := mergeExpr(opTypeOr, e.Left, "OR", e.Right)
	if err != nil {
		return "", err
	}

	if parentOpType == opTypeAnd {
		sql = fmt.Sprintf("(%s)", sql)
	}
	return sql, nil
}

func (e *OrExpr) SetField(field string) {
	setField(field, e.Left, e.Right)
}

type NotExpr struct {
	defaultExpr
	Expr Expr
}

func (e *NotExpr) SQL(_ int) (string, error) {
	sql, err := mergeExpr(opTypeNone, e.Expr)
	if err != nil {
		return "", err
	}
	sql = fmt.Sprintf("NOT (%s)", sql)
	return sql, nil
}

type GroupingExpr struct {
	defaultExpr
	Expr  Expr
	Boost float64
}

func (e *GroupingExpr) SQL(_ int) (string, error) {
	sql, err := mergeExpr(opTypeNone, e.Expr)
	if err != nil {
		return "", err
	}
	sql = fmt.Sprintf("(%s)", sql)
	return sql, nil
}

type StringExpr struct {
	defaultExpr
	Value string
}

func (e *StringExpr) SQL(_ int) (string, error) {
	return e.Value, nil
}

type OperatorExpr struct {
	defaultExpr
	Field     Expr
	Op        Expr
	Value     Expr // 可以是StringExpr、NumberExpr或RangeExpr
	IsQuoted  bool
	Boost     float64
	Fuzziness string
	Slop      int
}

func (e *OperatorExpr) SetField(field string) {
	e.Field = &StringExpr{
		Value: field,
	}
}

func (e *OperatorExpr) SQL(_ int) (string, error) {
	if e.Field == nil {
		e.Field = &StringExpr{
			Value: DefaultLogField,
		}
	}

	return mergeExpr(opTypeNone, e.Field, e.Op, e.Value)
}

// RangeExpr represents range values used in OperatorExpr
type RangeExpr struct {
	defaultExpr
	Start        Expr
	End          Expr
	IncludeStart Expr
	IncludeEnd   Expr
}

func (e *RangeExpr) SQL(_ int) (string, error) {
	items := make([]Expr, 0)
	if e.Start != nil {
		items = append(items, e.Start)
		items = append(items, e.IncludeStart)
	}
	if e.End != nil {
		items = append(items, e.IncludeEnd)
		items = append(items, e.End)
	}

	return mergeExpr(opTypeNone, items)
}

type ConditionMatchExpr struct {
	defaultExpr

	Field      Expr
	Conditions [][]Expr
}

func (e *ConditionMatchExpr) SetField(field string) {
	e.Field = &StringExpr{
		Value: field,
	}
}

func (e *ConditionMatchExpr) SQL(parentOpType int) (string, error) {
	if e.Field == nil {
		e.Field = &StringExpr{
			Value: DefaultLogField,
		}
	}

	orConditions := make([]string, 0, len(e.Conditions))
	for _, condition := range e.Conditions {
		andConditions := make([]string, 0, len(condition))
		for _, c := range condition {
			val, err := c.SQL(opTypeOr)
			if err != nil {
				return "", err
			}
			andConditions = append(andConditions, val)
		}

		if len(andConditions) > 1 {
			orConditions = append(orConditions, fmt.Sprintf("(%s)", strings.Join(andConditions, " AND ")))
		} else {
			orConditions = append(orConditions, andConditions...)
		}
	}

	sql := strings.Join(orConditions, " OR ")

	if parentOpType == opTypeAnd && len(orConditions) > 1 {
		sql = fmt.Sprintf("(%s)", sql)
	}

	return sql, nil
}
