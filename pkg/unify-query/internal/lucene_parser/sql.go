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

	"github.com/spf13/cast"
)

const DefaultLogField = "log"

const (
	opTypeNone = iota
	opTypeOr
	opTypeAnd
)

func (p *Parser) toSql(expr Expr) string {
	if expr == nil {
		return ""
	}

	sql := p.walkSQL(expr, opTypeNone)

	if needsTopLevelParentheses(expr) {
		return fmt.Sprintf("(%s)", sql)
	}

	// 如果顶层表达式是OR，总是加括号
	if _, isOr := expr.(*OrExpr); isOr {
		return fmt.Sprintf("(%s)", sql)
	}

	return sql
}

func needsTopLevelParentheses(expr Expr) bool {
	switch e := expr.(type) {
	case *OrExpr:
		// 如果包含AND或复杂嵌套，需要加括号
		return containsAndOrComplexNesting(e)
	case *AndExpr:
		if _, hasOr := e.Right.(*OrExpr); hasOr {
			return false
		}
		return containsOrInAndExpression(e)
	}
	return false
}

func containsAndOrComplexNesting(expr *OrExpr) bool {
	if _, hasAnd := expr.Left.(*AndExpr); hasAnd {
		return true
	}
	if _, hasAnd := expr.Right.(*AndExpr); hasAnd {
		return true
	}
	if _, hasNot := expr.Left.(*NotExpr); hasNot {
		return true
	}
	if _, hasNot := expr.Right.(*NotExpr); hasNot {
		return true
	}
	return false
}

func containsOrInAndExpression(expr *AndExpr) bool {
	if _, hasOr := expr.Left.(*OrExpr); hasOr {
		return true
	}
	if _, hasOr := expr.Right.(*OrExpr); hasOr {
		return true
	}
	return false
}

func (p *Parser) walkSQL(expr Expr, parentOpType int) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *AndExpr:
		left := p.walkSQL(e.Left, opTypeAnd)
		right := p.walkSQL(e.Right, opTypeAnd)

		if _, isOr := e.Right.(*OrExpr); isOr {
			right = fmt.Sprintf("(%s)", right)
		}

		sql := fmt.Sprintf("%s AND %s", left, right)
		return sql

	case *OrExpr:
		left := p.walkSQL(e.Left, opTypeOr)
		right := p.walkSQL(e.Right, opTypeOr)

		// 右侧如果是OR表达式，需要加括号
		if _, isOr := e.Right.(*OrExpr); isOr {
			right = fmt.Sprintf("(%s)", right)
		}

		sql := fmt.Sprintf("%s OR %s", left, right)

		// 只有当父操作是AND时才加括号
		if parentOpType == opTypeAnd {
			return fmt.Sprintf("(%s)", sql)
		}
		return sql

	case *NotExpr:
		return fmt.Sprintf("NOT (%s)", p.walkSQL(e.Expr, opTypeNone))

	case *GroupingExpr:
		// 这里的处理忽略了Boost
		result := fmt.Sprintf("(%s)", p.walkSQL(e.Expr, opTypeNone))
		return result

	case *OperatorExpr:
		rawField := getField(expr)
		if rawField == Empty {
			rawField = DefaultLogField
		}
		field := p.dorisSchema.transformField(rawField)
		isText := p.dorisSchema.isText(rawField)
		switch e.Op {
		case OpFuzzy:
			value := p.getValue(e.Value)
			formattedValue := p.dorisSchema.formatValue(rawField, value)
			if isText {
				return fmt.Sprintf("%s MATCH_PHRASE %s", field, formattedValue)
			} else {
				return fmt.Sprintf("%s LIKE '%%%s%%'", field, escapeSQL(value))
			}
		case OpMatch:
			value := p.getValue(e.Value)
			formattedValue := p.dorisSchema.formatValue(rawField, value)
			var result string

			if isText || e.Slop > 0 {
				result = fmt.Sprintf("%s MATCH_PHRASE %s", field, formattedValue)
			} else {
				result = fmt.Sprintf("%s = %s", field, formattedValue)
			}
			return result

		case OpWildcard:
			value := p.getValue(e.Value)
			value = strings.ReplaceAll(value, "?", "_")
			if !strings.Contains(value, "*") {
				value = "%" + value + "%"
			} else {
				value = strings.ReplaceAll(value, "*", "%")
			}
			result := fmt.Sprintf("%s LIKE '%s'", field, escapeSQL(value))
			return result

		case OpRegex:
			value := p.getValue(e.Value)
			result := fmt.Sprintf("%s REGEXP '%s'", field, escapeSQL(value))
			return result

		case OpRange:
			rangeExpr, ok := e.Value.(*RangeExpr)
			if !ok {
				return ""
			}

			// Check if this is a time range (for datetime field handling)
			isTimeRange := field == fmt.Sprintf("`%s`", DefaultLogField) ||
				(rangeExpr.Start != nil && looksLikeDate(p.getValue(rangeExpr.Start))) ||
				(rangeExpr.End != nil && looksLikeDate(p.getValue(rangeExpr.End)))

			if isTimeRange && field == fmt.Sprintf("`%s`", DefaultLogField) {
				field = "`datetime`"
			}

			var conditions []string
			if rangeExpr.Start != nil {
				startValue := p.getValue(rangeExpr.Start)
				if startValue != "*" {
					op := ">"
					if b, ok := rangeExpr.IncludeStart.(*BoolExpr); ok && b.Value {
						op = ">="
					}
					if isTimeRange {
						conditions = append(conditions, fmt.Sprintf("%s %s '%s'", field, op, escapeSQL(startValue)))
					} else {
						conditions = append(conditions, fmt.Sprintf("%s %s %s", field, op, startValue))
					}
				}
			}
			if rangeExpr.End != nil {
				endValue := p.getValue(rangeExpr.End)
				if endValue != "*" {
					op := "<"
					if b, ok := rangeExpr.IncludeEnd.(*BoolExpr); ok && b.Value {
						op = "<="
					}
					if isTimeRange {
						conditions = append(conditions, fmt.Sprintf("%s %s '%s'", field, op, escapeSQL(endValue)))
					} else {
						conditions = append(conditions, fmt.Sprintf("%s %s %s", field, op, endValue))
					}
				}
			}
			sql := strings.Join(conditions, " AND ")
			if parentOpType != opTypeNone && len(conditions) > 1 {
				return fmt.Sprintf("(%s)", sql)
			}
			return sql
		}
		return ""

	case *ConditionMatchExpr:
		rawField := getField(expr)
		if rawField == Empty {
			rawField = DefaultLogField
		}
		field := p.dorisSchema.transformField(rawField)
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
				value := p.getValue(expr)
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
		return cast.ToString(e.Value)

	default:
		return fmt.Sprintf("UNSUPPORTED_EXPR_TYPE:%T", expr)
	}
}

func (p *Parser) getValue(expr Expr) string {
	if expr == nil {
		return ""
	}
	return p.walkSQL(expr, opTypeNone)
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\\"", "\"")
	return strings.ReplaceAll(s, "'", "\\'")
}
