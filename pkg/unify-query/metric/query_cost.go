// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	queryCostRows = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_rows",
			Help:      "Rows returned to a UQ query leaf before time-series conversion.",
			Buckets:   prometheus.ExponentialBuckets(1, 10, 8),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostSeries = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_series",
			Help:      "Actual time series produced from a UQ query leaf.",
			Buckets:   prometheus.ExponentialBuckets(1, 10, 8),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostPoints = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_points",
			Help:      "Actual input points produced from a UQ query leaf.",
			Buckets:   prometheus.ExponentialBuckets(1, 10, 9),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostLabelBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_label_bytes",
			Help:      "Estimated bytes in label names and values produced from a UQ query leaf.",
			Buckets:   prometheus.ExponentialBuckets(1024, 8, 8),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostSeriesRowsRatio = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_series_rows_ratio",
			Help:      "Ratio of actual series to returned rows for a UQ query leaf.",
			Buckets:   []float64{0, 0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 1},
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostPointsPerSeries = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_points_per_series",
			Help:      "Average effective points per actual time series for a UQ query leaf.",
			Buckets:   prometheus.ExponentialBuckets(1, 4, 10),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostASTBranches = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_ast_branches",
			Help:      "Selector branch count in the source PromQL expression.",
			Buckets:   []float64{1, 2, 4, 8, 16, 32},
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostWindowSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_window_seconds",
			Help:      "PromQL range-function window in seconds.",
			Buckets:   prometheus.ExponentialBuckets(1, 4, 10),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryCostStepSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_cost_step_seconds",
			Help:      "PromQL evaluation step in seconds.",
			Buckets:   prometheus.ExponentialBuckets(1, 4, 10),
		},
		[]string{"select_all", "range_function", "step_less_than_window", "sql_pushdown"},
	)
	queryResultPointCapacity = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_result_point_capacity",
			Help:      "Total backing-slice capacity of points in a PromQL result.",
			Buckets:   prometheus.ExponentialBuckets(1, 10, 9),
		},
		[]string{"query_type"},
	)
	queryResultPointCapacityRatio = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_result_point_capacity_ratio",
			Help:      "Ratio of total PromQL result point capacity to effective result points.",
			Buckets:   []float64{1, 1.1, 1.25, 1.5, 2, 4, 8, 16, 64, 256, 1024},
		},
		[]string{"query_type"},
	)
)

// QueryCostProfileObserve records bounded-cardinality observations only. It
// deliberately has no rejection or admission side effects.
func QueryCostProfileObserve(
	ctx context.Context,
	selectAll, rangeFunction, stepLessThanWindow, sqlPushdown bool,
	astBranches, rows, series, points, labelBytes int,
	window, step time.Duration,
) {
	labels := []string{
		strconv.FormatBool(selectAll),
		strconv.FormatBool(rangeFunction),
		strconv.FormatBool(stepLessThanWindow),
		strconv.FormatBool(sqlPushdown),
	}
	observe(ctx, queryCostRows.WithLabelValues(labels...), float64(rows))
	observe(ctx, queryCostSeries.WithLabelValues(labels...), float64(series))
	observe(ctx, queryCostPoints.WithLabelValues(labels...), float64(points))
	observe(ctx, queryCostLabelBytes.WithLabelValues(labels...), float64(labelBytes))
	observe(ctx, queryCostASTBranches.WithLabelValues(labels...), float64(astBranches))
	if window > 0 {
		observe(ctx, queryCostWindowSeconds.WithLabelValues(labels...), window.Seconds())
	}
	if step > 0 {
		observe(ctx, queryCostStepSeconds.WithLabelValues(labels...), step.Seconds())
	}
	if rows > 0 {
		observe(ctx, queryCostSeriesRowsRatio.WithLabelValues(labels...), float64(series)/float64(rows))
	}
	if series > 0 {
		observe(ctx, queryCostPointsPerSeries.WithLabelValues(labels...), float64(points)/float64(series))
	}
}

// QueryResultCapacityObserve records len and cap separately without imposing a
// capacity threshold.
func QueryResultCapacityObserve(ctx context.Context, queryType string, points, capacity int) {
	switch queryType {
	case "range", "instant":
	default:
		queryType = "unknown"
	}
	observe(ctx, queryResultPointCapacity.WithLabelValues(queryType), float64(capacity))
	if points > 0 {
		observe(
			ctx,
			queryResultPointCapacityRatio.WithLabelValues(queryType),
			float64(capacity)/float64(points),
		)
	}
}
