// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function_test

import (
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestMergeSeriesSet(t *testing.T) {
	ts1 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "prometheus",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     1,
				Timestamp: 0,
			},
			{
				Value:     3,
				Timestamp: 120,
			},
		},
	}
	ts3 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "prometheus",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     4,
				Timestamp: 0,
			},
			{
				Value:     5,
				Timestamp: 60,
			},
		},
	}

	ts2 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "elasticsearch",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     2,
				Timestamp: 60,
			},
			{
				Value:     3,
				Timestamp: 120,
			},
		},
	}
	ts4 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "elasticsearch",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     8,
				Timestamp: 60,
			},
			{
				Value:     9,
				Timestamp: 120,
			},
		},
	}

	testCases := map[string]struct {
		qrs []*prompb.QueryResult
		ts  mock.TimeSeriesList
		fn  storage.VerticalSeriesMergeFunc
	}{
		"empty": {
			qrs: []*prompb.QueryResult{},
		},
		"one set": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts1,
			},
		},
		"two timeSeries with chainedSeriesMerge": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2, ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts2, *ts4, *ts1, *ts3,
			},
		},
		"two timeSeries with mergeSeriesSetWithFuncAndSort": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2, ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts2, *ts4, *ts1, *ts3,
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(""),
		},
		"two queryResult with chainedSeriesMerge": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     8,
							Timestamp: 60,
						},
						{
							Value:     9,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     4,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     10,
							Timestamp: 60,
						},
						{
							Value:     12,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     5,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(""),
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort max": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     8,
							Timestamp: 60,
						},
						{
							Value:     9,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     4,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(function.Max),
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort min": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     2,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     1,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort("min"),
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort avg": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     6,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     2.5,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(function.Avg),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var sets []storage.SeriesSet
			for _, r := range tc.qrs {
				sets = append(sets, remote.FromQueryResult(true, r))
			}

			if tc.fn == nil {
				tc.fn = storage.ChainedSeriesMerge
			}
			set := storage.NewMergeSeriesSet(sets, tc.fn)

			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)
			assert.Equal(t, tc.ts, ts)
		})
	}
}

func TestMergeSeriesSetWithTimeWeightedAvg(t *testing.T) {
	var (
		bucketStart = time.UnixMilli(0)
		bucketStep  = 5 * time.Minute
		bucketEnd   = bucketStart.Add(bucketStep)
	)

	type routeSeries struct {
		value        float64
		start        time.Time
		end          time.Time
		withRange    bool
		invalidRange bool
	}

	testCases := map[string]struct {
		fn        string
		routes    []routeSeries
		withRange bool
		expected  float64
	}{
		"avg uses route overlap as weight": {
			// bucket 为 [0s, 300s)，两段 route 覆盖时长分别是 132s 和 168s。
			// (10*132 + 30*168) / (132 + 168) = 21.2
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketStart.Add(132 * time.Second),
				},
				{
					value: 30,
					start: bucketStart.Add(132 * time.Second),
					end:   bucketEnd,
				},
			},
			withRange: true,
			expected:  21.2,
		},
		"mean uses route overlap as weight": {
			// mean 是 avg 的别名，同样要按 route 覆盖时长加权。
			// (10*132 + 30*168) / (132 + 168) = 21.2
			fn: function.Mean,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketStart.Add(132 * time.Second),
				},
				{
					value: 30,
					start: bucketStart.Add(132 * time.Second),
					end:   bucketEnd,
				},
			},
			withRange: true,
			expected:  21.2,
		},
		"avg_over_time uses route overlap as weight": {
			// avg_over_time 也是 avg 类函数，同样要按 route 覆盖时长加权。
			// (10*132 + 30*168) / (132 + 168) = 21.2
			fn: function.AvgOT,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketStart.Add(132 * time.Second),
				},
				{
					value: 30,
					start: bucketStart.Add(132 * time.Second),
					end:   bucketEnd,
				},
			},
			withRange: true,
			expected:  21.2,
		},
		"equal route overlap matches arithmetic average": {
			// 两段 route 覆盖时长相等时，加权平均结果应等同于普通平均。
			// (10*150 + 30*150) / (150 + 150) = 20
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketStart.Add(150 * time.Second),
				},
				{
					value: 30,
					start: bucketStart.Add(150 * time.Second),
					end:   bucketEnd,
				},
			},
			withRange: true,
			expected:  20,
		},
		"route without bucket overlap is ignored": {
			// 第二段 route 不覆盖当前 bucket，只使用第一段 route 的值。
			// (10*300) / 300 = 10
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketEnd,
				},
				{
					value: 30,
					start: bucketEnd,
					end:   bucketEnd.Add(bucketStep),
				},
			},
			withRange: true,
			expected:  10,
		},
		"missing route time range falls back to arithmetic average": {
			// 没有 route 时间范围就无法计算权重，保持原来的普通平均逻辑。
			// (10 + 30) / 2 = 20
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
				},
				{
					value: 30,
				},
			},
			expected: 20,
		},
		"unranged series uses full bucket weight": {
			// mixed route 合并中，普通无 route 时间段的 series 不能被丢弃，按完整 bucket 参与权重。
			// (10*300 + 30*300) / (300 + 300) = 20
			fn: function.Avg,
			routes: []routeSeries{
				{
					value:     10,
					start:     bucketStart,
					end:       bucketEnd,
					withRange: true,
				},
				{
					value: 30,
				},
			},
			expected: 20,
		},
		"invalid route time range is ignored": {
			// 带有无效 route 时间范围的 series 没有可用覆盖时长，不能参与加权。
			// (10*300) / 300 = 10
			fn: function.Avg,
			routes: []routeSeries{
				{
					value:     10,
					start:     bucketStart,
					end:       bucketEnd,
					withRange: true,
				},
				{
					value:        30,
					invalidRange: true,
				},
			},
			expected: 10,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			sets := make([]storage.SeriesSet, 0, len(tc.routes))
			for _, route := range tc.routes {
				routeSet := remote.FromQueryResult(true, &prompb.QueryResult{
					Timeseries: []*prompb.TimeSeries{
						{
							Labels: []prompb.Label{
								{Name: "__name__", Value: "up"},
								{Name: "job", Value: "elasticsearch"},
							},
							Samples: []prompb.Sample{
								{
									Value:     route.value,
									Timestamp: bucketStart.UnixMilli(),
								},
							},
						},
					},
				})
				if tc.withRange || route.withRange {
					routeSet = function.NewTimeRangeSeriesSet(routeSet, route.start, route.end)
				}
				if route.invalidRange {
					routeSet = invalidTimeRangeSeriesSet{SeriesSet: routeSet}
				}
				sets = append(sets, routeSet)
			}

			set := storage.NewMergeSeriesSet(sets, function.NewMergeSeriesSetWithFuncAndSortByStep(tc.fn, bucketStep))
			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)
			assert.Equal(t, mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{Name: "__name__", Value: "up"},
						{Name: "job", Value: "elasticsearch"},
					},
					Samples: []prompb.Sample{
						{
							Value:     tc.expected,
							Timestamp: bucketStart.UnixMilli(),
						},
					},
				},
			}, ts)
		})
	}
}

type invalidTimeRangeSeriesSet struct {
	storage.SeriesSet
}

func (s invalidTimeRangeSeriesSet) At() storage.Series {
	return invalidTimeRangeSeries{Series: s.SeriesSet.At()}
}

type invalidTimeRangeSeries struct {
	storage.Series
}

func (s invalidTimeRangeSeries) TimeRange() (int64, int64) {
	return 1, 1
}
