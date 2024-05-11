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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	BKAPM = "bkapm"
	BKLOG = "bklog"
	BKES  = "bkes"

	Timestamp  = "dtEventTimeStamp"
	TimeFormat = "epoch_millis"

	Type       = "type"
	Properties = "properties"

	OldStep = "."
	NewStep = "___"

	Min   = "min"
	Max   = "max"
	Sum   = "sum"
	Count = "count"
	Last  = "last"
	Mean  = "mean"
	Avg   = "avg"

	Percentiles = "percentiles"

	MinOT   = "min_over_time"
	MaxOT   = "max_over_time"
	SumOT   = "sum_over_time"
	CountOT = "count_over_time"
	LastOT  = "last_over_time"
	AvgOT   = "avg_over_time"

	TypeNested        = "nested"
	TypeTerms         = "terms"
	TypeDateHistogram = "date_histogram"
	TypeValue         = "value"
	TypePercentiles   = "percentiles"
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

type aggInfo struct {
	name     string
	typeName string
	args     []any
	kwArgs   map[string]any
}

type aggInfoList []aggInfo

type FormatFactory struct {
	ctx context.Context

	valueKey string

	mapping map[string]string
	data    map[string]any

	aggInfoList aggInfoList
}

func NewFormatFactory(ctx context.Context, valueKey string, mapping map[string]any) *FormatFactory {
	f := &FormatFactory{
		ctx: ctx,

		valueKey:    valueKey,
		mapping:     make(map[string]string),
		aggInfoList: make(aggInfoList, 0),
	}

	mapProperties("", mapping, f.mapping)
	return f
}

func (f *FormatFactory) appendAgg(name, typeName string, args ...any) {
	f.aggInfoList = append(
		f.aggInfoList, aggInfo{
			name: name, typeName: typeName, args: args,
		},
	)
}

func (f *FormatFactory) nestedAgg(key string) {
	lbs := strings.Split(key, OldStep)

	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], OldStep)
		if v, ok := f.mapping[checkKey]; ok {
			if v == TypeNested {
				f.appendAgg(checkKey, TypeNested)
			}
		}
	}

	return
}

func (f *FormatFactory) AggDataFormat(data elastic.Aggregations, isNotPromQL bool) (map[string]*prompb.TimeSeries, error) {
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

func (f *FormatFactory) Agg(size int) (name string, agg elastic.Aggregation, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(f.ctx, fmt.Sprintf("get mapping error: %s", r))
		}
	}()

	for _, info := range f.aggInfoList {
		switch info.typeName {
		case TypeValue:
			// 增加聚合函数
			switch info.name {
			case Min:
				agg = elastic.NewMinAggregation().Field(f.valueKey)
			case Max:
				agg = elastic.NewMaxAggregation().Field(f.valueKey)
			case Avg:
				agg = elastic.NewAvgAggregation().Field(f.valueKey)
			case Sum:
				agg = elastic.NewSumAggregation().Field(f.valueKey)
			case Count:
				agg = elastic.NewValueCountAggregation().Field("_index")
			case Percentiles:
				percents := make([]float64, 0)
				for _, arg := range info.args {
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
			default:
				err = fmt.Errorf("aggregation is not support this name %s, with %+v", info.name, info)
				return
			}
		case TypeDateHistogram:
			if len(info.args) != 2 {
				err = fmt.Errorf("type %s is error with args %+v", info.typeName, info.args)
				return
			}
			window := info.args[0].(string)
			timezone := info.args[1].(string)

			agg = elastic.NewDateHistogramAggregation().
				Field(Timestamp).FixedInterval(window).TimeZone(timezone).
				MinDocCount(1).SubAggregation(name, agg)
		case TypeNested:
			agg = elastic.NewNestedAggregation().Path(info.name).SubAggregation(name, agg)
		case TypeTerms:
			agg = elastic.NewTermsAggregation().Field(info.name).SubAggregation(name, agg).Size(size)
		default:
			err = fmt.Errorf("aggregation is not support, with type %s, info: %+v", info.typeName, info)
			return
		}
		name = info.name
	}

	return
}

func (f *FormatFactory) EsAgg(aggregateMethodList metadata.AggregateMethodList, size int) (string, elastic.Aggregation, error) {
	if len(aggregateMethodList) == 0 {
		err := errors.New("aggregate_method_list is empty")
		return "", nil, err
	}

	aggregateMethod := aggregateMethodList[0]
	f.appendAgg(aggregateMethod.Name, TypeValue, aggregateMethod.Args...)
	f.nestedAgg(aggregateMethod.Name)

	for _, dim := range aggregateMethod.Dimensions {
		dim = f.toEs(dim)
		f.appendAgg(dim, TypeTerms)
		f.nestedAgg(dim)
	}
	return f.Agg(size)
}

func (f *FormatFactory) PromAgg(timeAggregation *metadata.TimeAggregation, aggregateMethodList metadata.AggregateMethodList, timeZone string, size int) (string, elastic.Aggregation, error) {
	if len(aggregateMethodList) == 0 || timeAggregation == nil {
		err := errors.New("aggregateMethodList or timeAggregation is empty")
		return "", nil, err
	}

	// 如果使用了时间函数需要进行转换, sum(count_over_time) => count
	if !aggregateMethodList[0].Without && timeAggregation.WindowDuration > 0 {
		key := aggregateMethodList[0].Name + NewStep + timeAggregation.Function
		if name, ok := AggregationMap[key]; ok {
			f.appendAgg(name, TypeValue)

			// 判断是否是 nested
			f.nestedAgg(f.valueKey)

			// 增加时间函数
			f.appendAgg(Timestamp, TypeDateHistogram, shortDur(timeAggregation.WindowDuration), timeZone)

			// 增加维度聚合函数
			for _, dim := range aggregateMethodList[0].Dimensions {
				dim = f.toEs(dim)
				f.appendAgg(dim, TypeTerms)
				f.nestedAgg(dim)
			}
		} else {
			err := fmt.Errorf("aggregation is not support with: %s", key)
			return "", nil, err
		}
	} else {
		err := fmt.Errorf("aggregation is not supoort without or window is zero: %+v", aggregateMethodList)
		return "", nil, err
	}

	return f.Agg(size)
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
