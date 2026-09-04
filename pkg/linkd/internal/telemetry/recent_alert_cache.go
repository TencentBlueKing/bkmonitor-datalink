// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/lifecycle/recentalert"
)

type recentAlertCacheObserver struct{ metrics *instruments }

// RecentAlertCacheObserver 创建不携带业务身份的 Recent Alert 缓存观察器。
func (r *Runtime) RecentAlertCacheObserver() recentalert.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &recentAlertCacheObserver{metrics: r.metrics}
}

func (o *recentAlertCacheObserver) Operation(ctx context.Context, operation, outcome string) {
	o.metrics.lifecycleRecentAlertCacheOps.Add(ctx, 1, metric.WithAttributes(
		attribute.String("linkd.operation", operation),
		attribute.String("linkd.outcome", outcome),
	))
}

var _ recentalert.Observer = (*recentAlertCacheObserver)(nil)
