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

const (
	MIN   = "min"
	MAX   = "max"
	SUM   = "sum"
	COUNT = "count"
	LAST  = "last"
	MEAN  = "mean"
	AVG   = "avg"

	MinOT   = "min_over_time"
	MaxOT   = "max_over_time"
	SumOT   = "sum_over_time"
	CountOT = "count_over_time"
	LastOT  = "last_over_time"
	AvgOT   = "avg_over_time"
)

const (
	StaticMetricName  = "metric_name"
	StaticMetricValue = "metric_value"

	StaticField = "value"
)

// promql内置的几种运算对应的字符串
var PromqlOperatorMapping = map[labels.MatchType]string{
	labels.MatchEqual:     "=",
	labels.MatchNotEqual:  "!=",
	labels.MatchRegexp:    "=~",
	labels.MatchNotRegexp: "!~",
}

// 操作符号映射
type Operator string

const (
	EqualOperator      string = "="
	NEqualOperator     string = "!="
	UpperOperator      string = ">"
	UpperEqualOperator string = ">="
	LowerOperator      string = "<"
	LowerEqualOperator string = "<="
	RegexpOperator     string = "=~"
	NRegexpOperator    string = "!~"
)

// where类型说明，决定了where语句的渲染格式
type ValueType int

const (
	StringType ValueType = 0
	NumType    ValueType = 1
	RegexpType ValueType = 2
	TextType   ValueType = 3
)

const (
	AndOperator string = "and"
	OrOperator  string = "or"
)

// QueryTime
type QueryTime struct {
	Start int64
	End   int64
}

// WhereList
type WhereList struct {
	whereList   []*Where
	logicalList []string
}

// NewWhereList
func NewWhereList() *WhereList {
	return &WhereList{
		whereList:   make([]*Where, 0),
		logicalList: make([]string, 0),
	}
}

// Append
func (l *WhereList) Append(logicalOperator string, where *Where) {
	l.logicalList = append(l.logicalList, logicalOperator)
	l.whereList = append(l.whereList, where)
}

// String
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

// Check 判断条件里是包含tag的值，例如：tagName: bk_biz_id，tagValue：[1, 2]，bk_biz_id = 1 和 bk_biz_id = 2 都符合
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

// Where
type Where struct {
	Name      string
	Value     string
	Operator  string
	ValueType ValueType
}

// String
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

// NewWhere
func NewWhere(name string, value string, operator string, valueType ValueType) *Where {
	return &Where{
		Name:      name,
		Value:     value,
		Operator:  operator,
		ValueType: valueType,
	}
}

// NewTextWhere
func NewTextWhere(value string) *Where {
	return &Where{
		Name:      "",
		Value:     value,
		Operator:  "",
		ValueType: TextType,
	}
}

// SegmentedOpt
type SegmentedOpt struct {
	Enable      bool
	MinInterval string
	MaxRoutines int

	Start    int64
	End      int64
	Interval int64
}

// GetSegmented : 分段查询，通过时间拆分多段，使用并发提高查询速度
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

// makeExpression
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

// MakeAndConditions: 传入多个条件，拼接为对应的表达式
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

// MakeOrExpression
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
