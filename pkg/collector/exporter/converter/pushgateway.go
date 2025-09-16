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
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"
	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/maps"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

type pushGatewayEvent struct {
	define.CommonEvent
}

func (e pushGatewayEvent) RecordType() define.RecordType {
	return define.RecordPushGateway
}

type pushGatewayConverter struct{}

func (c pushGatewayConverter) Clean() {}

func (c pushGatewayConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return pushGatewayEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c pushGatewayConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c pushGatewayConverter) Convert(record *define.Record, f define.GatherFunc) {
	data := record.Data.(*define.PushGatewayData)
	dataId := c.ToDataID(record)
	now := time.Now().UnixMilli()

	c.publishEventsFromMetricFamily(record.Token, data, dataId, now, f)
}

type promMapper struct {
	Metrics    common.MapStr
	Target     string
	Timestamp  int64
	Dimensions map[string]string
	Exemplar   *dto.Exemplar
}

// AsMapStr 转换为 beat 框架要求的 MapStr 对象
func (p promMapper) AsMapStr() common.MapStr {
	ms := common.MapStr{
		"metrics":   p.Metrics,
		"target":    p.Target,
		"timestamp": p.Timestamp,
		"dimension": p.Dimensions,
	}

	// 按需处理 exemplar 数据
	exemplar := p.wrapExemplar()
	if exemplar != nil {
		ms["exemplar"] = exemplar
	}
	return ms
}

func (p promMapper) wrapExemplar() common.MapStr {
	if p.Exemplar == nil {
		return nil
	}

	if p.Exemplar != nil && p.Exemplar.Timestamp != nil && p.Exemplar.Value != nil {
		exemplarLbs := make(map[string]string)
		for _, pair := range p.Exemplar.Label {
			if pair.Name != nil && pair.Value != nil {
				exemplarLbs[*pair.Name] = *pair.Value
			}
		}

		traceID := exemplarLbs["traceID"]
		if traceID == "" {
			traceID = exemplarLbs["trace_id"]
		}
		spanID := exemplarLbs["spanID"]
		if spanID == "" {
			spanID = exemplarLbs["span_id"]
		}

		// 当且仅当 traceID/spanID 不为空时才追加至维度里
		if traceID != "" && spanID != "" {
			return common.MapStr{
				"bk_trace_timestamp": p.Exemplar.Timestamp.AsTime().UnixMilli(),
				"bk_trace_value":     *p.Exemplar.Value,
				"bk_trace_id":        traceID,
				"bk_span_id":         spanID,
			}
		}
	}
	return nil
}

func getTimestamp(now int64, t *int64) int64 {
	if t != nil {
		return *t
	}
	return now
}

func (c pushGatewayConverter) publishEventsFromMetricFamily(token define.Token, pd *define.PushGatewayData, dataId int32, now int64, f define.GatherFunc) {
	// instance 维度会被当做 target 处理 默认值为 unknown
	target := pd.Labels["instance"]
	if target == "" {
		target = "unknown"
	}

	name := *pd.MetricFamilies.Name
	metrics := pd.MetricFamilies.Metric
	pms := make([]*promMapper, 0)
	for _, metric := range metrics {
		lbs := map[string]string{}
		if len(metric.Label) != 0 {
			for _, label := range metric.Label {
				if label.GetName() != "" && label.GetValue() != "" {
					lbs[label.GetName()] = label.GetValue()
				}
			}
		}

		// 处理 Counter 类型数据
		counter := metric.GetCounter()
		if counter != nil && utils.IsValidFloat64(counter.GetValue()) {
			pms = append(pms, &promMapper{
				Metrics: common.MapStr{
					name: counter.GetValue(),
				},
				Target:     target,
				Timestamp:  getTimestamp(now, metric.TimestampMs),
				Dimensions: maps.Merge(lbs, pd.Labels),
				Exemplar:   counter.Exemplar,
			})
		}

		// 处理 Gauge 类型数据
		gauge := metric.GetGauge()
		if gauge != nil && utils.IsValidFloat64(gauge.GetValue()) {
			pms = append(pms, &promMapper{
				Metrics: common.MapStr{
					name: gauge.GetValue(),
				},
				Target:     target,
				Timestamp:  getTimestamp(now, metric.TimestampMs),
				Dimensions: maps.Merge(lbs, pd.Labels),
			})
		}

		// 处理 Summary 类型数据
		summary := metric.GetSummary()
		if summary != nil && utils.IsValidFloat64(summary.GetSampleSum()) {
			pms = append(pms, &promMapper{
				Metrics: common.MapStr{
					name + "_sum":   summary.GetSampleSum(),
					name + "_count": summary.GetSampleCount(),
				},
				Target:     target,
				Timestamp:  getTimestamp(now, metric.TimestampMs),
				Dimensions: maps.Merge(lbs, pd.Labels),
			})

			for _, quantile := range summary.GetQuantile() {
				if !utils.IsValidFloat64(quantile.GetValue()) {
					continue
				}

				fv := strconv.FormatFloat(quantile.GetQuantile(), 'f', -1, 64)
				pms = append(pms, &promMapper{
					Metrics: common.MapStr{
						name: quantile.GetValue(),
					},
					Target:     target,
					Timestamp:  getTimestamp(now, metric.TimestampMs),
					Dimensions: maps.Merge(lbs, map[string]string{"quantile": fv}, pd.Labels),
				})
			}
		}

		// 处理 Histogram 类型数据
		histogram := metric.GetHistogram()
		if histogram != nil && utils.IsValidFloat64(histogram.GetSampleSum()) {
			pms = append(pms, &promMapper{
				Metrics: common.MapStr{
					name + "_sum":   histogram.GetSampleSum(),
					name + "_count": histogram.GetSampleCount(),
				},
				Target:     target,
				Timestamp:  getTimestamp(now, metric.TimestampMs),
				Dimensions: maps.Merge(lbs, pd.Labels),
			})

			infSeen := false
			for _, bucket := range histogram.GetBucket() {
				if !utils.IsValidUint64(bucket.GetCumulativeCount()) {
					continue
				}
				if math.IsInf(bucket.GetUpperBound(), +1) {
					infSeen = true
				}

				fv := strconv.FormatFloat(bucket.GetUpperBound(), 'f', -1, 64)
				pms = append(pms, &promMapper{
					Metrics: common.MapStr{
						name + "_bucket": bucket.GetCumulativeCount(),
					},
					Target:     target,
					Timestamp:  getTimestamp(now, metric.TimestampMs),
					Dimensions: maps.Merge(lbs, map[string]string{"le": fv}, pd.Labels),
					Exemplar:   bucket.Exemplar,
				})
			}
			// 仅 expfmt.FmtText 格式支持 inf
			// 其他格式需要自行检查
			if !infSeen {
				fv := strconv.FormatFloat(math.Inf(+1), 'f', -1, 64)
				pms = append(pms, &promMapper{
					Metrics: common.MapStr{
						name + "_bucket": histogram.GetSampleCount(),
					},
					Target:     target,
					Timestamp:  getTimestamp(now, metric.TimestampMs),
					Dimensions: maps.Merge(lbs, map[string]string{"le": fv}, pd.Labels),
				})
			}
		}

		// 处理未知类型数据
		untyped := metric.GetUntyped()
		if untyped != nil && utils.IsValidFloat64(untyped.GetValue()) {
			pms = append(pms, &promMapper{
				Metrics: common.MapStr{
					name: untyped.GetValue(),
				},
				Target:     target,
				Timestamp:  getTimestamp(now, metric.TimestampMs),
				Dimensions: maps.Merge(lbs, pd.Labels),
			})
		}
	}

	pms = c.compactTrpcOTFilter(pms)
	if len(pms) == 0 {
		return
	}

	events := make([]define.Event, 0, len(pms))
	for _, pm := range pms {
		events = append(events, c.ToEvent(token, dataId, pm.AsMapStr()))
	}
	f(events...)
}

// compactTrpcOTFilter 兼容 trpc 框架 OTfilter 指标格式
// 当且仅当 `_type`/`_name` 两个维度存在且所有指标名称以 `trpc_` 开头的才进行转换
func (c pushGatewayConverter) compactTrpcOTFilter(pms []*promMapper) []*promMapper {
	const (
		labelType = "_type"
		labelName = "_name"
	)

	var ret []*promMapper
	for _, pm := range pms {
		var seen bool
		if len(pm.Dimensions[labelType]) == 0 || len(pm.Dimensions[labelName]) == 0 {
			seen = true
		}
		for k := range pm.Metrics {
			if !strings.HasPrefix(k, "trpc_") {
				seen = true
				break
			}
		}

		if seen {
			ret = append(ret, pm)
			continue
		}

		dims := make(map[string]string)
		for name, value := range pm.Dimensions {
			if name == labelType || name == labelName {
				continue
			}
			dims[name] = value
		}

		metrics := make(common.MapStr)
		for k, v := range pm.Metrics {
			metric := utils.NormalizeName(pm.Dimensions[labelName])
			// trpc_ 框架无 summary 概念 因此只支持 histogram 即可
			if pm.Dimensions[labelType] == "histogram" {
				switch {
				case strings.HasSuffix(k, "_bucket"):
					metric = metric + "_bucket"
				case strings.HasSuffix(k, "_count"):
					metric = metric + "_count"
				case strings.HasSuffix(k, "_sum"):
					metric = metric + "_sum"
				}
			}
			metrics[metric] = v
		}

		ret = append(ret, &promMapper{
			Metrics:    metrics,
			Target:     pm.Target,
			Timestamp:  pm.Timestamp,
			Dimensions: dims,
			Exemplar:   pm.Exemplar,
		})
	}
	return ret
}
