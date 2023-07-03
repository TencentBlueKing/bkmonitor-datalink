// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsbuilder

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type Metric struct {
	Val        float64
	Ts         pcommon.Timestamp
	Dimensions map[string]string
}

type Builder struct {
	pbMetrics   pmetric.Metrics
	metricSlice pmetric.MetricSlice
}

func New() *Builder {
	pbMetrics := pmetric.NewMetrics()
	scopeMetrics := pbMetrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	scopeMetrics.Metrics()
	return &Builder{pbMetrics: pbMetrics, metricSlice: scopeMetrics.Metrics()}
}

func (b Builder) Get() pmetric.Metrics {
	return b.pbMetrics
}

func (b Builder) Build(name string, ms ...Metric) {
	metrics := b.metricSlice.AppendEmpty()
	metrics.SetDataType(pmetric.MetricDataTypeGauge)
	metrics.SetName(name)

	for _, m := range ms {
		metric := metrics.Gauge().DataPoints().AppendEmpty()
		metric.SetDoubleVal(m.Val)
		metric.SetTimestamp(m.Ts)
		for k, v := range m.Dimensions {
			metric.Attributes().UpsertString(k, v)
		}
	}
}
