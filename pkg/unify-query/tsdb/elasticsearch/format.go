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

	"github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/prompb"
	mapping "github.com/zhuliquan/es-mapping"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	BKAPM = "bkapm"
	BKLOG = "bklog"
	BKES  = "bkes"

	KeyValue   = "_key"
	FieldValue = "_value"
	FieldTime  = "_time"

	Timestamp  = "dtEventTimeStamp"
	TimeFormat = "epoch_millis"

	Type       = "type"
	Properties = "properties"

	OldStep = "."
	NewStep = "___"

	Min         = "min"
	Max         = "max"
	Sum         = "sum"
	Count       = "count"
	Last        = "last"
	Mean        = "mean"
	Avg         = "avg"
	Cardinality = "cardinality"

	DateHistogram = "date_histogram"
	Percentiles   = "percentiles"

	MinOT   = "min_over_time"
	MaxOT   = "max_over_time"
	SumOT   = "sum_over_time"
	CountOT = "count_over_time"
	LastOT  = "last_over_time"
	AvgOT   = "avg_over_time"

	Nested = "nested"
	Terms  = "terms"
)

var (
	AggregationMap = map[string]string{
		Min + NewStep + MinOT:   Min,
		Max + NewStep + MaxOT:   Max,
		Sum + NewStep + SumOT:   Sum,
		Avg + NewStep + AvgOT:   Avg,
		Sum + NewStep + CountOT: Count,
	}
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
	Error         error
}

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + NewStep + k
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
			res[prefix] = t.(string)
		}
	}

	if properties, ok := data[Properties]; ok {
		for k, v := range properties.(map[string]any) {
			if prefix != "" {
				k = prefix + OldStep + k
			}
			switch v.(type) {
			case map[string]any:
				mapProperties(k, v.(map[string]any), res)
			}
		}
	}
}

type ValueAgg struct {
	Name     string
	FuncType string

	Args   []any
	KwArgs map[string]any
}

type TimeAgg struct {
	Name     string
	Window   string
	Timezone string
}

type TermAgg struct {
	Name  string
	Order map[string]bool
}

type NestedAgg struct {
	Name string
}

type aggInfoList []any

type FormatFactory struct {
	ctx context.Context

	valueKey string

	propertyMapping *mapping.PropertyMapping
	mapping         map[string]string
	data            map[string]any

	aggInfoList aggInfoList
	orders      metadata.Orders

	from     int
	size     int
	timezone string
}

func NewFormatFactory(ctx context.Context, valueKey string, mapping map[string]any, orders map[string]bool, from, size int, timezone string) *FormatFactory {
	f := &FormatFactory{
		ctx:         ctx,
		mapping:     make(map[string]string),
		aggInfoList: make(aggInfoList, 0),
		orders:      orders,
		from:        from,
		size:        size,
		timezone:    timezone,
	}

	f.valueKey = f.toEs(valueKey)

	mapProperties("", mapping, f.mapping)
	return f
}

func (f *FormatFactory) timeAgg(name string, window, timezone string) {
	f.aggInfoList = append(
		f.aggInfoList, TimeAgg{
			Name: name, Window: window, Timezone: timezone,
		},
	)
}

func (f *FormatFactory) termAgg(name string, isFirst bool) {
	info := TermAgg{
		Name: name,
	}

	info.Order = make(map[string]bool, len(f.orders))
	for key, asc := range f.orders {
		if name == f.toEs(key) {
			info.Order[KeyValue] = asc
		} else if isFirst {
			if key == FieldValue {
				info.Order[FieldValue] = asc
			}
		}
	}
	f.aggInfoList = append(f.aggInfoList, info)
}

func (f *FormatFactory) valueAgg(name, funcType string, args ...any) {
	f.aggInfoList = append(
		f.aggInfoList, ValueAgg{
			Name: name, FuncType: funcType, Args: args,
		},
	)
}

func (f *FormatFactory) nestedAgg(key string) {
	lbs := strings.Split(key, OldStep)

	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], OldStep)
		if v, ok := f.mapping[checkKey]; ok {
			if v == Nested {
				f.aggInfoList = append(
					f.aggInfoList, NestedAgg{
						Name: checkKey,
					},
				)
			}
		}
	}

	return
}

func (f *FormatFactory) AggDataFormat(data elastic.Aggregations, isNotPromQL bool, end int64) (map[string]*prompb.TimeSeries, error) {
	af := &aggFormat{
		aggInfoList: f.aggInfoList,
		toEs:        f.toEs,
		toProm:      f.toProm,
		isNotPromQL: isNotPromQL,
		items:       make(items, 0),
	}

	af.start()
	defer af.close()

	err := af.ts(len(f.aggInfoList), data)
	if err != nil {
		return nil, err
	}

	timeSeriesMap := make(map[string]*prompb.TimeSeries)
	for _, im := range af.items {
		var (
			tsLabels          []prompb.Label
			seriesNameBuilder strings.Builder
		)
		if len(im.labels) > 0 {
			tsLabels = make([]prompb.Label, 0, len(im.labels))
			for _, dim := range af.dims {
				seriesNameBuilder.WriteString(dim)
				seriesNameBuilder.WriteString(im.labels[dim])
				tsLabels = append(tsLabels, prompb.Label{
					Name:  dim,
					Value: im.labels[dim],
				})
			}
		}

		seriesKey := seriesNameBuilder.String()
		if _, ok := timeSeriesMap[seriesKey]; !ok {
			timeSeriesMap[seriesKey] = &prompb.TimeSeries{
				Samples: make([]prompb.Sample, 0),
			}
		}

		// 移除空算的点
		if tsLabels == nil && im.timestamp == 0 && im.value == 0 {
			continue
		}

		if isNotPromQL && im.timestamp == 0 {
			im.timestamp = end
		}

		timeSeriesMap[seriesKey].Labels = tsLabels
		timeSeriesMap[seriesKey].Samples = append(timeSeriesMap[seriesKey].Samples, prompb.Sample{
			Value:     im.value,
			Timestamp: im.timestamp,
		})
	}

	return timeSeriesMap, nil
}

func (f *FormatFactory) toProm(key string) string {
	vs := strings.Split(key, OldStep)
	return strings.Join(vs, NewStep)
}

func (f *FormatFactory) toEs(key string) string {
	vs := strings.Split(key, NewStep)
	return strings.Join(vs, OldStep)
}

func (f *FormatFactory) SetData(data map[string]any) {
	f.data = map[string]any{}
	mapData("", data, f.data)
}

func (f *FormatFactory) Agg() (name string, agg elastic.Aggregation, err error) {
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
				agg = elastic.NewMinAggregation().Field(f.valueKey)
				name = FieldValue
			case Max:
				agg = elastic.NewMaxAggregation().Field(f.valueKey)
				name = FieldValue
			case Avg:
				agg = elastic.NewAvgAggregation().Field(f.valueKey)
				name = FieldValue
			case Sum:
				agg = elastic.NewSumAggregation().Field(f.valueKey)
				name = FieldValue
			case Count:
				agg = elastic.NewValueCountAggregation().Field(f.valueKey)
				name = FieldValue
			case Cardinality:
				agg = elastic.NewCardinalityAggregation().Field(f.valueKey)
				name = FieldValue
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

				agg = elastic.NewPercentilesAggregation().Field(f.valueKey).Percentiles(percents...)
				name = FieldValue
			default:
				err = fmt.Errorf("valueagg aggregation is not support this type %s, info: %+v", info.FuncType, info)
				return
			}
		case TimeAgg:
			agg = elastic.NewDateHistogramAggregation().
				Field(Timestamp).FixedInterval(info.Window).TimeZone(info.Timezone).
				MinDocCount(1).SubAggregation(name, agg)
			name = info.Name
		case NestedAgg:
			agg = elastic.NewNestedAggregation().Path(info.Name).SubAggregation(name, agg)
		case TermAgg:
			termsAgg := elastic.NewTermsAggregation().Field(info.Name).SubAggregation(name, agg).Size(f.size)
			for key, asc := range info.Order {
				termsAgg = termsAgg.Order(key, asc)
			}

			agg = termsAgg
			name = info.Name
		default:
			err = fmt.Errorf("aggInfoList aggregation is not support this type %T, info: %+v", info, info)
			return
		}
	}

	return
}

func (f *FormatFactory) EsAgg(aggregateMethodList metadata.AggregateMethodList) (string, elastic.Aggregation, error) {
	if len(aggregateMethodList) == 0 {
		err := errors.New("aggregate_method_list is empty")
		return "", nil, err
	}

	// todo 遍历聚合函数，转换成 es 可用的函数
	for _, am := range aggregateMethodList {
		switch am.Name {
		case DateHistogram:
			f.timeAgg(Timestamp, shortDur(am.Window), f.timezone)
		case Max, Min, Avg, Sum, Count, Cardinality, Percentiles:
			f.valueAgg(FieldValue, am.Name, am.Args...)
			f.nestedAgg(am.Name)

			for idx, dim := range am.Dimensions {
				dim = f.toEs(dim)
				f.termAgg(dim, idx == 0)
				f.nestedAgg(dim)
			}
		default:
			err := fmt.Errorf("esAgg aggregation is not support with: %+v", am)
			return "", nil, err
		}
	}

	return f.Agg()
}

func (f *FormatFactory) PromAgg(timeAggregation *metadata.TimeAggregation, aggregateMethodList metadata.AggregateMethodList) (string, elastic.Aggregation, error) {
	if len(aggregateMethodList) == 0 || timeAggregation == nil {
		err := errors.New("aggregateMethodList or timeAggregation is empty")
		return "", nil, err
	}

	// 如果使用了时间函数需要进行转换, sum(count_over_time) => count
	if !aggregateMethodList[0].Without && timeAggregation.WindowDuration > 0 {
		key := aggregateMethodList[0].Name + NewStep + timeAggregation.Function
		if name, ok := AggregationMap[key]; ok {
			f.valueAgg(FieldValue, name)

			// 判断是否是 nested
			f.nestedAgg(f.valueKey)

			// 增加时间函数
			f.timeAgg(Timestamp, shortDur(timeAggregation.WindowDuration), f.timezone)

			// 增加维度聚合函数
			for idx, dim := range aggregateMethodList[0].Dimensions {
				dim = f.toEs(dim)
				f.termAgg(dim, idx == 0)
				f.nestedAgg(dim)
			}
		} else {
			err := fmt.Errorf("promAgg aggregation is not support with: %s", key)
			return "", nil, err
		}
	} else {
		err := fmt.Errorf("aggregation is not supoort without or window is zero: %+v", aggregateMethodList)
		return "", nil, err
	}

	return f.Agg()
}

func (f *FormatFactory) Size(ss *elastic.SearchService) *elastic.SearchService {
	ss = ss.From(f.from).Size(f.size)
	return ss
}

func (f *FormatFactory) Order() map[string]bool {
	order := make(map[string]bool)
	for name, asc := range f.orders {
		if name == FieldValue {
			name = f.valueKey
		} else if name == FieldTime {
			name = Timestamp
		}

		if _, ok := f.mapping[name]; ok {
			order[name] = asc
		}
	}
	return order
}

// Query 把 ts 的 conditions 转换成 es 查询
func (f *FormatFactory) Query(queryString string, allConditions metadata.AllConditions) (elastic.Query, error) {
	if len(allConditions) == 0 {
		return nil, nil
	}

	boolQuery := elastic.NewBoolQuery()
	for _, conditions := range allConditions {
		andQuery := elastic.NewBoolQuery()
		for _, con := range conditions {
			q := elastic.NewBoolQuery()
			key := f.toEs(con.DimensionName)

			value := strings.Join(con.Value, ",")
			// 如果为空则，则使用判断是否存在的逻辑
			if value == "" {
				switch con.Operator {
				case structured.ConditionRegEqual, structured.ConditionEqual, structured.Contains:
					q.MustNot(elastic.NewExistsQuery(key))
				case structured.ConditionNotRegEqual, structured.ConditionNotEqual, structured.Ncontains:
					q.Must(elastic.NewExistsQuery(key))
				}
			} else {
				// 非空才进行验证
				switch con.Operator {
				case structured.ConditionEqual, structured.ConditionContains:
					q.Must(elastic.NewMatchQuery(key, value))
				case structured.ConditionNotEqual, structured.ConditionNotContains:
					q.MustNot(elastic.NewMatchQuery(key, value))
				case structured.ConditionRegEqual:
					q.Must(elastic.NewRegexpQuery(key, value))
				case structured.ConditionNotRegEqual:
					q.MustNot(elastic.NewRegexpQuery(key, value))
				case structured.ConditionGt:
					q.Must(elastic.NewRangeQuery(key).Gt(value))
				case structured.ConditionGte:
					q.Must(elastic.NewRangeQuery(key).Gte(value))
				case structured.ConditionLt:
					q.Must(elastic.NewRangeQuery(key).Lt(value))
				case structured.ConditionLte:
					q.Must(elastic.NewRangeQuery(key).Lte(value))
				default:
					return nil, fmt.Errorf("operator is not support, %+v", con)
				}
			}

			andQuery.Must(q)
		}
		boolQuery.Should(andQuery)
	}
	if queryString != "" {

		qs := elastic.NewQueryStringQuery(queryString)
		boolQuery = boolQuery.Must(qs)
	}

	return boolQuery, nil
}

func (f *FormatFactory) Sample() (prompb.Sample, error) {
	var (
		err error
		ok  bool

		timestamp interface{}
		value     interface{}

		sample = prompb.Sample{}
	)
	if value, ok = f.data[f.valueKey]; ok {
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
			return sample, fmt.Errorf("value key %s type is error: %T, %v", f.valueKey, value, value)
		}
	} else {
		sample.Value = 0
	}

	if timestamp, ok = f.data[Timestamp]; ok {
		switch timestamp.(type) {
		case int64:
			sample.Timestamp = timestamp.(int64) * 1e3
		case int:
			sample.Timestamp = int64(timestamp.(int) * 1e3)
		case string:
			sample.Timestamp, err = strconv.ParseInt(timestamp.(string), 10, 64)
		default:
			return sample, fmt.Errorf("timestamp key type is error: %T, %v", timestamp, timestamp)
		}
	} else {
		return sample, fmt.Errorf("timestamp is empty %s", Timestamp)
	}

	return sample, nil
}

func (f *FormatFactory) Labels() (lbs *prompb.Labels, err error) {
	lbl := make([]string, 0)
	for k := range f.data {
		if k == f.valueKey {
			continue
		}
		if k == Timestamp {
			continue
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
		switch d.(type) {
		case string:
			value = fmt.Sprintf("%s", d)
		case float64, float32:
			value = fmt.Sprintf("%.f", d)
		case int64, int32, int:
			value = fmt.Sprintf("%d", d)
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