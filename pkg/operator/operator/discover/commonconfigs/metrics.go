// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package commonconfigs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/prometheus/discovery"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	sdRefreshFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prometheus_sd_refresh_failures_total",
			Help: "Number of refresh failures for the given SD mechanism.",
		},
		[]string{"mechanism"},
	)

	sdRefreshDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "prometheus_sd_refresh_duration_seconds",
			Help:    "The duration of a refresh in seconds for the given SD mechanism.",
			Buckets: define.DefObserveDuration,
		},
		[]string{"mechanism"},
	)
)

type refreshMetricsInstantiator struct{}

func (refreshMetricsInstantiator) Instantiate(mech string) *discovery.RefreshMetrics {
	return &discovery.RefreshMetrics{
		Failures: sdRefreshFailures.WithLabelValues(mech),
		Duration: sdRefreshDuration.WithLabelValues(mech),
	}
}

func DefaultRefreshMetricsInstantiator() discovery.RefreshMetricsInstantiator {
	return refreshMetricsInstantiator{}
}

type discovererMetrics struct{}

func NoopDiscovererMetrics() discovery.DiscovererMetrics {
	return discovererMetrics{}
}

func (c discovererMetrics) Register() error { return nil }

func (c discovererMetrics) Unregister() {}
