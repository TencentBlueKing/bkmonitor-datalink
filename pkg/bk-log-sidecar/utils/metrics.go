// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"github.com/prometheus/client_golang/prometheus"
	clmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	containerCacheTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "cri",
		Name:      "container_cache_total",
		Help:      "current cache container count",
	}, []string{"cri"})
)

// NewContainerCacheTotal new container cache total count
func NewContainerCacheTotal(cri string) prometheus.Counter {
	return containerCacheTotal.WithLabelValues(cri)
}

func init() {
	clmetrics.Registry.MustRegister(containerCacheTotal)
}
