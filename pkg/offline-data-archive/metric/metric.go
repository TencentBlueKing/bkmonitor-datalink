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
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

const (
	ActionInfo  = "info"
	ActionQuery = "query"

	StatusReceived = "received"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
)

var (
	requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "offline-data-archive-query",
			Name:      "request_count_total",
			Help:      "request handled count",
		},
		[]string{"action", "status"},
	)

	requestHandleSecondHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "offline-data-archive-query",
			Name:      "request_handle_seconds",
			Help:      "request handle seconds",
			Buckets:   []float64{0, 0.5, 1, 3, 5, 10, 30},
		},
		[]string{"url"},
	)
)

// RequestInc http 访问指标
func RequestInc(ctx context.Context, params ...string) error {
	metric, err := requestCount.GetMetricWithLabelValues(params...)
	if err != nil {
		return err
	}
	return counterInc(ctx, metric, params...)
}

func RequestSecond(ctx context.Context, duration time.Duration, params ...string) error {
	metric, err := requestHandleSecondHistogram.GetMetricWithLabelValues(params...)
	if err != nil {
		return err
	}
	return observe(ctx, metric, duration, params...)
}

func gaugeSet(
	ctx context.Context, metric prometheus.Gauge, value float64, params ...string,
) error {
	metric.Set(value)
	return nil
}

// handleCount
func counterInc(
	ctx context.Context, metric prometheus.Counter, params ...string,
) error {
	sp := trace.SpanFromContext(ctx).SpanContext()
	if sp.IsSampled() {
		exemplarAdder, ok := metric.(prometheus.ExemplarAdder)
		if ok {
			exemplarAdder.AddWithExemplar(1, prometheus.Labels{
				"traceID": sp.TraceID().String(),
				"spanID":  sp.SpanID().String(),
			})
		} else {
			return fmt.Errorf("metric type is wrong: %T, %v", metric, metric)
		}
	} else {
		metric.Inc()
	}
	return nil
}

func observe(
	ctx context.Context, metric prometheus.Observer, duration time.Duration, params ...string,
) error {
	sp := trace.SpanFromContext(ctx).SpanContext()
	if sp.IsSampled() {
		// exemplarObserve 只支持 histograms 类型，使用 summary 会报错
		exemplarObserve, ok := metric.(prometheus.ExemplarObserver)
		if ok {
			exemplarObserve.ObserveWithExemplar(duration.Seconds(), prometheus.Labels{
				"traceID": sp.TraceID().String(),
				"spanID":  sp.SpanID().String(),
			})
		} else {
			return fmt.Errorf("metric type is wrong: %T, %v", metric, metric)
		}
	} else {
		metric.Observe(duration.Seconds())
	}

	return nil
}

// init
func init() {
	prometheus.MustRegister(
		requestCount, requestHandleSecondHistogram,
	)
}
