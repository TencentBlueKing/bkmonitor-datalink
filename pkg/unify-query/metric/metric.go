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
	"go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	ActionConvert     = "convert"
	ActionInfo        = "info"
	ActionQuery       = "query"
	ActionLabelValues = "label_values"

	TypeTS     = "ts"
	TypeES     = "es"
	TypePromql = "promql"

	StatusReceived = "received"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
)

const (
	_ = 1 << (10 * iota)
	KB
	MB
	GB
)

var (
	secondsBuckets = []float64{0, 0.05, 0.1, 0.2, 0.5, 1, 3, 5, 10, 20, 30, 60}
	bytesBuckets   = []float64{0, KB, 100 * KB, 500 * KB, MB, 5 * MB, 20 * MB, 50 * MB, 100 * MB}

	minuteBuckets = []float64{5, 30, 60, 3 * 60, 6 * 60, 12 * 60, 24 * 60, 2 * 24 * 60, 7 * 24 * 60, 30 * 24 * 60, 6 * 30 * 24 * 60}
)

var (
	apiRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "api_request_total",
			Help:      "unify-query api request",
		},
		[]string{"api", "status", "space_uid", "source_type", "version", "commit_id"},
	)

	apiRequestSecondHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "api_request_second",
			Help:      "unify-query api request second",
			Buckets:   secondsBuckets,
		},
		[]string{"api", "space_uid", "version", "commit_id"},
	)

	resultTableInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "unify_query",
			Name:      "result_table_info",
		},
		[]string{"rt_table_id", "rt_data_id", "rt_measurement_type", "vm_table_id", "bcs_cluster_id"},
	)

	tsDBRequestBytesHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "tsdb_request_bytes",
			Help:      "tsdb request bytes",
			Buckets:   bytesBuckets,
		},
		[]string{"tsdb_type"},
	)

	tsDBRequestSecondHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "tsdb_request_seconds",
			Help:      "tsdb request seconds",
			Buckets:   secondsBuckets,
		},
		[]string{"tsdb_type", "url"},
	)

	tsDBRequestRangeMinuteHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "tsdb_request_range_minute",
			Help:      "tsdb request range minute",
			Buckets:   minuteBuckets,
		},
		[]string{"tsdb_type"},
	)

	jwtRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "jwt_request_total",
			Help:      "unify-query jwt request",
		},
		[]string{"user_agent", "client_ip", "api", "jwt_app_code", "jwt_app_user_name", "space_uid", "status"},
	)

	bkDataApiRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "bk_data_api_request_total",
			Help:      "unify-query bk_data api request",
		},
		[]string{"space_uid", "table_id", "is_match", "is_ff"},
	)
)

func APIRequestInc(ctx context.Context, api, status, spaceUID, sourceType string) {
	// 拼接 version 和 commit_id
	params := append([]string{}, api, status, spaceUID, sourceType, config.Version, config.CommitHash)

	metric, _ := apiRequestTotal.GetMetricWithLabelValues(params...)
	counterInc(ctx, metric)
}

func APIRequestSecond(ctx context.Context, duration time.Duration, api, spaceUID string) {
	// 拼接 version 和 commit_id
	params := append([]string{}, api, spaceUID, config.Version, config.CommitHash)

	metric, _ := apiRequestSecondHistogram.GetMetricWithLabelValues(params...)
	observe(ctx, metric, duration.Seconds())
}

func TsDBRequestSecond(ctx context.Context, duration time.Duration, tsdbType, url string) {
	metric, _ := tsDBRequestSecondHistogram.GetMetricWithLabelValues(tsdbType, url)
	observe(ctx, metric, duration.Seconds())
}

func TsDBRequestBytes(ctx context.Context, bytes int, tsdbType string) {
	metric, _ := tsDBRequestBytesHistogram.GetMetricWithLabelValues(tsdbType)
	observe(ctx, metric, float64(bytes))
}

func TsDBRequestRangeMinute(ctx context.Context, duration time.Duration, tsdbType string) {
	metric, _ := tsDBRequestRangeMinuteHistogram.GetMetricWithLabelValues(tsdbType)
	observe(ctx, metric, duration.Minutes())
}

func ResultTableInfoSet(ctx context.Context, value float64, rtTableID, rtDataID, rtMeasurementType, vmTableID, bcsClusterID string) {
	metric, _ := resultTableInfo.GetMetricWithLabelValues(rtTableID, rtDataID, rtMeasurementType, vmTableID, bcsClusterID)
	gaugeSet(ctx, metric, value)
}

func JWTRequestInc(ctx context.Context, userAgent, clusterIP, api, jwtAppCode, jwtAppUserName, spaceUID, status string) {
	return
	// metric, _ := jwtRequestTotal.GetMetricWithLabelValues(userAgent, clusterIP, api, jwtAppCode, jwtAppUserName, spaceUID, status)
	// counterInc(ctx, metric)
}

func BkDataRequestInc(ctx context.Context, spaceUID, tableID, isMatch, isFF string) {
	metric, _ := bkDataApiRequestTotal.GetMetricWithLabelValues(spaceUID, tableID, isMatch, isFF)
	counterInc(ctx, metric)
}

func gaugeSet(
	_ context.Context, metric prometheus.Gauge, value float64,
) {
	if metric == nil {
		return
	}
	metric.Set(value)
}

func counterInc(
	ctx context.Context, metric prometheus.Counter,
) {
	counterAdd(ctx, metric, 1)
}

// handleCount
func counterAdd(
	ctx context.Context, metric prometheus.Counter, val float64,
) {
	if metric == nil {
		return
	}
	sp := trace.SpanFromContext(ctx).SpanContext()
	if sp.IsSampled() {
		exemplarAdder, ok := metric.(prometheus.ExemplarAdder)
		if ok {
			exemplarAdder.AddWithExemplar(val, prometheus.Labels{
				"traceID": sp.TraceID().String(),
				"spanID":  sp.SpanID().String(),
			})
		} else {
			log.Errorf(ctx, "metric type is wrong: %T, %v", metric, metric)
		}
	} else {
		metric.Add(val)
	}
}

func observe(
	ctx context.Context, metric prometheus.Observer, value float64,
) {
	if metric == nil {
		return
	}

	sp := trace.SpanFromContext(ctx).SpanContext()
	if sp.IsSampled() {
		// exemplarObserve 只支持 histograms 类型，使用 summary 会报错
		exemplarObserve, ok := metric.(prometheus.ExemplarObserver)
		if ok {
			exemplarObserve.ObserveWithExemplar(value, prometheus.Labels{
				"traceID": sp.TraceID().String(),
				"spanID":  sp.SpanID().String(),
			})
		} else {
			log.Errorf(ctx, "metric type is wrong: %T, %v", metric, metric)
		}
	} else {
		metric.Observe(value)
	}
}
