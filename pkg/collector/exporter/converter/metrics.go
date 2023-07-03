// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type metricsEvent struct {
	define.CommonEvent
}

func (e metricsEvent) RecordType() define.RecordType {
	return define.RecordMetrics
}

var MetricsConverter EventConverter = metricsConverter{}

type metricsConverter struct{}

func (c metricsConverter) ToEvent(dataId int32, data common.MapStr) define.Event {
	logger.Debugf("convert otlp data, dataid=%v, metrics: %+v", dataId, data)
	return metricsEvent{define.NewCommonEvent(dataId, data)}
}

func (c metricsConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c metricsConverter) Convert(record *define.Record, f define.GatherFunc) {
	pdMetrics, ok := record.Data.(pmetric.Metrics)
	if !ok {
		return
	}
	resourceMetricsSlice := pdMetrics.ResourceMetrics()
	if resourceMetricsSlice.Len() == 0 {
		return
	}
	dataId := c.ToDataID(record)

	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		resources := resourceMetrics.Resource().Attributes()
		scopeMetricsSlice := resourceMetrics.ScopeMetrics()
		events := make([]define.Event, 0)
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				for _, dp := range c.Extract(dataId, metrics.At(k), resources) {
					events = append(events, c.ToEvent(dataId, dp))
				}
			}
		}
		if len(events) > 0 {
			f(events...)
		}
	}
}

type otMetricMapper struct {
	Metric     string
	Value      float64
	Dimensions map[string]interface{}
	Time       time.Time
}

func (p otMetricMapper) AsMapStr() common.MapStr {
	ms := common.MapStr{
		"metrics":   map[string]float64{p.Metric: p.Value},
		"target":    "otlp",
		"timestamp": p.Time.UnixMilli(),
		"dimension": p.Dimensions,
	}

	logger.Debugf("convert opentelemetry metrics: %+v", ms)
	return ms
}

func (c metricsConverter) Extract(dataId int32, pdMetric pmetric.Metric, resources pcommon.Map) []common.MapStr {
	var items []common.MapStr
	switch pdMetric.DataType() {
	case pmetric.MetricDataTypeSum:
		dps := pdMetric.Sum().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)

			if !utils.IsValidFloat64(dp.DoubleVal()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}
			m := otMetricMapper{
				Metric:     pdMetric.Name(),
				Value:      dp.DoubleVal(),
				Time:       dp.Timestamp().AsTime(),
				Dimensions: MergeReplaceAttributeMaps(dp.Attributes(), resources),
			}
			items = append(items, m.AsMapStr())
		}

	case pmetric.MetricDataTypeHistogram:
		dps := pdMetric.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			dimensions := MergeReplaceAttributeMaps(dp.Attributes(), resources)

			if !utils.IsValidFloat64(dp.Sum()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}
			m := otMetricMapper{
				Metric:     pdMetric.Name() + "_sum",
				Value:      dp.Sum(),
				Dimensions: dimensions,
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())

			if !utils.IsValidUint64(dp.Count()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}

			m = otMetricMapper{
				Metric:     pdMetric.Name() + "_count",
				Value:      float64(dp.Count()),
				Dimensions: dimensions,
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())

			if len(dp.MExplicitBounds()) != len(dp.MBucketCounts()) {
				return items
			}

			bounds := dp.MExplicitBounds()
			bucketCounts := dp.MBucketCounts()
			for j := 0; j < len(dp.MExplicitBounds()); j++ {
				additional := map[string]interface{}{"le": strconv.FormatFloat(bounds[j], 'f', -1, 64)}
				m = otMetricMapper{
					Metric:     pdMetric.Name() + "_bucket",
					Value:      float64(bucketCounts[j]),
					Dimensions: MergeReplaceMaps(additional, dimensions),
					Time:       dp.Timestamp().AsTime(),
				}
				items = append(items, m.AsMapStr())
			}
		}

	case pmetric.MetricDataTypeGauge:
		dps := pdMetric.Gauge().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			if !utils.IsValidFloat64(dp.DoubleVal()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}

			m := otMetricMapper{
				Metric:     pdMetric.Name(),
				Value:      dp.DoubleVal(),
				Dimensions: MergeReplaceAttributeMaps(dp.Attributes(), resources),
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())
		}

	case pmetric.MetricDataTypeSummary:
		dps := pdMetric.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			dp := dps.At(i)
			dimensions := MergeReplaceAttributeMaps(dp.Attributes(), resources)

			if !utils.IsValidFloat64(dp.Sum()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}

			m := otMetricMapper{
				Metric:     pdMetric.Name() + "_sum",
				Value:      dp.Sum(),
				Dimensions: dimensions,
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())

			if !utils.IsValidUint64(dp.Count()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
				continue
			}

			m = otMetricMapper{
				Metric:     pdMetric.Name() + "_count",
				Value:      float64(dp.Count()),
				Dimensions: dimensions,
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())

			quantile := dp.QuantileValues()
			for j := 0; j < quantile.Len(); j++ {
				qua := quantile.At(j)
				additional := map[string]interface{}{"quantile": strconv.FormatFloat(qua.Quantile(), 'f', -1, 64)}

				if !utils.IsValidFloat64(qua.Value()) {
					DefaultMetricMonitor.IncConverterFailedCounter(define.RecordMetrics, dataId)
					continue
				}

				m = otMetricMapper{
					Metric:     pdMetric.Name(),
					Value:      qua.Value(),
					Dimensions: MergeReplaceMaps(additional, dimensions),
					Time:       dp.Timestamp().AsTime(),
				}
				items = append(items, m.AsMapStr())
			}
		}
	}

	return items
}
