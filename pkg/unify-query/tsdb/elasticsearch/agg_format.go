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
	"sync"

	elastic "github.com/olivere/elastic/v7"
	"golang.org/x/exp/slices"
)

var itemsPool = sync.Pool{
	New: func() any {
		return items{}
	},
}

type item struct {
	labels    map[string]string
	timestamp int64
	value     float64
}

type items []item

type aggFormat struct {
	aggInfoList aggInfoList

	toEs   func(string) string
	toProm func(string) string

	isNotPromQL bool

	dims  []string
	item  item
	items items
}

func (a *aggFormat) start() {
	a.items = itemsPool.Get().(items)
	a.items = make(items, 0)
}

func (a *aggFormat) close() {
	a.items = nil
	itemsPool.Put(a.items)
}

func (a *aggFormat) addLabel(name, value string) {
	name = a.toProm(name)

	value = strings.Trim(value, `""`)
	newLb := make(map[string]string)
	for k, v := range a.item.labels {
		newLb[k] = v
	}
	newLb[name] = value
	a.item.labels = newLb
}

func (a *aggFormat) reset() {
	if len(a.dims) == 0 && len(a.item.labels) > 0 {
		for k := range a.item.labels {
			a.dims = append(a.dims, k)
		}
		slices.Sort(a.dims)
	}

	a.items = append(a.items, a.item)
}

// idx 是层级信息，默认为 len(a.aggInfoList), 因为聚合结果跟聚合列表是相反的，通过聚合层级递归解析 data 里面的内容
// 例如该查询 sum(count_over_time(metric[1m])) by (dim-1, dim-2) 的聚合层级为：dim-1, dim-2, time range, count
func (a *aggFormat) ts(idx int, data elastic.Aggregations) error {
	idx--
	if idx >= 0 {
		switch info := a.aggInfoList[idx].(type) {
		case TermAgg:
			if bucketRangeItems, ok := data.Range(info.Name); ok {
				if len(bucketRangeItems.Buckets) == 0 {
					return nil
				}

				for _, bucket := range bucketRangeItems.Buckets {
					// 每一个 name 都是一个新的层级，需要把 name 暂存在 a.timeSeries 里面
					if value, ok := bucket.Aggregations["key"]; ok {
						vs, err := value.MarshalJSON()
						if err != nil {
							return err
						}

						a.addLabel(info.Name, string(vs))
						if err = a.ts(idx, bucket.Aggregations); err != nil {
							return err
						}
					}
				}
			}
		case NestedAgg:
			if singleBucket, ok := data.Nested(info.Name); ok {
				if err := a.ts(idx, singleBucket.Aggregations); err != nil {
					return err
				}
			}
		case TimeAgg:
			if bucketHistogramItems, ok := data.Histogram(info.Name); ok {
				if len(bucketHistogramItems.Buckets) == 0 {
					return nil
				}

				for _, bucket := range bucketHistogramItems.Buckets {
					// 时间和值也是不同层级，需要暂存在 a.sample 里
					a.item.timestamp = int64(bucket.Key)
					if err := a.ts(idx, bucket.Aggregations); err != nil {
						return err
					}
				}
			}
		case ValueAgg:
			switch info.FuncType {
			case Min:
				if valueMetric, ok := data.Min(info.Name); ok {
					a.item.value = *valueMetric.Value
					a.reset()
				} else {
					return fmt.Errorf("%s is empty", info.Name)
				}
			case Sum:
				if valueMetric, ok := data.Sum(info.Name); ok {
					a.item.value = *valueMetric.Value
					a.reset()
				} else {
					return fmt.Errorf("%s is empty", info.Name)
				}
			case Avg:
				if valueMetric, ok := data.Avg(info.Name); ok {
					a.item.value = *valueMetric.Value
					a.reset()
				} else {
					return fmt.Errorf("%s is empty", info.Name)
				}
			case Count:
				if valueMetric, ok := data.ValueCount(info.Name); ok {
					// 计算数量需要造数据
					repNum := 1
					if !a.isNotPromQL {
						repNum = int(*valueMetric.Value)
					}

					for j := 0; j < repNum; j++ {
						a.item.value = *valueMetric.Value
						a.reset()
					}
				} else {
					return fmt.Errorf("%s is empty", info.Name)
				}
			case Max:
				if valueMetric, ok := data.Max(info.Name); ok {
					a.item.value = *valueMetric.Value
					a.reset()
				} else {
					return fmt.Errorf("%s is empty", info.Name)
				}
			case Percentiles:
				if percentMetric, ok := data.Percentiles(info.Name); ok {
					for k, v := range percentMetric.Values {
						a.addLabel("le", k)
						a.item.value = v
						a.reset()
					}
				}
			default:
				return fmt.Errorf("%s type is error", info.FuncType)
			}
		default:
			return fmt.Errorf("%s type is error", info)
		}
	}
	return nil
}
