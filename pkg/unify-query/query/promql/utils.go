// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/influxdata/influxql"
	"github.com/prometheus/prometheus/model/labels"
)

// 聚合函数常量定义
const (
	MIN   = "min"   // 最小值聚合
	MAX   = "max"   // 最大值聚合
	SUM   = "sum"   // 求和聚合
	COUNT = "count" // 计数聚合
	LAST  = "last"  // 最后一个值
	MEAN  = "mean"  // 平均值聚合
	AVG   = "avg"   // 平均值聚合（别名）

	// 时间窗口聚合函数
	MinOT   = "min_over_time"   // 时间窗口内最小值
	MaxOT   = "max_over_time"   // 时间窗口内最大值
	SumOT   = "sum_over_time"   // 时间窗口内求和
	CountOT = "count_over_time" // 时间窗口内计数
	LastOT  = "last_over_time"  // 时间窗口内最后一个值
	AvgOT   = "avg_over_time"   // 时间窗口内平均值
)

// 静态字段常量定义
const (
	StaticMetricName  = "metric_name"  // 静态指标名称字段
	StaticMetricValue = "metric_value" // 静态指标值字段

	StaticField = "value" // 静态值字段
)

// PromqlOperatorMapping PromQL 内置操作符映射表
// 将 PromQL 的 MatchType 映射为对应的操作符字符串
var PromqlOperatorMapping = map[labels.MatchType]string{
	labels.MatchEqual:     "=",  // 等于
	labels.MatchNotEqual:  "!=", // 不等于
	labels.MatchRegexp:    "=~", // 正则匹配
	labels.MatchNotRegexp: "!~", // 正则不匹配
}

// Operator 操作符类型定义
type Operator string

// 操作符常量定义
const (
	EqualOperator      string = "="  // 等于操作符
	NEqualOperator     string = "!=" // 不等于操作符
	UpperOperator      string = ">"  // 大于操作符
	UpperEqualOperator string = ">=" // 大于等于操作符
	LowerOperator      string = "<"  // 小于操作符
	LowerEqualOperator string = "<=" // 小于等于操作符
	RegexpOperator     string = "=~" // 正则匹配操作符
	NRegexpOperator    string = "!~" // 正则不匹配操作符
)

// ValueType 值类型定义，决定了 where 语句的渲染格式
type ValueType int

const (
	StringType ValueType = 0 // 字符串类型，使用单引号包裹
	NumType    ValueType = 1 // 数值类型，直接使用
	RegexpType ValueType = 2 // 正则表达式类型，使用斜杠包裹
	TextType   ValueType = 3 // 文本类型，直接追加（用于特殊处理）
)

// 逻辑操作符常量
const (
	AndOperator string = "and" // 逻辑与操作符
	OrOperator  string = "or"  // 逻辑或操作符
)

// QueryTime 表示一个查询时间范围
type QueryTime struct {
	Start int64 // 开始时间戳（Unix 时间戳，秒）
	End   int64 // 结束时间戳（Unix 时间戳，秒）
}

// WhereList 表示一个 WHERE 条件列表
// 用于构建复杂的查询条件，支持多个条件通过逻辑操作符连接
type WhereList struct {
	whereList   []*Where // WHERE 条件列表
	logicalList []string // 逻辑操作符列表（and/or），用于连接相邻的条件
}

// NewWhereList 创建一个新的 WHERE 条件列表
// 返回: 新创建的 WhereList 指针
func NewWhereList() *WhereList {
	return &WhereList{
		whereList:   make([]*Where, 0),
		logicalList: make([]string, 0),
	}
}

// Append 向 WHERE 条件列表追加一个新的条件
// 参数:
//   - logicalOperator: 逻辑操作符（"and" 或 "or"），用于连接新条件和已有条件
//   - where: 要追加的 WHERE 条件对象
func (l *WhereList) Append(logicalOperator string, where *Where) {
	l.logicalList = append(l.logicalList, logicalOperator)
	l.whereList = append(l.whereList, where)
}

// String 将 WHERE 条件列表转换为字符串表示
// 返回: 格式化的 WHERE 条件字符串，例如: "field1 = 'value1' and field2 != 'value2'"
func (l *WhereList) String() string {
	b := new(strings.Builder)
	for index, where := range l.whereList {
		if index != 0 {
			b.WriteString(" " + l.logicalList[index-1] + " ")
		}
		b.WriteString(where.String())
	}
	return b.String()
}

// Check 判断条件列表中是否包含指定 tag 的值
// 参数:
//   - tagName: 要检查的 tag 名称，例如 "bk_biz_id"
//   - tagValue: tag 的可能值列表，例如 ["1", "2"]
//
// 返回: 如果条件列表中存在 tagName = tagValue 中任意一个值的条件，则返回 true
// 示例: 如果条件列表包含 "bk_biz_id = 1" 或 "bk_biz_id = 2"，且 tagValue 为 ["1", "2"]，则返回 true
func (l *WhereList) Check(tagName string, tagValue []string) bool {
	tagMap := make(map[string]any)
	for _, v := range tagValue {
		tagMap[v] = nil
	}
	for _, w := range l.whereList {
		if w.Name == tagName && w.ValueType == StringType && w.Operator == EqualOperator {
			if _, ok := tagMap[w.Value]; ok {
				return true
			}
		}
	}
	return false
}

// Where 表示一个 WHERE 条件
type Where struct {
	Name      string    // 字段名称
	Value     string    // 字段值
	Operator  string    // 操作符（=, !=, >, <, =~, !~ 等）
	ValueType ValueType // 值类型，决定如何格式化输出
}

// String 将 WHERE 条件转换为字符串表示
// 根据 ValueType 的不同，采用不同的格式化方式:
//   - NumType: 直接输出，例如 "time >= 1000"
//   - RegexpType: 使用斜杠包裹，并对斜杠进行转义，例如 "field =~ /test\/path/"
//   - TextType: 直接输出值（用于特殊处理）
//   - StringType: 使用单引号包裹，例如 "field = 'value'"
//
// 返回: 格式化的 WHERE 条件字符串
func (w *Where) String() string {
	switch w.ValueType {
	case NumType:
		return fmt.Sprintf("%s %s %s", influxql.QuoteIdent(w.Name), w.Operator, w.Value)
	case RegexpType:
		// influxdb 中以 "/" 为分隔符，所以这里将正则中的 "/" 做个简单的转义 "\/"
		return fmt.Sprintf("%s %s /%s/", influxql.QuoteIdent(w.Name), w.Operator, strings.ReplaceAll(w.Value, "/", "\\/"))
	case TextType:
		// 直接将长文本追加，作为一种特殊处理逻辑，influxQL 需要转义反斜杠
		return w.Value
	default:
		// 默认为字符串类型
		return fmt.Sprintf("%s %s '%s'", influxql.QuoteIdent(w.Name), w.Operator, w.Value)
	}
}

// NewWhere 创建一个新的 WHERE 条件对象
// 参数:
//   - name: 字段名称
//   - value: 字段值
//   - operator: 操作符
//   - valueType: 值类型
//
// 返回: 新创建的 Where 对象指针
func NewWhere(name string, value string, operator string, valueType ValueType) *Where {
	return &Where{
		Name:      name,
		Value:     value,
		Operator:  operator,
		ValueType: valueType,
	}
}

// NewTextWhere 创建一个文本类型的 WHERE 条件对象
// 用于特殊场景，直接将文本值作为条件（不包含字段名和操作符）
// 参数:
//   - value: 文本值
//
// 返回: 新创建的 Where 对象指针，ValueType 为 TextType
func NewTextWhere(value string) *Where {
	return &Where{
		Name:      "",
		Value:     value,
		Operator:  "",
		ValueType: TextType,
	}
}

// SegmentedOpt 分段查询选项配置
// 用于将大时间范围的查询拆分为多个小段，通过并发提高查询速度
type SegmentedOpt struct {
	Enable      bool   // 是否启用分段查询
	MinInterval string // 最小时间间隔（duration 字符串，如 "1m"）
	MaxRoutines int    // 最大并发协程数

	Start    int64 // 查询开始时间戳（Unix 时间戳，秒）
	End      int64 // 查询结束时间戳（Unix 时间戳，秒）
	Interval int64 // 每段的时间间隔（毫秒）
}

// GetSegmented 分段查询，通过时间拆分多段，使用并发提高查询速度
// 参数:
//   - opt: 分段查询选项配置
//
// 返回: 查询时间范围列表，每个元素代表一个查询段
// 注意: 如果 Enable 为 false 或配置无效，则返回包含整个时间范围的单个段
func GetSegmented(opt SegmentedOpt) []QueryTime {
	var (
		queryTimes       []QueryTime
		defaultQueryTime = []QueryTime{
			{
				Start: opt.Start,
				End:   opt.End,
			},
		}
	)

	if !opt.Enable {
		return defaultQueryTime
	}

	minInterval, err := time.ParseDuration(opt.MinInterval)
	if err != nil {
		return defaultQueryTime
	}
	interval := opt.Interval
	if interval < minInterval.Milliseconds() {
		interval = minInterval.Milliseconds()
	}
	left := opt.End - opt.Start
	add := 1
	routinesNum := int(math.Floor(float64(left) / float64(interval)))
	if routinesNum > opt.MaxRoutines {
		add = int(math.Ceil(float64(routinesNum) / float64(opt.MaxRoutines)))
	}
	if routinesNum < 1 {
		return defaultQueryTime
	}

	var timeList []int64
	for j := 0; j < routinesNum; j += add {
		t := opt.Start + int64(j)*interval
		timeList = append(timeList, t)
	}
	timeList = append(timeList, opt.End)
	for j := 0; j < len(timeList)-1; j++ {
		queryTimes = append(queryTimes, QueryTime{
			Start: timeList[j],
			End:   timeList[j+1],
		})
	}
	return queryTimes
}

// makeExpression 将条件字段转换为表达式字符串
// 参数:
//   - condition: 条件字段对象，包含字段名、值、操作符等信息
//
// 返回: 格式化的条件表达式字符串
// 处理逻辑:
//   - 单值情况: 直接格式化为 "field operator 'value'" 或 "field operator /regexp/"
//   - 多值情况: 使用逻辑操作符连接多个值
//   - 对于等于操作符（=, =~）: 使用 OR 连接，表示匹配任意一个值
//   - 对于不等于操作符（!=, !~）: 使用 AND 连接，表示不匹配所有值
func makeExpression(condition ConditionField) string {
	if len(condition.Value) == 1 {
		if condition.Operator == NRegexpOperator || condition.Operator == RegexpOperator {
			// influxdb 中以 "/" 为分隔符，所以这里将正则中的 "/" 做个简单的转义 "\/"
			return fmt.Sprintf(
				"%s%s/%s/",
				influxql.QuoteIdent(condition.DimensionName), condition.Operator,
				strings.ReplaceAll(condition.Value[0], "/", "\\/"),
			)
		}
		return fmt.Sprintf("%s%s'%s'", influxql.QuoteIdent(condition.DimensionName), condition.Operator, condition.Value[0])
	}

	// value多值的情况，目前只限于==和!=的场景
	text := ""
	logical := OrOperator
	// 如果是不等于，则要用and连接
	if condition.Operator == NEqualOperator || condition.Operator == NRegexpOperator {
		logical = AndOperator
	}

	for index, value := range condition.Value {
		var item string

		if condition.Operator == NRegexpOperator || condition.Operator == RegexpOperator {
			item = fmt.Sprintf(
				"%s%s/%s/",
				influxql.QuoteIdent(condition.DimensionName), condition.Operator,
				strings.ReplaceAll(value, "/", "\\/"),
			)
		} else {
			item = fmt.Sprintf("%s%s'%s'", influxql.QuoteIdent(condition.DimensionName), condition.Operator, value)
		}

		if index == 0 {
			text = item
			continue
		}
		text = fmt.Sprintf(
			"(%s %s %s)",
			text, logical, item,
		)
	}
	return text
}

// MakeAndConditions 将多个条件字段拼接为 AND 连接的表达式
// 参数:
//   - row: 条件字段列表
//
// 返回: 格式化的条件表达式字符串，多个条件使用 AND 连接
// 示例: 输入 [field1='v1', field2='v2'] 返回 "(field1='v1' and field2='v2')"
// 注意: 如果只有一个条件，直接返回该条件的表达式，不添加括号
func MakeAndConditions(row []ConditionField) string {
	// 如果只有一个条件，直接将这个条件本身返回
	if len(row) == 1 {
		return makeExpression(row[0])
	}

	left := makeExpression(row[0])
	operator := "and"
	right := MakeAndConditions(row[1:])

	return fmt.Sprintf("(%s %s %s)", left, operator, right)
}

// MakeOrExpression 将多行条件字段拼接为 OR 连接的表达式
// 参数:
//   - row: 条件字段行的列表，每行包含多个条件字段
//
// 返回: 格式化的条件表达式字符串，多行之间使用 OR 连接，每行内部使用 AND 连接
// 示例: 输入 [[field1='v1'], [field2='v2']] 返回 "(field1='v1') or (field2='v2')"
// 注意: 如果只有一行，直接返回该行的 AND 表达式
func MakeOrExpression(row [][]ConditionField) string {
	// 如果只有一个条件，直接将这个条件本身返回
	if len(row) == 1 {
		return MakeAndConditions(row[0])
	}

	left := MakeAndConditions(row[0])
	operator := "or"
	right := MakeOrExpression(row[1:])

	return fmt.Sprintf("(%s %s %s)", left, operator, right)
}
