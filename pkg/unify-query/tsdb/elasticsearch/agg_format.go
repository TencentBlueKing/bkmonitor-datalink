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
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/prompb"
)

type AggFormat struct {
	aggs []*EsAgg

	sample     prompb.Sample
	seriesKey  strings.Builder
	timeSeries *prompb.TimeSeries

	relabel func(lb prompb.Label) prompb.Label

	TimeSeriesMap map[string]*prompb.TimeSeries
}

func dataFormat(aggs []*EsAgg, data elastic.Aggregations, relabel func(lb prompb.Label) prompb.Label) (a *AggFormat, err error) {
	a = &AggFormat{
		aggs:          aggs,
		relabel:       relabel,
		TimeSeriesMap: make(map[string]*prompb.TimeSeries),
	}
	// 初始化 a.timeSeries
	a.reset()
	err = a.ts(-1, data)
	if err != nil {
		return
	}
	return a, nil
}

func (a *AggFormat) String() string {
	var s strings.Builder
	s.WriteString("\n")
	for _, ts := range a.TimeSeriesMap {
		lbs := make([]string, 0, len(ts.Labels))
		for _, lb := range ts.Labels {
			lbs = append(lbs, fmt.Sprintf("%s=%s", lb.GetName(), lb.GetValue()))
		}
		s.WriteString(strings.Join(lbs, ",") + "\n")
		for _, p := range ts.Samples {
			t := time.UnixMilli(p.GetTimestamp())
			s.WriteString(fmt.Sprintf("%s, %.f\n", t.Format("2006-01-02 15:04:05"), p.GetValue()))
		}
	}

	return s.String()
}

func (a *AggFormat) reset() {
	a.seriesKey.Reset()
	a.timeSeries = &prompb.TimeSeries{
		Labels:  make([]prompb.Label, 0),
		Samples: make([]prompb.Sample, 0),
	}
}

func (a *AggFormat) addLabel(name, value string) {
	lb := a.relabel(prompb.Label{
		Name: name, Value: value,
	})
	a.seriesKey.WriteString(fmt.Sprintf("%s=%s,", name, value))
	a.timeSeries.Labels = append(a.timeSeries.Labels, lb)
}

// 使用暂存的时间加上参数值，构建一个 sample，同时清理暂存的 a.sample
func (a *AggFormat) addSample(v float64) {
	a.sample.Value = v
	a.timeSeries.Samples = append(a.timeSeries.Samples, a.sample)
}

// idx 是层级信息，默认为 -1，通过聚合层级递归解析 data 里面的内容
// 例如该查询 sum(count_over_time(metric[1m])) by (dim-1, dim-2) 的聚合层级为：dim-1, dim-2, time range, count
func (a *AggFormat) ts(idx int, data elastic.Aggregations) error {
	idx++
	if idx < len(a.aggs) {
		agg := a.aggs[idx]
		name := agg.Name
		switch agg.Agg.(type) {
		case *elastic.TermsAggregation:
			if histogram, ok := data.Range(name); ok {
				for _, bucket := range histogram.Buckets {
					// 每一个 label 都是一个新的层级，需要把 label 暂存在 a.timeSeries 里面
					a.addLabel(name, bucket.Key)
					if err := a.ts(idx, bucket.Aggregations); err != nil {
						return err
					}
				}
			}
		case *elastic.DateHistogramAggregation:
			if histogram, ok := data.Histogram(name); ok {
				for _, bucket := range histogram.Buckets {
					// 时间和值也是不同层级，需要暂存在 a.sample 里
					a.sample.Timestamp = int64(bucket.Key)
					if err := a.ts(idx, bucket.Aggregations); err != nil {
						return err
					}
				}

				// 该 series 下的所有时序点都添加完成
				a.TimeSeriesMap[a.seriesKey.String()] = a.timeSeries
				// 把暂存的 a.timeSeries 置空
				a.reset()
			}
		default:
			var (
				value *elastic.AggregationValueMetric
				ok    bool
			)
			switch agg.Agg.(type) {
			case *elastic.MinAggregation:
				if value, ok = data.Min(name); !ok {
					return fmt.Errorf("%s is empty", name)
				}
			case *elastic.SumAggregation:
				if value, ok = data.Sum(name); !ok {
					return fmt.Errorf("%s is empty", name)
				}
			case *elastic.AvgAggregation:
				if value, ok = data.Avg(name); !ok {
					return fmt.Errorf("%s is empty", name)
				}
			case *elastic.ValueCountAggregation:
				if value, ok = data.ValueCount(name); !ok {
					return fmt.Errorf("%s is empty", name)
				}
			case *elastic.MaxAggregation:
				if value, ok = data.Max(name); !ok {
					return fmt.Errorf("%s is empty", name)
				}
			default:
				return fmt.Errorf("%T type is error", agg.Agg)
			}

			// 计算数量需要造数据
			repNum := 1
			if agg.Name == COUNT {
				repNum = int(*value.Value)
			}
			for j := 0; j < repNum; j++ {
				a.addSample(*value.Value)
			}
			a.sample = prompb.Sample{}
		}
	}
	return nil
}
