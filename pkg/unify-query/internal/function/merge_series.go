// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"sort"
	"strings"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

func NewMergeSeriesSetWithFuncAndSort(name string) func(...storage.Series) storage.Series {
	return func(series ...storage.Series) storage.Series {
		// 处理空输入
		if len(series) == 0 {
			return nil
		}

		// 处理单个series的情况
		if len(series) == 1 {
			return series[0]
		}

		// 根据name选择聚合函数
		var aggFunc func(float64, float64) float64
		switch strings.ToLower(name) {
		case Min, MinOT:
			aggFunc = func(a, b float64) float64 {
				if a < b {
					return a
				}
				return b
			}
		case Max, MaxOT:
			aggFunc = func(a, b float64) float64 {
				if a > b {
					return a
				}
				return b
			}
		default: // 默认使用sum
			aggFunc = func(a, b float64) float64 {
				return a + b
			}
		}

		// 按时间戳合并值
		valueMap := make(map[int64]float64)
		for _, s := range series {
			it := s.Iterator(nil)
			for it.Next() == chunkenc.ValFloat {
				t, v := it.At()
				if existing, ok := valueMap[t]; ok {
					valueMap[t] = aggFunc(existing, v)
				} else {
					valueMap[t] = v
				}
			}
			if err := it.Err(); err != nil {
				return &storage.SeriesEntry{
					Lset: series[0].Labels(),
					SampleIteratorFn: func(iterator chunkenc.Iterator) chunkenc.Iterator {
						return &seriesIterator{
							err: err,
						}
					},
				}
			}
		}

		sortedData := make([]prompb.Sample, 0, len(valueMap))
		for t, v := range valueMap {
			sortedData = append(sortedData, prompb.Sample{Timestamp: t, Value: v})
		}
		sort.Slice(sortedData, func(i, j int) bool {
			return sortedData[i].Timestamp < sortedData[j].Timestamp
		})

		return &storage.SeriesEntry{
			Lset: series[0].Labels(),
			SampleIteratorFn: func(iterator chunkenc.Iterator) chunkenc.Iterator {
				return &seriesIterator{
					list: sortedData,
					idx:  -1,
				}
			},
		}
	}
}

type seriesIterator struct {
	list []prompb.Sample
	idx  int
	err  error
}

func (it *seriesIterator) AtHistogram() (int64, *histogram.Histogram) {
	return 0, nil
}

func (it *seriesIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	return 0, nil
}

func (it *seriesIterator) AtT() int64 {
	s := it.list[it.idx]
	return s.GetTimestamp()
}

func (it *seriesIterator) At() (int64, float64) {
	s := it.list[it.idx]
	return s.GetTimestamp(), s.GetValue()
}

func (it *seriesIterator) Next() chunkenc.ValueType {
	it.idx++
	if it.idx < len(it.list) {
		return chunkenc.ValFloat
	}
	return chunkenc.ValNone
}

func (it *seriesIterator) Seek(t int64) chunkenc.ValueType {
	if it.idx == -1 {
		it.idx = 0
	}
	if it.idx >= len(it.list) {
		return chunkenc.ValNone
	}
	if s := it.list[it.idx]; s.GetTimestamp() >= t {
		return chunkenc.ValFloat
	}
	// Do binary search between current position and end.
	it.idx += sort.Search(len(it.list)-it.idx, func(i int) bool {
		s := it.list[i+it.idx]
		return s.GetTimestamp() >= t
	})
	if it.idx < len(it.list) {
		return chunkenc.ValFloat
	}

	return chunkenc.ValNone
}

func (it *seriesIterator) Err() error {
	return it.err
}
