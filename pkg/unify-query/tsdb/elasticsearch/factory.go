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
	"fmt"
	"strings"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	OLDSEP = "."
	NEWSEP = "__"

	BKAPM = "bkapm"
	BKLOG = "bklog"
	BKES  = "bkes"

	ATTRIBUTES = "attributes"
	EXT        = "__ext"

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

var (
	sourceTagMap = map[string]string{
		BKAPM: ATTRIBUTES,
		BKLOG: EXT,
	}
)

func NewFactory(k string) *Factory {
	fact := &Factory{}
	if sourceTag, ok := sourceTagMap[k]; ok {
		fact.oldPrefix = sourceTag + OLDSEP
		fact.newPrefix = sourceTag + NEWSEP
	}
	return fact
}

type Factory struct {
	oldPrefix string
	newPrefix string
}

func (f *Factory) esToLabel(key string) string {
	if strings.HasPrefix(key, f.oldPrefix) {
		key = strings.Replace(key, f.oldPrefix, f.newPrefix, 1)
	}
	return key
}

func (f *Factory) labelToEs(key string) string {
	if strings.HasPrefix(key, f.newPrefix) {
		key = strings.Replace(key, f.newPrefix, f.oldPrefix, 1)
	}
	return key
}

func (f *Factory) Query(query *metadata.Query) (elastic.Query, error) {
	boolQuery := elastic.NewBoolQuery()

	for _, conditions := range query.AllConditions {
		andQuery := elastic.NewBoolQuery()
		for _, con := range conditions {
			q := elastic.NewBoolQuery()
			key := f.labelToEs(con.DimensionName)

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
	if query.QueryString != "" {
		qs := elastic.NewQueryStringQuery(query.QueryString)
		boolQuery = boolQuery.Must(qs)
	}
	return boolQuery, nil
}

func (f *Factory) Relabel(lb prompb.Label) prompb.Label {
	key := f.esToLabel(lb.GetName())

	return prompb.Label{
		Name:  key,
		Value: lb.GetValue(),
	}
}

func (f *Factory) key(a, b string) string {
	return fmt.Sprintf("%s_%s", a, b)
}

func (f *Factory) Aggs(query *metadata.Query) (esAggs *EsAggs, err error) {
	idx := 1
	if len(query.AggregateMethodList) < idx {
		err = fmt.Errorf("functions is error, %+v", query.AggregateMethodList)
		return
	}

	var (
		name string
		agg  elastic.Aggregation
	)

	esAggs = NewEsAggregations(3)
	window := query.TimeAggregation.WindowDuration
	if window.Milliseconds() == 0 {
		err = fmt.Errorf("window is empty, %+v", window.String())
		return
	}

	aggregateMethod := query.AggregateMethodList[0]

	field := query.Field
	switch f.key(aggregateMethod.Name, query.TimeAggregation.Function) {
	case f.key(MIN, MinOT):
		name = MIN
		agg = elastic.NewMinAggregation().Field(field)
	case f.key(MAX, MaxOT):
		name = MAX
		agg = elastic.NewMaxAggregation().Field(field)
	case f.key(AVG, AvgOT):
		name = AVG
		agg = elastic.NewAvgAggregation().Field(field)
	case f.key(SUM, SumOT):
		name = SUM
		agg = elastic.NewSumAggregation().Field(field)
	case f.key(SUM, CountOT):
		name = COUNT
		agg = elastic.NewValueCountAggregation().Field("_index")
	default:
		err = fmt.Errorf("function is not support, with %+v", query)
		return
	}
	esAggs.Insert(&EsAgg{
		Name: name,
		Agg:  agg,
	})

	// add time group
	agg = elastic.NewDateHistogramAggregation().
		Field(Timestamp).FixedInterval(shortDur(window)).TimeZone(query.Timezone).
		MinDocCount(1).SubAggregation(name, agg)
	name = Timestamp
	esAggs.Insert(&EsAgg{
		Name: name,
		Agg:  agg,
	})

	for _, dim := range aggregateMethod.Dimensions {
		dim = f.labelToEs(dim)
		agg = elastic.NewTermsAggregation().Field(dim).SubAggregation(name, agg)
		name = dim

		esAggs.Insert(&EsAgg{
			Name: name,
			Agg:  agg,
		})
	}

	return
}
