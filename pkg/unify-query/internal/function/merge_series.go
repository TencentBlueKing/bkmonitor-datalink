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

		name = strings.ToLower(name)
		// avg 类函数只要存在 route 有效时间段，就按 bucket 覆盖时长做加权合并；仅用于迁移重叠查询的 route 不参与权重。
		if isAvgFunc(name) && step > 0 && hasAnyTimeRange(series...) {
			return mergeAvgSeriesSetWithTimeWeight(series, step)
		}

		return mergeSeriesSetWithFunc(name, series)
	}
}

// mergeSeriesSetWithFunc 按函数名合并同 label series；分段路由会先按 route 生效范围过滤样本。
func mergeSeriesSetWithFunc(name string, series []storage.Series) storage.Series {
	valueMap := make(map[int64]float64)
	countMap := make(map[int64]float64)
	candidateValueMap := make(map[int64]float64)
	candidateCountMap := make(map[int64]float64)
	aggFunc := seriesAggFunc(name)
	isRouteRangeFilterEnabled := hasAnyTimeRange(series...)

	addSample := func(values, counts map[int64]float64, t int64, v float64) {
		if existing, ok := values[t]; ok {
			values[t] = aggFunc(existing, v)
		} else {
			values[t] = v
		}
		counts[t]++
	}

	for _, s := range series {
		tr, ok := s.(SeriesTimeRange)
		start, end := int64(0), int64(0)
		if ok {
			start, end = tr.TimeRange()
		}

		it := s.Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			t, v := it.At()
			if isRouteRangeFilterEnabled && ok {
				if start >= end {
					addSample(candidateValueMap, candidateCountMap, t, v)
					continue
				}
				if t < start || t >= end {
					continue
				}
			}
			addSample(valueMap, countMap, t, v)
		}
		if err := it.Err(); err != nil {
			return newErrSeries(series[0], err)
		}
	}

	if isRouteRangeFilterEnabled {
		mergeCandidateSamples(valueMap, countMap, candidateValueMap, candidateCountMap)
	}

	return newSampleSeries(series[0], mergeSeriesSamples(name, valueMap, countMap))
}

// seriesAggFunc 返回同 timestamp 多条样本的合并函数；avg 类在调用方用 countMap 做二次平均。
func seriesAggFunc(name string) func(float64, float64) float64 {
	switch name {
	case Min, MinOT:
		return func(a, b float64) float64 {
			if a < b {
				return a
			}
			return b
		}
	case Max, MaxOT:
		return func(a, b float64) float64 {
			if a > b {
				return a
			}
			return b
		}
	default:
		return func(a, b float64) float64 {
			return a + b
		}
	}
}

// mergeCandidateSamples 将仅来自迁移重叠查询的候选样本补入主结果；同 timestamp 已有有效 route 样本时不覆盖。
func mergeCandidateSamples(
	valueMap, countMap, candidateValueMap, candidateCountMap map[int64]float64,
) {
	for t, v := range candidateValueMap {
		if _, ok := valueMap[t]; ok {
			continue
		}
		valueMap[t] = v
		countMap[t] = candidateCountMap[t]
	}
}

// mergeSeriesSamples 将合并后的 timestamp map 转成有序样本，并处理缺少时间权重时的 avg 普通平均。
func mergeSeriesSamples(name string, valueMap, countMap map[int64]float64) []prompb.Sample {
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
	return sortedData
}

// newErrSeries 返回带 iterator 错误的 Series，用于把底层遍历错误传递给调用方。
func newErrSeries(template storage.Series, err error) storage.Series {
	return &storage.SeriesEntry{
		Lset: template.Labels(),
		SampleIteratorFn: func(iterator chunkenc.Iterator) chunkenc.Iterator {
			return &seriesIterator{
				err: err,
			}
		},
	}
}

// newSampleSeries 用已有 labels 和内存样本构造可遍历的 Series。
func newSampleSeries(template storage.Series, samples []prompb.Sample) storage.Series {
	return &storage.SeriesEntry{
		Lset: template.Labels(),
		SampleIteratorFn: func(iterator chunkenc.Iterator) chunkenc.Iterator {
			return &seriesIterator{
				list: samples,
				idx:  -1,
			}
		},
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

func (s *timeRangeSeries) Iterator(iterator chunkenc.Iterator) chunkenc.Iterator {
	it := s.Series.Iterator(iterator)
	if s.start >= s.end {
		return it
	}
	return &timeRangeSeriesIterator{
		Iterator: it,
		start:    s.start,
		end:      s.end,
	}
}

type timeRangeSeriesIterator struct {
	chunkenc.Iterator
	start int64
	end   int64
}

func (it *timeRangeSeriesIterator) Next() chunkenc.ValueType {
	for {
		valueType := it.Iterator.Next()
		if valueType == chunkenc.ValNone || it.AtT() >= it.start && it.AtT() < it.end {
			return valueType
		}
	}
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
	candidateValueMap := make(map[int64]float64)
	candidateCountMap := make(map[int64]float64)
	for _, s := range series {
		tr, ok := s.(SeriesTimeRange)
		start, end := int64(0), int64(0)
		if ok {
			start, end = tr.TimeRange()
		}
		it := newTimeWeightIterator(s)
		for it.Next() == chunkenc.ValFloat {
			t, v := it.At()
			if ok && start >= end {
				candidateValueMap[t] += v
				candidateCountMap[t]++
				continue
			}
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
	for t, v := range candidateValueMap {
		if weightMap[t] > 0 {
			continue
		}
		if count := candidateCountMap[t]; count > 0 {
			valueMap[t] = v / count
		}
	}
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

func newTimeWeightIterator(s storage.Series) chunkenc.Iterator {
	if tr, ok := s.(*timeRangeSeries); ok {
		return tr.Series.Iterator(nil)
	}
	return s.Iterator(nil)
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
