// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

type Expr interface {
}

type AndExpr struct {
	Left  Expr
	Right Expr
}

type OrExpr struct {
	Left  Expr
	Right Expr
}

type NotExpr struct {
	Expr Expr
}

type GroupingExpr struct {
	Expr Expr
}

type MatchExpr struct {
	Field    Expr
	Value    Expr
	IsQuoted bool
}

func (m *MatchExpr) SetField(field string) {
	m.Field = &StringExpr{Value: field}
}

type WildcardExpr struct {
	Field Expr
	Value Expr
}

func (w *WildcardExpr) SetField(field string) {
	w.Field = &StringExpr{Value: field}
}

type RegexpExpr struct {
	Field Expr
	Value Expr
}

func (r *RegexpExpr) SetField(field string) {
	r.Field = &StringExpr{Value: field}
}

type NumberRangeExpr struct {
	Field        Expr
	Start        Expr
	End          Expr
	IncludeStart Expr
	IncludeEnd   Expr
}

func (nr *NumberRangeExpr) SetField(field string) {
	nr.Field = &StringExpr{Value: field}
}

type TimeRangeExpr struct {
	Field        Expr
	Start        Expr
	End          Expr
	IncludeStart Expr
	IncludeEnd   Expr
}

func (tr *TimeRangeExpr) SetField(field string) {
	tr.Field = &StringExpr{Value: field}
}

type ConditionExpr struct {
	Values [][]Expr
}

type ConditionMatchExpr struct {
	Field Expr
	Value *ConditionExpr
}

func (cm *ConditionMatchExpr) SetField(field string) {
	cm.Field = &StringExpr{Value: field}
}

type StringExpr struct {
	Value string
}

type BoolExpr struct {
	Value bool
}

type NumberExpr struct {
	Value float64
}
