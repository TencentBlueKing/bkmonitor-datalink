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

func TestMergeSeriesSetWithRouteRangeFilter(t *testing.T) {
	var (
		firstS1Start  = time.Unix(100, 0)
		firstS1End    = time.Unix(200, 0)
		secondS1Start = time.Unix(300, 0)
		secondS1End   = time.Unix(400, 0)
	)
	sample := func(value float64, timestamp time.Time) prompb.Sample {
		return prompb.Sample{
			Value:     value,
			Timestamp: timestamp.UnixMilli(),
		}
	}

	type routeSeries struct {
		samples   []prompb.Sample
		start     time.Time
		end       time.Time
		zeroRange bool
	}

	testCases := map[string]struct {
		fn       string
		step     time.Duration
		routes   []routeSeries
		expected []prompb.Sample
	}{
		"sum 不应重复累计同 storage 回切窗口查回的完整 SelectHints 样本": {
			// 场景：storage 路由发生 A -> B -> A 回切。
			//
			// s1: [100s-------------200s)
			// s2:                  [200s-------------300s)
			// s1:                                    [300s-------------400s)
			//
			// 同一个 physical storage=s1 有两个不连续 route window。selectFn 为保留 range/lookback
			// 会对两段 s1 route 都下发完整 SelectHints 范围，如果同一后端两次都返回完整样本，
			// 非 avg merge 不能像两个独立 storage 一样直接按 timestamp 累加。
			fn: function.Sum,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(7, time.Unix(120, 0)),
						sample(11, time.Unix(320, 0)),
					},
					start: firstS1Start,
					end:   firstS1End,
				},
				{
					samples: []prompb.Sample{
						sample(7, time.Unix(120, 0)),
						sample(11, time.Unix(320, 0)),
					},
					start: secondS1Start,
					end:   secondS1End,
				},
			},
			expected: []prompb.Sample{
				sample(7, time.Unix(120, 0)),
				sample(11, time.Unix(320, 0)),
			},
		},
		"count 不应重复累计同 storage 回切窗口查回的完整 SelectHints 样本": {
			fn: function.Count,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(1, time.Unix(120, 0)),
						sample(1, time.Unix(320, 0)),
					},
					start: firstS1Start,
					end:   firstS1End,
				},
				{
					samples: []prompb.Sample{
						sample(1, time.Unix(120, 0)),
						sample(1, time.Unix(320, 0)),
					},
					start: secondS1Start,
					end:   secondS1End,
				},
			},
			expected: []prompb.Sample{
				sample(1, time.Unix(120, 0)),
				sample(1, time.Unix(320, 0)),
			},
		},
		"仅来自迁移重叠查询的候选样本不覆盖有效 route 样本": {
			fn: function.Sum,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(7, time.Unix(120, 0)),
					},
					start: firstS1Start,
					end:   firstS1End,
				},
				{
					samples: []prompb.Sample{
						sample(100, time.Unix(120, 0)),
						sample(11, time.Unix(320, 0)),
					},
					zeroRange: true,
				},
			},
			expected: []prompb.Sample{
				sample(7, time.Unix(120, 0)),
				sample(11, time.Unix(320, 0)),
			},
		},
		"单条 route 保留完整 SelectHints 中的 lookback 样本": {
			fn: function.Sum,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(5, time.Unix(90, 0)),
						sample(7, time.Unix(120, 0)),
					},
					start: firstS1Start,
					end:   firstS1End,
				},
			},
			expected: []prompb.Sample{
				sample(5, time.Unix(90, 0)),
				sample(7, time.Unix(120, 0)),
			},
		},
		"sum_over_time bucket 跨 route 切换时按 bucket 交集保留两段部分结果": {
			fn:   function.SumOT,
			step: 5 * time.Minute,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(2, time.Unix(0, 0)),
					},
					start: time.Unix(0, 0),
					end:   time.Unix(120, 0),
				},
				{
					samples: []prompb.Sample{
						sample(3, time.Unix(0, 0)),
					},
					start: time.Unix(120, 0),
					end:   time.Unix(300, 0),
				},
			},
			expected: []prompb.Sample{
				sample(5, time.Unix(0, 0)),
			},
		},
		"count_over_time bucket 跨 route 切换时按 bucket 交集保留两段部分结果": {
			fn:   function.CountOT,
			step: 5 * time.Minute,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(2, time.Unix(0, 0)),
					},
					start: time.Unix(0, 0),
					end:   time.Unix(120, 0),
				},
				{
					samples: []prompb.Sample{
						sample(3, time.Unix(0, 0)),
					},
					start: time.Unix(120, 0),
					end:   time.Unix(300, 0),
				},
			},
			expected: []prompb.Sample{
				sample(5, time.Unix(0, 0)),
			},
		},
		"plain avg fallback 也会先过滤 route 生效范围": {
			fn: function.Avg,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(10, time.Unix(120, 0)),
						sample(30, time.Unix(320, 0)),
					},
					start: firstS1Start,
					end:   firstS1End,
				},
				{
					samples: []prompb.Sample{
						sample(10, time.Unix(120, 0)),
						sample(30, time.Unix(320, 0)),
					},
					start: secondS1Start,
					end:   secondS1End,
				},
			},
			expected: []prompb.Sample{
				sample(10, time.Unix(120, 0)),
				sample(30, time.Unix(320, 0)),
			},
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
								{Name: "job", Value: "rollback-storage"},
							},
							Samples: route.samples,
						},
					},
				})
				if route.zeroRange {
					routeSet = function.NewZeroTimeRangeSeriesSet(routeSet)
				} else {
					routeSet = function.NewTimeRangeSeriesSet(routeSet, route.start, route.end)
				}
				sets = append(sets, routeSet)
			}

			set := storage.NewMergeSeriesSet(sets, function.NewMergeSeriesSetWithFuncAndSortByStep(tc.fn, tc.step))
			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)
			assert.Equal(t, mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{Name: "__name__", Value: "up"},
						{Name: "job", Value: "rollback-storage"},
					},
					Samples: tc.expected,
				},
			}, ts)
		})
	}
}

func TestMergeSeriesSetWithTimeWeightedAvg(t *testing.T) {
	var (
		bucketStart = time.UnixMilli(0)
		bucketStep  = 5 * time.Minute
		bucketEnd   = bucketStart.Add(bucketStep)
	)
	sample := func(value float64, timestamp time.Time) prompb.Sample {
		return prompb.Sample{
			Value:     value,
			Timestamp: timestamp.UnixMilli(),
		}
	}
	// 注释中的时间轴统一按 5 分钟 bucket 展示：
	// bucket: 表示当前待合并的统计桶，[0s, 300s)。
	// 生效路由: 表示该路由在当前时间段真实承载数据写入，可参与 avg 权重计算。
	// 无区间序列: 表示没有 route 时间范围的普通序列，按完整 bucket 参与权重。
	// 重叠查询部分: 表示迁移切换点前后为兜底边界样本额外查询的相邻存储，真实生效区间为 0。

	type routeSeries struct {
		value     float64
		samples   []prompb.Sample
		start     time.Time
		end       time.Time
		withRange bool
		zeroRange bool
	}

	testCases := map[string]struct {
		fn              string
		routes          []routeSeries
		withRange       bool
		step            time.Duration
		withoutStep     bool
		expected        float64
		expectedSamples []prompb.Sample
	}{
		"avg 按路由覆盖时长加权": {
			// 时间轴：
			// bucket:   [0s------------------------------300s)
			// 路由 A:   [0s----------132s) value=10
			// 路由 B:                 [132s--------------300s) value=30
			// 权重：路由 A 覆盖 132s，路由 B 覆盖 168s。
			// 结果：(10*132 + 30*168) / (132 + 168) = 21.2。Prometheus avg 结果保持 float64，不按输入是否为整数取整。
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
		"avg 按路由覆盖时长加权时遇到小数分段会保留浮点结果": {
			// 时间轴：
			// bucket:   [0s------------------------------300s)
			// 路由 A:   [0s----------132s) value=10
			// 路由 B:                 [132s--------------300s) value=30.5
			// 权重：只要参与加权的任一分段 avg 是小数，最终结果继续保留 float64。
			// 结果：(10*132 + 30.5*168) / (132 + 168) = 21.48
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketStart.Add(132 * time.Second),
				},
				{
					value: 30.5,
					start: bucketStart.Add(132 * time.Second),
					end:   bucketEnd,
				},
			},
			withRange: true,
			expected:  21.48,
		},
		"mean 按路由覆盖时长加权": {
			// 时间轴：
			// bucket:   [0s------------------------------300s)
			// 路由 A:   [0s----------132s) value=10
			// 路由 B:                 [132s--------------300s) value=30
			// mean 是 avg 的别名，同样按路由覆盖时长加权。
			// 结果：(10*132 + 30*168) / (132 + 168) = 21.2。mean 是 avg 的别名，同样保持 float64。
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
		"avg_over_time 按路由覆盖时长加权": {
			// 时间轴：
			// bucket:   [0s------------------------------300s)
			// 路由 A:   [0s----------132s) value=10
			// 路由 B:                 [132s--------------300s) value=30
			// avg_over_time 也是 avg 类函数，同样按路由覆盖时长加权。
			// 结果：(10*132 + 30*168) / (132 + 168) = 21.2。avg_over_time 也是 Prometheus float avg 语义。
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
		"avg_over_time 缺少 bucket 宽度时会按 timestamp 过滤后退化为普通平均": {
			// 时间轴：
			// bucket 宽度: 0，无法计算路由与 bucket 的覆盖时长
			// 路由 A:     [0s----------132s) value=10
			// 路由 B:                   [132s--------------300s) value=30
			// 权重：没有 bucket 宽度时，merge 层按样本 timestamp 过滤 route 生效范围，再退化为同 timestamp 普通平均。
			// 结果：30@0s 不在路由 B 生效范围内，最终只保留 10@0s。
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
			withRange:   true,
			withoutStep: true,
			expected:    10,
		},
		"路由覆盖时长相等时等同于普通平均": {
			// 时间轴：
			// bucket:   [0s------------------------------300s)
			// 路由 A:   [0s--------------150s) value=10
			// 路由 B:                       [150s-------300s) value=30
			// 权重：两段路由各覆盖 150s，加权平均应等同于普通平均。
			// 结果：(10*150 + 30*150) / (150 + 150) = 20
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
		"与当前 bucket 无交集的路由会被忽略": {
			// 时间轴：
			// 当前 bucket: [0s----------------------------300s)
			// 路由 A:      [0s-----------------------------300s) value=10
			// 路由 B:                                      [300s----------------600s) value=30
			// 权重：路由 B 从 300s 才开始，与当前 bucket 没有交集，当前 bucket 只使用路由 A。
			// 结果：(10*300) / 300 = 10
			fn: function.Avg,
			routes: []routeSeries{
				{
					value: 10,
					start: bucketStart,
					end:   bucketEnd,
				},
				{
					value: 30,
					start: bucketEnd, // 这一段开始是上一段的结束
					end:   bucketEnd.Add(bucketStep),
				},
			},
			withRange: true,
			expected:  10,
		},
		"缺少路由时间范围时回退到普通平均": {
			// 时间轴：
			// bucket:      [0s----------------------------300s)
			// 序列 A:      10@0s，无 route 时间范围
			// 序列 B:      30@0s，无 route 时间范围
			// 权重：两条序列都没有 route 时间范围，无法计算覆盖时长，回退到普通平均。
			// 结果：(10 + 30) / 2 = 20
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
		"无区间序列按完整 bucket 参与加权": {
			// 时间轴：
			// bucket:     [0s-----------------------------300s)
			// 生效路由:   [0s------------------------------300s) value=10
			// 无区间序列: value=30，按完整 bucket 参与权重
			// 权重：混合合并时，无 route 时间范围的普通序列不能丢弃，按 300s 参与加权。
			// 结果：(10*300 + 30*300) / (300 + 300) = 20
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
		"仅用于重叠查询的零时间范围路由会被忽略": {
			// 时间轴：
			// bucket:      [0s------------------------------300s)
			// 生效路由:    [0s-------------------------------300s) value=10
			// 重叠查询部分: value=30，仅兜底查相邻存储，真实生效区间为 0
			// 权重：重叠查询部分只有查询扩展范围，没有真实生效区间，不能参与加权。
			// 结果：(10*300) / 300 = 10
			fn: function.Avg,
			routes: []routeSeries{
				{
					value:     10,
					start:     bucketStart,
					end:       bucketEnd,
					withRange: true,
				},
				{
					value:     30,
					zeroRange: true,
				},
			},
			expected: 10,
		},
		"仅用于重叠查询的路由会保留生效路由缺失的样本": {
			// 时间轴：
			// bucket 1:    [0s------------------------------300s)
			// bucket 2:    [300s----------------------------600s)
			// 生效路由:    [10@0s------------------------------------------)
			// 重叠查询部分:                         30@300s，仅兜底查相邻存储，真实生效区间为 0
			// 权重：重叠查询部分不参与已有 bucket 的 avg 权重。
			// 样本：30@300s 是生效路由缺失的边界样本，最终结果仍然要保留。
			// 当前 bug：zero-range series 被整条 continue，导致 30@bucketStep 在检查 timestamp 前就被丢弃。
			fn: function.Avg,
			routes: []routeSeries{
				{
					samples: []prompb.Sample{
						sample(10, bucketStart),
					},
					start:     bucketStart,
					end:       bucketStart.Add(2 * bucketStep),
					withRange: true,
				},
				{
					samples: []prompb.Sample{
						sample(30, bucketStart.Add(bucketStep)),
					},
					zeroRange: true,
				},
			},
			expectedSamples: []prompb.Sample{
				sample(10, bucketStart),
				sample(30, bucketStart.Add(bucketStep)),
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			sets := make([]storage.SeriesSet, 0, len(tc.routes))
			for _, route := range tc.routes {
				samples := route.samples
				if samples == nil {
					samples = []prompb.Sample{
						sample(route.value, bucketStart),
					}
				}
				routeSet := remote.FromQueryResult(true, &prompb.QueryResult{
					Timeseries: []*prompb.TimeSeries{
						{
							Labels: []prompb.Label{
								{Name: "__name__", Value: "up"},
								{Name: "job", Value: "elasticsearch"},
							},
							Samples: samples,
						},
					},
				})
				if tc.withRange || route.withRange {
					routeSet = function.NewTimeRangeSeriesSet(routeSet, route.start, route.end)
				}
				if route.zeroRange {
					routeSet = function.NewZeroTimeRangeSeriesSet(routeSet)
				}
				sets = append(sets, routeSet)
			}

			step := tc.step
			if step == 0 && !tc.withoutStep {
				step = bucketStep
			}
			set := storage.NewMergeSeriesSet(sets, function.NewMergeSeriesSetWithFuncAndSortByStep(tc.fn, step))
			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)
			expectedSamples := tc.expectedSamples
			if expectedSamples == nil {
				expectedSamples = []prompb.Sample{
					sample(tc.expected, bucketStart),
				}
			}
			assert.Equal(t, mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{Name: "__name__", Value: "up"},
						{Name: "job", Value: "elasticsearch"},
					},
					Samples: expectedSamples,
				},
			}, ts)
		})
	}
}
