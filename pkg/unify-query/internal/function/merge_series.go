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
	"time"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

func NewMergeSeriesSetWithFuncAndSort(name string) func(...storage.Series) storage.Series {
	return NewMergeSeriesSetWithFuncAndSortByStep(name, 0)
}

func NewMergeSeriesSetWithFuncAndSortByStep(name string, step time.Duration) func(...storage.Series) storage.Series {
	return func(series ...storage.Series) storage.Series {
		// 处理空输入
		if len(series) == 0 {
			return nil
		}

		// 处理单个series的情况
		if len(series) == 1 {
			return series[0]
		}

		name = strings.ToLower(name)
		// avg 类函数只要存在 route 有效时间段，就按 bucket 覆盖时长做加权合并；没有有效时间段的 overlap-only route 不参与权重。
		if isAvgFunc(name) && step > 0 && hasAnyTimeRange(series...) {
			return mergeAvgSeriesSetWithTimeWeight(series, step)
		}

		// 根据name选择聚合函数
		var aggFunc func(float64, float64) float64
		switch name {
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
		case Avg, AvgOT, Mean:
			aggFunc = func(a, b float64) float64 {
				return a + b
			}
		default: // 默认使用sum
			aggFunc = func(a, b float64) float64 {
				return a + b
			}
		}

		// 按时间戳合并值
		valueMap := make(map[int64]float64)
		countMap := make(map[int64]float64)
		for _, s := range series {
			it := s.Iterator(nil)
			for it.Next() == chunkenc.ValFloat {
				t, v := it.At()
				if existing, ok := valueMap[t]; ok {
					valueMap[t] = aggFunc(existing, v)
				} else {
					valueMap[t] = v
				}
				countMap[t]++
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
			if isAvgFunc(name) {
				// 缺少 route 时间范围或 step 时无法计算时间权重，回退为同 timestamp 普通平均。
				if count := countMap[t]; count > 0 {
					v = v / count
				}
			}
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

// SeriesTimeRange 标记某条 Series 在本次查询中实际覆盖的 route 时间段，单位为毫秒时间戳。
type SeriesTimeRange interface {
	TimeRange() (start, end int64)
}

func NewTimeRangeSeriesSet(set storage.SeriesSet, start, end time.Time) storage.SeriesSet {
	if set == nil || start.IsZero() || end.IsZero() || !start.Before(end) {
		return set
	}

	return &timeRangeSeriesSet{
		SeriesSet: set,
		start:     start.UnixMilli(),
		end:       end.UnixMilli(),
	}
}

func NewZeroTimeRangeSeriesSet(set storage.SeriesSet) storage.SeriesSet {
	if set == nil {
		return nil
	}

	return &timeRangeSeriesSet{
		SeriesSet: set,
	}
}

type timeRangeSeriesSet struct {
	storage.SeriesSet
	start int64
	end   int64
}

func (s *timeRangeSeriesSet) At() storage.Series {
	return &timeRangeSeries{
		Series: s.SeriesSet.At(),
		start:  s.start,
		end:    s.end,
	}
}

type timeRangeSeries struct {
	storage.Series
	start int64
	end   int64
}

func (s *timeRangeSeries) TimeRange() (int64, int64) {
	return s.start, s.end
}

func hasAnyTimeRange(series ...storage.Series) bool {
	for _, s := range series {
		tr, ok := s.(SeriesTimeRange)
		if !ok {
			continue
		}
		start, end := tr.TimeRange()
		if start < end {
			return true
		}
	}
	return false
}

func mergeAvgSeriesSetWithTimeWeight(series []storage.Series, step time.Duration) storage.Series {
	stepMs := step.Milliseconds()
	if stepMs <= 0 {
		return NewMergeSeriesSetWithFuncAndSort(Avg)(series...)
	}

	valueMap := make(map[int64]float64)
	weightMap := make(map[int64]float64)
	for _, s := range series {
		tr, ok := s.(SeriesTimeRange)
		start, end := int64(0), int64(0)
		if ok {
			start, end = tr.TimeRange()
			if start >= end {
				continue
			}
		}
		it := s.Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			t, v := it.At()
			// 无 route 时间段的普通 series 使用完整 bucket 权重，避免 mixed route 合并时被丢弃。
			weight := stepMs
			if ok {
				// 权重取 route 时间段与当前 bucket [t, t+step) 的交集时长。
				weight = overlapDuration(t, t+stepMs, start, end)
			}
			if weight <= 0 {
				continue
			}
			// 加权平均 = sum(avg * overlap) / sum(overlap)
			valueMap[t] += v * float64(weight)
			weightMap[t] += float64(weight)
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
		if weight := weightMap[t]; weight > 0 {
			v = v / weight
		}
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

func overlapDuration(start, end, otherStart, otherEnd int64) int64 {
	if start < otherStart {
		start = otherStart
	}
	if end > otherEnd {
		end = otherEnd
	}
	if start >= end {
		return 0
	}
	return end - start
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
