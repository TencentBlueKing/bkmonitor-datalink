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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	SelectAll = "*"
	TimeStamp = "_timestamp_"
	Value     = "_value_"

	FieldValue = "_value"
	FieldTime  = "_time"

	theDate = "thedate"
)

// ErrorMatchAll 定义全字段检索错误提示信息
var (
	ErrorMatchAll = "不支持全字段检索"
)

// SQLExpr 定义SQL表达式生成接口
// 接口包含字段映射、查询字符串解析、全条件解析等功能
type SQLExpr interface {
	// WithFieldsMap 设置字段类型
	WithFieldsMap(fieldsMap map[string]string) SQLExpr
	// WithEncode 字段转换方法
	WithEncode(func(string) string) SQLExpr
	// WithInternalFields 设置内部字段
	WithInternalFields(timeField, valueField string) SQLExpr
	// ParserQueryString 解析 es 特殊语法 queryString 生成SQL条件
	ParserQueryString(qs string) (string, error)
	// ParserAllConditions 解析全量条件生成SQL条件表达式
	ParserAllConditions(allConditions metadata.AllConditions) (string, error)
	// ParserAggregatesAndOrders 解析聚合条件生成SQL条件表达式
	ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) ([]string, []string, []string, error)
}

// SQL表达式注册管理相关变量
var (
	_ SQLExpr = (*DefaultSQLExpr)(nil) // 接口实现检查

	lock    sync.RWMutex               // 读写锁用于并发安全
	exprMap = make(map[string]SQLExpr) // 存储注册的SQL表达式实现
)

// GetSQLExpr 获取指定key的SQL表达式实现
// 参数：
//
//	key - 注册时使用的标识符
//
// 返回值：
//
//	找到返回对应实现，未找到返回默认实现
func GetSQLExpr(key string) SQLExpr {
	lock.RLock()
	defer lock.RUnlock()
	if sqlExpr, ok := exprMap[key]; ok {
		return sqlExpr
	} else {
		return &DefaultSQLExpr{}
	}
}

// Register 注册SQL表达式实现
// 参数：
//
//	key - 实现标识符
//	sqlExpr - 具体的SQL表达式实现
func Register(key string, sqlExpr SQLExpr) {
	lock.Lock()
	defer lock.Unlock()
	exprMap[key] = sqlExpr
}

// UnRegister 注销指定key的SQL表达式实现
// 参数：
//
//	key - 要注销的实现标识符
func UnRegister(key string) {
	lock.Lock()
	defer lock.Unlock()
	if _, ok := exprMap[key]; ok {
		delete(exprMap, key)
	}
}

// DefaultSQLExpr SQL表达式默认实现
type DefaultSQLExpr struct {
	encodeFunc func(string) string

	fieldsMap map[string]string

	timeField  string
	valueField string
}

func (d *DefaultSQLExpr) WithInternalFields(timeField, valueField string) SQLExpr {
	d.timeField = timeField
	d.valueField = valueField
	return d
}

func (d *DefaultSQLExpr) WithEncode(fn func(string) string) SQLExpr {
	d.encodeFunc = fn
	return d
}

func (d *DefaultSQLExpr) WithFieldsMap(fieldsMap map[string]string) SQLExpr {
	d.fieldsMap = fieldsMap
	return d
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

// ParserQueryString 解析查询字符串（当前实现返回空）
func (d *DefaultSQLExpr) ParserQueryString(_ string) (string, error) {
	return "", nil
}

// ParserAggregatesAndOrders 解析聚合函数，生成 select 和 group by 字段
func (d *DefaultSQLExpr) ParserAggregatesAndOrders(aggregates metadata.Aggregates, orders metadata.Orders) (selectFields []string, groupByFields []string, orderByFields []string, err error) {
	valueField, err := d.dimTransform(d.valueField)
	if err != nil {
		return
	}

	for _, agg := range aggregates {
		for _, dim := range agg.Dimensions {
			dim, err = d.dimTransform(dim)
			if err != nil {
				return
			}
			selectFields = append(selectFields, dim)
			groupByFields = append(groupByFields, dim)
		}

		if valueField == "" {
			valueField = SelectAll
		}
		selectFields = append(selectFields, fmt.Sprintf("%s(%s) AS `%s`", strings.ToUpper(agg.Name), valueField, Value))

		if agg.Window > 0 {
			// 时间聚合函数兼容时区
			loc, locErr := time.LoadLocation(agg.TimeZone)
			if locErr != nil {
				loc = time.UTC
			}
			// 获取时区偏移量
			_, offset := time.Now().In(loc).Zone()
			offsetMillis := offset * 1000

			timeField := fmt.Sprintf("(`%s` - ((`%s` - %d) %% %d - %d))", d.timeField, d.timeField, offsetMillis, agg.Window.Milliseconds(), offsetMillis)

			groupByFields = append(groupByFields, timeField)
			selectFields = append(selectFields, fmt.Sprintf("MAX(%s) AS `%s`", timeField, TimeStamp))

			orderByFields = append(orderByFields, fmt.Sprintf("`%s` ASC", TimeStamp))
		}
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

	for key, asc := range orders {
		var orderField string
		switch key {
		case FieldValue:
			orderField = d.valueField
		case FieldTime:
			orderField = TimeStamp
		default:
			orderField = key
		}

		orderField, err = d.dimTransform(orderField)
		if err != nil {
			return
		}

		ascName := "ASC"
		if !asc {
			ascName = "DESC"
		}
		orderByFields = append(orderByFields, fmt.Sprintf("%s %s", orderField, ascName))
	}

	return
}

// ParserAllConditions 解析全量条件生成SQL条件表达式
// 实现逻辑：
//  1. 将多个AND条件组合成OR条件
//  2. 当有多个OR条件时用括号包裹
func (d *DefaultSQLExpr) ParserAllConditions(allConditions metadata.AllConditions) (string, error) {
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

// buildCondition 构建单个条件表达式
// 参数：
//
//	c - 条件字段对象
//
// 返回值：
//
//	生成的SQL条件表达式字符串和错误信息
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
