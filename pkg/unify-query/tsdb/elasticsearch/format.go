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
	"encoding/json"
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
	KeyValue   = "_key"
	FieldValue = "_value"
	FieldTime  = "_time"

	Timestamp  = "dtEventTimeStamp"
	TimeFormat = "epoch_millis"

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
)

const (
	KeyWord = "keyword"
	Text    = "text"
	Integer = "integer"
	Long    = "long"
	Date    = "date"
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
	Error         error
}

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + structured.EsNewStep + k
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
				k = prefix + structured.EsOldStep + k
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

	toEs   func(k string) string
	toProm func(k string) string

	mapping map[string]string
	data    map[string]any

	aggInfoList aggInfoList
	orders      metadata.Orders

	from     int
	size     int
	timezone string

	start int64
	end   int64
}

func NewFormatFactory(ctx context.Context) *FormatFactory {
	f := &FormatFactory{
		ctx:         ctx,
		mapping:     make(map[string]string),
		aggInfoList: make(aggInfoList, 0),
	}

	return f
}

func (f *FormatFactory) WithQuery(valueKey string, start, end int64, timezone string, from, size int) *FormatFactory {
	f.valueKey = valueKey
	f.start = start
	f.end = end
	f.timezone = timezone
	f.from = from
	f.size = size
	return f
}

func (f *FormatFactory) WithTransform(toEs, toProm func(string) string) *FormatFactory {
	f.toEs = toEs
	f.toProm = toProm
	return f
}

func (f *FormatFactory) WithOrders(orders map[string]bool) *FormatFactory {
	f.orders = orders
	return f
}

func (f *FormatFactory) WithMapping(mapping map[string]any) *FormatFactory {
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

func (f *FormatFactory) NestedField(field string) string {
	lbs := strings.Split(field, structured.EsOldStep)
	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], structured.EsOldStep)
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
	if nf != "" {
		f.aggInfoList = append(
			f.aggInfoList, NestedAgg{
				Name: nf,
			},
		)
	}

	return
}

func (f *FormatFactory) AggDataFormat(data elastic.Aggregations) (map[string]*prompb.TimeSeries, error) {
	af := &aggFormat{
		aggInfoList: f.aggInfoList,
		items:       make(items, 0),
		toEs:        f.toEs,
		toProm:      f.toProm,
	}

	af.get()
	defer af.put()

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

		if im.timestamp == 0 {
			im.timestamp = f.start
		}

		timeSeriesMap[seriesKey].Labels = tsLabels
		timeSeriesMap[seriesKey].Samples = append(timeSeriesMap[seriesKey].Samples, prompb.Sample{
			Value:     im.value,
			Timestamp: im.timestamp,
		})
	}

	return timeSeriesMap, nil
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
				curName := FieldValue
				curAgg := elastic.NewMinAggregation().Field(f.valueKey)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Max:
				curName := FieldValue
				curAgg := elastic.NewMaxAggregation().Field(f.valueKey)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Avg:
				curName := FieldValue
				curAgg := elastic.NewAvgAggregation().Field(f.valueKey)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Sum:
				curName := FieldValue
				curAgg := elastic.NewSumAggregation().Field(f.valueKey)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Count:
				curName := FieldValue
				curAgg := elastic.NewValueCountAggregation().Field(f.valueKey)
				if agg != nil {
					curAgg = curAgg.SubAggregation(name, agg)
				}

				agg = curAgg
				name = curName
			case Cardinality:
				curName := FieldValue
				curAgg := elastic.NewCardinalityAggregation().Field(f.valueKey)
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

				curAgg := elastic.NewPercentilesAggregation().Field(f.valueKey).Percentiles(percents...)
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
		case TimeAgg:
			curName := info.Name
			curAgg := elastic.NewDateHistogramAggregation().
				Field(Timestamp).FixedInterval(info.Window).TimeZone(info.Timezone).
				MinDocCount(0).ExtendedBounds(f.start, f.end)
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
			curAgg := elastic.NewTermsAggregation().Field(info.Name).Size(f.size)
			for key, asc := range info.Order {
				curAgg = curAgg.Order(key, asc)
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

func (f *FormatFactory) EsAgg(aggregates metadata.Aggregates) (string, elastic.Aggregation, error) {
	if len(aggregates) == 0 {
		err := errors.New("aggregate_method_list is empty")
		return "", nil, err
	}

	for _, am := range aggregates {
		switch am.Name {
		case DateHistogram:
			f.timeAgg(Timestamp, shortDur(am.Window), f.timezone)
		case Max, Min, Avg, Sum, Count, Cardinality, Percentiles:
			f.valueAgg(FieldValue, am.Name, am.Args...)
			f.nestedAgg(f.valueKey)

			if am.Window > 0 && !am.Without {
				// 增加时间函数
				f.timeAgg(Timestamp, shortDur(am.Window), am.TimeZone)
			}

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

func (f *FormatFactory) Size(ss *elastic.SearchSource) {
	ss.From(f.from).Size(f.size)
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
func (f *FormatFactory) Query(allConditions metadata.AllConditions) (elastic.Query, error) {
	bootQueries := make([]elastic.Query, 0)
	orQuery := make([]elastic.Query, 0, len(allConditions))
	for _, conditions := range allConditions {
		andQuery := make([]elastic.Query, 0, len(conditions))
		for _, con := range conditions {
			q := elastic.NewBoolQuery()
			key := f.toEs(con.DimensionName)

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
							q.MustNot(query)
						case structured.ConditionNotEqual, structured.Ncontains:
							q.Must(query)
						default:
							return nil, fmt.Errorf("operator is not support with empty, %+v", con)
						}
						continue
					} else {
						// 非空才进行验证
						switch con.Operator {
						case structured.ConditionEqual, structured.ConditionNotEqual:
							query = elastic.NewMatchPhraseQuery(key, value)
						case structured.ConditionContains, structured.ConditionNotContains:
							query = elastic.NewWildcardQuery(key, value)
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
							return nil, fmt.Errorf("operator is not support, %+v", con)
						}
					}
				} else {
					query = elastic.NewQueryStringQuery(value)
				}

				queries = append(queries, query)
			}

			// 非空才进行验证
			switch con.Operator {
			case structured.ConditionEqual, structured.ConditionContains, structured.ConditionRegEqual:
				q.Should(queries...)
			case structured.ConditionNotEqual, structured.ConditionNotContains, structured.ConditionNotRegEqual:
				q.MustNot(queries...)
			case structured.ConditionGt, structured.ConditionGte, structured.ConditionLt, structured.ConditionLte:
				q.Must(queries...)
			default:
				return nil, fmt.Errorf("operator is not support, %+v", con)
			}

			var nq elastic.Query
			nf := f.NestedField(con.DimensionName)
			if nf != "" {
				nq = elastic.NewNestedQuery(nf, q)
			} else {
				nq = q
			}

			andQuery = append(andQuery, nq)
		}

		orQuery = append(orQuery, elastic.NewBoolQuery().Must(andQuery...))
	}
	bootQueries = append(bootQueries, elastic.NewBoolQuery().Should(orQuery...))

	return elastic.NewBoolQuery().Must(bootQueries...), nil
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
