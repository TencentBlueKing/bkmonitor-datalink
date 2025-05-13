// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	Doris         = "doris"
	DorisTypeText = "text"

	ShardKey = "__shard_key__"

	DefaultKey = "log"
)

type DorisSQLExpr struct {
	encodeFunc func(string) string

	timeField  string
	valueField string

	keepColumns []string
	fieldsMap   map[string]string

	isSetLabels bool
	lock        sync.Mutex
	labelCheck  map[string]struct{}
	labelMap    map[string][]string
}

var _ SQLExpr = (*DorisSQLExpr)(nil)

func (d *DorisSQLExpr) Type() string {
	return Doris
}

func (d *DorisSQLExpr) WithInternalFields(timeField, valueField string) SQLExpr {
	d.timeField = timeField
	d.valueField = valueField
	return d
}

func (d *DorisSQLExpr) WithEncode(fn func(string) string) SQLExpr {
	d.encodeFunc = fn
	return d
}

func (d *DorisSQLExpr) IsSetLabels(isSetLabels bool) SQLExpr {
	d.isSetLabels = isSetLabels
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

func (d *DorisSQLExpr) GetLabelMap() map[string][]string {
	return d.labelMap
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

// ParserAggregatesAndOrders 解析聚合函数，生成 select 和 group by 字段
func (d *DorisSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, timeAggregate TimeAggregate, err error) {
	valueField, _ := d.dimTransform(d.valueField)

	var (
		window        time.Duration
		offsetMinutes int64

		timezone     string
		dimensionMap = map[string]struct{}{
			FieldValue: {},
			FieldTime:  {},
		}
	)

	for _, agg := range aggregates {
		for _, dim := range agg.Dimensions {
			var (
				isObject = false

				newDim      string
				selectAlias string
			)
			newDim, isObject = d.dimTransform(dim)
			if isObject && d.encodeFunc != nil {
				selectAlias = fmt.Sprintf("%s AS `%s`", newDim, d.encodeFunc(dim))
				newDim = d.encodeFunc(dim)
			} else {
				selectAlias = newDim
			}

			dimensionMap[dim] = struct{}{}

			selectFields = append(selectFields, selectAlias)
			groupByFields = append(groupByFields, newDim)
		}

		if valueField == "" {
			valueField = SelectAll
		}

		switch agg.Name {
		case "cardinality":
			selectFields = append(selectFields, fmt.Sprintf("COUNT(DISTINCT %s) AS `%s`", valueField, Value))
		// date_histogram 不支持无需进行函数聚合
		case "date_histogram":
		default:
			selectFields = append(selectFields, fmt.Sprintf("%s(%s) AS `%s`", strings.ToUpper(agg.Name), valueField, Value))
		}

		if agg.Window > 0 {
			window = agg.Window
			timezone = agg.TimeZone
		}
	}

	if window > 0 {
		// 获取时区偏移量
		// 如果是按天聚合，则增加时区偏移量
		if window.Milliseconds()%(24*time.Hour).Milliseconds() == 0 {
			// 时间聚合函数兼容时区
			loc, locErr := time.LoadLocation(timezone)
			if locErr != nil {
				loc = time.UTC
			}
			_, offset := time.Now().In(loc).Zone()
			offsetMinutes = int64(offset) / 60
		}

		// 如果是按照分钟聚合，则使用 __shard_key__ 作为时间字段
		var timeField string
		if int64(window.Seconds())%60 == 0 {
			windowMinutes := int(window.Minutes())
			timeField = fmt.Sprintf(`((CAST((%s / 1000 + %d) / %d AS INT) * %d - %d) * 60 * 1000)`, ShardKey, offsetMinutes, windowMinutes, windowMinutes, offsetMinutes)
		} else {
			timeField = fmt.Sprintf(`CAST(%s / %d AS INT) * %d `, d.timeField, window.Milliseconds(), window.Milliseconds())
		}

		selectFields = append(selectFields, fmt.Sprintf("%s AS `%s`", timeField, TimeStamp))
		groupByFields = append(groupByFields, TimeStamp)
		orderByFields = append(orderByFields, fmt.Sprintf("`%s` ASC", TimeStamp))
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
		// 如果是聚合操作的话，只能使用维度进行排序
		if len(aggregates) > 0 {
			if _, ok := dimensionMap[order.Name]; !ok {
				continue
			}
		}

		var orderField string
		switch order.Name {
		case FieldValue:
			orderField = Value
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

	// 回传时间聚合信息
	timeAggregate = TimeAggregate{
		Window:       window,
		OffsetMillis: offsetMinutes,
	}

	return
}

func (d *DorisSQLExpr) ParserAllConditions(allConditions metadata.AllConditions) (string, error) {
	return parserAllConditions(allConditions, d.buildCondition)
}

func (d *DorisSQLExpr) buildCondition(c metadata.ConditionField) (string, error) {
	if len(c.Value) == 0 {
		return "", nil
	}

	var (
		oldKey string
		key    string
		op     string
		val    string
	)

	oldKey = c.DimensionName
	key, _ = d.dimTransform(oldKey)

	// 对值进行转义处理
	for i, v := range c.Value {
		c.Value[i] = d.valueTransform(v)
	}

	// 根据操作符类型生成不同的SQL表达式
	switch c.Operator {
	// 处理等于类操作符（=, IN, LIKE）
	case metadata.ConditionEqual, metadata.ConditionExact, metadata.ConditionContains:
		if len(c.Value) == 1 && c.Value[0] == "" {
			op = "IS NULL"
			break
		}

		if len(c.Value) > 1 && !c.IsWildcard && !d.checkMatchALL(c.DimensionName) {
			op = "IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
			break
		}

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
			d.addLabel(oldKey, v)
			filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
		}
		key = ""
		if len(filter) == 1 {
			val = filter[0]
		} else {
			val = fmt.Sprintf("(%s)", strings.Join(filter, " OR "))
		}
	// 处理不等于类操作符（!=, NOT IN, NOT LIKE）
	case metadata.ConditionNotEqual, metadata.ConditionNotContains:
		if len(c.Value) == 1 && c.Value[0] == "" {
			op = "IS NOT NULL"
			break
		}

		if len(c.Value) > 1 && !c.IsWildcard && !d.checkMatchALL(c.DimensionName) {
			op = "NOT IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
			break
		}

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
			if v != "" {
				d.addLabel(key, v)
			}
			filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
		}
		key = ""
		if len(filter) == 1 {
			val = filter[0]
		} else {
			val = fmt.Sprintf("(%s)", strings.Join(filter, " AND "))
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
		condition := fmt.Sprintf("%s %s", key, op)
		if val != "" {
			d.addLabel(oldKey, val)
			condition = fmt.Sprintf("%s %s", condition, val)
		}
		return condition, nil
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

func (d *DorisSQLExpr) addLabel(key, value string) {
	if !d.isSetLabels {
		return
	}

	d.lock.Lock()
	defer d.lock.Unlock()

	if d.labelCheck == nil {
		d.labelCheck = make(map[string]struct{})
	}
	if d.labelMap == nil {
		d.labelMap = make(map[string][]string)
	}

	if _, ok := d.labelCheck[key+value]; !ok {
		d.labelCheck[key+value] = struct{}{}
		d.labelMap[key] = append(d.labelMap[key], value)
	}
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
			c.Field = DefaultKey
		}

		d.addLabel(c.Field, c.Value)
		field, _ := d.dimTransform(c.Field)
		return fmt.Sprintf("%s LIKE '%%%s%%'", field, c.Value), nil
	case *querystring.MatchExpr:
		if c.Field == "" {
			c.Field = DefaultKey
		}
		d.addLabel(c.Field, c.Value)
		field, _ := d.dimTransform(c.Field)
		if d.checkMatchALL(c.Field) {
			return fmt.Sprintf("%s MATCH_PHRASE_PREFIX '%s'", field, c.Value), nil
		}

		return fmt.Sprintf("%s = '%s'", field, c.Value), nil
	case *querystring.NumberRangeExpr:
		if c.Field == "" {
			c.Field = DefaultKey
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
		return fmt.Sprintf("CAST(%s[\"%s\"] AS STRING)", fs[0], strings.Join(fs[1:], `.`)), true
	}
	return fmt.Sprintf("`%s`", s), false
}

func (d *DorisSQLExpr) valueTransform(s string) string {
	if strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "''")
	}
	return s
}
