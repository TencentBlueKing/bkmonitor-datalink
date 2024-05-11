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
		info := a.aggInfoList[idx]
		switch info.typeName {
		case TypeTerms:
			if bucketRangeItems, ok := data.Range(info.name); ok {
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

						a.addLabel(info.name, string(vs))
						if err = a.ts(idx, bucket.Aggregations); err != nil {
							return err
						}
					}
				}
			}
		case TypeNested:
			if singleBucket, ok := data.Nested(info.name); ok {
				if err := a.ts(idx, singleBucket.Aggregations); err != nil {
					return err
				}
			}
		case TypeDateHistogram:
			if bucketHistogramItems, ok := data.Histogram(info.name); ok {
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

		case TypeValue:
			var (
				value float64
			)
			switch info.name {
			case Min:
				if valueMetric, ok := data.Min(info.name); !ok {
					return fmt.Errorf("%s is empty", info.name)
				} else {
					value = *valueMetric.Value
				}
			case Sum:
				if valueMetric, ok := data.Sum(info.name); !ok {
					return fmt.Errorf("%s is empty", info.name)
				} else {
					value = *valueMetric.Value
				}
			case Avg:
				if valueMetric, ok := data.Avg(info.name); !ok {
					return fmt.Errorf("%s is empty", info.name)
				} else {
					value = *valueMetric.Value
				}
			case Count:
				if valueMetric, ok := data.ValueCount(info.name); !ok {
					return fmt.Errorf("%s is empty", info.name)
				} else {
					value = *valueMetric.Value
				}
			case Max:
				if valueMetric, ok := data.Max(info.name); !ok {
					return fmt.Errorf("%s is empty", info.name)
				} else {
					value = *valueMetric.Value
				}
			case Percentiles:
				if len(info.args) != 1 {
					return fmt.Errorf("args length is not 1, %+v", info.args)
				}

				var key string
				switch v := info.args[0].(type) {
				case int, int32, int64:
					key = fmt.Sprintf("%d.0", v)
				case float64:
					key = fmt.Sprintf("%.1f", v)
				case string:
					key = v
				default:
					return fmt.Errorf("aggregation is not support this type %T, with %v", v, v)
				}

				if percentMetric, ok := data.Percentiles(info.name); ok {
					if value, ok = percentMetric.Values[key]; !ok {
						return fmt.Errorf("percent metric values is error, key: %s in %+v", key, percentMetric.Values)
					}
				}
			default:
				return fmt.Errorf("%s type is error", info)
			}

			// 计算数量需要造数据
			repNum := 1
			if !a.isNotPromQL && info.name == Count {
				repNum = int(value)
			}

			for j := 0; j < repNum; j++ {
				a.item.value = value
				a.reset()
			}
		}
	}
	return nil
}
