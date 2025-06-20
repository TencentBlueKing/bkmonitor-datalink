// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	KeyValue   = "_key"
	FieldValue = "_value"
	FieldTime  = "_time"

	DefaultTimeFieldName = "dtEventTimeStamp"
	DefaultTimeFieldType = TimeFieldTypeTime
	DefaultTimeFieldUnit = function.Millisecond

	DefaultReverseAggName = "reverse_nested"

	Type       = "type"
	Properties = "properties"

	Min         = "min"
	Max         = "max"
	Sum         = "sum"
	Count       = "count"
	Avg         = "avg"
	Cardinality = "cardinality"

	DateHistogram = "date_histogram"
	Percentiles   = "percentiles"

	Nested = "nested"
	Terms  = "terms"

	ESStep = "."
)

const (
	KeyDocID = "__doc_id"
	KeySort  = "sort"

	KeyIndex   = "__index"
	KeyTableID = "__result_table"
	KeyAddress = "__address"

	KeyDataLabel = "__data_label"
)

const (
	KeyWord = "keyword"
	Text    = "text"
	Integer = "integer"
	Long    = "long"
	Date    = "date"
)

const (
	TimeFieldTypeTime = "date"
	TimeFieldTypeInt  = "long"
)

const (
	EpochSecond       = "epoch_second"
	EpochMillis       = "epoch_millis"
	EpochMicroseconds = "epoch_microseconds"
	EpochNanoseconds  = "epoch_nanoseconds"
)

const (
	Must      = "must"
	MustNot   = "must_not"
	Should    = "should"
	ShouldNot = "should_not"
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
	Error         error
}

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + ESStep + k
		}
		switch v.(type) {
		case map[string]any:
			mapData(k, v.(map[string]any), res)
		default:
			res[k] = v
		}
	}
}

func mapProperties(prefix string, data map[string]any, res map[string]string) {
	if prefix != "" {
		if t, ok := data[Type]; ok {
			switch ts := t.(type) {
			case string:
				res[prefix] = ts
			}
		}
	}

	if properties, ok := data[Properties]; ok {
		for k, v := range properties.(map[string]any) {
			if prefix != "" {
				k = prefix + ESStep + k
			}
			switch v.(type) {
			case map[string]any:
				mapProperties(k, v.(map[string]any), res)
			}
		}
	}
}

type ValueAgg struct {
	FieldName string
	Name      string
	FuncType  string

	Args   []any
	KwArgs map[string]any
}

type TimeAgg struct {
	Name     string
	Window   time.Duration
	Timezone string
}

type TermAgg struct {
	Name   string
	Orders metadata.Orders
}

type ReverNested struct {
	Name string
}

type NestedAgg struct {
	Name string
}

type aggInfoList []any

type FormatFactory struct {
	ctx context.Context

	valueField string
	timeField  metadata.TimeField

	decode func(k string) string
	encode func(k string) string

	mapping map[string]string
	data    map[string]any

	aggInfoList aggInfoList
	orders      metadata.Orders

	size     int
	timezone string

	start      time.Time
	end        time.Time
	timeFormat string

	isReference bool
}

func NewFormatFactory(ctx context.Context) *FormatFactory {
	f := &FormatFactory{
		ctx:         ctx,
		mapping:     make(map[string]string),
		aggInfoList: make(aggInfoList, 0),

		// default encode / decode
		encode: func(k string) string {
			return k
		},
		decode: func(k string) string {
			return k
		},
	}

	return f
}

func (f *FormatFactory) WithIsReference(isReference bool) *FormatFactory {
	f.isReference = isReference
	return f
}

func (f *FormatFactory) toFixInterval(window time.Duration) (string, error) {
	switch f.timeField.Unit {
	case function.Second:
		window /= 1e3
	case function.Microsecond:
		window *= 1e3
	case function.Nanosecond:
		window *= 1e6
	}

	if window.Milliseconds() < 1 {
		return "", fmt.Errorf("date histogram aggregation interval must be greater than 0ms")
	}
	return shortDur(window), nil
}

func (f *FormatFactory) toMillisecond(i int64) int64 {
	switch f.timeField.Unit {
	case function.Second:
		return i * 1e3
	case function.Microsecond:
		return i / 1e3
	case function.Nanosecond:
		return i / 1e6
	default:
		// 默认用毫秒
		return i
	}
}

func (f *FormatFactory) timeFormatToEpoch(unit string) string {
	switch unit {
	case function.Millisecond:
		return EpochMillis
	case function.Microsecond:
		return EpochMicroseconds
	case function.Nanosecond:
		return EpochNanoseconds
	default:
		// 默认用秒
		return EpochSecond
	}
}

func (f *FormatFactory) queryToUnix(t time.Time, unit string) int64 {
	switch unit {
	case function.Millisecond:
		return t.UnixMilli()
	case function.Microsecond:
		return t.UnixMicro()
	case function.Nanosecond:
		return t.UnixNano()
	default:
		// 默认用秒
		return t.Unix()
	}
}

func (f *FormatFactory) WithQuery(valueKey string, timeField metadata.TimeField, start, end time.Time, timeFormat string, size int) *FormatFactory {
	if timeField.Name == "" {
		timeField.Name = DefaultTimeFieldName
	}
	if timeField.Type == "" {
		timeField.Type = DefaultTimeFieldType
	}
	if timeField.Unit == "" {
		timeField.Unit = DefaultTimeFieldUnit
	}
	if timeFormat == "" {
		timeFormat = function.Second
	}

	f.start = start
	f.end = end
	f.timeFormat = timeFormat
	f.valueField = valueKey
	f.timeField = timeField
	f.size = size

	return f
}

func (f *FormatFactory) WithTransform(encode func(string) string, decode func(string) string) *FormatFactory {
	if encode != nil {
		f.encode = encode
	}
	if decode != nil {
		f.decode = decode
		// 如果有 decode valueField 需要重新载入
		f.valueField = decode(f.valueField)
	}
	return f
}

func (f *FormatFactory) WithOrders(orders metadata.Orders) *FormatFactory {
	f.orders = make(metadata.Orders, 0, len(orders))
	for _, order := range orders {
		if f.decode != nil {
			order.Name = f.encode(order.Name)
		}
		f.orders = append(f.orders, order)
	}
	return f
}

// WithMappings 合并 mapping，后面的合并前面的
func (f *FormatFactory) WithMappings(mappings ...map[string]any) *FormatFactory {
	for _, mapping := range mappings {
		mapProperties("", mapping, f.mapping)
	}
	return f
}

func (f *FormatFactory) RangeQuery() (elastic.Query, error) {
	var (
		err error
	)

	fieldName := f.timeField.Name
	fieldType := f.timeField.Type

	var query elastic.Query
	switch fieldType {
	case TimeFieldTypeInt:
		// int 类型，直接按照 tableID 配置的单位转换
		query = elastic.NewRangeQuery(fieldName).
			Gte(f.queryToUnix(f.start, f.timeField.Unit)).
			Lte(f.queryToUnix(f.end, f.timeField.Unit))
	case TimeFieldTypeTime:
		// date 类型，使用 查询的单位转换
		query = elastic.NewRangeQuery(fieldName).
			Gte(f.queryToUnix(f.start, f.timeFormat)).
			Lte(f.queryToUnix(f.end, f.timeFormat)).
			Format(f.timeFormatToEpoch(f.timeFormat))
	default:
		err = fmt.Errorf("time field type is error %s", fieldType)
	}
	return query, err
}

func (f *FormatFactory) timeAgg(name string, window time.Duration, timezone string) {
	f.aggInfoList = append(
		f.aggInfoList, TimeAgg{
			Name: name, Window: window, Timezone: timezone,
		},
	)
	f.nestedAgg(name)
}

func (f *FormatFactory) termAgg(name string, isFirst bool) {
	info := TermAgg{
		Name: name,
	}

	for _, order := range f.orders {
		if name == order.Name {
			order.Name = KeyValue
			info.Orders = append(info.Orders, order)
		} else if isFirst {
			if order.Name == FieldValue {
				info.Orders = append(info.Orders, order)
			}
		}
	}

	f.aggInfoList = append(f.aggInfoList, info)
	f.nestedAgg(name)
}

func (f *FormatFactory) valueAgg(fieldName, name, funcType string, args ...any) {
	f.aggInfoList = append(
		f.aggInfoList, ValueAgg{
			FieldName: fieldName, Name: name, FuncType: funcType, Args: args,
		},
	)
	f.nestedAgg(fieldName)
}

func (f *FormatFactory) NestedField(field string) string {
	lbs := strings.Split(field, ESStep)
	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], ESStep)
		if v, ok := f.mapping[checkKey]; ok {
			if v == Nested {
				return checkKey
			}
		}
	}
	return ""
}

func (f *FormatFactory) nestedAgg(key string) {
	nf := f.NestedField(key)

	var agg any
	if nf != "" {
		agg = NestedAgg{Name: nf}
	} else {
		agg = ReverNested{Name: DefaultReverseAggName}
	}

	f.aggInfoList = append(f.aggInfoList, agg)
	return
}

// AggDataFormat 解析 es 的聚合计算
func (f *FormatFactory) AggDataFormat(data elastic.Aggregations, metricLabel *prompb.Label) (*prompb.QueryResult, error) {
	if data == nil {
		return &prompb.QueryResult{
			Timeseries: []*prompb.TimeSeries{},
		}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			log.Errorf(f.ctx, fmt.Sprintf("agg data format %v", r))
		}
	}()

	af := &aggFormat{
		aggInfoList:    f.aggInfoList,
		items:          make(items, 0),
		promDataFormat: f.encode,
		timeFormat:     f.toMillisecond,
	}

	af.get()
	defer af.put()

	err := af.ts(len(f.aggInfoList), data)
	if err != nil {
		return nil, err
	}

	timeSeriesMap := make(map[string]*prompb.TimeSeries)
	keySort := make([]string, 0)

	for _, im := range af.items {
		var (
			tsLabels []prompb.Label
		)
		if len(im.labels) > 0 {
			for _, dim := range af.dims {
				tsLabels = append(tsLabels, prompb.Label{
					Name:  dim,
					Value: im.labels[dim],
				})
			}
		}

		if metricLabel != nil {
			tsLabels = append(tsLabels, *metricLabel)
		}

		var seriesNameBuilder strings.Builder
		for _, l := range tsLabels {
			seriesNameBuilder.WriteString(l.String())
		}

		seriesKey := seriesNameBuilder.String()
		if _, ok := timeSeriesMap[seriesKey]; !ok {
			keySort = append(keySort, seriesKey)
			timeSeriesMap[seriesKey] = &prompb.TimeSeries{
				Samples: make([]prompb.Sample, 0),
			}
		}

		if im.timestamp == 0 {
			im.timestamp = f.start.UnixMilli()
		}

		timeSeriesMap[seriesKey].Labels = tsLabels
		timeSeriesMap[seriesKey].Samples = append(timeSeriesMap[seriesKey].Samples, prompb.Sample{
			Value:     im.value,
			Timestamp: im.timestamp,
		})
	}

	tss := make([]*prompb.TimeSeries, 0, len(timeSeriesMap))
	for _, key := range keySort {
		if ts, ok := timeSeriesMap[key]; ok {
			tss = append(tss, ts)
		}
	}

	return &prompb.QueryResult{Timeseries: tss}, nil
}

func (f *FormatFactory) SetData(data map[string]any) {
	f.data = map[string]any{}
	mapData("", data, f.data)
}

// isNestedPath 检查 path 是否是嵌套路径
// - 用于对外暴露操作nestedPathSet的方法
// - 如果是嵌套路径，则返回 true，并且不添加到 nestedPathSet 中
// - 如果不是嵌套路径，则添加到 nestedPathSet 中，并返回 false
func (f *FormatFactory) isNestedPath(path string, nestedPathSet *set.Set[string]) bool {
	for _, s := range nestedPathSet.ToArray() {
		if path == s {
			return true
		}

		if strings.Contains(fmt.Sprintf("%s.", s), path) {
			return true
		}
		nestedPathSet.Add(s)
	}

	return false
}

// resetAggInfoListWithNested 反向过滤aggInfoList
// - 如果是 NestedAgg，则检查是否是嵌套路径，如果是，则跳过
// - 如果是 ReverNested, 则检查外围是否有 NestedAgg，如果没有，则跳过
func (f *FormatFactory) resetAggInfoListWithNested() {
	// 因为我们在生成aggInfoList的时候是从整个ES查询DSL的最内层开始的(如果要AggSubAggregation要保证子聚合在父聚合之前)，
	// 而且在这个过程我们是没有关心nestedPath的(如果当前操作的字段是嵌套的会直接添加一个NestedAgg否则直接添加ReverNested)，
	// 所以在这里需要反向过滤掉那些不需要的NestedAgg和ReverNested
	var (
		newAggInfoList []any
		nestedPathSet  = set.New[string]()
	)

	for i := len(f.aggInfoList) - 1; i >= 0; i-- {
		aggInfo := f.aggInfoList[i]
		switch info := aggInfo.(type) {
		case NestedAgg:
			if f.isNestedPath(info.Name, nestedPathSet) {
				continue
			}
			nestedPathSet.Add(info.Name)
		case ReverNested:
			if nestedPathSet.Size() == 0 {
				continue
			}
			nestedPathSet.Clean()
		}
		newAggInfoList = append([]any{aggInfo}, newAggInfoList...)
	}
	f.aggInfoList = newAggInfoList

	return
}

func (f *FormatFactory) Agg(ctx context.Context) (name string, agg elastic.Aggregation, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(f.ctx, fmt.Sprintf("get mapping error: %s", r))
		}
	}()

	for _, aggInfo := range f.aggInfoList {
		switch info := aggInfo.(type) {
		case ValueAgg:
			switch info.FuncType {
			case Min:
				curName := FieldValue
				curAgg := elastic.NewMinAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Max:
				curName := FieldValue
				curAgg := elastic.NewMaxAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Avg:
				curName := FieldValue
				curAgg := elastic.NewAvgAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Sum:
				curName := FieldValue
				curAgg := elastic.NewSumAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Count:
				curName := FieldValue
				curAgg := elastic.NewValueCountAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Cardinality:
				curName := FieldValue
				curAgg := elastic.NewCardinalityAggregation().Field(info.FieldName)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Percentiles:
				percents := make([]float64, 0)
				for _, arg := range info.Args {
					var percent float64
					switch v := arg.(type) {
					case float64:
						percent = float64(int(v))
					case int:
						percent = float64(v)
					case int32:
						percent = float64(v)
					case int64:
						percent = float64(v)
					default:
						err = fmt.Errorf("percent type is error: %T, %+v", v, v)
					}
					percents = append(percents, percent)
				}

				curAgg := elastic.NewPercentilesAggregation().Field(info.FieldName).Percentiles(percents...)
				curName := FieldValue
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			default:
				err = fmt.Errorf("valueagg aggregation is not support this type %s, info: %+v", info.FuncType, info)
				return
			}
		case ReverNested:
			curName := info.Name
			curAgg := elastic.NewReverseNestedAggregation()
			if agg != nil {
				curAgg = curAgg.SubAggregation(name, agg)
			}
			agg = curAgg
			name = curName
		case TimeAgg:
			curName := info.Name

			var interval string

			if f.timeField.Type == TimeFieldTypeInt {
				interval, err = f.toFixInterval(info.Window)
				if err != nil {
					return
				}
			} else {
				interval = shortDur(info.Window)
			}

			curAgg := elastic.NewDateHistogramAggregation().
				Field(f.timeField.Name).Interval(interval).MinDocCount(0).
				ExtendedBounds(f.timeFieldUnix(f.start), f.timeFieldUnix(f.end))

			// https://github.com/elastic/elasticsearch/issues/42270 非date类型不支持timezone, time format也无效
			if f.timeField.Type == TimeFieldTypeTime && function.IsAlignTime(info.Window) {
				curAgg = curAgg.TimeZone(info.Timezone)
			}
			if agg != nil {
				curAgg = curAgg.SubAggregation(name, agg)
			}

			agg = curAgg
			name = curName
		case NestedAgg:
			agg = elastic.NewNestedAggregation().Path(info.Name).SubAggregation(name, agg)
			name = info.Name
		case TermAgg:
			curName := info.Name
			curAgg := elastic.NewTermsAggregation().Field(info.Name)
			fieldType, ok := f.mapping[info.Name]
			if !ok || fieldType == Text || fieldType == KeyWord {
				curAgg = curAgg.Missing(" ")
			}

			if f.size > 0 {
				curAgg = curAgg.Size(f.size)
			}

			lmf, exist := function.LabelMapFactory(ctx)
			if exist {
				fieldValues, valuesExist := lmf.FetchIncludeFieldValues(info.Name)
				if valuesExist && len(fieldValues) > 0 {
					structValues := make([]any, 0, len(fieldValues))
					for _, v := range fieldValues {
						if v == "" {
							continue
						}
						if f.decode != nil {
							v = f.decode(v)
						}
						structValues = append(structValues, v)
					}
					curAgg = curAgg.IncludeValues(structValues...)
				}
			}

			for _, order := range info.Orders {
				curAgg = curAgg.Order(order.Name, order.Ast)
			}
			if agg != nil {
				curAgg = curAgg.SubAggregation(name, agg)
			}

			agg = curAgg
			name = curName
		default:
			err = fmt.Errorf("aggInfoList aggregation is not support this type %T, info: %+v", info, info)
			return
		}
	}

	return
}

func (f *FormatFactory) EsAgg(ctx context.Context, aggregates metadata.Aggregates) (string, elastic.Aggregation, error) {
	if len(aggregates) == 0 {
		err := errors.New("aggregate_method_list is empty")
		return "", nil, err
	}

	for _, am := range aggregates {
		switch am.Name {
		case DateHistogram:
			f.timeAgg(f.timeField.Name, am.Window, am.TimeZone)
		case Max, Min, Avg, Sum, Count, Cardinality, Percentiles:
			f.valueAgg(f.valueField, FieldValue, am.Name, am.Args...)

			if am.Window > 0 && !am.Without {
				// 增加时间函数
				f.timeAgg(f.timeField.Name, am.Window, am.TimeZone)
			}

			for idx, dim := range am.Dimensions {
				if dim == "" || dim == labels.MetricName {
					continue
				}
				if f.decode != nil {
					dim = f.decode(dim)
				}

				f.termAgg(dim, idx == 0)
			}
		default:
			err := fmt.Errorf("esAgg aggregation is not support with: %+v", am)
			return "", nil, err
		}
	}

	f.resetAggInfoListWithNested()
	return f.Agg(ctx)
}

func (f *FormatFactory) Orders() metadata.Orders {
	orders := make(metadata.Orders, 0, len(f.orders))
	for _, order := range f.orders {
		if order.Name == FieldValue {
			order.Name = f.valueField
		} else if order.Name == FieldTime {
			order.Name = f.timeField.Name
		}

		if _, ok := f.mapping[order.Name]; ok {
			orders = append(orders, order)
		}
	}
	return orders
}

func (f *FormatFactory) timeFieldUnix(t time.Time) (u int64) {
	switch f.timeField.Unit {
	case function.Millisecond:
		u = t.UnixMilli()
	case function.Microsecond:
		u = t.UnixMicro()
	case function.Nanosecond:
		u = t.UnixNano()
	default:
		u = t.Unix()
	}

	return
}

func (f *FormatFactory) getQuery(key string, qs ...elastic.Query) (q elastic.Query) {
	if len(qs) == 0 {
		return q
	}

	switch key {
	case Must:
		if len(qs) == 1 {
			q = qs[0]
		} else {
			q = elastic.NewBoolQuery().Must(qs...)
		}
	case Should:
		if len(qs) == 1 {
			q = qs[0]
		} else {
			q = elastic.NewBoolQuery().Should(qs...)
		}
	case MustNot:
		q = elastic.NewBoolQuery().MustNot(qs...)
	}
	return q
}

// Query 把 ts 的 conditions 转换成 es 查询
func (f *FormatFactory) Query(allConditions metadata.AllConditions) (elastic.Query, error) {
	bootQueries := make([]elastic.Query, 0)
	orQuery := make([]elastic.Query, 0, len(allConditions))

	for _, conditions := range allConditions {
		// Track nested fields separately for each condition group
		nestedFields := set.New[string]()
		nestedQueries := make(map[string][]elastic.Query)
		nonNestedQueries := make([]elastic.Query, 0)

		// First pass: process all conditions and separate nested from non-nested
		for _, con := range conditions {
			key := con.DimensionName
			if f.decode != nil {
				key = f.decode(key)
			}

			// Check if this dimension is in a nested field
			nestedPath := f.NestedField(con.DimensionName)

			var (
				q elastic.Query

				isNestedBefore = false
			)

			err := func() error {
				switch con.Operator {
				case structured.ConditionExisted:
					q = elastic.NewExistsQuery(key)
				case structured.ConditionNotExisted:
					q = elastic.NewExistsQuery(key)
					if nestedPath != "" {
						q = elastic.NewNestedQuery(nestedPath, q)
						isNestedBefore = true
					}
					q = f.getQuery(MustNot, q)
				default:
					// 根据字段类型，判断是否使用 isExistsQuery 方法判断非空
					fieldType, ok := f.mapping[key]
					isExistsQuery := true
					if ok {
						if fieldType == Text || fieldType == KeyWord {
							isExistsQuery = false
						}
					}

					queries := make([]elastic.Query, 0)
					for _, value := range con.Value {
						var query elastic.Query
						if con.DimensionName != "" {
							// 如果是字符串类型，则需要使用 match_phrase 进行非空判断
							if value == "" && isExistsQuery {
								query = elastic.NewExistsQuery(key)
								switch con.Operator {
								case structured.ConditionEqual, structured.Contains:
									if nestedPath != "" {
										isNestedBefore = true
										query = elastic.NewNestedQuery(nestedPath, query)
									}
									q = f.getQuery(MustNot, query)
								case structured.ConditionNotEqual, structured.Ncontains:
									q = query
								default:
									return fmt.Errorf("operator is not support with empty, %+v", con)
								}
								return nil
							} else {
								// 非空才进行验证
								switch con.Operator {
								case structured.ConditionEqual, structured.ConditionNotEqual:
									if con.IsPrefix {
										query = elastic.NewMatchPhrasePrefixQuery(key, value)
									} else {
										query = elastic.NewMatchPhraseQuery(key, value)
									}
								case structured.ConditionContains, structured.ConditionNotContains:
									if fieldType == KeyWord {
										value = fmt.Sprintf("*%s*", value)
									}

									if !con.IsWildcard && fieldType == Text {
										if con.IsPrefix {
											query = elastic.NewMatchPhrasePrefixQuery(key, value)
										} else {
											query = elastic.NewMatchPhraseQuery(key, value)
										}
									} else {
										query = elastic.NewWildcardQuery(key, value)
									}
								case structured.ConditionRegEqual, structured.ConditionNotRegEqual:
									query = elastic.NewRegexpQuery(key, value)
								case structured.ConditionGt:
									query = elastic.NewRangeQuery(key).Gt(value)
								case structured.ConditionGte:
									query = elastic.NewRangeQuery(key).Gte(value)
								case structured.ConditionLt:
									query = elastic.NewRangeQuery(key).Lt(value)
								case structured.ConditionLte:
									query = elastic.NewRangeQuery(key).Lte(value)
								default:
									return fmt.Errorf("operator is not support, %+v", con)
								}
							}
						} else {
							query = elastic.NewQueryStringQuery(value)
						}

						if query != nil {
							queries = append(queries, query)
						}
					}

					// 非空才进行验证
					switch con.Operator {
					case structured.ConditionEqual, structured.ConditionContains, structured.ConditionRegEqual:
						q = f.getQuery(Should, queries...)
					case structured.ConditionNotEqual, structured.ConditionNotContains, structured.ConditionNotRegEqual:
						if nestedPath != "" {
							// 如果是 keyword 或者 text 类型的字段: MustNot -> nested -> field
							innerQuery := f.getQuery(Should, queries...)
							nestedQuery := elastic.NewNestedQuery(nestedPath, innerQuery)
							q = f.getQuery(MustNot, nestedQuery)
							isNestedBefore = true // Mark as already nested to avoid double wrapping
						} else {
							// 非嵌套字段直接使用MustNot
							q = f.getQuery(MustNot, queries...)
						}
					case structured.ConditionGt, structured.ConditionGte, structured.ConditionLt, structured.ConditionLte:
						q = f.getQuery(Must, queries...)
					default:
						return fmt.Errorf("operator is not support, %+v", con)
					}
				}

				return nil
			}()

			if err != nil {
				return nil, err
			}

			// Add to the appropriate query collection
			if q != nil {
				if nestedPath != "" && !isNestedBefore {
					nestedFields.Add(nestedPath)
					nestedQueries[nestedPath] = append(nestedQueries[nestedPath], q)
				} else {
					nonNestedQueries = append(nonNestedQueries, q)
				}
			}
		}

		// Combine nested queries by field
		nestedFieldQueries := make([]elastic.Query, 0, nestedFields.Size())
		nestedFieldsArray := nestedFields.ToArray()

		// 排序输出
		sort.Strings(nestedFieldsArray)

		for _, field := range nestedFieldsArray {
			if queries, ok := nestedQueries[field]; ok && len(queries) > 0 {
				// Create a nested query for this field
				nestedQuery := elastic.NewNestedQuery(field, f.getQuery(Must, queries...))
				nestedFieldQueries = append(nestedFieldQueries, nestedQuery)
			}
		}

		// Combine all queries (nested and non-nested)
		var allQueries []elastic.Query
		allQueries = append(allQueries, nonNestedQueries...)
		allQueries = append(allQueries, nestedFieldQueries...)

		// Add to OR query
		if len(allQueries) > 0 {
			aq := f.getQuery(Must, allQueries...)
			if aq != nil {
				orQuery = append(orQuery, aq)
			}
		}
	}

	oq := f.getQuery(Should, orQuery...)
	if oq != nil {
		bootQueries = append(bootQueries, oq)
	}

	var resQuery elastic.Query
	if len(bootQueries) > 1 {
		resQuery = elastic.NewBoolQuery().Must(bootQueries...)
	} else if len(bootQueries) == 1 {
		resQuery = bootQueries[0]
	}

	return resQuery, nil
}

func (f *FormatFactory) Sample() (prompb.Sample, error) {
	var (
		err error
		ok  bool

		timestamp interface{}
		value     interface{}

		sample = prompb.Sample{}
	)

	// 如果是非 prom 计算场景，则提前退出
	if f.isReference {
		return sample, nil
	}

	if value, ok = f.data[f.valueField]; ok {
		switch value.(type) {
		case float64:
			sample.Value = value.(float64)
		case int64:
			sample.Value = float64(value.(int64))
		case int:
			sample.Value = float64(value.(int))
		case string:
			sample.Value, err = strconv.ParseFloat(value.(string), 64)
			if err != nil {
				return sample, err
			}
		default:
			return sample, fmt.Errorf("value key %s type is error: %T, %v", f.valueField, value, value)
		}
	} else {
		sample.Value = 0
	}

	if timestamp, ok = f.data[f.timeField.Name]; ok {
		switch timestamp.(type) {
		case int64:
			sample.Timestamp = timestamp.(int64)
		case int:
			sample.Timestamp = int64(timestamp.(int))
		case float64:
			sample.Timestamp = int64(timestamp.(float64))
		case string:
			v, parseErr := strconv.ParseInt(timestamp.(string), 10, 64)
			if parseErr != nil {
				return sample, parseErr
			}
			sample.Timestamp = v
		default:
			return sample, fmt.Errorf("timestamp key type is error: %T, %v", timestamp, timestamp)
		}
		sample.Timestamp = f.toMillisecond(sample.Timestamp)
	} else {
		return sample, fmt.Errorf("timestamp is empty %s", f.timeField.Name)
	}

	return sample, nil
}

func (f *FormatFactory) Labels() (lbs *prompb.Labels, err error) {
	lbl := make([]string, 0)
	for k := range f.data {
		// 只有 promEngine 查询的场景需要跳过该字段
		if !f.isReference {
			if k == f.valueField {
				continue
			}
			if k == f.timeField.Name {
				continue
			}
		}

		if f.encode != nil {
			k = f.encode(k)
		}

		lbl = append(lbl, k)
	}

	sort.Strings(lbl)

	lbs = &prompb.Labels{
		Labels: make([]prompb.Label, 0, len(lbl)),
	}

	for _, k := range lbl {
		var value string
		d := f.data[k]

		if d == nil {
			continue
		}

		switch d.(type) {
		case string:
			value = fmt.Sprintf("%s", d)
		case float64, float32:
			value = fmt.Sprintf("%.f", d)
		case int64, int32, int:
			value = fmt.Sprintf("%d", d)
		case []interface{}:
			o, _ := json.Marshal(d)
			value = fmt.Sprintf("%s", o)
		default:
			err = fmt.Errorf("dimensions key type is error: %T, %v", d, d)
			return
		}

		lbs.Labels = append(lbs.Labels, prompb.Label{
			Name:  k,
			Value: value,
		})
	}

	return
}

func (f *FormatFactory) GetTimeField() metadata.TimeField {
	return f.timeField
}
