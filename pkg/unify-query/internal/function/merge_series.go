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
	"context"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
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
		if len(series) == 1 {
			if tr, ok := series[0].(SeriesTimeRange); ok {
				start, end := tr.TimeRange()
				if start < end {
					return newRouteRangeFilteredSeries(name, step, series[0], start, end)
				}
			}
			return series[0]
		}

		// avg 类函数只要存在 route 有效时间段，就按 bucket 覆盖时长做加权合并；仅用于迁移重叠查询的 route 不参与权重。
		if IsAvgFunc(name) && step > 0 && hasAnyTimeRange(series...) {
			return mergeAvgSeriesSetWithTimeWeight(name, series, step)
		}

		return mergeSeriesSetWithFunc(name, step, series)
	}
}

// mergeSeriesSetWithFunc 按函数名合并同 label series；分段路由会先按 route 生效范围过滤样本。
func mergeSeriesSetWithFunc(name string, step time.Duration, series []storage.Series) storage.Series {
	valueMap := make(map[int64]float64)
	countMap := make(map[int64]float64)
	candidateValueMap := make(map[int64]float64)
	candidateCountMap := make(map[int64]float64)
	aggFunc := seriesAggFunc(name)
	isRouteRangeFilterEnabled := hasAnyTimeRange(series...)
	stepMs := step.Milliseconds()
	filterReasonCount := make(map[string]float64)

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
					filterReasonCount[metric.RouteSeriesFilterZeroRangeCandidate]++
					addSample(candidateValueMap, candidateCountMap, t, v)
					continue
				}
				if ok, reason := routeRangeFilterReason(name, stepMs, t, start, end); !ok {
					filterReasonCount[reason]++
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
	recordRouteSeriesFilterSamples(name, filterReasonCount)

	return newSampleSeries(series[0], buildSortedSeriesSamples(name, valueMap, countMap))
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

// buildSortedSeriesSamples 将合并后的 timestamp map 转成有序样本，并处理缺少时间权重时的 avg 普通平均。
func buildSortedSeriesSamples(name string, valueMap, countMap map[int64]float64) []prompb.Sample {
	sortedData := make([]prompb.Sample, 0, len(valueMap))
	for t, v := range valueMap {
		if IsAvgFunc(name) {
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

func isSampleInRouteRange(name string, stepMs, t, start, end int64) bool {
	ok, _ := routeRangeFilterReason(name, stepMs, t, start, end)
	return ok
}

func routeRangeFilterReason(name string, stepMs, t, start, end int64) (bool, string) {
	if stepMs > 0 && isForwardRangeBucketFunc(name) {
		// 这里处理的是存储侧下推后的窗口聚合结果：样本 timestamp 表示 bucket 起点，
		// route 过滤应判断 bucket [t, t+window) 是否与 route 生效区间相交。
		// PromQL 原生 *_over_time(range-vector) 的 timestamp 是 evaluation instant，不能复用该 forward bucket 语义。
		return rangeOverlapFilterReason(t, t+stepMs, start, end)
	}
	if stepMs > 0 && isBackwardRangeBucketFunc(name) {
		// PromQL *_over_time(range-vector) 的 timestamp 是 evaluation instant，
		// 实际统计窗口为 [t-window, t)，需要按向后窗口判断与 route 生效区间是否相交。
		// t == routeStart 时窗口在 routeStart 前结束，与当前 route 无交集，因此这里必须是严格大于。
		return rangeOverlapFilterReason(t-stepMs, t, start, end)
	}
	if t < start {
		return false, metric.RouteSeriesFilterBeforeStart
	}
	if t >= end {
		return false, metric.RouteSeriesFilterAfterEnd
	}
	return true, ""
}

// routeIteratorFilterReason 用在 merge 前的 per-route wrapper。
// backward range bucket 需要保留与 route 有交集的样本，并兼容单路 routeStart 首个 evaluation bucket。
func routeIteratorFilterReason(name string, stepMs, t, start, end int64) (bool, string) {
	if stepMs > 0 && isBackwardRangeBucketFunc(name) {
		if t == start {
			return true, ""
		}
		return rangeOverlapFilterReason(t-stepMs, t, start, end)
	}
	return routeRangeFilterReason(name, stepMs, t, start, end)
}

func rangeOverlapFilterReason(start, end, otherStart, otherEnd int64) (bool, string) {
	if start < otherEnd && end > otherStart {
		return true, ""
	}
	if end <= otherStart {
		return false, metric.RouteSeriesFilterBeforeStart
	}
	if start >= otherEnd {
		return false, metric.RouteSeriesFilterAfterEnd
	}
	return false, metric.RouteSeriesFilterNoOverlap
}

func recordRouteSeriesFilterSamples(name string, reasonCount map[string]float64) {
	for reason, count := range reasonCount {
		metric.RouteSeriesFilterSamplesAdd(context.Background(), name, reason, count)
	}
}

func isForwardRangeBucketFunc(name string) bool {
	switch strings.ToLower(name) {
	case Sum, Count, Min, Max, Avg, Mean:
		return true
	default:
		return false
	}
}

func isBackwardRangeBucketFunc(name string) bool {
	switch strings.ToLower(name) {
	case SumOT, CountOT, MinOT, MaxOT, AvgOT:
		return true
	default:
		return false
	}
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

// newRouteRangeFilteredSeries 按 route 生效范围裁剪样本，同时保持底层 iterator 的样本类型，
// 避免 native histogram 被转换或丢弃。
func newRouteRangeFilteredSeries(
	name string, step time.Duration, series storage.Series, start, end int64,
) storage.Series {
	return &routeRangeFilteredSeries{
		Series: series,
		name:   name,
		stepMs: step.Milliseconds(),
		start:  start,
		end:    end,
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

func NewRouteRangeFilterSeriesSet(set storage.SeriesSet, name string, step time.Duration) storage.SeriesSet {
	if set == nil {
		return nil
	}

	return &routeRangeFilterSeriesSet{
		SeriesSet: set,
		name:      strings.ToLower(name),
		step:      step,
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

type routeRangeFilterSeriesSet struct {
	storage.SeriesSet
	name string
	step time.Duration
}

func (s *routeRangeFilterSeriesSet) At() storage.Series {
	series := s.SeriesSet.At()
	tr, ok := series.(SeriesTimeRange)
	if !ok {
		return series
	}
	start, end := tr.TimeRange()
	if start >= end {
		return series
	}
	return newRouteRangeFilteredSeries(s.name, s.step, series, start, end)
}

type routeRangeFilteredSeries struct {
	storage.Series
	name   string
	stepMs int64
	start  int64
	end    int64
}

func (s *routeRangeFilteredSeries) Iterator(iterator chunkenc.Iterator) chunkenc.Iterator {
	return &routeRangeFilterIterator{
		it:     s.Series.Iterator(iterator),
		name:   s.name,
		stepMs: s.stepMs,
		start:  s.start,
		end:    s.end,
	}
}

func (s *routeRangeFilteredSeries) TimeRange() (int64, int64) {
	return s.start, s.end
}

func hasAnyTimeRange(series ...storage.Series) bool {
	for _, s := range series {
		if _, ok := s.(SeriesTimeRange); ok {
			return true
		}
	}
	return false
}

func mergeAvgSeriesSetWithTimeWeight(name string, series []storage.Series, step time.Duration) storage.Series {
	stepMs := step.Milliseconds()
	if stepMs <= 0 {
		return NewMergeSeriesSetWithFuncAndSort(Avg)(series...)
	}

	// valueMap/weightMap 记录同 timestamp 的加权分子和分母，最终按 sum(avg*overlap)/sum(overlap) 输出。
	// 已知限制：这里的时间覆盖加权是近似计算，不等价于按底层原始样本数加权。
	valueMap := make(map[int64]float64)
	weightMap := make(map[int64]float64)
	// candidate* 记录仅来自迁移重叠查询的零权重样本；只有有效 route 没有同 timestamp 样本时才兜底补入。
	candidateValueMap := make(map[int64]float64)
	candidateCountMap := make(map[int64]float64)
	filterReasonCount := make(map[string]float64)
	for _, s := range series {
		tr, ok := s.(SeriesTimeRange)
		it := s.Iterator(nil)
		if !ok {
			for it.Next() == chunkenc.ValFloat {
				t, v := it.At()
				// 无 route 时间段的普通 series 使用完整 bucket 权重，避免 mixed route 合并时被丢弃。
				valueMap[t] += v * float64(stepMs)
				weightMap[t] += float64(stepMs)
			}
			if err := it.Err(); err != nil {
				return newErrSeries(series[0], err)
			}
			continue
		}

		start, end := tr.TimeRange()
		for it.Next() == chunkenc.ValFloat {
			t, v := it.At()
			// start >= end 表示该路由只有扩展查询范围、没有真实生效区间，不能参与 avg 权重。
			if start >= end {
				filterReasonCount[metric.RouteSeriesFilterZeroRangeCandidate]++
				candidateValueMap[t] += v
				candidateCountMap[t]++
				continue
			}
			bucketStart, bucketEnd := avgBucketRange(name, t, stepMs)
			// 权重取 route 时间段与当前统计窗口的交集时长。
			weight := overlapDuration(bucketStart, bucketEnd, start, end)
			if weight <= 0 {
				_, reason := rangeOverlapFilterReason(bucketStart, bucketEnd, start, end)
				filterReasonCount[reason]++
				continue
			}
			// 加权平均 = sum(avg * overlap) / sum(overlap)
			valueMap[t] += v * float64(weight)
			weightMap[t] += float64(weight)
		}
		if err := it.Err(); err != nil {
			return newErrSeries(series[0], err)
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
	recordRouteSeriesFilterSamples(name, filterReasonCount)

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

func avgBucketRange(name string, t, stepMs int64) (int64, int64) {
	if name == AvgOT {
		// PromQL range selector 会从当前 evaluation instant 向前选取样本，
		// *_over_time(range-vector) 会在该 instant 返回一个 instant-vector 样本。
		// 因此 timestamp 为 t 的 avg_over_time 覆盖窗口是 (t-window, t]，不是 [t, t+window)。
		// 参考：
		// https://prometheus.io/docs/prometheus/latest/querying/basics/#range-vector-selectors
		// https://prometheus.io/docs/prometheus/latest/querying/functions/#aggregation_over_time
		// https://github.com/prometheus/prometheus/blob/main/promql/engine.go#L978-L997
		// https://github.com/prometheus/prometheus/blob/main/promql/engine.go#L3631-L3643
		return t - stepMs, t
	}
	return t, t + stepMs
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

type routeRangeFilterIterator struct {
	it          chunkenc.Iterator
	name        string
	stepMs      int64
	start       int64
	end         int64
	reasonCount map[string]float64
	recorded    bool
}

func (it *routeRangeFilterIterator) AtHistogram() (int64, *histogram.Histogram) {
	return it.it.AtHistogram()
}

func (it *routeRangeFilterIterator) AtFloatHistogram() (int64, *histogram.FloatHistogram) {
	return it.it.AtFloatHistogram()
}

func (it *routeRangeFilterIterator) AtT() int64 {
	return it.it.AtT()
}

func (it *routeRangeFilterIterator) At() (int64, float64) {
	return it.it.At()
}

func (it *routeRangeFilterIterator) Next() chunkenc.ValueType {
	return it.advance(it.it.Next())
}

func (it *routeRangeFilterIterator) Seek(t int64) chunkenc.ValueType {
	return it.advance(it.it.Seek(t))
}

func (it *routeRangeFilterIterator) Err() error {
	it.recordFilteredSamples()
	return it.it.Err()
}

func (it *routeRangeFilterIterator) advance(valueType chunkenc.ValueType) chunkenc.ValueType {
	for valueType != chunkenc.ValNone {
		t := it.sampleTimestamp(valueType)
		if ok, reason := routeIteratorFilterReason(it.name, it.stepMs, t, it.start, it.end); ok {
			return valueType
		} else {
			it.countFilterReason(reason)
		}
		valueType = it.it.Next()
	}
	it.recordFilteredSamples()
	return chunkenc.ValNone
}

func (it *routeRangeFilterIterator) countFilterReason(reason string) {
	if it.reasonCount == nil {
		it.reasonCount = make(map[string]float64)
	}
	it.reasonCount[reason]++
}

func (it *routeRangeFilterIterator) recordFilteredSamples() {
	if it.recorded {
		return
	}
	it.recorded = true
	recordRouteSeriesFilterSamples(it.name, it.reasonCount)
}

func (it *routeRangeFilterIterator) sampleTimestamp(valueType chunkenc.ValueType) int64 {
	switch valueType {
	case chunkenc.ValFloat:
		t, _ := it.it.At()
		return t
	case chunkenc.ValHistogram:
		t, _ := it.it.AtHistogram()
		return t
	case chunkenc.ValFloatHistogram:
		t, _ := it.it.AtFloatHistogram()
		return t
	default:
		return it.it.AtT()
	}
}
