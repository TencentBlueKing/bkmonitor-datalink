// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sqlExpr

import (
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	Doris         = "doris"
	DorisTypeText = "text"

	ShardKey = "__shard_key__"
)

type DorisSQLExpr struct {
	encodeFunc func(string) string

	timeField  string
	valueField string

	keepColumns []string
	fieldsMap   map[string]string
}

var _ SQLExpr = (*DorisSQLExpr)(nil)

func (d *DorisSQLExpr) WithInternalFields(timeField, valueField string) SQLExpr {
	d.timeField = timeField
	d.valueField = valueField
	return d
}

func (d *DorisSQLExpr) WithEncode(fn func(string) string) SQLExpr {
	d.encodeFunc = fn
	return d
}

func (d *DorisSQLExpr) WithFieldsMap(fieldsMap map[string]string) SQLExpr {
	d.fieldsMap = fieldsMap
	return d
}

func (d *DorisSQLExpr) WithKeepColumns(cols []string) SQLExpr {
	d.keepColumns = cols
	return d
}

func (d *DorisSQLExpr) FieldMap() map[string]string {
	return d.fieldsMap
}

func (d *DorisSQLExpr) ParserQueryString(qs string) (string, error) {
	expr, err := querystring.Parse(qs)
	if err != nil {
		return "", err
	}
	if expr == nil {
		return "", nil
	}

	return d.walk(expr)
}

func (d *DorisSQLExpr) DescribeTableSQL(table string) string {
	return fmt.Sprintf("SHOW CREATE TABLE %s", table)
}

func (d *DorisSQLExpr) aggregateTransform(aggregates metadata.Aggregates) metadata.Aggregates {
	newAggregates := make(metadata.Aggregates, 0)
	for _, agg := range aggregates {
		switch agg.Name {
		case "cardinality":
		default:
			newAggregates = append(newAggregates, agg)
		}
	}
	return newAggregates
}

// ParserAggregatesAndOrders 解析聚合函数，生成 select 和 group by 字段
func (d *DorisSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, err error) {
	valueField, _ := d.dimTransform(d.valueField)

	newAggregates := d.aggregateTransform(aggregates)

	for _, agg := range newAggregates {
		for _, dim := range agg.Dimensions {
			var (
				isObject = false

				newDim      string
				selectAlias string
			)
			newDim, isObject = d.dimTransform(dim)
			if isObject && d.encodeFunc != nil {
				selectAlias = fmt.Sprintf("%s AS `%s`", newDim, d.encodeFunc(dim))
			} else {
				selectAlias = newDim
			}

			selectFields = append(selectFields, selectAlias)
			groupByFields = append(groupByFields, newDim)
		}

		if valueField == "" {
			valueField = SelectAll
		}

		switch agg.Name {
		case "cardinality":
			selectFields = append(selectFields, fmt.Sprintf("COUNT(DISTINCT %s) AS `%s`", valueField, Value))
		default:
			selectFields = append(selectFields, fmt.Sprintf("%s(%s) AS `%s`", strings.ToUpper(agg.Name), valueField, Value))
		}

		if agg.Window > 0 {
			// 获取时区偏移量
			var offsetMinutes int
			// 如果是按天聚合，则增加时区偏移量
			if agg.Window.Milliseconds()%(24*time.Hour).Milliseconds() == 0 {
				// 时间聚合函数兼容时区
				loc, locErr := time.LoadLocation(agg.TimeZone)
				if locErr != nil {
					loc = time.UTC
				}
				_, offset := time.Now().In(loc).Zone()
				offsetMinutes = offset / 60
			}

			// 如果是按照分钟聚合，则使用 __shard_key__ 作为时间字段
			var timeField string
			if int64(agg.Window.Seconds())%60 == 0 {
				windowMinutes := int(agg.Window.Minutes())
				timeField = fmt.Sprintf(`((CAST((%s / 1000 + %d) / %d AS INT) * %d - %d) * 60 * 1000)`, ShardKey, offsetMinutes, windowMinutes, windowMinutes, offsetMinutes)
			} else {
				timeField = fmt.Sprintf(`CAST(%s / %d AS INT) * %d `, d.timeField, agg.Window.Milliseconds(), agg.Window.Milliseconds())
			}

			selectFields = append(selectFields, fmt.Sprintf("%s AS `%s`", timeField, TimeStamp))
			groupByFields = append(groupByFields, TimeStamp)
			orderByFields = append(orderByFields, fmt.Sprintf("`%s` ASC", TimeStamp))
		}
	}

	if len(selectFields) == 0 {
		if len(d.keepColumns) > 0 {
			selectFields = append(selectFields, d.keepColumns...)
		} else {
			selectFields = append(selectFields, SelectAll)
		}

		if valueField != "" {
			selectFields = append(selectFields, fmt.Sprintf("%s AS `%s`", valueField, Value))
		}
		if d.timeField != "" {
			selectFields = append(selectFields, fmt.Sprintf("`%s` AS `%s`", d.timeField, TimeStamp))
		}
	}

	for _, order := range orders {
		var orderField string
		switch order.Name {
		case FieldValue:
			orderField = d.valueField
		case FieldTime:
			orderField = TimeStamp
		default:
			orderField = order.Name
		}

		orderField, _ = d.dimTransform(orderField)

		ascName := "ASC"
		if !order.Ast {
			ascName = "DESC"
		}
		orderByFields = append(orderByFields, fmt.Sprintf("%s %s", orderField, ascName))
	}

	return
}

func (d *DorisSQLExpr) ParserAllConditions(allConditions metadata.AllConditions) (string, error) {
	var (
		orConditions []string
	)

	// 遍历所有OR条件组
	for _, conditions := range allConditions {
		var andConditions []string
		// 处理每个AND条件组
		for _, cond := range conditions {
			buildCondition, err := d.buildCondition(cond)
			if err != nil {
				return "", err
			}
			if buildCondition != "" {
				andConditions = append(andConditions, buildCondition)
			}
		}
		// 合并AND条件
		if len(andConditions) > 0 {
			orConditions = append(orConditions, strings.Join(andConditions, " AND "))
		}
	}

	// 处理最终OR条件组合
	if len(orConditions) > 0 {
		if len(orConditions) == 1 {
			return orConditions[0], nil
		}
		return fmt.Sprintf("(%s)", strings.Join(orConditions, " OR ")), nil
	}

	return "", nil
}

func (d *DorisSQLExpr) buildCondition(c metadata.ConditionField) (string, error) {
	if len(c.Value) == 0 {
		return "", nil
	}

	var (
		key string
		op  string
		val string
	)

	key, _ = d.dimTransform(c.DimensionName)

	// 对值进行转义处理
	for i, v := range c.Value {
		c.Value[i] = d.valueTransform(v)
	}

	// 根据操作符类型生成不同的SQL表达式
	switch c.Operator {
	// 处理等于类操作符（=, IN, LIKE）
	case metadata.ConditionEqual, metadata.ConditionExact, metadata.ConditionContains:
		if len(c.Value) > 1 && !c.IsWildcard && !d.checkMatchALL(c.DimensionName) {
			op = "IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "LIKE"
			} else {
				format = "'%s'"
				if d.checkMatchALL(c.DimensionName) {
					op = "MATCH_PHRASE_PREFIX"
				} else {
					op = "="
				}
			}

			var filter []string
			for _, v := range c.Value {
				filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
			}
			key = ""
			if len(filter) == 1 {
				val = filter[0]
			} else {
				val = fmt.Sprintf("(%s)", strings.Join(filter, " OR "))
			}
		}
	// 处理不等于类操作符（!=, NOT IN, NOT LIKE）
	case metadata.ConditionNotEqual, metadata.ConditionNotContains:
		if len(c.Value) > 1 && !c.IsWildcard && !d.checkMatchALL(c.DimensionName) {
			op = "NOT IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "NOT LIKE"
			} else {
				format = "'%s'"
				if d.checkMatchALL(c.DimensionName) {
					op = "NOT MATCH_PHRASE_PREFIX"
				} else {
					op = "!="
				}
			}

			var filter []string
			for _, v := range c.Value {
				filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
			}
			key = ""
			if len(filter) == 1 {
				val = filter[0]
			} else {
				val = fmt.Sprintf("(%s)", strings.Join(filter, " AND "))
			}
		}
	// 处理正则表达式匹配
	case metadata.ConditionRegEqual:
		op = "REGEXP"
		val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|")) // 多个值用|连接
	case metadata.ConditionNotRegEqual:
		op = "NOT REGEXP"
		val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|"))
	// 处理数值比较操作符（>, >=, <, <=）
	case metadata.ConditionGt:
		op = ">"
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		val = c.Value[0]
	case metadata.ConditionGte:
		op = ">="
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		val = c.Value[0]
	case metadata.ConditionLt:
		op = "<"
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		val = c.Value[0]
	case metadata.ConditionLte:
		op = "<="
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		val = c.Value[0]
	default:
		return "", fmt.Errorf("unknown operator %s", c.Operator)
	}

	if key != "" {
		return fmt.Sprintf("%s %s %s", key, op, val), nil
	}
	return val, nil
}

func (d *DorisSQLExpr) checkMatchALL(k string) bool {
	if d.fieldsMap != nil {
		if t, ok := d.fieldsMap[k]; ok {
			if t == DorisTypeText {
				return true
			}
		}
	}
	return false
}

func (d *DorisSQLExpr) walk(e querystring.Expr) (string, error) {
	var (
		err   error
		left  string
		right string
	)

	switch c := e.(type) {
	case *querystring.NotExpr:
		left, err = d.walk(c.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("NOT (%s)", left), nil
	case *querystring.OrExpr:
		left, err = d.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = d.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s OR %s)", left, right), nil
	case *querystring.AndExpr:
		left, err = d.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = d.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s AND %s", left, right), nil
	case *querystring.WildcardExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll)
			return "", err
		}

		field, _ := d.dimTransform(c.Field)

		return fmt.Sprintf("%s LIKE '%%%s%%'", field, c.Value), nil
	case *querystring.MatchExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll + ": " + c.Value)
			return "", err
		}
		field, _ := d.dimTransform(c.Field)

		if d.checkMatchALL(c.Field) {
			return fmt.Sprintf("%s MATCH_PHRASE_PREFIX '%s'", field, c.Value), nil
		}

		return fmt.Sprintf("%s = '%s'", field, c.Value), nil
	case *querystring.NumberRangeExpr:
		if c.Field == "" {
			err = fmt.Errorf(Doris + " " + ErrorMatchAll)
			return "", err
		}
		field, _ := d.dimTransform(c.Field)

		var timeFilter []string
		if c.Start != nil && *c.Start != "*" {
			var op string
			if c.IncludeStart {
				op = ">="
			} else {
				op = ">"
			}
			timeFilter = append(timeFilter, fmt.Sprintf("%s %s %s", field, op, *c.Start))
		}

		if c.End != nil && *c.End != "*" {
			var op string
			if c.IncludeEnd {
				op = "<="
			} else {
				op = "<"
			}
			timeFilter = append(timeFilter, fmt.Sprintf("%s %s %s", field, op, *c.End))
		}

		return fmt.Sprintf("%s", strings.Join(timeFilter, " AND ")), nil
	default:
		err = fmt.Errorf("expr type is not match %T", e)
	}

	return "", err
}

func (d *DorisSQLExpr) dimTransform(s string) (string, bool) {
	if s == "" {
		return "", false
	}

	fs := strings.Split(s, ".")
	if len(fs) > 1 {
		return fmt.Sprintf("CAST(%s[\"%s\"] AS STRING)", fs[0], strings.Join(fs[1:], "][")), true
	}
	return fmt.Sprintf("`%s`", s), false
}

func (d *DorisSQLExpr) valueTransform(s string) string {
	if strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "''")
	}
	return s
}

func init() {
	Register(Doris, &DorisSQLExpr{})
}
