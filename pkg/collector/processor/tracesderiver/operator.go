// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver/accumulator"
)

type Operator interface {
	Operate(*define.Record) *define.Record
	Clean()
}

func NewTracesOperator(conf Config) Operator {
	ch := NewConfigHandler(conf)

	to := tracesOperator{
		dm: NewSpanDimensionMatcher(ch),
	}

	accumulatorConfig := ch.GetAccumulatorConfig()
	if accumulatorConfig != nil {
		to.accumulator = accumulator.New(accumulatorConfig, processor.PublishNonSchedRecords)
	}
	extractorConfig := ch.GetExtractorConfig()
	if extractorConfig != nil {
		to.extractor = NewExtractor(extractorConfig)
	}

	return to
}

type tracesOperator struct {
	dm          DimensionMatcher
	accumulator *accumulator.Accumulator
	extractor   *Extractor
}

func (to tracesOperator) Clean() {
	if to.accumulator != nil {
		to.accumulator.Stop()
	}
	if to.extractor != nil {
		to.extractor.Stop()
	}
}

func (to tracesOperator) Operate(record *define.Record) *define.Record {
	pdTraces := record.Data.(ptrace.Traces)
	resourceSpansSlice := pdTraces.ResourceSpans()
	types := to.dm.Types()

	var metrics []define.MetricV2
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		scopeSpansSlice := resourceSpansSlice.At(i).ScopeSpans()
		resources := to.dm.MatchResource(resourceSpansSlice.At(i))
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			spans := scopeSpansSlice.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				for _, t := range types {
					// 如果该 type 没有匹配到任何指标 直接跳过
					dim, ok := to.dm.Match(t.Type, span)
					if !ok {
						continue
					}

					// 匹配 resource keys 并提取合并维度
					keys := to.dm.ResourceKeys(t.Type)
					for _, key := range keys {
						if v, exist := resources[key]; exist {
							dim[key] = v
						}
					}

					// 派生指标补充 token app_name 维度 (´･_･) 此处硬编码
					dim[define.TokenAppName] = record.Token.AppName
					hash := labels.HashFromMap(dim) // 避免重复计算

					// extractor 处理
					if to.extractor != nil {
						if to.extractor.Set(record.Token.MetricsDataId, hash) {
							val := to.extractor.Extract(span)
							metrics = append(metrics, define.MetricV2{
								Metrics:   map[string]float64{t.MetricName: val},
								Dimension: dim,
								Timestamp: span.EndTimestamp().AsTime().UnixMilli(),
							})
						}
					}

					// accumulator 处理
					if to.accumulator != nil {
						val := utils.CalcSpanDuration(span)
						to.accumulator.Accumulate(record.Token.MetricsDataId, dim, hash, val)
					}
				}
			}
		}
	}

	return &define.Record{
		RecordType:  define.RecordMetricV2,
		RequestType: define.RequestDerived,
		Token:       record.Token,
		Data:        &define.MetricV2Data{Data: metrics},
	}
}
