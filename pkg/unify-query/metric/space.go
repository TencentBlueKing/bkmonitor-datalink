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
)

const (
	SpaceActionDelete = "delete"
	SpaceActionRead   = "read"
	SpaceActionWrite  = "write"
	SpaceActionCreate = "create"

	SpaceTypeBolt  = "bolt"
	SpaceTypeCache = "cache"
)

var (
	spaceRequestCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "space_request_total",
			Help:      "space request total",
		},
		[]string{"key", "type", "action"},
	)

	spaceRouterNotExistCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "space_router_total",
			Help:      "space router not exist check",
		},
		[]string{"space_uid", "result_table", "metric", "reason"},
	)
)

func SpaceRouterNotExistInc(ctx context.Context, params ...string) {
	metric, _ := spaceRouterNotExistCounter.GetMetricWithLabelValues(params...)
	counterInc(ctx, metric)
}

func SpaceRequestCounterInc(ctx context.Context, params ...string) {
	metric, _ := spaceRequestCounter.GetMetricWithLabelValues(params...)
	counterInc(ctx, metric)
}

func SpaceRequestCounterAdd(ctx context.Context, val float64, params ...string) {
	metric, _ := spaceRequestCounter.GetMetricWithLabelValues(params...)
	counterAdd(ctx, metric, val)
}

func init() {
	prometheus.MustRegister(
		spaceRequestCounter,
		spaceRouterNotExistCounter,
	)
}
