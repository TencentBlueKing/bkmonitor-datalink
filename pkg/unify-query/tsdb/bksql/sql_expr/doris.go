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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	Doris = "doris"

	ShardKey = "__shard_key__"

	SelectIndex = "_index"

	DefaultKey = "log"
)

const (
	DorisTypeInt       = "INT"
	DorisTypeTinyInt   = "TINYINT"
	DorisTypeSmallInt  = "SMALLINT"
	DorisTypeLargeInt  = "LARGEINT"
	DorisTypeBigInt    = "BIGINT"
	DorisTypeFloat     = "FLOAT"
	DorisTypeDouble    = "DOUBLE"
	DorisTypeDecimal   = "DECIMAL"
	DorisTypeDecimalV3 = "DECIMALV3"

	DorisTypeDate      = "DATE"
	DorisTypeDatetime  = "DATETIME"
	DorisTypeTimestamp = "TIMESTAMP"

	DorisTypeBoolean = "BOOLEAN"

	DorisTypeString     = "STRING"
	DorisTypeText       = "TEXT"
	DorisTypeVarchar512 = "VARCHAR(512)"

	DorisTypeArrayTransform = "%s ARRAY"
	DorisTypeArray          = "ARRAY<%s>"
)

type DorisSQLExpr struct {
	encodeFunc func(string) string

	timeField  string
	valueField string

	keepColumns []string
	fieldsMap   map[string]string
	fieldAlias  metadata.FieldAlias

	isSetLabels bool
	lock        sync.Mutex
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

func (d *DorisSQLExpr) WithFieldAlias(fieldAlias metadata.FieldAlias) SQLExpr {
	d.fieldAlias = fieldAlias
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
	expr, err := querystring.ParseWithFieldAlias(qs, d.fieldAlias)
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
func (d *DorisSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, dimensionSet *set.Set[string], timeAggregate TimeAggregate, err error) {
	valueField, _ := d.dimTransform(d.valueField)

	var (
		window        time.Duration
		offsetMinutes int64

		timezone string
	)

	dimensionSet = set.New[string]([]string{FieldValue, FieldTime}...)
	for _, agg := range aggregates {
		for _, dim := range agg.Dimensions {
			var (
				isObject = false

				newDim      string
				selectAlias string
			)

			dimensionSet.Add(dim)

			newDim, isObject = d.dimTransform(dim)
			if isObject && d.encodeFunc != nil {
				selectAlias = fmt.Sprintf("%s AS `%s`", newDim, d.encodeFunc(dim))
				newDim = d.encodeFunc(dim)
			} else {
				selectAlias = newDim
			}

			selectFields = append(selectFields, selectAlias)
			groupByFields = append(groupByFields, newDim)
		}

		if valueField == "" || valueField == SelectIndex {
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
		if function.IsAlignTime(window) {
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
			timeField = fmt.Sprintf(`((CAST((FLOOR(%s / 1000) + %d) / %d AS INT) * %d - %d) * 60 * 1000)`, ShardKey, offsetMinutes, windowMinutes, windowMinutes, offsetMinutes)
		} else {
			timeField = fmt.Sprintf(`CAST(FLOOR(%s / %d) AS INT) * %d `, d.timeField, window.Milliseconds(), window.Milliseconds())
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
			if !dimensionSet.Existed(order.Name) {
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

func (d *DorisSQLExpr) ParserRangeTime(timeField string, start, end time.Time) string {
	return fmt.Sprintf("`%s` >= %d AND `%s` <= %d", timeField, start.UnixMilli(), timeField, end.UnixMilli())
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

		if len(c.Value) > 1 && !c.IsWildcard && !d.isText(c.DimensionName) && !d.isArray(c.DimensionName) {
			op = "IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
			break
		}

		var (
			filter []string
		)

		if d.isArray(c.DimensionName) {
			for _, v := range c.Value {
				var value string
				if c.IsWildcard {
					value = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x LIKE '%%%s%%', %s)", v, key)
				} else {
					value = fmt.Sprintf("ARRAY_CONTAINS(%s, '%s') == 1", key, v)
				}
				filter = append(filter, value)
			}
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "LIKE"
			} else {
				format = "'%s'"
				if d.isText(c.DimensionName) {
					op = "MATCH_PHRASE_PREFIX"
				} else {
					op = "="
				}
			}

			for _, v := range c.Value {
				filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
			}
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

		if len(c.Value) > 1 && !c.IsWildcard && !d.isText(c.DimensionName) {
			op = "NOT IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
			break
		}

		var filter []string

		if d.isArray(c.DimensionName) {
			for _, v := range c.Value {
				var value string
				if c.IsWildcard {
					value = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x NOT LIKE '%%%s%%', %s)", v, key)
				} else {
					value = fmt.Sprintf("ARRAY_CONTAINS(%s, '%s') != 1", key, v)
				}
				filter = append(filter, value)
			}
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "NOT LIKE"
			} else {
				format = "'%s'"
				if d.isText(c.DimensionName) {
					op = "NOT MATCH_PHRASE_PREFIX"
				} else {
					op = "!="
				}
			}

			for _, v := range c.Value {
				filter = append(filter, fmt.Sprintf("%s %s %s", key, op, fmt.Sprintf(format, v)))
			}
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
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s '%s', %s)", op, strings.Join(c.Value, "|"), key)
			key = ""
		} else {
			val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|")) // 多个值用|连接
		}
	case metadata.ConditionNotRegEqual:
		op = "NOT REGEXP"
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s '%s', %s)", op, strings.Join(c.Value, "|"), key)
			key = ""
		} else {
			val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|")) // 多个值用|连接
		}
	// 处理数值比较操作符（>, >=, <, <=）
	case metadata.ConditionGt:
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		op = ">"
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s %s, %s)", op, c.Value[0], key)
			key = ""
		} else {
			val = c.Value[0]
		}
	case metadata.ConditionGte:
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		op = ">="
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s %s, %s)", op, c.Value[0], key)
			key = ""
		} else {
			val = c.Value[0]
		}
	case metadata.ConditionLt:
		if len(c.Value) != 1 {
			return "", fmt.Errorf("operator %s only support 1 value", op)
		}
		op = "<"
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s %s', %s)", op, val, key)
			key = ""
		} else {
			val = c.Value[0]
		}
	case metadata.ConditionLte:
		op = "<="
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s %s, %s)", op, c.Value[0], key)
			key = ""
		} else {
			val = c.Value[0]
		}
	default:
		return "", fmt.Errorf("unknown operator %s", c.Operator)
	}

	if key != "" {
		condition := fmt.Sprintf("%s %s", key, op)
		if val != "" {
			condition = fmt.Sprintf("%s %s", condition, val)
		}
		return condition, nil
	}
	return val, nil
}

func (d *DorisSQLExpr) isArray(k string) bool {
	fieldType := d.getFieldType(k)
	return strings.Contains(fieldType, "ARRAY")
}

func (d *DorisSQLExpr) isText(k string) bool {
	return d.getFieldType(k) == DorisTypeText
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
		field, _ := d.dimTransform(c.Field)
		return fmt.Sprintf("%s LIKE '%%%s%%'", field, c.Value), nil
	case *querystring.MatchExpr:
		if c.Field == "" {
			c.Field = DefaultKey
		}
		field, _ := d.dimTransform(c.Field)
		if d.isText(c.Field) {
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

func (d *DorisSQLExpr) getFieldType(s string) (fieldType string) {
	if d.fieldsMap == nil {
		return
	}

	var ok bool
	if fieldType, ok = d.fieldsMap[s]; ok {
		fieldType = strings.ToUpper(fieldType)
	}
	return
}

func (d *DorisSQLExpr) getArrayType(s string) string {
	return fmt.Sprintf(DorisTypeArray, s)
}

func (d *DorisSQLExpr) arrayTypeTransform(s string) string {
	return fmt.Sprintf(DorisTypeArrayTransform, s)
}

func (d *DorisSQLExpr) dimTransform(s string) (string, bool) {
	if s == "" {
		return "", false
	}

	var castType string
	fieldType := d.getFieldType(s)
	switch fieldType {
	case DorisTypeTinyInt, DorisTypeSmallInt, DorisTypeInt, DorisTypeBigInt, DorisTypeLargeInt:
		castType = DorisTypeInt
	case DorisTypeFloat, DorisTypeDouble, DorisTypeDecimal, DorisTypeDecimalV3:
		castType = DorisTypeDouble
	case d.getArrayType(DorisTypeText):
		castType = d.arrayTypeTransform(DorisTypeText)
	case d.getArrayType(DorisTypeTinyInt), d.getArrayType(DorisTypeSmallInt), d.getArrayType(DorisTypeInt), d.getArrayType(DorisTypeBigInt), d.getArrayType(DorisTypeLargeInt):
		castType = d.arrayTypeTransform(DorisTypeInt)
	default:
		castType = DorisTypeString
	}

	fs := strings.Split(s, ".")
	if len(fs) > 1 {
		return fmt.Sprintf(`CAST(%s['%s'] AS %s)`, fs[0], strings.Join(fs[1:], `']['`), castType), true
	}
	return fmt.Sprintf("`%s`", s), false
}

func (d *DorisSQLExpr) valueTransform(s string) string {
	if strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "''")
	}
	return s
}
