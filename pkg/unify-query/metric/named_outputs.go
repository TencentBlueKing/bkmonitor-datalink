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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

const (
	NamedOutputsRequestReceived = "received"
	NamedOutputsRequestSuccess  = "success"
	NamedOutputsRequestError    = "error"

	NamedOutputsOutputSuccess      = "success"
	NamedOutputsOutputSuccessEmpty = "success_empty"
	NamedOutputsOutputPartial      = "partial"
	NamedOutputsOutputError        = "error"

	NamedOutputsSelectorCacheHit  = "hit"
	NamedOutputsSelectorCacheMiss = "miss"
	NamedOutputsSelectorCacheWait = "wait"

	NamedOutputsSelectorCacheSuccess = "success"
	NamedOutputsSelectorCachePartial = "partial"
	NamedOutputsSelectorCacheError   = "error"

	NamedOutputsModeDirect     = "direct"
	NamedOutputsModePromEngine = "prom_engine"

	NamedOutputsRejectValidation          = "validation"
	NamedOutputsRejectUnsupportedContract = "unsupported_contract"
	NamedOutputsRejectOutputLimit         = "output_limit"
	NamedOutputsRejectCapacity            = "capacity"
	NamedOutputsRejectDeadline            = "deadline"
)

var (
	namedOutputsRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_requests_total",
			Help:      "named output requests by a fixed result set",
		},
		[]string{"result", "version", "commit_id"},
	)
	namedOutputsOutputStatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_output_states_total",
			Help:      "named output results by a fixed state set",
		},
		[]string{"state", "version", "commit_id"},
	)
	namedOutputsSelectorCacheEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_selector_cache_events_total",
			Help:      "request-local named output selector cache events",
		},
		[]string{"event", "version", "commit_id"},
	)
	namedOutputsSelectorCacheEntriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_selector_cache_entries_total",
			Help:      "materialized named output selector cache entries by typed state",
		},
		[]string{"state", "version", "commit_id"},
	)
	namedOutputsOutputCount = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_output_count",
			Help:      "number of requested named outputs",
			Buckets:   []float64{1, 2, 3, 4},
		},
		[]string{"version", "commit_id"},
	)
	namedOutputsDownstreamCalls = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_downstream_calls",
			Help:      "downstream calls per named output request by fixed execution mode",
			Buckets:   []float64{0, 1, 2, 3, 4, 8, 16},
		},
		[]string{"mode", "version", "commit_id"},
	)
	namedOutputsDownstreamAmplificationRatio = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_downstream_amplification_ratio",
			Help:      "actual selector loads or direct calls divided by named output count",
			Buckets:   []float64{0, 0.25, 0.5, 0.75, 1, 2, 4},
		},
		[]string{"mode", "version", "commit_id"},
	)
	namedOutputsResponseBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_response_bytes",
			Help:      "encoded named output response size",
			Buckets:   bytesBuckets,
		},
		[]string{"version", "commit_id"},
	)
	namedOutputsDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_duration_seconds",
			Help:      "named output request duration by a fixed result set",
			Buckets:   secondsBuckets,
		},
		[]string{"result", "version", "commit_id"},
	)
	namedOutputsResultSeries = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_result_series",
			Help:      "number of returned series in a named output response",
			Buckets:   []float64{0, 1, 10, 100, 1000, 10000},
		},
		[]string{"version", "commit_id"},
	)
	namedOutputsResultPoints = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_result_points",
			Help:      "number of returned points in a named output response",
			Buckets:   []float64{0, 1, 10, 100, 1000, 10000, 100000, 1000000},
		},
		[]string{"version", "commit_id"},
	)
	namedOutputsRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "named_outputs_rejections_total",
			Help:      "named output request rejections by a fixed reason set",
		},
		[]string{"reason", "version", "commit_id"},
	)
)

func NamedOutputsRequestInc(ctx context.Context, result string) {
	if !isNamedOutputsRequestResult(result) {
		return
	}
	counter, _ := namedOutputsRequestsTotal.GetMetricWithLabelValues(result, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func NamedOutputsOutputStateInc(ctx context.Context, state string) {
	if !isNamedOutputsOutputState(state) {
		return
	}
	counter, _ := namedOutputsOutputStatesTotal.GetMetricWithLabelValues(state, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func NamedOutputsSelectorCacheEventInc(ctx context.Context, event string) {
	if !isNamedOutputsSelectorCacheEvent(event) {
		return
	}
	counter, _ := namedOutputsSelectorCacheEventsTotal.GetMetricWithLabelValues(event, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func NamedOutputsSelectorCacheStateInc(ctx context.Context, state string) {
	if !isNamedOutputsSelectorCacheState(state) {
		return
	}
	counter, _ := namedOutputsSelectorCacheEntriesTotal.GetMetricWithLabelValues(state, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func NamedOutputsOutputCountObserve(ctx context.Context, count int) {
	if count <= 0 {
		return
	}
	observer, _ := namedOutputsOutputCount.GetMetricWithLabelValues(config.Version, config.CommitHash)
	observe(ctx, observer, float64(count))
}

func NamedOutputsDownstreamCallsObserve(ctx context.Context, mode string, calls int) {
	if calls < 0 || !isNamedOutputsMode(mode) {
		return
	}
	observer, _ := namedOutputsDownstreamCalls.GetMetricWithLabelValues(mode, config.Version, config.CommitHash)
	observe(ctx, observer, float64(calls))
}

func NamedOutputsDownstreamAmplificationObserve(ctx context.Context, mode string, calls, outputs int) {
	if calls < 0 || outputs <= 0 || !isNamedOutputsMode(mode) {
		return
	}
	observer, _ := namedOutputsDownstreamAmplificationRatio.GetMetricWithLabelValues(mode, config.Version, config.CommitHash)
	observe(ctx, observer, float64(calls)/float64(outputs))
}

func NamedOutputsResponseBytesObserve(ctx context.Context, bytes int) {
	if bytes <= 0 {
		return
	}
	observer, _ := namedOutputsResponseBytes.GetMetricWithLabelValues(config.Version, config.CommitHash)
	observe(ctx, observer, float64(bytes))
}

func NamedOutputsDurationObserve(ctx context.Context, result string, duration time.Duration) {
	if duration < 0 || !isNamedOutputsRequestResult(result) {
		return
	}
	observer, _ := namedOutputsDurationSeconds.GetMetricWithLabelValues(result, config.Version, config.CommitHash)
	observe(ctx, observer, duration.Seconds())
}

func NamedOutputsResultSizeObserve(ctx context.Context, series, points int) {
	if series < 0 || points < 0 {
		return
	}
	seriesObserver, _ := namedOutputsResultSeries.GetMetricWithLabelValues(config.Version, config.CommitHash)
	observe(ctx, seriesObserver, float64(series))
	pointsObserver, _ := namedOutputsResultPoints.GetMetricWithLabelValues(config.Version, config.CommitHash)
	observe(ctx, pointsObserver, float64(points))
}

func NamedOutputsRejectInc(ctx context.Context, reason string) {
	if !isNamedOutputsRejectReason(reason) {
		return
	}
	counter, _ := namedOutputsRejectionsTotal.GetMetricWithLabelValues(reason, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func isNamedOutputsRequestResult(result string) bool {
	return result == NamedOutputsRequestReceived || result == NamedOutputsRequestSuccess || result == NamedOutputsRequestError
}

func isNamedOutputsOutputState(state string) bool {
	switch state {
	case NamedOutputsOutputSuccess, NamedOutputsOutputSuccessEmpty, NamedOutputsOutputPartial, NamedOutputsOutputError:
		return true
	default:
		return false
	}
}

func isNamedOutputsSelectorCacheEvent(event string) bool {
	return event == NamedOutputsSelectorCacheHit || event == NamedOutputsSelectorCacheMiss || event == NamedOutputsSelectorCacheWait
}

func isNamedOutputsSelectorCacheState(state string) bool {
	return state == NamedOutputsSelectorCacheSuccess || state == NamedOutputsSelectorCachePartial || state == NamedOutputsSelectorCacheError
}

func isNamedOutputsMode(mode string) bool {
	return mode == NamedOutputsModeDirect || mode == NamedOutputsModePromEngine
}

func isNamedOutputsRejectReason(reason string) bool {
	switch reason {
	case NamedOutputsRejectValidation,
		NamedOutputsRejectUnsupportedContract,
		NamedOutputsRejectOutputLimit,
		NamedOutputsRejectCapacity,
		NamedOutputsRejectDeadline:
		return true
	default:
		return false
	}
}
