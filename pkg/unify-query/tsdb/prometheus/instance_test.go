// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var (
	defaultMetric      = "metric123456789"
	defaultStorageID   = "test"
	defaultClusterName = "test"
	defaultStart       = time.Unix(0, 0)
	testTag            = "kafka"
)

type queryable struct{}

var _ storage.Queryable = (*queryable)(nil)

func (q *queryable) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	return &querier{}, nil
}

func TestMergeBucketDuration(t *testing.T) {
	testCases := map[string]struct {
		name     string
		queries  QueryList
		fallback time.Duration
		expected time.Duration
	}{
		"函数匹配时使用聚合窗口": {
			name: "avg_over_time",
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "sum"},
							{Name: "avg_over_time", Window: time.Minute},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: time.Minute,
		},
		"普通 avg 没有聚合窗口时不使用 bucket 宽度": {
			name: "avg",
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "avg"},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: 0,
		},
		"hint 中的 avg_over_time 缺少聚合窗口时按原始样本处理": {
			name: "avg_over_time",
			queries: QueryList{
				{
					qry: &metadata.Query{},
				},
			},
			fallback: time.Minute,
			expected: 0,
		},
		"hint 中的 avg_over_time 缺少聚合窗口时不使用查询步长": {
			name: "avg_over_time",
			queries: QueryList{
				{
					qry: &metadata.Query{},
				},
			},
			fallback: time.Minute,
			expected: 0,
		},
		"hint 中的 sum_over_time 缺少聚合窗口时按原始样本处理": {
			name: "sum_over_time",
			queries: QueryList{
				{
					qry: &metadata.Query{},
				},
			},
			fallback: time.Minute,
			expected: 0,
		},
		"hint 使用 avg 别名时仍匹配 avg_over_time 窗口": {
			name: "avg",
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "avg_over_time", Window: time.Minute},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: time.Minute,
		},
		"plain count 有聚合窗口时使用该窗口": {
			name: function.Count,
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Count, Window: time.Minute},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: time.Minute,
		},
		"plain sum 没有聚合窗口时不使用查询步长当 bucket": {
			name: function.Sum,
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Sum},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: 0,
		},
		"函数不匹配时回退到查询步长": {
			name: "increase",
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "avg_over_time", Window: time.Minute},
						},
					},
				},
			},
			fallback: 5 * time.Minute,
			expected: 5 * time.Minute,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.queries.mergeBucketDuration(tc.name, tc.fallback))
		})
	}
}

func TestClonePromQLResultsBreaksPooledSliceAndHistogramAliases(t *testing.T) {
	hist := &histogram.FloatHistogram{Count: 1}
	matrix := promql.Matrix{{
		Metric: labels.FromStrings("key", "value"),
		Points: []promql.Point{{T: 1, H: hist}},
	}}
	vector := promql.Vector{{
		Metric: labels.FromStrings("key", "value"),
		Point:  promql.Point{T: 1, H: hist},
	}}

	clonedMatrix := clonePromQLMatrix(matrix)
	clonedVector := clonePromQLVector(vector)
	matrix[0].Metric[0].Value = "changed"
	matrix[0].Points[0].T = 2
	hist.Count = 2

	assert.Equal(t, "value", clonedMatrix[0].Metric[0].Value)
	assert.Equal(t, int64(1), clonedMatrix[0].Points[0].T)
	assert.Equal(t, float64(1), clonedMatrix[0].Points[0].H.Count)
	assert.Equal(t, "value", clonedVector[0].Metric[0].Value)
	assert.Equal(t, int64(1), clonedVector[0].Point.T)
	assert.Equal(t, float64(1), clonedVector[0].Point.H.Count)
	assert.NotSame(t, hist, clonedMatrix[0].Points[0].H)
	assert.NotSame(t, hist, clonedVector[0].Point.H)
}

func TestSortAndLimitLabelValues(t *testing.T) {
	values := []string{"c", "a", "b"}
	assert.Equal(t, []string{"a", "b"}, sortAndLimitLabelValues(values, 2))
	assert.Equal(t, []string{"a", "b", "c"}, sortAndLimitLabelValues(values, 0))
}

func TestQueryListAllowRouteStartBoundaryBucketPerSource(t *testing.T) {
	newQuery := func(tableID string, start, end, queryStart, queryEnd int64) *Query {
		return &Query{
			qry: &metadata.Query{
				DataSource: "bkmonitor",
				TableID:    tableID,
				Field:      "value",
			},
			start:      time.Unix(start, 0),
			end:        time.Unix(end, 0),
			queryStart: time.Unix(queryStart, 0),
			queryEnd:   time.Unix(queryEnd, 0),
		}
	}

	t.Run("同一 reference 的独立 RT 各自保留首 bucket", func(t *testing.T) {
		first := newQuery("first.rt", 100, 200, 40, 260)
		second := newQuery("second.rt", 100, 200, 40, 260)
		queries := QueryList{first, second}

		assert.True(t, queries.allowRouteStartBoundaryBucket(first))
		assert.True(t, queries.allowRouteStartBoundaryBucket(second))
	})

	t.Run("同一 source 的后续 route 禁用首 bucket 例外", func(t *testing.T) {
		first := newQuery("shared.rt", 100, 150, 40, 150)
		second := newQuery("shared.rt", 150, 200, 150, 260)
		queries := QueryList{first, second}

		assert.True(t, queries.allowRouteStartBoundaryBucket(first))
		assert.False(t, queries.allowRouteStartBoundaryBucket(second))
	})

	t.Run("同一 source 的 overlap-only 前路由也算相邻 route", func(t *testing.T) {
		previous := newQuery("shared.rt", 0, 0, 40, 150)
		current := newQuery("shared.rt", 150, 200, 150, 260)
		queries := QueryList{previous, current}

		assert.False(t, queries.allowRouteStartBoundaryBucket(current))
	})

	t.Run("同一 source 的并行完整范围不是相邻时间 route", func(t *testing.T) {
		first := newQuery("shared.rt", 100, 200, 40, 260)
		second := newQuery("shared.rt", 100, 200, 40, 260)
		queries := QueryList{first, second}

		assert.True(t, queries.allowRouteStartBoundaryBucket(first))
		assert.True(t, queries.allowRouteStartBoundaryBucket(second))
	})
}

func TestMergeFuncName(t *testing.T) {
	testCases := map[string]struct {
		hints    *storage.SelectHints
		queries  QueryList
		expected string
	}{
		"没有下推 avg 时使用 Prometheus hint": {
			hints: &storage.SelectHints{
				Func: "max_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{},
				},
			},
			expected: "max_over_time",
		},
		"sum_over_time 下推后使用 forward sum bucket": {
			hints: &storage.SelectHints{
				Func: function.SumOT,
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Sum, Window: time.Minute},
						},
					},
				},
			},
			expected: function.Sum,
		},
		"avg_over_time 下推后使用 forward avg bucket": {
			hints: &storage.SelectHints{
				Func: function.AvgOT,
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Avg, Window: time.Minute},
						},
					},
				},
			},
			expected: function.Avg,
		},
		"over_time 没有匹配的下推窗口时保留 Prometheus hint": {
			hints: &storage.SelectHints{
				Func: function.SumOT,
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Avg, Window: time.Minute},
						},
					},
				},
			},
			expected: function.SumOT,
		},
		"over_time 下推聚合没有窗口时保留 Prometheus hint": {
			hints: &storage.SelectHints{
				Func: function.SumOT,
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Sum},
						},
					},
				},
			},
			expected: function.SumOT,
		},
		"last_over_time 仅用于回看窗口时优先使用下推 avg": {
			hints: &storage.SelectHints{
				Func: "last_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "avg"},
						},
					},
				},
			},
			expected: "avg",
		},
		"last_over_time 仅用于回看窗口时优先使用下推 count": {
			hints: &storage.SelectHints{
				Func: "last_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Count, Window: time.Minute},
							{Name: function.Sum},
						},
					},
				},
			},
			expected: function.Count,
		},
		"last_over_time 仅用于回看窗口时优先使用下推 sum": {
			hints: &storage.SelectHints{
				Func: "last_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Sum, Window: time.Minute},
						},
					},
				},
			},
			expected: function.Sum,
		},
		"last_over_time 仅用于回看窗口时优先使用下推 min": {
			hints: &storage.SelectHints{
				Func: "last_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Min, Window: time.Minute},
						},
					},
				},
			},
			expected: function.Min,
		},
		"last_over_time 仅用于回看窗口时优先使用下推 max": {
			hints: &storage.SelectHints{
				Func: "last_over_time",
			},
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: function.Max, Window: time.Minute},
						},
					},
				},
			},
			expected: function.Max,
		},
		"hint 为空时使用下推聚合函数": {
			queries: QueryList{
				{
					qry: &metadata.Query{
						Aggregates: metadata.Aggregates{
							{Name: "avg_over_time"},
						},
					},
				},
			},
			expected: "avg_over_time",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.queries.mergeFuncName(tc.hints))
		})
	}
}

func TestPushedOverTimeKeepsForwardBucketAtRouteStart(t *testing.T) {
	previous := &Query{
		qry: &metadata.Query{
			DataSource: "bkmonitor",
			TableID:    "shared.rt",
			Field:      "value",
			Aggregates: metadata.Aggregates{
				{Name: function.Sum, Window: time.Minute},
			},
		},
		start: time.Unix(0, 0),
		end:   time.Unix(100, 0),
	}
	current := &Query{
		qry:        previous.qry,
		start:      time.Unix(100, 0),
		end:        time.Unix(200, 0),
		queryStart: time.Unix(100, 0),
		queryEnd:   time.Unix(200, 0),
	}
	queries := QueryList{previous, current}
	mergeFunc := queries.mergeFuncName(&storage.SelectHints{Func: function.SumOT})

	assert.Equal(t, function.Sum, mergeFunc)
	assert.False(t, queries.allowRouteStartBoundaryBucket(current))

	set := promRemote.FromQueryResult(true, &prompb.QueryResult{
		Timeseries: []*prompb.TimeSeries{
			{
				Labels: []prompb.Label{
					{Name: "__name__", Value: "requests_total"},
				},
				Samples: []prompb.Sample{
					{Timestamp: time.Unix(100, 0).UnixMilli(), Value: 42},
				},
			},
		},
	})
	set = function.NewTimeRangeSeriesSet(set, current.start, current.end)
	set = function.NewRouteRangeFilterSeriesSet(set, mergeFunc, time.Minute)

	ts, err := mock.SeriesSetToTimeSeries(set)
	assert.NoError(t, err)
	assert.Equal(t, mock.TimeSeriesList{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "requests_total"},
			},
			Samples: []prompb.Sample{
				{Timestamp: time.Unix(100, 0).UnixMilli(), Value: 42},
			},
		},
	}, ts)
}

func TestQueryCalcSelectStrategy(t *testing.T) {
	testCases := map[string]struct {
		start           time.Time
		end             time.Time
		routeStart      time.Time
		routeEnd        time.Time
		routeQueryStart time.Time
		routeQueryEnd   time.Time
		expected        querySelectStrategy
		expectedOK      bool
	}{
		"查询范围命中 route 查询窗口时保留原始 SelectHints 范围": {
			start:           time.Unix(120, 0),
			end:             time.Unix(180, 0),
			routeQueryStart: time.Unix(100, 0),
			routeQueryEnd:   time.Unix(200, 0),
			expected: querySelectStrategy{
				queryStart:  time.Unix(120, 0),
				queryEnd:    time.Unix(180, 0),
				weightStart: time.Unix(120, 0),
				weightEnd:   time.Unix(180, 0),
				wrapKind:    seriesSetWrapZeroRouteRange,
			},
			expectedOK: true,
		},
		"overlap-only route 即使未命中 SelectHints 也保留候选查询": {
			start:           time.Unix(100, 0),
			end:             time.Unix(200, 0),
			routeQueryStart: time.Unix(200, 0),
			routeQueryEnd:   time.Unix(300, 0),
			expected: querySelectStrategy{
				queryStart:  time.Unix(100, 0),
				queryEnd:    time.Unix(200, 0),
				weightStart: time.Unix(100, 0),
				weightEnd:   time.Unix(200, 0),
				wrapKind:    seriesSetWrapZeroRouteRange,
			},
			expectedOK: true,
		},
		"有效 route 未命中 route 查询窗口时跳过该路由": {
			start:           time.Unix(100, 0),
			end:             time.Unix(200, 0),
			routeStart:      time.Unix(200, 0),
			routeEnd:        time.Unix(300, 0),
			routeQueryStart: time.Unix(200, 0),
			routeQueryEnd:   time.Unix(300, 0),
			expectedOK:      false,
		},
		"查询范围部分超出 route 查询窗口时仍保留完整 SelectHints 范围": {
			// 对于已选中的路由，SelectHints.Start/End 是 PromQL 引擎计算表达式实际需要的取数范围。
			// routeQueryStart/End 只用来判断这一路是否需要查询，不能把已经扩展过的 SelectHints 再裁窄。
			start:           time.Unix(90, 0),
			end:             time.Unix(210, 0),
			routeQueryStart: time.Unix(100, 0),
			routeQueryEnd:   time.Unix(200, 0),
			expected: querySelectStrategy{
				queryStart:  time.Unix(90, 0),
				queryEnd:    time.Unix(210, 0),
				weightStart: time.Unix(90, 0),
				weightEnd:   time.Unix(210, 0),
				wrapKind:    seriesSetWrapZeroRouteRange,
			},
			expectedOK: true,
		},
		"有真实 route 生效范围时使用生效范围计算权重": {
			start:           time.Unix(90, 0),
			end:             time.Unix(210, 0),
			routeStart:      time.Unix(120, 0),
			routeEnd:        time.Unix(180, 0),
			routeQueryStart: time.Unix(60, 0),
			routeQueryEnd:   time.Unix(240, 0),
			expected: querySelectStrategy{
				queryStart:  time.Unix(90, 0),
				queryEnd:    time.Unix(210, 0),
				weightStart: time.Unix(120, 0),
				weightEnd:   time.Unix(180, 0),
				wrapKind:    seriesSetWrapValidRouteRange,
			},
			expectedOK: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			query := &Query{
				start:      tc.routeStart,
				end:        tc.routeEnd,
				queryStart: tc.routeQueryStart,
				queryEnd:   tc.routeQueryEnd,
			}
			strategy, ok := query.calcSelectStrategy(tc.start, tc.end)
			assert.Equal(t, tc.expectedOK, ok)
			if !tc.expectedOK {
				return
			}
			assert.Equal(t, tc.expected, strategy)
		})
	}
}

func TestQueryCalcSelectStrategyWithMergeContext(t *testing.T) {
	query := &Query{
		start:      time.Unix(300, 0),
		end:        time.Unix(360, 0),
		queryStart: time.Unix(0, 0),
		queryEnd:   time.Unix(360, 0),
	}

	strategy, ok := query.calcSelectStrategyWithMergeContext(time.Unix(0, 0), time.Unix(360, 0), "avg_over_time")
	assert.True(t, ok)
	assert.Equal(t, querySelectStrategy{
		queryStart:  time.Unix(0, 0),
		queryEnd:    time.Unix(360, 0),
		weightStart: time.Unix(0, 0),
		weightEnd:   time.Unix(360, 0),
		wrapKind:    seriesSetWrapValidRouteRange,
	}, strategy)
}

func TestIntersectTimeRangePreservesSelectHintsLongLookback(t *testing.T) {
	testCases := map[string]struct {
		hintStart       time.Time
		hintEnd         time.Time
		routeQueryStart time.Time
		routeQueryEnd   time.Time
	}{
		"long lookback extends before route query start": {
			// 这里的问题是 SelectHints.Start/End 表示 PromQL 引擎计算表达式实际需要的取数范围。
			// 对 avg_over_time(metric[2h]) 这类 range selector，第一个计算点需要查询开始时间之前的 2h 样本，
			// 因此 SelectHints.Start 会早于用户查询开始时间，也可能早于迁移路由的 1h overlap 查询窗口。
			// routeQueryStart/End 只是迁移路由为了多查相邻 storage 设置的 overlap 判断范围，
			// 它不能反过来把 SelectHints 已经扩展出来的长 lookback 范围裁掉。
			hintStart:       time.Unix(0, 0),
			hintEnd:         time.Unix(200, 0),
			routeQueryStart: time.Unix(100, 0),
			routeQueryEnd:   time.Unix(260, 0),
		},
		"select hint end can also extend beyond route query end": {
			// 右边界同样保持 SelectHints 原值：如果上层已经决定这个 storage route 需要查询，
			// 这里不应该再把实际取数范围裁回 routeQueryEnd，否则可能破坏 range/lookback 所需样本。
			hintStart:       time.Unix(100, 0),
			hintEnd:         time.Unix(300, 0),
			routeQueryStart: time.Unix(40, 0),
			routeQueryEnd:   time.Unix(200, 0),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			query := &Query{
				queryStart: tc.routeQueryStart,
				queryEnd:   tc.routeQueryEnd,
			}

			strategy, ok := query.calcSelectStrategy(tc.hintStart, tc.hintEnd)
			assert.True(t, ok)
			assert.Equal(t, tc.hintStart, strategy.queryStart)
			assert.Equal(t, tc.hintEnd, strategy.queryEnd)
		})
	}
}

type querier struct{}

// LabelValues 返回可能的标签(维度)值。
// 在查询器的生命周期以外使用这些字符串是不安全的
func (qr *querier) LabelValues(name string, matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	return nil, nil, nil
}

// LabelNames 以块中的排序顺序返回所有的唯一的标签
func (qr *querier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	return nil, nil, nil
}

// Close 释放查询器的所有资源
func (qr *querier) Close() error {
	return nil
}

func (qr *querier) Select(sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	ts := []*prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "+Inf",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "0.01",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "0.5",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "1",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "2",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "5",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "10",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "30",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []prompb.Label{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "60",
				},
			},
			Samples: []prompb.Sample{
				{Timestamp: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{Timestamp: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
	}
	data := &prompb.QueryResult{
		Timeseries: ts,
	}

	return promRemote.FromQueryResult(true, data)
}

func TestQueryRange(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	timeout := time.Second * 300
	engine := promql.NewEngine(promql.EngineOpts{
		Reg:        prometheus.DefaultRegisterer,
		Timeout:    timeout,
		MaxSamples: 1e10,
	})
	ins := NewInstance(ctx, engine, &queryable{}, 0, 100)

	testCases := map[string]struct {
		db          string
		measurement string
		field       string

		q        string
		start    time.Time
		end      time.Time
		step     time.Duration
		expected string
	}{
		"a1": {
			q:     fmt.Sprintf("count(%s) by (%s)", defaultMetric, testTag),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345"} =>
5 @[60000]
5 @[120000]
5 @[180000]
5 @[240000]
5 @[300000]
5 @[360000]
5 @[420000]
5 @[480000]
5 @[540000]
5 @[600000]
{kafka="7890"} =>
4 @[60000]
4 @[120000]
4 @[180000]
4 @[240000]
4 @[300000]
4 @[360000]
4 @[420000]
4 @[480000]
4 @[540000]
4 @[600000]`,
		},
		"a2": {
			q:     fmt.Sprintf(`avg(idelta(%s[2m])) by (%s) != bool 0`, defaultMetric, testTag),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
{kafka="7890"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]`,
		},
		"a3": {
			q:     fmt.Sprintf(`histogram_quantile(0.95, max by (le) (rate(%s[2m])))`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{} =>
NaN @[120000]
NaN @[180000]
NaN @[240000]
NaN @[300000]
NaN @[360000]`,
		},
		"a4": {
			q:     fmt.Sprintf(`label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{__name__="metric123456789", kafka="12345", le="+Inf", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="0.01", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="0.5", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="1", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="2", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="10", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="30", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="5", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="60", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]`,
		},
		"a5": {
			q:     fmt.Sprintf(`delta(label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")[2m:1m])`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345", le="+Inf", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.01", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="1", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="2", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="10", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="30", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="60", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
		"a6": {
			q:     fmt.Sprintf(`sum(delta(label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")[2m:1m])) by (metric_name, le)`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{le="+Inf", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="0.01", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="0.5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="1", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="10", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="2", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="30", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{le="60", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
		"a7": {
			q:     fmt.Sprintf(`sum(label_replace(delta({__name__="%s"}[2m:1m]), "metric_name", "$1", "__name__", "metric(.*)")) by (metric_name)`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
		"test": {
			q:     fmt.Sprintf(`rate(%s[1m])`, defaultMetric),
			start: time.UnixMilli(1723781659695),
			end:   time.UnixMilli(1723784726058),
			step:  time.Minute,
		},
		"now": {
			q:     fmt.Sprintf(`rate(%s[1m])`, defaultMetric),
			start: time.Unix(1723788909, 0),
			end:   time.Unix(1723792509, 0),
			step:  time.Minute,
		},
	}

	for k, c := range testCases {
		t.Run(k, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			res, _, err := ins.DirectQueryRange(ctx, c.q, c.start, c.end, c.step)

			a := res.String()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, a)
		})
	}
}

// rangeAggregateTestStorage 直接返回存储侧序列，不模拟文档查询和聚合。
type rangeAggregateTestStorage struct {
	tsdb.DefaultInstance
	selectSeries func(*metadata.Query, time.Time, time.Time) storage.SeriesSet
}

func (s *rangeAggregateTestStorage) QuerySeriesSet(_ context.Context, q *metadata.Query, start, end time.Time) storage.SeriesSet {
	return s.selectSeries(q, start, end)
}

func TestSelectRangeAggregate(t *testing.T) {
	mock.Init()
	start := time.Unix(1700000000, 0)
	cut := start.Add(time.Hour)
	end := cut.Add(time.Hour)

	type storageResponse struct {
		method       string
		start, end   time.Time
		endExclusive bool
		series       mock.TimeSeriesList
	}
	// 路由按秒配置，样本时间戳为毫秒；查询范围仍保留微秒精度。
	// 无时间分桶时，存储返回的单点是区间聚合结果，不能按该点的虚拟时间戳再裁剪。
	testCases := map[string]struct {
		method     string
		start, end time.Time
		routes     query.StorageClusterRecords
		storages   map[string][]storageResponse
		want       mock.TimeSeriesList
	}{
		"切换前仅查询旧存储": {
			method: "count",
			start:  start,
			end:    cut.Add(-time.Microsecond),
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "count",
						start:        start,
						end:          cut.Add(-time.Microsecond),
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 2},
					},
				},
			},
		},
		"切换后仅查询新存储": {
			method: "count",
			start:  cut,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"新存储": {
					{
						method:       "count",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.UnixMilli(), Value: 3},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.UnixMilli(), Value: 1},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.UnixMilli(), Value: 3},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.UnixMilli(), Value: 1},
					},
				},
			},
		},
		"跨切换点计数保留两侧序列": {
			method: "count",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "count",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "count",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 3},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 1},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 5},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 1},
					},
				},
			},
		},
		"跨切换点求和不重复": {
			method: "sum",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "sum",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 30},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "sum",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 120},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 7},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 150},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 7},
					},
				},
			},
		},
		"跨切换点取最小值": {
			method: "min",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "min",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 10},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "min",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 30},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 7},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 10},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 7},
					},
				},
			},
		},
		"跨切换点取最大值": {
			method: "max",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "max",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 20},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "max",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 50},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 7},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 50},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 7},
					},
				},
			},
		},
		"跨切换点按有效值数量加权平均": {
			method: "avg",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "sum",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 30},
								},
							},
						},
					},
					{
						method:       "count",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "sum",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 120},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 7},
								},
							},
						},
					},
					{
						method:       "count",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 3},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 1},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 30},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 7},
					},
				},
			},
		},
		"跨切换点无分组计数": {
			method: "count",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "count",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "count",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 5},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 7},
					},
				},
			},
		},
		"跨切换点无分组加权平均": {
			method: "avg",
			start:  start,
			end:    end,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "sum",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 30},
								},
							},
						},
					},
					{
						method:       "count",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "sum",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 127},
								},
							},
						},
					},
					{
						method:       "count",
						start:        cut,
						end:          end,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 4},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 157.0 / 6},
					},
				},
			},
		},
		"结束时间等于切换点": {
			method: "count",
			start:  start,
			end:    cut,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "count",
						start:        start,
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 2},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "count",
						start:        cut,
						end:          cut,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 1},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: start.UnixMilli(), Value: 1},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 3},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: start.UnixMilli(), Value: 1},
					},
				},
			},
		},
		"仅查询切换点": {
			method: "count",
			start:  cut,
			end:    cut,
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"新存储": {
					{
						method:       "count",
						start:        cut,
						end:          cut,
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.UnixMilli(), Value: 1},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.UnixMilli(), Value: 1},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.UnixMilli(), Value: 1},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.UnixMilli(), Value: 1},
					},
				},
			},
		},
		"切换点前后各一微秒": {
			method: "sum",
			start:  cut.Add(-time.Microsecond),
			end:    cut.Add(time.Microsecond),
			routes: query.StorageClusterRecords{
				{StorageID: "旧存储", EnableTime: start.Add(-time.Hour).Unix()},
				{StorageID: "新存储", EnableTime: cut.Unix()},
			},
			storages: map[string][]storageResponse{
				"旧存储": {
					{
						method:       "sum",
						start:        cut.Add(-time.Microsecond),
						end:          cut,
						endExclusive: true,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.Add(-time.Microsecond).UnixMilli(), Value: 20},
								},
							},
						},
					},
				},
				"新存储": {
					{
						method:       "sum",
						start:        cut,
						end:          cut.Add(time.Microsecond),
						endExclusive: false,
						series: mock.TimeSeriesList{
							{
								Labels: []prompb.Label{{Name: "service", Value: "api"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.Add(-time.Microsecond).UnixMilli(), Value: 70},
								},
							},
							{
								Labels: []prompb.Label{{Name: "service", Value: "worker"}},
								Samples: []prompb.Sample{
									{Timestamp: cut.Add(-time.Microsecond).UnixMilli(), Value: 7},
								},
							},
						},
					},
				},
			},
			want: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{{Name: "service", Value: "api"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.Add(-time.Microsecond).UnixMilli(), Value: 90},
					},
				},
				{
					Labels: []prompb.Label{{Name: "service", Value: "worker"}},
					Samples: []prompb.Sample{
						{Timestamp: cut.Add(-time.Microsecond).UnixMilli(), Value: 7},
					},
				},
			},
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			metadata.GetQueryParams(ctx).
				SetTime(c.start, c.start, c.end, time.Minute, "ms", "UTC").
				SetIsReference(true)
			router := query.TsDBV2{
				StorageType:           metadata.ElasticsearchStorageType,
				StorageClusterRecords: c.routes,
			}
			type storageQuery struct {
				storageID, method string
				start, end        time.Time
				endExclusive      bool
			}
			var wantQueries, gotQueries []storageQuery
			for storageID, responses := range c.storages {
				for _, response := range responses {
					wantQueries = append(wantQueries, storageQuery{storageID, response.method, response.start, response.end, response.endExclusive})
				}
			}
			var mu sync.Mutex
			var queries QueryList
			for _, route := range router.GetStorageIDRangesWithDirectionalOverlap(c.start, c.end, 0, 0) {
				storageID := route.StorageID
				backend := &rangeAggregateTestStorage{
					selectSeries: func(q *metadata.Query, from, to time.Time) storage.SeriesSet {
						method := q.Aggregates.OuterAggName()
						mu.Lock()
						gotQueries = append(gotQueries, storageQuery{q.StorageID, method, from, to, q.RangeEndExclusive})
						mu.Unlock()
						assert.True(t, q.IsRangeAggregation)
						assert.Equal(t, storageID, q.StorageID)
						for _, response := range c.storages[storageID] {
							if response.method != method || !response.start.Equal(from) || !response.end.Equal(to) || response.endExclusive != q.RangeEndExclusive {
								continue
							}
							result := &prompb.QueryResult{}
							for i := range response.series {
								result.Timeseries = append(result.Timeseries, &response.series[i])
							}
							return promRemote.FromQueryResult(true, result)
						}
						return storage.ErrSeriesSet(fmt.Errorf("%s 收到未预期的查询：%s [%s,%s] 排他上界=%t", storageID, method, from, to, q.RangeEndExclusive))
					},
				}
				queries = append(queries, &Query{
					instance: backend,
					qry: &metadata.Query{
						StorageID:   storageID,
						StorageType: metadata.ElasticsearchStorageType,
						Field:       "value",
						Aggregates:  metadata.Aggregates{{Name: c.method}},
					},
					start:        route.Start,
					end:          route.End,
					endInclusive: route.EndInclusive,
					queryStart:   route.QueryStart,
					queryEnd:     route.QueryEnd,
				})
			}
			require.True(t, queries.hasRoutedRangeAggregate())
			querier := NewQuerier(ctx, c.start, c.end, 4, time.Second)
			actual, err := mock.SeriesSetToTimeSeries(querier.selectRangeAggregate(ctx, queries))
			require.NoError(t, err)
			assert.ElementsMatch(t, wantQueries, gotQueries)
			assert.ElementsMatch(t, c.want, actual)
			assert.Nil(t, metadata.GetStatus(ctx))
		})
	}
}
