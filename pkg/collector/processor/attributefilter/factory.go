// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package attributefilter

import (
	"github.com/mitchellh/mapstructure"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorAttributeFilter, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*attributeFilter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Clean()
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Clean()
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &attributeFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type attributeFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p attributeFilter) Name() string {
	return define.ProcessorAttributeFilter
}

func (p attributeFilter) IsDerived() bool {
	return false
}

func (p attributeFilter) IsPreCheck() bool {
	return false
}

func (p attributeFilter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.AsString.Keys) > 0 {
		p.asStringAction(record)
	}
	if config.FromToken.BizId != "" || config.FromToken.AppName != "" {
		p.fromTokenAction(record)
	}
	return nil, nil
}

func (p attributeFilter) fromTokenAction(record *define.Record) {
	config := p.configs.GetByToken(record.Token.Original).(Config)

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces.ResourceSpans(), func(span ptrace.Span) {
			attrs := span.Attributes()
			if config.FromToken.BizId != "" {
				attrs.UpsertInt(config.FromToken.BizId, int64(record.Token.BizId))
			}
			if config.FromToken.AppName != "" {
				attrs.UpsertString(config.FromToken.AppName, record.Token.AppName)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		resourceMetricsSlice := pdMetrics.ResourceMetrics()
		for i := 0; i < resourceMetricsSlice.Len(); i++ {
			scopeMetricsSlice := resourceMetricsSlice.At(i).ScopeMetrics()
			for j := 0; j < scopeMetricsSlice.Len(); j++ {
				metrics := scopeMetricsSlice.At(j).Metrics()
				for k := 0; k < metrics.Len(); k++ {
					metric := metrics.At(k)
					switch metric.DataType() {
					case pmetric.MetricDataTypeGauge:
						dps := metric.Gauge().DataPoints()
						for n := 0; n < dps.Len(); n++ {
							attrs := dps.At(n).Attributes()
							if config.FromToken.BizId != "" {
								attrs.UpsertInt(config.FromToken.BizId, int64(record.Token.BizId))
							}
							if config.FromToken.AppName != "" {
								attrs.UpsertString(config.FromToken.AppName, record.Token.AppName)
							}
						}
					}
				}
			}
		}
	}
}

func (p attributeFilter) asStringAction(record *define.Record) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for _, key := range p.configs.GetByToken(record.Token.Original).(Config).AsString.Keys {
			for i := 0; i < resourceSpansSlice.Len(); i++ {
				resourceSpans := resourceSpansSlice.At(i)

				attributes := resourceSpans.Resource().Attributes()
				if v, ok := attributes.Get(key); ok {
					attributes.UpsertString(key, v.AsString())
				}

				scopeSpansSlice := resourceSpans.ScopeSpans()
				for j := 0; j < scopeSpansSlice.Len(); j++ {
					spans := scopeSpansSlice.At(j).Spans()
					for k := 0; k < spans.Len(); k++ {
						span := spans.At(k)
						attributes = span.Attributes()
						v, ok := attributes.Get(key)
						if !ok {
							continue
						}
						attributes.UpsertString(key, v.AsString())
					}
				}
			}
		}
	}
}
