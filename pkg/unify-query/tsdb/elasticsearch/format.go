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
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	Timestamp  = "dtEventTimeStamp"
	TimeFormat = "epoch_millis"

	Type       = "type"
	Properties = "properties"

	OldStep = "."
	NewStep = "__"

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

	TypeNested = "nested"
)

var (
	AggregationMap = map[string]string{
		MIN + MinOT:   MIN,
		MAX + MaxOT:   MAX,
		SUM + SumOT:   SUM,
		AVG + AvgOT:   AVG,
		SUM + CountOT: COUNT,
	}
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
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

type FormatFactory struct {
	valueKey string

	mapping map[string]string
	data    map[string]any
}

func NewFormatFactory(valueKey string, mapping map[string]any) *FormatFactory {
	f := &FormatFactory{
		valueKey: valueKey,
		mapping:  make(map[string]string),
	}

	mapProperties("", mapping, f.mapping)
	return f
}

func (f *FormatFactory) nestedAgg(key, name string, agg elastic.Aggregation) (string, elastic.Aggregation) {
	lbs := strings.Split(key, OldStep)

	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], OldStep)
		if v, ok := f.mapping[checkKey]; ok {
			if v == TypeNested {
				agg = elastic.NewNestedAggregation().Path(checkKey).SubAggregation(name, agg)
				name = checkKey
			}
		}
	}

	return name, agg
}

func (f *FormatFactory) toEs(key string) string {
	vs := strings.Split(key, NewStep)
	return strings.Join(vs, OldStep)
}

func (f *FormatFactory) SetData(data map[string]any) {
	f.data = map[string]any{}
	mapData("", data, f.data)
}

func (f *FormatFactory) Agg(timeAggregation *metadata.TimeAggregation, aggregateMethodList metadata.AggregateMethodList, timeZone string) (name string, agg elastic.Aggregation, err error) {
	if len(aggregateMethodList) == 0 && timeAggregation == nil {
		return
	}

	// 如果使用了时间函数需要进行转换, sum(count_over_time) => count
	if timeAggregation != nil {
		if len(aggregateMethodList) > 0 {
			err = errors.New("aggregate_method_list is empty")
			return
		}

		var ok bool

		if !aggregateMethodList[0].Without && timeAggregation.WindowDuration > 0 {
			key := aggregateMethodList[0].Name + timeAggregation.Function
			if name, ok = AggregationMap[key]; !ok {
				err = fmt.Errorf("aggregation is not support with: %s", key)
				return
			} else {
				// 增加聚合函数
				switch name {
				case MIN:
					agg = elastic.NewMinAggregation().Field(f.valueKey)
				case MAX:
					agg = elastic.NewMaxAggregation().Field(f.valueKey)
				case AVG:
					agg = elastic.NewAvgAggregation().Field(f.valueKey)
				case SUM:
					agg = elastic.NewSumAggregation().Field(f.valueKey)
				case COUNT:
					agg = elastic.NewValueCountAggregation().Field("_index")
				default:
					err = fmt.Errorf("aggregation is not support, with %+v", name)
					return
				}

				// 判断是否是 nested
				name, agg = f.nestedAgg(f.valueKey, name, agg)

				// 增加时间函数
				agg = elastic.NewDateHistogramAggregation().
					Field(Timestamp).FixedInterval(shortDur(timeAggregation.WindowDuration)).TimeZone(timeZone).
					MinDocCount(1).SubAggregation(name, agg)

				// 增加维度聚合函数
				for _, dim := range aggregateMethodList[0].Dimensions {
					dim = f.toEs(dim)
					agg = elastic.NewTermsAggregation().Field(dim).SubAggregation(name, agg)
					name, agg = f.nestedAgg(dim, dim, agg)
				}
			}
		} else {
			err = fmt.Errorf("aggregation is not support with: %+v", aggregateMethodList)
			return
		}
	}
	return
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
