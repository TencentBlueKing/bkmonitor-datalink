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

func isNumeric(s string) bool {
	if s == "" || s == "*" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

type SQLPart struct {
	Type  SQLPartType
	Value string
}

type SQLPartType int

const (
	Condition SQLPartType = iota
	Operator
	LeftParen
	RightParen
)

type SQLBuilder struct {
	parts       []SQLPart
	logicStack  *LogicStack
	currentCond []string
}

type LogicStack struct {
	stack []LogicFrame
}

type LogicFrame struct {
	Operator string
	Depth    int
}

func (ls *LogicStack) Push(op string, depth int) {
	ls.stack = append(ls.stack, LogicFrame{Operator: op, Depth: depth})
}

func (ls *LogicStack) Pop() string {
	if len(ls.stack) == 0 {
		return ""
	}
	result := ls.stack[len(ls.stack)-1].Operator
	ls.stack = ls.stack[:len(ls.stack)-1]
	return result
}

func (ls *LogicStack) Peek() string {
	if len(ls.stack) == 0 {
		return ""
	}
	return ls.stack[len(ls.stack)-1].Operator
}

func (sb *SQLBuilder) AddCondition(cond string) {
	sb.currentCond = append(sb.currentCond, cond)
}

func (sb *SQLBuilder) AddOperator(op string) {
	sb.parts = append(sb.parts, SQLPart{Type: Operator, Value: op})
}

func (sb *SQLBuilder) AddLeftParen() {
	sb.parts = append(sb.parts, SQLPart{Type: LeftParen, Value: "("})
}

func (sb *SQLBuilder) AddRightParen() {
	sb.parts = append(sb.parts, SQLPart{Type: RightParen, Value: ")"})
}

func (sb *SQLBuilder) Build() string {
	if len(sb.currentCond) == 0 {
		return ""
	}

	return strings.Join(sb.currentCond, " AND ")
}

type FieldSQLBuilder struct {
	Field  string
	Op     string
	Values []string
	Encode Encode
}

func NewFieldSQLBuilder(field string, encode Encode) *FieldSQLBuilder {
	return &FieldSQLBuilder{
		Field:  field,
		Encode: encode,
		Values: make([]string, 0),
	}
}

func (fb *FieldSQLBuilder) AddValue(value string) {
	fb.Values = append(fb.Values, value)
}

func (fb *FieldSQLBuilder) SetOp(op string) {
	fb.Op = op
}

// Build 构建字段条件SQL
func (fb *FieldSQLBuilder) Build() string {
	if fb.Field == "" || len(fb.Values) == 0 {
		return ""
	}

	// 使用Encode进行字段转换
	fieldName := fb.Field
	if fb.Encode != nil {
		if encoded, ok := fb.Encode(fieldName); ok {
			fieldName = encoded
		}
	}

	// 处理不同操作符的情况
	switch fb.Op {
	case "=", "==":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" = '%s'`, fieldName, fb.Values[0])
		}
		// 多个值使用IN
		values := strings.Join(fb.Values, "','")
		return fmt.Sprintf(`"%s" IN ('%s')`, fieldName, values)
	case "!=", "<>":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" != '%s'`, fieldName, fb.Values[0])
		}
		// 多个值使用NOT IN
		values := strings.Join(fb.Values, "','")
		return fmt.Sprintf(`"%s" NOT IN ('%s')`, fieldName, values)
	case "REGEXP":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" REGEXP '%s'`, fieldName, fb.Values[0])
		}
	case ">":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" > %s`, fieldName, fb.Values[0])
		}
	case "<":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" < %s`, fieldName, fb.Values[0])
		}
	case ">=":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" >= %s`, fieldName, fb.Values[0])
		}
	case "<=":
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" <= %s`, fieldName, fb.Values[0])
		}
	case "BETWEEN":
		if len(fb.Values) == 2 {
			return fmt.Sprintf(`"%s" BETWEEN '%s' AND '%s'`, fieldName, fb.Values[0], fb.Values[1])
		}
	default:
		if len(fb.Values) == 1 {
			return fmt.Sprintf(`"%s" = '%s'`, fieldName, fb.Values[0])
		}
		values := strings.Join(fb.Values, "','")
		return fmt.Sprintf(`"%s" IN ('%s')`, fieldName, values)
	}

	return ""
}

type RangeSQLBuilder struct {
	Field          string
	Start          string
	End            string
	StartInclusive bool
	EndInclusive   bool
	Encode         Encode
}

func NewRangeSQLBuilder(field string, encode Encode) *RangeSQLBuilder {
	return &RangeSQLBuilder{
		Field:          field,
		Encode:         encode,
		StartInclusive: true,
		EndInclusive:   true,
	}
}

func (rb *RangeSQLBuilder) SetRange(start, end string, startInclusive, endInclusive bool) {
	rb.Start = start
	rb.End = end
	rb.StartInclusive = startInclusive
	rb.EndInclusive = endInclusive
}

func (rb *RangeSQLBuilder) Build() string {
	if rb.Field == "" {
		return ""
	}

	fieldName := rb.Field
	if rb.Encode != nil {
		if encoded, ok := rb.Encode(fieldName); ok {
			fieldName = encoded
		}
	}

	isStartNumeric := isNumeric(rb.Start)
	isEndNumeric := isNumeric(rb.End)

	if rb.StartInclusive && rb.EndInclusive {
		if isStartNumeric && isEndNumeric {
			return fmt.Sprintf(`"%s" BETWEEN %s AND %s`, fieldName, rb.Start, rb.End)
		} else {
			return fmt.Sprintf(`"%s" BETWEEN '%s' AND '%s'`, fieldName, rb.Start, rb.End)
		}
	}

	var parts []string
	if rb.Start != "" {
		if isStartNumeric {
			if rb.StartInclusive {
				parts = append(parts, fmt.Sprintf(`"%s" >= %s`, fieldName, rb.Start))
			} else {
				parts = append(parts, fmt.Sprintf(`"%s" > %s`, fieldName, rb.Start))
			}
		} else {
			if rb.StartInclusive {
				parts = append(parts, fmt.Sprintf(`"%s" >= '%s'`, fieldName, rb.Start))
			} else {
				parts = append(parts, fmt.Sprintf(`"%s" > '%s'`, fieldName, rb.Start))
			}
		}
	}

	if rb.End != "" {
		if isEndNumeric {
			if rb.EndInclusive {
				parts = append(parts, fmt.Sprintf(`"%s" <= %s`, fieldName, rb.End))
			} else {
				parts = append(parts, fmt.Sprintf(`"%s" < %s`, fieldName, rb.End))
			}
		} else {
			if rb.EndInclusive {
				parts = append(parts, fmt.Sprintf(`"%s" <= '%s'`, fieldName, rb.End))
			} else {
				parts = append(parts, fmt.Sprintf(`"%s" < '%s'`, fieldName, rb.End))
			}
		}
	}

	if len(parts) == 1 {
		return parts[0]
	}

	return strings.Join(parts, " AND ")
}

type LogicSQLBuilder struct {
	Conditions []string
	Operators  []string
	Depth      int
}

func NewLogicSQLBuilder() *LogicSQLBuilder {
	return &LogicSQLBuilder{
		Conditions: make([]string, 0),
		Operators:  make([]string, 0),
		Depth:      0,
	}
}

func (lb *LogicSQLBuilder) AddCondition(condition string) {
	lb.Conditions = append(lb.Conditions, condition)
}

func (lb *LogicSQLBuilder) AddOperator(operator string) {
	lb.Operators = append(lb.Operators, operator)
}

// Build 构建逻辑条件SQL
func (lb *LogicSQLBuilder) Build() string {
	if len(lb.Conditions) == 0 {
		return ""
	}

	if len(lb.Conditions) == 1 {
		return lb.Conditions[0]
	}

	// 简单的AND/OR逻辑
	if len(lb.Operators) == 0 {
		return strings.Join(lb.Conditions, " AND ")
	}

	// 根据操作符构建SQL
	result := lb.Conditions[0]
	for i := 1; i < len(lb.Conditions); i++ {
		operator := "AND"
		if i-1 < len(lb.Operators) {
			operator = lb.Operators[i-1]
		}
		result = fmt.Sprintf("%s %s %s", result, operator, lb.Conditions[i])
	}

	return result
}

type QuerySQLBuilder struct {
	WhereBuilder *LogicSQLBuilder
	Encode       Encode
}

func (qb *QuerySQLBuilder) AddWhereCondition(condition string) {
	qb.WhereBuilder.AddCondition(condition)
}

func (qb *QuerySQLBuilder) Build() string {
	return qb.WhereBuilder.Build()
}
