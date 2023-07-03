// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ProcessCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "etl_processes",
		Help: "etl process data count",
	}, []string{
		"name",
	})
	FormatterDropCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "formatter_drops",
		Help: "formatter drops data",
	}, []string{
		"name",
	})
)

func init() {
	prometheus.MustRegister(ProcessCounter)
	prometheus.MustRegister(FormatterDropCounter)
}
