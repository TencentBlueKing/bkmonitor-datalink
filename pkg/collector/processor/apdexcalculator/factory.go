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
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorApdexCalculator, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*apdexCalculator, error) {
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

type apdexCalculator struct {
	processor.CommonProcessor
	configs     *confengine.TierConfig // type: *Config
	calculators *confengine.TierConfig // type: Calculator
}

func (p *apdexCalculator) Name() string {
	return define.ProcessorApdexCalculator
}

func (p *apdexCalculator) IsDerived() bool {
	return false
}

func (p *apdexCalculator) IsPreCheck() bool {
	return false
}

func (p *apdexCalculator) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
	p.calculators = f.calculators
}

func (p *apdexCalculator) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordMetrics:
		p.processMetrics(record)
		return record, nil
	case define.RecordTraces, define.RecordRum:
		p.processTraces(record)
		return record, nil
	}
	return nil, nil
}

const (
	keyInstance = "bk.instance.id"
	keyService  = "service.name"
	keyKind     = "kind"
)

func (p *apdexCalculator) processTraces(record *define.Record) {
	pdTraces := record.Data.(ptrace.Traces)
	foreach.SpansWithResource(pdTraces, func(rs pcommon.Map, span ptrace.Span) {
		var service, instance string
		if v, ok := rs.Get(keyInstance); ok {
			instance = v.AsString()
		}
		if v, ok := rs.Get(keyService); ok {
			service = v.AsString()
		}

		attrs := span.Attributes()
		config := p.configs.Get(record.Token.Original, service, instance).(*Config)
		kind := span.Kind().String()
		if span.Name() == "HTTP GET" {
			// Special case for HTTP spans created by OpenTelemetry SDK, which have span name "HTTP GET/POST/PUT/DELETE" but kind SERVER/CLIENT. We want to match them with rules of kind "HTTP" instead of "SERVER"/"CLIENT".
			println(span.Name())
		}
		predicateKeys := config.GetPredicateKeys(kind)
		var foundPk string
		for _, pk := range predicateKeys {
			if findTracePredicate(pk, rs, span) {
				foundPk = pk
				break
			}
		}

		rule, found := config.Rule(kind, foundPk)
		if !found {
			return
		}

		calculator := p.calculators.Get(record.Token.Original, service, instance).(Calculator)
		status := calculator.Calc(calcTraceDurationByRule(rule, span), rule.ApdexT)
		attrs.UpsertString(rule.Destination, status)
	})
}

// calcTraceDurationByRule calculates trace duration by rule configuration.
//
// Priority:
// 1. If rule.Duration is configured and start/end events both exist, use event timestamp delta.
// 2. Otherwise fallback to span.StartTimestamp/endTimestamp.
func calcTraceDurationByRule(rule RuleConfig, span ptrace.Span) float64 {
	if rule.Duration == nil || rule.Duration.StartEvent == "" || rule.Duration.EndEvent == "" {
		return utils.CalcSpanDuration(span)
	}

	startTs, ok := findEventTimestampByName(span, rule.Duration.StartEvent)
	if !ok {
		return utils.CalcSpanDuration(span)
	}

	endTs, ok := findEventTimestampByName(span, rule.Duration.EndEvent)
	if !ok {
		return utils.CalcSpanDuration(span)
	}

	if startTs > endTs {
		return 0
	}

	return float64(endTs - startTs)
}

// findEventTimestampByName finds the timestamp of the first event with given name.
func findEventTimestampByName(span ptrace.Span, eventName string) (pcommon.Timestamp, bool) {
	events := span.Events()
	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		if event.Name() == eventName {
			return event.Timestamp(), true
		}
	}

	return 0, false
}

var spanKindMap = map[string]string{
	"0": "SPAN_KIND_UNSPECIFIED",
	"1": "SPAN_KIND_INTERNAL",
	"2": "SPAN_KIND_SERVER",
	"3": "SPAN_KIND_CLIENT",
	"4": "SPAN_KIND_PRODUCER",
	"5": "SPAN_KIND_CONSUMER",
}

func (p *apdexCalculator) processMetrics(record *define.Record) {
	pdMetrics := record.Data.(pmetric.Metrics)
	foreach.Metrics(pdMetrics, func(metric pmetric.Metric) {
		name := metric.Name()
		switch metric.DataType() {
		case pmetric.MetricDataTypeGauge:
			dps := metric.Gauge().DataPoints()
			for n := 0; n < dps.Len(); n++ {
				dp := dps.At(n)
				attrs := dp.Attributes()

				var service, instance string
				if v, ok := attrs.Get(keyService); ok {
					service = v.AsString()
				}
				if v, ok := attrs.Get(keyInstance); ok {
					instance = v.AsString()
				}

				config := p.configs.Get(record.Token.Original, service, instance).(*Config)
				var kind string
				if v, ok := attrs.Get(keyKind); ok {
					kind = spanKindMap[v.StringVal()]
				}

				predicateKeys := config.GetPredicateKeys(kind)
				var foundPk string
				for _, pk := range predicateKeys {
					// TODO(mando): 目前 predicateKey 暂时只支持 attributes 后续可能会扩展
					if findMetricsAttributes(pk, attrs) {
						foundPk = pk
						break
					}
				}

				rule, found := matchRules(config, kind, foundPk, name)
				if !found {
					continue
				}

				calculator := p.calculators.Get(record.Token.Original, service, instance).(Calculator)
				status := calculator.Calc(dp.DoubleVal(), rule.ApdexT)
				attrs.UpsertString(rule.Destination, status)
			}
		}
	})
}

func matchRules(config *Config, kind, foundPk, name string) (RuleConfig, bool) {
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

func findMetricsAttributes(pk string, attrs pcommon.Map) bool {
	ff, s := fields.DecodeFieldFrom(pk)
	switch ff {
	case fields.FieldFromAttributes:
		v, ok := attrs.Get(s)
		if ok {
			return v.AsString() != ""
		}
		return false
	}
	return false
}

// findTracePredicate checks whether the predicate key is present and non-empty in traces/rum context.
//
// Supported predicate sources:
// - "span_name": reads span.Name().
// - "attributes.*": reads span attributes.
// - "resource.*": reads resource attributes.
//
// Note: the function only checks existence/non-empty, and does not compare expected values.
func findTracePredicate(pk string, rs pcommon.Map, span ptrace.Span) bool {
	if pk == "span_name" {
		// Special key for span name, not a prefixed field path.
		return span.Name() != ""
	}

	ff, s := fields.DecodeFieldFrom(pk)
	switch ff {
	case fields.FieldFromAttributes:
		// attributes.<key>
		v, ok := span.Attributes().Get(s)
		if ok {
			return v.AsString() != ""
		}
		return false
	case fields.FieldFromResource:
		// resource.<key>
		v, ok := rs.Get(s)
		if ok {
			return v.AsString() != ""
		}
		return false
	default:
		// Unsupported source for traces/rum predicate.
		return false
	}
}
