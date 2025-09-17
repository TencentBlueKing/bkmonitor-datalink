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
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring_parser"
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
	DefaultSQLExpr

	encodeFunc func(string) string

	timeField  string
	valueField string

	keepColumns []string
	fieldsMap   map[string]FieldOption
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

func (d *DorisSQLExpr) WithFieldsMap(fieldsMap map[string]FieldOption) SQLExpr {
	d.fieldsMap = fieldsMap
	return d
}

func (d *DorisSQLExpr) WithKeepColumns(cols []string) SQLExpr {
	d.keepColumns = cols
	return d
}

func (d *DorisSQLExpr) FieldMap() map[string]FieldOption {
	return d.fieldsMap
}

func (d *DorisSQLExpr) ParserQueryString(qs string) (string, error) {
	expr, err := querystring_parser.ParseWithFieldAlias(qs, d.fieldAlias)
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

func (d *DorisSQLExpr) ParserSQLWithVisitor(ctx context.Context, q, table, where string) (sql string, err error) {
	return "", nil
}

func (d *DorisSQLExpr) ParserSQL(ctx context.Context, q, table, where string) (sql string, err error) {
	opt := &doris_parser.Option{
		DimensionTransform: func(field string) (string, bool) {
			var (
				originFiled string
				ok          bool
			)
			if originFiled, ok = d.fieldAlias[field]; ok {
				field = originFiled
			}
			field, _ = d.dimTransform(field)
			return field, ok
		},
		Table: table,
		Where: where,
	}

	return doris_parser.ParseDorisSQLWithVisitor(ctx, q, opt)
}

// ParserAggregatesAndOrders 解析聚合函数，生成 select 和 group by 字段
func (d *DorisSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, dimensionSet *set.Set[string], timeAggregate TimeAggregate, err error) {
	valueField, _ := d.dimTransform(d.valueField)

	var (
		window         time.Duration
		timeZoneOffset int64
	)

	dimensionSet = set.New[string]([]string{FieldValue}...)

	for _, agg := range aggregates {
		for _, dim := range agg.Dimensions {
			var (
				as          string
				newDim      string
				selectAlias string
			)

			dimensionSet.Add(dim)

			newDim, as = d.dimTransform(dim)
			if as != "" {
				selectAlias = fmt.Sprintf("%s AS `%s`", newDim, as)
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
		case "distinct":
			// distinct 聚合：生成 SELECT DISTINCT 查询，不需要聚合函数包装
			// 字段转换已经在前面的 dimension 处理中完成
			// 这里不添加 valueField，因为 DISTINCT 只关心维度字段
		// date_histogram 不支持无需进行函数聚合
		case "date_histogram":
		default:
			selectFields = append(selectFields, fmt.Sprintf("%s(%s) AS `%s`", strings.ToUpper(agg.Name), valueField, Value))
		}

		if agg.Window > 0 {
			window = agg.Window
			timeZoneOffset = agg.TimeZoneOffset
		}
	}

	if window > 0 {
		fh_1 := "+"
		fh_2 := "-"
		if timeZoneOffset > 0 {
			fh_1 = "-"
			fh_2 = "+"
		} else {
			timeZoneOffset *= -1
		}

		// 如果是按照分钟聚合，则使用 __shard_key__ 作为时间字段
		var timeField string
		if int64(window.Seconds())%60 == 0 {
			windowMinutes := int(window.Minutes())
			timeField = fmt.Sprintf(`((CAST((FLOOR(%s / 1000) %s %d) / %d AS INT) * %d %s %d) * 60 * 1000)`, ShardKey, fh_1, timeZoneOffset/6e4, windowMinutes, windowMinutes, fh_2, timeZoneOffset/6e4)
		} else {
			timeField = fmt.Sprintf(`(CAST((FLOOR(%s %s %d) / %d) AS INT) * %d %s %d)`, d.timeField, fh_1, timeZoneOffset, window.Milliseconds(), window.Milliseconds(), fh_2, timeZoneOffset)
		}

		selectFields = append(selectFields, fmt.Sprintf("%s AS `%s`", timeField, TimeStamp))
		groupByFields = append(groupByFields, TimeStamp)

		// 只有时间聚合的条件下，才可以使用时间聚合排序
		dimensionSet.Add(FieldTime)
	}

	if len(selectFields) == 0 {
		if len(d.keepColumns) > 0 {
			selectFields = append(selectFields, lo.Map(d.keepColumns, func(item string, index int) string {
				field, as := d.dimTransform(item)
				if as != "" {
					return fmt.Sprintf("%s AS `%s`", field, as)
				}
				return field
			})...)
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

	orderNameSet := set.New[string]()
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

		// 移除重复的排序字段
		if orderNameSet.Existed(orderField) {
			continue
		}
		orderNameSet.Add(orderField)

		ascName := "ASC"
		if !order.Ast {
			ascName = "DESC"
		}
		orderByFields = append(orderByFields, fmt.Sprintf("%s %s", orderField, ascName))
	}

	// 回传时间聚合信息
	timeAggregate = TimeAggregate{
		Window:       window,
		OffsetMillis: timeZoneOffset,
	}

	return selectFields, groupByFields, orderByFields, dimensionSet, timeAggregate, err
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

		filter []string

		err error
	)

	if c.DimensionName == "*" || c.DimensionName == "" {
		c.DimensionName = DefaultKey
	}

	oldKey = c.DimensionName
	key, _ = d.dimTransform(oldKey)

	// 对值进行转义处理
	for i, v := range c.Value {
		c.Value[i] = d.valueTransform(v)
	}

	// doris 里面 array<int> 类型需要特殊处理
	checkArrayIntByOp := func(o string) (string, string, error) {
		if len(c.Value) != 1 {
			return "", "", fmt.Errorf("operator %s only support 1 value", o)
		}
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s %s, %s)", o, c.Value[0], key)
			key = ""
		} else {
			val = c.Value[0]
		}
		return key, val, nil
	}

	// doris 里面 array<string> 类型需要特殊处理
	checkArrayStringByOp := func(op string) (string, string) {
		if d.isArray(c.DimensionName) {
			val = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x %s '%s', %s)", op, strings.Join(c.Value, "|"), key)
			key = ""
		} else {
			val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|")) // 多个值用|连接
		}
		return key, val
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

		if d.isArray(c.DimensionName) {
			for _, v := range c.Value {
				var value string
				if c.IsWildcard {
					value = fmt.Sprintf("ARRAY_MATCH_ANY(x -> x LIKE '%s', %s)", d.likeValue(v), key)
				} else {
					value = fmt.Sprintf("ARRAY_CONTAINS(%s, '%s') == 1", key, v)
				}
				filter = append(filter, value)
			}
		} else {
			if c.IsWildcard {
				op = "LIKE"
			} else {
				if c.IsPrefix {
					op = "MATCH_PHRASE_PREFIX"
				} else if c.IsSuffix {
					op = "MATCH_PHRASE_EDGE"
				} else {
					if c.Operator == metadata.ConditionContains {
						op = "MATCH_PHRASE"
					} else {
						if d.isAnalyzed(c.DimensionName) {
							op = "MATCH_PHRASE"
						} else {
							op = "="
						}
					}
				}
			}

			for _, v := range c.Value {
				if c.IsWildcard {
					v = d.likeValue(v)
				}
				filter = append(filter, fmt.Sprintf("%s %s '%s'", key, op, v))
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
			if c.IsWildcard {
				op = "NOT LIKE"
			} else {
				if c.IsPrefix {
					op = "NOT MATCH_PHRASE_PREFIX"
				} else if c.IsSuffix {
					op = "NOT MATCH_PHRASE_EDGE"
				} else {
					if c.Operator == metadata.ConditionNotContains {
						op = "NOT MATCH_PHRASE"
					} else {
						if d.isAnalyzed(c.DimensionName) {
							op = "NOT MATCH_PHRASE"
						} else {
							op = "!="
						}
					}
				}
			}

			for _, v := range c.Value {
				if c.IsWildcard {
					v = d.likeValue(v)
				}
				filter = append(filter, fmt.Sprintf("%s %s '%s'", key, op, v))
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
		key, val = checkArrayStringByOp(op)
	case metadata.ConditionNotRegEqual:
		op = "NOT REGEXP"
		key, val = checkArrayStringByOp(op)
	// 处理数值比较操作符（>, >=, <, <=）
	case metadata.ConditionGt:
		op = ">"
		key, val, err = checkArrayIntByOp(op)
		if err != nil {
			return "", err
		}
	case metadata.ConditionGte:
		op = ">="
		key, val, err = checkArrayIntByOp(op)
		if err != nil {
			return "", err
		}
	case metadata.ConditionLt:
		op = "<"
		key, val, err = checkArrayIntByOp(op)
		if err != nil {
			return "", err
		}
	case metadata.ConditionLte:
		op = "<="
		key, val, err = checkArrayIntByOp(op)
		if err != nil {
			return "", err
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
	_, ok := d.caseAs(fieldType.Type)
	return ok
}

func (d *DorisSQLExpr) isText(k string) bool {
	return d.getFieldType(k).Type == DorisTypeText
}

func (d *DorisSQLExpr) isAnalyzed(k string) bool {
	return d.getFieldType(k).Analyzed
}

func (d *DorisSQLExpr) likeValue(s string) string {
	if s == "" {
		return s
	}

	charChange := func(cur, last rune) rune {
		if last == '\\' {
			return cur
		}

		if cur == '*' {
			return '%'
		}

		if cur == '?' {
			return '_'
		}

		return cur
	}

	var (
		ns       []rune
		lastChar rune
	)
	for _, char := range s {
		ns = append(ns, charChange(char, lastChar))
		lastChar = char
	}

	return string(ns)
}

func (d *DorisSQLExpr) walk(e querystring_parser.Expr) (string, error) {
	var (
		err   error
		left  string
		right string
	)

	switch c := e.(type) {
	case *querystring_parser.NotExpr:
		left, err = d.walk(c.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("NOT (%s)", left), nil
	case *querystring_parser.OrExpr:
		left, err = d.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = d.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s OR %s)", left, right), nil
	case *querystring_parser.AndExpr:
		left, err = d.walk(c.Left)
		if err != nil {
			return "", err
		}
		right, err = d.walk(c.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s AND %s", left, right), nil
	case *querystring_parser.WildcardExpr:
		if c.Field == "" {
			c.Field = DefaultKey
		}
		field, _ := d.dimTransform(c.Field)
		return fmt.Sprintf("%s LIKE '%s'", field, d.likeValue(c.Value)), nil
	case *querystring_parser.MatchExpr:
		if c.Field == "" {
			c.Field = DefaultKey
		}
		field, _ := d.dimTransform(c.Field)
		if d.isAnalyzed(c.Field) {
			return fmt.Sprintf("%s MATCH_PHRASE '%s'", field, c.Value), nil
		}

		return fmt.Sprintf("%s = '%s'", field, c.Value), nil
	case *querystring_parser.NumberRangeExpr:
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

func (d *DorisSQLExpr) getFieldType(s string) (opt FieldOption) {
	if d.fieldsMap == nil {
		return opt
	}

	var ok bool
	if opt, ok = d.fieldsMap[s]; ok {
		opt.Type = strings.ToUpper(opt.Type)
	}
	return opt
}

func (d *DorisSQLExpr) caseAs(s string) (string, bool) {
	// 如果字段不存在则默认使用 string 类型
	if s == "" {
		return DorisTypeString, false
	}

	re := regexp.MustCompile(`ARRAY<([^>]+)>`) // 匹配 < 和 > 之间的非 > 字符
	matches := re.FindStringSubmatch(s)
	if len(matches) > 1 {
		return fmt.Sprintf("%s ARRAY", matches[1]), true
	}
	return s, false
}

func (d *DorisSQLExpr) getArrayType(s string) string {
	return fmt.Sprintf(DorisTypeArray, s)
}

func (d *DorisSQLExpr) arrayTypeTransform(s string) string {
	return fmt.Sprintf(DorisTypeArrayTransform, s)
}

func (d *DorisSQLExpr) dimTransform(s string) (ns string, as string) {
	ns = s
	if s == "" || s == "*" {
		return ns, as
	}
	if alias, ok := d.fieldAlias[s]; ok {
		ns = alias
		as = s
	}

	fieldType := d.getFieldType(s)
	castType, _ := d.caseAs(fieldType.Type)

	fs := strings.Split(s, ".")
	if len(fs) == 1 {
		ns = fmt.Sprintf("`%s`", ns)
		return ns, as
	}

	// 如果是 resource 或 attributes 字段里都是用户上报的内容，采用 . 作为 key 上报，所以这里增加了特殊处理
	// 例如： events['attributes']['exception.type']
	mapFieldSet := set.New[string]([]string{"resource", "attributes"}...)

	var (
		suffixFields strings.Builder
		// 协议自定义是 map 结构
		sep string
	)
	for index, f := range fs {
		// 第一个补充开头
		if index == 0 {
			sep = `['`
		} else if index == len(fs)-1 {
			// 最后一个不需要补充
			sep = `']`
		}

		suffixFields.WriteString(f + sep)
		// 用户上报的分隔符为 .
		if mapFieldSet.Existed(f) {
			sep = "."
		} else if sep != "." {
			sep = "']['"
		}
	}

	if as == "" {
		as = s
	}
	if d.encodeFunc != nil {
		as = d.encodeFunc(as)
	}

	ns = fmt.Sprintf(`CAST(%s AS %s)`, suffixFields.String(), castType)
	return ns, as
}

func (d *DorisSQLExpr) valueTransform(s string) string {
	if strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "''")
	}
	return s
}
