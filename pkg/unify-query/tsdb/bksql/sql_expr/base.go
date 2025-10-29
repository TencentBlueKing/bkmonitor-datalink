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
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	SelectAll = "*"
	TimeStamp = "_timestamp_"
	Value     = "_value_"

	FieldValue = "_value"
	FieldTime  = "_time"

	theDate = "thedate"

	HDFS = "hdfs"
)

// ErrorMatchAll 定义全字段检索错误提示信息
var (
	ErrorMatchAll = "不支持全字段检索"
)

type FieldOption struct {
	Type     string
	Analyzed bool
}

type TimeAggregate struct {
	Window       time.Duration
	OffsetMillis int64
}

// SQLExpr 定义SQL表达式生成接口
// 接口包含字段映射、查询字符串解析、全条件解析等功能
type SQLExpr interface {
	// WithKeepColumns 设置保留字段
	WithKeepColumns([]string) SQLExpr
	// WithFieldAlias 设置字段别名
	WithFieldAlias(fieldAlias metadata.FieldAlias) SQLExpr
	// WithFieldsMap 设置字段类型
	WithFieldsMap(fieldsMap metadata.FieldsMap) SQLExpr
	// WithEncode 字段转换方法
	WithEncode(func(string) string) SQLExpr
	// WithInternalFields 设置内部字段
	WithInternalFields(timeField, valueField string) SQLExpr
	// ParserRangeTime 解析开始结束时间
	ParserRangeTime(timeField string, start, end time.Time) string
	// ParserQueryString 解析 es 特殊语法 queryString 生成SQL条件
	ParserQueryString(ctx context.Context, qs string) (string, error)
	// ParserAllConditions 解析全量条件生成SQL条件表达式
	ParserAllConditions(allConditions metadata.AllConditions) (string, error)
	// ParserAggregatesAndOrders 解析聚合条件生成SQL条件表达式
	ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) ([]string, []string, []string, *set.Set[string], TimeAggregate, error)
	// ParserSQL 解析 String 语句
	ParserSQL(ctx context.Context, q string, tables []string, where string, offset, limit int, isScroll bool) (string, error)
	// DescribeTableSQL 返回当前表结构
	DescribeTableSQL(table string) string
	// FieldMap 返回当前表结构
	FieldMap() metadata.FieldsMap
	// Type 返回表达式类型
	Type() string
}

// SQL表达式注册管理相关变量
var (
	_ SQLExpr = (*DefaultSQLExpr)(nil) // 接口实现检查
)

// NewSQLExpr 获取指定key的SQL表达式实现
// 参数：
//
//	key - 注册时使用的标识符
//
// 返回值：
//
//	找到返回对应实现，未找到返回默认实现
func NewSQLExpr(key string) SQLExpr {
	switch key {
	case Doris:
		return &DorisSQLExpr{}
	default:
		return &DefaultSQLExpr{key: key}
	}
}

// DefaultSQLExpr SQL表达式默认实现
type DefaultSQLExpr struct {
	encodeFunc func(string) string

	keepColumns []string
	fieldMap    metadata.FieldsMap
	fieldAlias  metadata.FieldAlias

	timeField  string
	valueField string

	key string
}

func (d *DefaultSQLExpr) Type() string {
	return "default"
}

func (d *DefaultSQLExpr) WithInternalFields(timeField, valueField string) SQLExpr {
	d.timeField = timeField
	d.valueField = valueField
	return d
}

func (d *DefaultSQLExpr) WithFieldAlias(fieldAlias metadata.FieldAlias) SQLExpr {
	d.fieldAlias = fieldAlias
	return d
}

func (d *DefaultSQLExpr) WithEncode(fn func(string) string) SQLExpr {
	d.encodeFunc = fn
	return d
}

func (d *DefaultSQLExpr) WithFieldsMap(fieldMap metadata.FieldsMap) SQLExpr {
	d.fieldMap = fieldMap
	return d
}

func (d *DefaultSQLExpr) ParserSQL(ctx context.Context, q string, tables []string, where string, offset, limit int, isScroll bool) (string, error) {
	return "", nil
}

func (d *DefaultSQLExpr) WithKeepColumns(cols []string) SQLExpr {
	d.keepColumns = cols
	return d
}

func (d *DefaultSQLExpr) GetLabelMap() map[string][]string {
	return nil
}

func (d *DefaultSQLExpr) FieldMap() metadata.FieldsMap {
	return d.fieldMap
}

// ParserQueryString 解析查询字符串（当前实现返回空）
func (d *DefaultSQLExpr) ParserQueryString(ctx context.Context, _ string) (string, error) {
	return "", nil
}

// ParserAggregatesAndOrders 解析聚合函数，生成 select 和 group by 字段
func (d *DefaultSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, dimensionSet *set.Set[string], timeAggregate TimeAggregate, err error) {
	valueField, err := d.dimTransform(d.valueField)
	if err != nil {
		return selectFields, groupByFields, orderByFields, dimensionSet, timeAggregate, err
	}

	var (
		window       time.Duration
		offsetMillis int64
		timezone     string
	)

	// 时间和值排序属于内置字段，默认需要支持
	dimensionSet = set.New[string]([]string{FieldValue}...)
	for _, agg := range aggregates {
		for _, dim := range agg.Dimensions {
			var newDim string

			dimensionSet.Add(dim)
			newDim, err = d.dimTransform(dim)
			if err != nil {
				return selectFields, groupByFields, orderByFields, dimensionSet, timeAggregate, err
			}

			selectFields = append(selectFields, newDim)
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
		if function.IsAlignTime(window) {
			// 时间聚合函数兼容时区
			loc, locErr := time.LoadLocation(timezone)
			if locErr != nil {
				loc = time.UTC
			}
			// 获取时区偏移量
			_, offset := time.Now().In(loc).Zone()
			offsetMillis = int64(offset) * 1e3
		}

		timeField := fmt.Sprintf("(FLOOR((%s + %d) / %d) * %d - %d)", d.timeField, offsetMillis, window.Milliseconds(), window.Milliseconds(), offsetMillis)

		groupByFields = append(groupByFields, timeField)
		selectFields = append(selectFields, fmt.Sprintf("MAX%s AS `%s`", timeField, TimeStamp))

		// 只有时间聚合的条件下，才可以使用时间聚合排序
		dimensionSet.Add(FieldTime)
	}

	if len(selectFields) == 0 {
		selectFields = append(selectFields, SelectAll)
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
			orderField = d.valueField
		case FieldTime:
			orderField = TimeStamp
		default:
			orderField = order.Name
		}

		orderField, err = d.dimTransform(orderField)
		if err != nil {
			return selectFields, groupByFields, orderByFields, dimensionSet, timeAggregate, err
		}

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
		OffsetMillis: offsetMillis,
	}

	return selectFields, groupByFields, orderByFields, dimensionSet, timeAggregate, err
}

func (d *DefaultSQLExpr) ParserRangeTime(timeField string, start, end time.Time) string {
	return fmt.Sprintf("`%s` >= %d AND `%s` < %d", timeField, start.UnixMilli(), timeField, end.UnixMilli())
}

// ParserAllConditions 解析全量条件生成SQL条件表达式
// 实现逻辑：
//  1. 提取所有OR分支中的公共条件
//  2. 将公共条件和剩余条件用AND连接
//  3. 剩余条件按原有逻辑用OR连接
func (d *DefaultSQLExpr) ParserAllConditions(allConditions metadata.AllConditions) (string, error) {
	return parserAllConditions(allConditions, d.buildCondition)
}

func (d *DefaultSQLExpr) DescribeTableSQL(table string) string {
	return ""
}

// buildCondition 构建单个条件表达式
func (d *DefaultSQLExpr) buildCondition(c metadata.ConditionField) (string, error) {
	if len(c.Value) == 0 {
		return "", nil
	}

	var (
		key string
		op  string
		val string
	)

	key, err := d.dimTransform(c.DimensionName)
	if err != nil {
		return "", err
	}

	// 对值进行转义处理
	for i, v := range c.Value {
		c.Value[i] = d.valueTransform(v)
	}

	// 根据操作符类型生成不同的SQL表达式
	switch c.Operator {
	// 处理等于类操作符（=, IN, LIKE）
	case metadata.ConditionEqual, metadata.ConditionExact, metadata.ConditionContains:
		if len(c.Value) > 1 && !c.IsWildcard {
			op = "IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "LIKE"
			} else {
				format = "'%s'"
				op = "="
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
		if len(c.Value) > 1 && !c.IsWildcard {
			op = "NOT IN"
			val = fmt.Sprintf("('%s')", strings.Join(c.Value, "', '"))
		} else {
			var format string
			if c.IsWildcard {
				format = "'%%%s%%'"
				op = "NOT LIKE"
			} else {
				format = "'%s'"
				op = "!="
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
	// 根据数据库类型选择不同的正则语法：
	// - HDFS 使用 regexp_like() 函数
	// - 其他数据库使用 REGEXP 操作符
	case metadata.ConditionRegEqual:
		if d.key == HDFS {
			pattern := strings.Join(c.Value, "|") // 多个值用|连接
			val = fmt.Sprintf("regexp_like(%s, '%s')", key, pattern)
			key = ""
		} else {
			op = "REGEXP"
			val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|"))
		}
	case metadata.ConditionNotRegEqual:
		if d.key == HDFS {
			pattern := strings.Join(c.Value, "|")
			val = fmt.Sprintf("NOT regexp_like(%s, '%s')", key, pattern)
			key = ""
		} else {
			op = "NOT REGEXP"
			val = fmt.Sprintf("'%s'", strings.Join(c.Value, "|"))
		}

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

func (d *DefaultSQLExpr) valueTransform(s string) string {
	if strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "''")
	}
	return s
}

func (d *DefaultSQLExpr) dimTransform(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	fs := strings.Split(s, ".")
	if len(fs) > 1 {
		return "", fmt.Errorf("query is not support object with %s", s)
	}

	return fmt.Sprintf("`%s`", s), nil
}

// extractCommonConditions 提取所有OR分支中的公共条件
// 返回：公共条件列表和移除公共条件后的剩余分支
func extractCommonConditions(allConditions metadata.AllConditions) (
	common []metadata.ConditionField,
	remaining metadata.AllConditions,
) {
	// 少于2个分支，无需优化
	if len(allConditions) <= 1 {
		return nil, allConditions
	}

	// 1. 统计每个条件签名在多少个分支中，并提取公共部分
	// 使用字符串连接的方式标记每个 or 里面都命中该下标
	hitCount := make(map[string]string)
	allCount := ""
	for i := range allConditions {
		allCount += fmt.Sprintf("|%d", i)
	}

	for i, branch := range allConditions {
		for _, cond := range branch {
			sig := cond.Signature()
			hitCount[sig] += fmt.Sprintf("|%d", i)
			if hitCount[sig] == allCount {
				common = append(common, cond)
			}
		}
	}

	// 没有公共条件，直接返回原始数据
	if len(common) == 0 {
		return nil, allConditions
	}

	// 构建剩余非公共条件
	for _, branch := range allConditions {
		var newBranch []metadata.ConditionField
		for _, cond := range branch {
			if hitCount[cond.Signature()] != allCount {
				newBranch = append(newBranch, cond)
			}
		}

		// 判断如果其中有一个分组为长度为空，则说明里面的条件都是公共条件，所以后面的 remaining 就必定为 true 可以提前返回
		// 例如： a = 1 and b = 2 or a = 1 and b = 3 or a = 1
		// 只需要保留 a = 1 即可，而无需完整的写成： a = 1 and (b = 2 or b = 3 or 1 = 1)
		if len(newBranch) == 0 {
			return common, nil
		}

		remaining = append(remaining, newBranch)
	}

	return common, remaining
}

// parserAllConditions 解析全量条件并进行优化，提取公共条件
// 算法流程：
// 1. 提取所有OR分支中的公共条件
// 2. 构建公共条件的SQL
// 3. 构建剩余条件的OR表达式（不进行递归优化，避免无限递归）
// 4. 用AND连接公共条件和剩余条件
func parserAllConditions(allConditions metadata.AllConditions, bc func(c metadata.ConditionField) (string, error)) (string, error) {
	// 1. 提取公共条件
	common, remaining := extractCommonConditions(allConditions)

	var parts []string

	// 2. 构建公共条件SQL
	for _, cond := range common {
		condSQL, err := bc(cond)
		if err != nil {
			return "", err
		}
		if condSQL != "" {
			parts = append(parts, condSQL)
		}
	}

	// 3. 构建剩余条件SQL（不进行递归优化）
	// 直接构建OR逻辑，不进行递归优化
	var orConditions []string
	for _, conditions := range remaining {
		var andConditions []string
		for _, cond := range conditions {
			buildCondition, err := bc(cond)
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

	// 处理OR条件组合
	if len(orConditions) > 0 {
		var remainingSQL string
		if len(orConditions) == 1 {
			remainingSQL = orConditions[0]
		} else {
			remainingSQL = fmt.Sprintf("(%s)", strings.Join(orConditions, " OR "))
		}
		if remainingSQL != "" {
			parts = append(parts, remainingSQL)
		}
	}

	// 4. 组合最终SQL
	if len(parts) == 0 {
		return "", nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return strings.Join(parts, " AND "), nil
}
