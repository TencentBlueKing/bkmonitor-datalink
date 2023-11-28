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
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/influxdata/influxql"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
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
	tagMap := make(map[string]interface{})
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

// MakeInfluxdbQuerys: 在结构化解析时，将解析的queryInfo塞进ctx中
// 方法会从ctx中拿出queryInfo，并解析出influxql，和db信息
var MakeInfluxdbQuerys = func(
	ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher,
) ([]influxdb.SQLInfo, error) {
	return makeInfluxdbQuery(ctx, hints, matchers...)
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

// makeInfluxdbQuery
func makeInfluxdbQuery(
	ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher,
) ([]influxdb.SQLInfo, error) {
	var (
		referenceName string

		// where列表表示where语句的第一层，所有条件以and连接
		whereList    = NewWhereList()
		start, stop  string
		totalSQL     string
		sqlInfos     []influxdb.SQLInfo
		span         oleltrace.Span
		matchersAttr []string
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "promql-utils-makeInfluxdbQuery")
	if span != nil {
		defer span.End()
	}

	// 1. 遍历获取所有的filter条件
	for _, matcherInfo := range matchers {
		switch matcherInfo.Name {
		// 指标过滤
		case MetricLabelName:
			referenceName = matcherInfo.Value
		// 默认数据过滤
		default:
			// 如果发现默认的filter是为空，则表示之前没有任何过滤，此时是第一个除DB、表以外的第一个过滤条件，
			// 建立一个二元表达式即可
			operator, ok := PromqlOperatorMapping[matcherInfo.Type]
			if !ok {
				return nil, ErrOperatorType
			}

			var operatorType ValueType
			// 根据操作类型，判断where语句的渲染格式
			switch operator {
			case RegexpOperator, NRegexpOperator:
				// 如果发现操作类型为正则，则where语句转换为正则表达式格式
				operatorType = RegexpType
			default:
				// 其他场景都直接判断为字符型条件，不考虑数值型，因为promql里不存在
				operatorType = StringType
			}
			whereList.Append(AndOperator, NewWhere(matcherInfo.Name, matcherInfo.Value, operator, operatorType))
			log.Debugf(ctx,
				fmt.Sprintf("normal label matcher name->[%s] value->[%s]", matcherInfo.Name, matcherInfo.Value),
			)
		}
		matchersAttr = append(matchersAttr, matcherInfo.String())
	}
	trace.InsertStringSliceIntoSpan("prom-label-matchers", matchersAttr, span)
	trace.InsertStringIntoSpan("reference-name", referenceName, span)

	// 先通过context获取查询信息
	queries, err := QueryInfoFromContext(ctx, referenceName)
	if err != nil {
		// 该流程下queryinfo不应为空，为空则报错
		return nil, err
	}

	trace.InsertStringIntoSpan("query-info-is-count", fmt.Sprintf("%v", queries.IsCount), span)

	for i, query := range queries.QueryList {
		var (
			// 随机维度值
			bkTaskValue  = query.TableID
			withGroupBy  bool
			isCountGroup bool
		)

		// 兼容查询
		if bkTaskValue == "" {
			bkTaskValue = fmt.Sprintf("%s%s", query.DB, query.Measurement)
		}

		trace.InsertStringIntoSpan("query-info-clusterID", query.ClusterID, span)
		trace.InsertStringIntoSpan("query-info-db", query.DB, span)
		trace.InsertStringIntoSpan("query-info-measurement", query.Measurement, span)
		trace.InsertStringIntoSpan("query-info-filed", query.Field, span)

		trace.InsertStringIntoSpan("prom-hints-func", hints.Func, span)
		trace.InsertStringIntoSpan("prom-hints-start", fmt.Sprintf("%d", hints.Start), span)
		trace.InsertStringIntoSpan("prom-hints-end", fmt.Sprintf("%d", hints.End), span)
		trace.InsertStringIntoSpan("prom-hints-step", fmt.Sprintf("%d", hints.Step), span)
		trace.InsertStringIntoSpan("prom-hints-range", fmt.Sprintf("%d", hints.Range), span)
		trace.InsertStringSliceIntoSpan("prom-hints-grouping", hints.Grouping, span)

		startStr := time.Unix(hints.Start/1e3, 0).String()
		endStr := time.Unix(hints.End/1e3, 0).String()
		offsetStr := query.OffsetInfo.OffSet.String()

		trace.InsertStringIntoSpan("query-start-str", startStr, span)
		trace.InsertStringIntoSpan("query-end-str", endStr, span)
		trace.InsertStringIntoSpan("query-offset-str", offsetStr, span)

		aggr, grouping, dimensions := getDownSampleFunc(query.AggregateMethodList, hints, queries.IsCount)

		opt := SegmentedOpt{
			Start:    hints.Start,
			End:      hints.End,
			Interval: grouping.Milliseconds(),
		}

		if segmented != nil {
			opt.Enable = segmented.Enable
			opt.MaxRoutines = segmented.MaxRoutines
			opt.MinInterval = segmented.MinInterval
		}

		// tableID 配置覆盖全局配置
		if query.SegmentedEnable {
			opt.Enable = true
		}
		queryTimes := GetSegmented(opt)
		var queryStr []string
		for _, q := range queryTimes {
			queryStr = append(queryStr,
				fmt.Sprintf("%s => %s", time.Unix(0, q.Start*1e6).String(), time.Unix(0, q.End*1e6).String()),
			)
		}

		trace.InsertIntIntoSpan(fmt.Sprintf("segmented-count-%d", i), len(queryTimes), span)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("segmented-%d", i), queryStr, span)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("query-dimensions-%d", i), dimensions, span)

		// 分段查询
		for j, q := range queryTimes {
			var queryTimesWhereList = NewWhereList()
			*queryTimesWhereList = *whereList

			// 增加 query 查询条件
			if query.Condition != "" {
				queryTimesWhereList.Append(AndOperator, NewTextWhere(query.Condition))
			}

			// 获取起止时间，hints 里面返回的 start 和 end 都是毫秒级别，查询 InfluxDB 需要使用纳秒级别
			start = strconv.FormatInt(q.Start*1000000, 10)
			stop = strconv.FormatInt(q.End*1000000, 10)

			queryTimesWhereList.Append(AndOperator, NewWhere("time", start, UpperEqualOperator, NumType))
			queryTimesWhereList.Append(AndOperator, NewWhere("time", stop, LowerOperator, NumType))

			totalSQL, withGroupBy, isCountGroup = generateSQL(
				ctx, query.Field, query.Measurement, query.DB, aggr, queryTimesWhereList, dimensions, grouping,
			)
			trace.InsertStringIntoSpan(fmt.Sprintf("query-sql-%d-%d", i, j), totalSQL, span)

			// sql注入防范
			err = influxdb.CheckSelectSQL(ctx, totalSQL)
			if err != nil {
				trace.InsertStringIntoSpan(fmt.Sprintf("query-error-%d-%d", i, j), err.Error(), span)
				return nil, err
			}

			sqlInfos = append(sqlInfos, influxdb.SQLInfo{
				ClusterID:    query.ClusterID,
				MetricName:   queries.MetricName,
				DB:           query.DB,
				SQL:          totalSQL,
				Limit:        query.OffsetInfo.Limit,
				SLimit:       query.OffsetInfo.SLimit,
				WithGroupBy:  withGroupBy,
				IsCountGroup: isCountGroup,
				BkTaskValue:  bkTaskValue,
			})
		}
	}

	return sqlInfos, nil
}

// selectKey
func selectKey(sqlInfos []influxdb.SQLInfo) string {
	var s string
	for _, info := range sqlInfos {
		s = fmt.Sprintf("%s|%s,%s,%v,%v", s, info.DB, info.SQL, info.WithGroupBy, info.IsCountGroup)
	}
	return s
}

// 这里降低influxdb流量，主要不是根据时间减少点数，而是预先聚合减少series数量
func generateSQL(
	ctx context.Context, field, measurement, db, aggregation string,
	whereList *WhereList, dimensions []string, window time.Duration,
) (string, bool, bool) {

	var (
		groupingStr    string
		isWithGroupBy  bool
		withTag        = ",*::tag"
		aggField       string
		rpName         string
		isCountGroup   bool
		newAggregation string
		span           oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "generate-sql")
	if span != nil {
		defer span.End()
	}

	rpName, field, newAggregation = GetRp(ctx, db, measurement, field, aggregation, window, whereList)
	// 根据RP重新生成measurement
	if rpName != "" {
		measurement = fmt.Sprintf("\"%s\".\"%s\"", rpName, measurement)
	} else {
		measurement = fmt.Sprintf("\"%s\"", measurement)
	}

	// 存在聚合条件，需要增加聚合
	if newAggregation != "" {
		var groupList []string
		isWithGroupBy = true
		isCountGroup = aggregation == COUNT

		// 如果是Last类型，因为Last类型只适用于点数，所以需要聚合所有维度也就是增加group by *
		// 兼容只有 xxx_over_time(a) 的方案，这种场景下 dimensions 为 nil
		if newAggregation == LAST {
			groupList = []string{"*"}
		} else {
			if len(dimensions) > 0 {
				for _, d := range dimensions {
					group := d
					if group != "*" {
						group = fmt.Sprintf(`"%s"`, group)
					}
					groupList = append(groupList, group)
				}
			}
		}

		if window > 0 {
			groupList = append(groupList, "time("+window.String()+")")
		}
		if len(groupList) > 0 {
			groupingStr = " group by " + strings.Join(groupList, ",")
		}
		withTag = ""
		aggField = fmt.Sprintf("%s(\"%s\")", newAggregation, field)
	} else {
		aggField = fmt.Sprintf("\"%s\"", field)
	}

	whereString := ""
	if len(whereList.whereList) > 0 {
		whereString = fmt.Sprintf("where %s", whereList.String())
	}

	return fmt.Sprintf("select %s as %s,time as %s%s from %s %s%s",
		aggField, influxdb.ResultColumnName, influxdb.TimeColumnName, withTag, measurement, whereString, groupingStr,
	), isWithGroupBy, isCountGroup
}

// getDownSampleFunc
func getDownSampleFunc(
	methods []metadata.AggrMethod, hints *storage.SelectHints, isCount bool,
) (string, time.Duration, []string) {
	var (
		dims   []string
		window = time.Duration(hints.Range * 1e6)
		step   = time.Duration(hints.Step * 1e6)

		grouping time.Duration
	)

	// 为了保持数据的精度，如果 step 小于 window 则使用 step 的聚合，否则使用 window
	if step < window {
		grouping = step
	} else {
		grouping = window
	}

	log.Debugf(context.TODO(), "getDownSampleFunc(methods: %+v, hints: %+v)", methods, hints)

	for _, a := range []string{MIN, MAX, COUNT, SUM, AVG} {
		if hints.Func == a && grouping > time.Minute {
			return LAST, grouping, []string{"*"}
		}
	}

	if len(methods) > 0 {
		lastMethod := methods[0]
		// 判断是否是 without
		if lastMethod.Without {
			return "", 0, nil
		}
		dims = lastMethod.Dimensions
		if lastMethod.Name == MAX && hints.Func == MaxOT {
			return MAX, grouping, dims
		}
		if lastMethod.Name == MIN && hints.Func == MinOT {
			return MIN, grouping, dims
		}
		if lastMethod.Name == MEAN && hints.Func == AvgOT {
			return MEAN, grouping, dims
		}
		if lastMethod.Name == AVG && hints.Func == AvgOT {
			return MEAN, grouping, dims
		}
		if lastMethod.Name == SUM && hints.Func == SumOT && isCount {
			return COUNT, grouping, dims
		}
		if lastMethod.Name == SUM && hints.Func == SumOT && !isCount {
			return SUM, grouping, dims
		}
	}

	return "", 0, nil
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
