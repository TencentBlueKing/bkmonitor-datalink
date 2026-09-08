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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

const (
	QueryResourceSeries            = "series"
	QueryResourcePoints            = "points"
	QueryResourceBytes             = "bytes"
	QueryResourceResponseBytes     = "response_bytes"
	QueryResourceEvalCapacityBytes = "eval_capacity_bytes"
	QueryResourceEvalSteps         = "eval_steps"
)

var (
	queryResourceBudgetRejectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "resource_budget_rejections_total",
			Help:      "query resource budget rejections by fixed resource type",
		},
		[]string{"resource", "version", "commit_id"},
	)
	queryResourceBudgetUsage = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "resource_budget_usage",
			Help:      "final request-local query resource usage by fixed resource type",
			Buckets:   prometheus.ExponentialBuckets(1, 8, 14),
		},
		[]string{"resource", "version", "commit_id"},
	)
)

func QueryResourceBudgetRejectInc(ctx context.Context, resource string) {
	if !isQueryResource(resource) {
		return
	}
	counter, _ := queryResourceBudgetRejectionsTotal.GetMetricWithLabelValues(resource, config.Version, config.CommitHash)
	counterInc(ctx, counter)
}

func QueryResourceBudgetUsageObserve(ctx context.Context, resource string, value int64) {
	if value < 0 || !isQueryResource(resource) {
		return
	}
	observer, _ := queryResourceBudgetUsage.GetMetricWithLabelValues(resource, config.Version, config.CommitHash)
	observe(ctx, observer, float64(value))
}

func isQueryResource(resource string) bool {
	switch resource {
	case QueryResourceSeries,
		QueryResourcePoints,
		QueryResourceBytes,
		QueryResourceResponseBytes,
		QueryResourceEvalCapacityBytes,
		QueryResourceEvalSteps:
		return true
	default:
		return false
	}
}
