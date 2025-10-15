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
	"math"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/prometheus/model/value"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

type metricsEvent struct {
	define.CommonEvent
}

func (e metricsEvent) RecordType() define.RecordType {
	return define.RecordMetrics
}

type metricsConverter struct{}

func (c metricsConverter) Clean() {}

func (c metricsConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return metricsEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c metricsConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c metricsConverter) Convert(record *define.Record, f define.GatherFunc) {
	pdMetrics := record.Data.(pmetric.Metrics)
	resourceMetricsSlice := pdMetrics.ResourceMetrics()
	if resourceMetricsSlice.Len() == 0 {
		return
	}
	dataId := c.ToDataID(record)

	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		rs := resourceMetrics.Resource().Attributes()
		scopeMetricsSlice := resourceMetrics.ScopeMetrics()
		events := make([]define.Event, 0)
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			scopeMetric := scopeMetricsSlice.At(j)
			dimensions := pcommon.NewMap()
			rs.CopyTo(dimensions)
			dimensions.InsertString("scope_name", scopeMetric.Scope().Name())
			metrics := scopeMetric.Metrics()
			for k := 0; k < metrics.Len(); k++ {
				for _, dp := range c.Extract(metrics.At(k), dimensions) {
					events = append(events, c.ToEvent(record.Token, dataId, dp))
				}
			}
		}
		if len(events) > 0 {
			f(events...)
		}
	}
}

type otMetricMapper struct {
	Metrics    map[string]float64
	Dimensions map[string]string
	Time       time.Time
}

func (p otMetricMapper) AsMapStr() common.MapStr {
	target, ok := p.Dimensions["target"]
	if !ok {
		target = define.Identity()
	}
	return common.MapStr{
		"metrics":   p.Metrics,
		"target":    target,
		"timestamp": p.Time.UnixMilli(),
		"dimension": p.Dimensions,
	}
}

func toFloatValue(dp pmetric.NumberDataPoint) float64 {
	var val float64
	switch dp.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		val = dp.DoubleVal()
	case pmetric.NumberDataPointValueTypeInt:
		val = float64(dp.IntVal())
	}

	if dp.Flags().HasFlag(pmetric.MetricDataPointFlagNoRecordedValue) {
		val = math.Float64frombits(value.StaleNaN)
	}
	return val
}

func (c metricsConverter) convertSumMetrics(pdMetric pmetric.Metric, rs pcommon.Map) []common.MapStr {
	dps := pdMetric.Sum().DataPoints()
	items := make([]common.MapStr, 0, dps.Len())
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)

		val := toFloatValue(dp)
		if !utils.IsValidFloat64(val) {
			continue
		}
		m := otMetricMapper{
			Metrics:    map[string]float64{pdMetric.Name(): val},
			Time:       dp.Timestamp().AsTime(),
			Dimensions: utils.MergeReplaceAttributeMaps(dp.Attributes(), rs),
		}
		items = append(items, m.AsMapStr())
	}
	return items
}

func (c metricsConverter) convertHistogramMetrics(pdMetric pmetric.Metric, rs pcommon.Map) []common.MapStr {
	var items []common.MapStr
	dps := pdMetric.Histogram().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		dpTime := dp.Timestamp().AsTime()
		dimensions := utils.MergeReplaceAttributeMaps(dp.Attributes(), rs)
		metrics := make(map[string]float64)

		// 当且仅当 Sum 存在时才追加 _sum 指标
		if dp.HasSum() && utils.IsValidFloat64(dp.Sum()) {
			metrics[pdMetric.Name()+"_sum"] = dp.Sum()
		}

		// 当且仅当 Min 存在时才追加 _min 指标
		if dp.HasMin() && utils.IsValidFloat64(dp.Min()) {
			metrics[pdMetric.Name()+"_min"] = dp.Min()
		}

		// 当且仅当 Max 存在时才追加 _max 指标
		if dp.HasMax() && utils.IsValidFloat64(dp.Max()) {
			metrics[pdMetric.Name()+"_max"] = dp.Max()
		}

		// 追加 _count 指标
		if utils.IsValidUint64(dp.Count()) {
			metrics[pdMetric.Name()+"_count"] = float64(dp.Count())
		}

		if len(metrics) > 0 {
			m := otMetricMapper{
				Metrics:    metrics,
				Dimensions: dimensions,
				Time:       dpTime,
			}
			items = append(items, m.AsMapStr())
		}

		// 追加 buckets 指标
		bounds := dp.MExplicitBounds()
		bucketCounts := dp.MBucketCounts()
		var cumulativeCount uint64
		for j := 0; j < len(bounds) && j < len(bucketCounts); j++ {
			cumulativeCount += bucketCounts[j]
			val := float64(cumulativeCount)
			if dp.Flags().HasFlag(pmetric.MetricDataPointFlagNoRecordedValue) {
				val = math.Float64frombits(value.StaleNaN)
			}

			fv := strconv.FormatFloat(bounds[j], 'f', -1, 64)
			m := otMetricMapper{
				Metrics:    map[string]float64{pdMetric.Name() + "_bucket": val},
				Dimensions: utils.MergeMapWith(dimensions, "le", fv),
				Time:       dpTime,
			}
			items = append(items, m.AsMapStr())
		}

		// 追加 +Inf bucket
		val := float64(dp.Count())
		if dp.Flags().HasFlag(pmetric.MetricDataPointFlagNoRecordedValue) {
			val = math.Float64frombits(value.StaleNaN)
		}
		m := otMetricMapper{
			Metrics:    map[string]float64{pdMetric.Name() + "_bucket": val},
			Dimensions: utils.MergeMapWith(dimensions, "le", "+Inf"),
			Time:       dpTime,
		}
		items = append(items, m.AsMapStr())
	}
	return items
}

func (c metricsConverter) convertGaugeMetrics(pdMetric pmetric.Metric, rs pcommon.Map) []common.MapStr {
	dps := pdMetric.Gauge().DataPoints()
	items := make([]common.MapStr, 0, dps.Len())
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)

		val := toFloatValue(dp)
		if !utils.IsValidFloat64(val) {
			continue
		}

		m := otMetricMapper{
			Metrics:    map[string]float64{pdMetric.Name(): val},
			Dimensions: utils.MergeReplaceAttributeMaps(dp.Attributes(), rs),
			Time:       dp.Timestamp().AsTime(),
		}
		items = append(items, m.AsMapStr())
	}
	return items
}

func (c metricsConverter) convertSummaryMetrics(pdMetric pmetric.Metric, rs pcommon.Map) []common.MapStr {
	var items []common.MapStr
	dps := pdMetric.Summary().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		dimensions := utils.MergeReplaceAttributeMaps(dp.Attributes(), rs)
		metrics := make(map[string]float64)

		// 当且仅当有效数值才进行处理
		if utils.IsValidFloat64(dp.Sum()) {
			metrics[pdMetric.Name()+"_sum"] = dp.Sum()
		}
		if utils.IsValidUint64(dp.Count()) {
			metrics[pdMetric.Name()+"_count"] = float64(dp.Count())
		}

		if len(metrics) > 0 {
			m := otMetricMapper{
				Metrics:    metrics,
				Dimensions: dimensions,
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())
		}

		quantile := dp.QuantileValues()
		for j := 0; j < quantile.Len(); j++ {
			qua := quantile.At(j)
			if !utils.IsValidFloat64(qua.Value()) {
				continue
			}

			fv := strconv.FormatFloat(qua.Quantile(), 'f', -1, 64)
			m := otMetricMapper{
				Metrics:    map[string]float64{pdMetric.Name(): qua.Value()},
				Dimensions: utils.MergeMapWith(dimensions, "quantile", fv),
				Time:       dp.Timestamp().AsTime(),
			}
			items = append(items, m.AsMapStr())
		}
	}
	return items
}

func (c metricsConverter) Extract(pdMetric pmetric.Metric, rs pcommon.Map) []common.MapStr {
	name := utils.NormalizeName(pdMetric.Name())
	pdMetric.SetName(name)

	switch pdMetric.DataType() {
	case pmetric.MetricDataTypeSum:
		return c.convertSumMetrics(pdMetric, rs)

	case pmetric.MetricDataTypeHistogram:
		return c.convertHistogramMetrics(pdMetric, rs)

	case pmetric.MetricDataTypeGauge:
		return c.convertGaugeMetrics(pdMetric, rs)

	case pmetric.MetricDataTypeSummary:
		return c.convertSummaryMetrics(pdMetric, rs)
	}

	return nil
}
