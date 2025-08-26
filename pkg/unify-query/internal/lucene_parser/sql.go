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
	"strconv"
	"strings"
)

const DefaultLogField = "log"

const (
	opTypeNone = iota
	opTypeOr
	opTypeAnd
)

func ToSQL(expr Expr) string {
	if expr == nil {
		return ""
	}
	return walkSQL(expr, opTypeNone)
}

func walkSQL(expr Expr, parentOpType int) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *AndExpr:
		left := walkSQL(e.Left, opTypeAnd)
		right := walkSQL(e.Right, opTypeAnd)
		if needsLeftParentheses(e.Left) {
			left = fmt.Sprintf("(%s)", left)
		}

		sql := fmt.Sprintf("%s AND %s", left, right)
		if parentOpType == opTypeOr {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *OrExpr:
		sql := fmt.Sprintf("%s OR %s", walkSQL(e.Left, opTypeOr), walkSQL(e.Right, opTypeOr))
		if parentOpType == opTypeAnd {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *NotExpr:
		return fmt.Sprintf("NOT (%s)", walkSQL(e.Expr, opTypeNone))

	case *GroupingExpr:
		return fmt.Sprintf("(%s)", walkSQL(e.Expr, opTypeNone))

	case *MatchExpr:
		if parentOpType == opTypeNone && expr != nil {
			if _, ok := expr.(*NotExpr); ok {
				return fmt.Sprintf("(%s)", fmt.Sprintf("%s = '%s'", getFieldName(e.Field), escapeSQL(getValue(e.Value))))
			}
		}
		return fmt.Sprintf("%s = '%s'", getFieldName(e.Field), escapeSQL(getValue(e.Value)))

	case *WildcardExpr:
		field := getFieldName(e.Field)
		value := getValue(e.Value)
		value = strings.ReplaceAll(value, "?", "_")
		if !strings.Contains(value, "*") {
			value = "%" + value + "%"
		} else {
			value = strings.ReplaceAll(value, "*", "%")
		}
		return fmt.Sprintf("%s LIKE '%s'", field, escapeSQL(value))

	case *RegexpExpr:
		field := getFieldName(e.Field)
		value := getValue(e.Value)
		return fmt.Sprintf("%s REGEXP '%s'", field, escapeSQL(value))

	case *NumberRangeExpr:
		field := getFieldName(e.Field)
		var conditions []string
		if e.Start != nil {
			startValue := getValue(e.Start)
			if startValue != "*" {
				op := ">"
				if b, ok := e.IncludeStart.(*BoolExpr); ok && b.Value {
					op = ">="
				}
				conditions = append(conditions, fmt.Sprintf("%s %s %s", field, op, startValue))
			}
		}
		if e.End != nil {
			endValue := getValue(e.End)
			if endValue != "*" {
				op := "<"
				if b, ok := e.IncludeEnd.(*BoolExpr); ok && b.Value {
					op = "<="
				}
				conditions = append(conditions, fmt.Sprintf("%s %s %s", field, op, endValue))
			}
		}
		sql := strings.Join(conditions, " AND ")
		if parentOpType != opTypeNone && len(conditions) > 1 {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *TimeRangeExpr:
		field := getFieldName(e.Field)
		if field == fmt.Sprintf("`%s`", DefaultLogField) {
			field = "`datetime`"
		}
		var conditions []string
		if e.Start != nil {
			startValue := getValue(e.Start)
			if startValue != "*" {
				op := ">"
				if b, ok := e.IncludeStart.(*BoolExpr); ok && b.Value {
					op = ">="
				}
				conditions = append(conditions, fmt.Sprintf("%s %s '%s'", field, op, escapeSQL(startValue)))
			}
		}
		if e.End != nil {
			endValue := getValue(e.End)
			if endValue != "*" {
				op := "<"
				if b, ok := e.IncludeEnd.(*BoolExpr); ok && b.Value {
					op = "<="
				}
				conditions = append(conditions, fmt.Sprintf("%s %s '%s'", field, op, escapeSQL(endValue)))
			}
		}
		sql := strings.Join(conditions, " AND ")
		if parentOpType != opTypeNone && len(conditions) > 1 {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *ConditionMatchExpr:
		field := getFieldName(e.Field)
		if e.Value == nil || len(e.Value.Values) == 0 {
			return ""
		}
		var orConditions []string
		for _, andGroup := range e.Value.Values {
			if len(andGroup) == 0 {
				continue
			}
			var andConditions []string
			for _, expr := range andGroup {
				value := getValue(expr)
				andConditions = append(andConditions, fmt.Sprintf("%s LIKE '%%%s%%'", field, escapeSQL(value)))
			}
			if len(andConditions) > 1 {
				orConditions = append(orConditions, fmt.Sprintf("(%s)", strings.Join(andConditions, " AND ")))
			} else {
				orConditions = append(orConditions, andConditions...)
			}
		}
		sql := strings.Join(orConditions, " OR ")
		if parentOpType == opTypeAnd && len(orConditions) > 1 {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *StringExpr:
		return e.Value

	case *BoolExpr:
		return fmt.Sprintf("%v", e.Value)

	case *NumberExpr:
		return strconv.FormatFloat(e.Value, 'f', -1, 64)

	default:
		return fmt.Sprintf("UNSUPPORTED_EXPR_TYPE:%T", expr)
	}
}

func getFieldName(fieldExpr Expr) string {
	if fieldExpr != nil {
		if s, ok := fieldExpr.(*StringExpr); ok {
			return fmt.Sprintf("`%s`", s.Value)
		}
	}
	return fmt.Sprintf("`%s`", DefaultLogField)
}

func getValue(expr Expr) string {
	if expr == nil {
		return ""
	}
	return walkSQL(expr, opTypeNone)
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\\"", "\"")
	return strings.ReplaceAll(s, "'", "\\'")
}

func needsLeftParentheses(expr Expr) bool {
	switch e := expr.(type) {
	case *AndExpr:
		if _, ok := e.Left.(*ConditionMatchExpr); ok {
			if _, ok := e.Right.(*ConditionMatchExpr); ok {
				return true
			}
		}
	}
	return false
}
