// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apdexcalculator

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorApdexCalculator, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*apdexCalculator, error) {
	configs := confengine.NewTierConfig()
	calculators := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Setup()
	configs.SetGlobal(c)
	calculators.SetGlobal(NewCalculator(*c))

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Setup()
		configs.Set(custom.Token, custom.Type, custom.ID, cfg)
		calculators.Set(custom.Token, custom.Type, custom.ID, NewCalculator(*cfg))
	}

	return &apdexCalculator{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
		calculators:     calculators,
	}, nil
}

var SpanKindMap = map[string]string{
	"0": "SPAN_KIND_UNSPECIFIED",
	"1": "SPAN_KIND_INTERNAL",
	"2": "SPAN_KIND_SERVER",
	"3": "SPAN_KIND_CLIENT",
	"4": "SPAN_KIND_PRODUCER",
	"5": "SPAN_KIND_CONSUMER",
}

type apdexCalculator struct {
	processor.CommonProcessor
	configs     *confengine.TierConfig // type: *Config
	calculators *confengine.TierConfig // type: Calculator
}

func (p apdexCalculator) Name() string {
	return define.ProcessorApdexCalculator
}

func (p apdexCalculator) IsDerived() bool {
	return false
}

func (p apdexCalculator) IsPreCheck() bool {
	return false
}

func (p apdexCalculator) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordMetrics:
		p.processMetrics(record)
		return record, nil
	}

	return nil, nil
}

func (p apdexCalculator) processMetrics(record *define.Record) {
	pdMetrics := record.Data.(pmetric.Metrics)
	foreach.Metrics(pdMetrics.ResourceMetrics(), func(metric pmetric.Metric) {
		name := metric.Name()
		switch metric.DataType() {
		case pmetric.MetricDataTypeGauge:
			dps := metric.Gauge().DataPoints()
			for n := 0; n < dps.Len(); n++ {
				dp := dps.At(n)
				dpAttrs := dp.Attributes()

				var service, instance string
				if v, ok := dpAttrs.Get(processor.KeyService); ok {
					service = v.AsString()
				}
				if v, ok := dpAttrs.Get(processor.KeyInstance); ok {
					instance = v.AsString()
				}

				config := p.configs.Get(record.Token.Original, service, instance).(*Config)
				var kind string
				if v, ok := dpAttrs.Get(processor.KeyKind); ok {
					kind = SpanKindMap[v.StringVal()]
				}

				predicateKeys := config.GetPredicateKeys(kind)
				var foundPk string
				for _, pk := range predicateKeys {
					// TODO(mando): 目前 predicateKey 暂时只支持 attributes 后续可能会扩展
					if p.findMetricsAttributes(pk, dpAttrs) {
						foundPk = pk
						break
					}
				}

				rule, found := p.matchRules(config, kind, foundPk, name)
				if !found {
					logger.Debugf("no rules found, kind=%v, pk=%v, name=%v", kind, foundPk, name)
					continue
				}

				calculator := p.calculators.Get(record.Token.Original, service, instance).(Calculator)
				status := calculator.Calc(dp.DoubleVal(), rule.ApdexT)
				dpAttrs.UpsertString(rule.Destination, status)
			}
		}
	})
}

func (p apdexCalculator) matchRules(config *Config, kind, foundPk, name string) (RuleConfig, bool) {
	rule, ok := config.Rule(kind, foundPk)
	if !ok {
		return rule, false
	}
	if rule.MetricName != name {
		logger.Warnf("metric name '%s' is not supported", name)
		return rule, false
	}
	return rule, true
}

func (p apdexCalculator) findMetricsAttributes(pk string, attrMap pcommon.Map) bool {
	df, s := processor.DecodeDimensionFrom(pk)
	switch df {
	case processor.DimensionFromAttribute:
		v, ok := attrMap.Get(s)
		if ok {
			return v.AsString() != ""
		}
		return false
	}
	return false
}
