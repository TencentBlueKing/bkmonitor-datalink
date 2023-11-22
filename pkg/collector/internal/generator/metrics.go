// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generator

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

type MetricsGenerator struct {
	opts define.MetricsOptions

	attributes pcommon.Map
	resources  pcommon.Map
}

func NewMetricsGenerator(opts define.MetricsOptions) *MetricsGenerator {
	attributes := random.AttributeMap(opts.RandomAttributeKeys, opts.DimensionsValueType)
	resources := random.AttributeMap(opts.RandomResourceKeys, opts.DimensionsValueType)
	return &MetricsGenerator{
		attributes: attributes,
		resources:  resources,
		opts:       opts,
	}
}

func (g *MetricsGenerator) Generate() pmetric.Metrics {
	pdMetrics := pmetric.NewMetrics()
	rs := pdMetrics.ResourceMetrics().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "generator.service")
	g.resources.CopyTo(rs.Resource().Attributes())
	for k, v := range g.opts.Resources {
		rs.Resource().Attributes().UpsertString(k, v)
	}

	now := time.Now()
	for i := 0; i < g.opts.GaugeCount; i++ {
		metric := rs.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(random.String(12))
		if g.opts.MetricName != "" {
			metric.SetName(g.opts.MetricName)
		}
		metric.SetDataType(pmetric.MetricDataTypeGauge)
		dp := metric.Gauge().DataPoints().AppendEmpty()
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
		dp.SetDoubleVal(float64(i))
		if g.opts.Value != nil {
			dp.SetDoubleVal(*g.opts.Value)
		}
		g.attributes.CopyTo(dp.Attributes())
		for k, v := range g.opts.Attributes {
			dp.Attributes().UpsertString(k, v)
		}
	}

	for i := 0; i < g.opts.CounterCount; i++ {
		metric := rs.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(random.String(12))
		if g.opts.MetricName != "" {
			metric.SetName(g.opts.MetricName)
		}
		metric.SetDataType(pmetric.MetricDataTypeSum)
		dp := metric.Sum().DataPoints().AppendEmpty()
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
		dp.SetDoubleVal(float64(i))
		if g.opts.Value != nil {
			dp.SetDoubleVal(*g.opts.Value)
		}
		g.attributes.CopyTo(dp.Attributes())
		for k, v := range g.opts.Attributes {
			dp.Attributes().UpsertString(k, v)
		}
	}

	for i := 0; i < g.opts.HistogramCount; i++ {
		metric := rs.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(random.String(12))
		if g.opts.MetricName != "" {
			metric.SetName(g.opts.MetricName)
		}
		metric.SetDataType(pmetric.MetricDataTypeHistogram)
		dp := metric.Histogram().DataPoints().AppendEmpty()
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
		dp.SetSum(float64(i))
		g.attributes.CopyTo(dp.Attributes())
		for k, v := range g.opts.Attributes {
			dp.Attributes().UpsertString(k, v)
		}
	}

	for i := 0; i < g.opts.SummaryCount; i++ {
		metric := rs.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(random.String(12))
		if g.opts.MetricName != "" {
			metric.SetName(g.opts.MetricName)
		}
		metric.SetDataType(pmetric.MetricDataTypeSummary)
		dp := metric.Summary().DataPoints().AppendEmpty()
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
		dp.SetSum(float64(i))

		for j := 0; j < 6; j++ {
			qua := dp.QuantileValues().AppendEmpty()
			qua.SetQuantile(float64(j * 10))
			qua.SetQuantile(float64(j))
		}

		g.attributes.CopyTo(dp.Attributes())
		for k, v := range g.opts.Attributes {
			dp.Attributes().UpsertString(k, v)
		}
	}

	return pdMetrics
}
