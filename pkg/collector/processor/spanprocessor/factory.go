// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package spanprocessor

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/opmatch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorSpanProcessor, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*spanProcessor, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &spanProcessor{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		fetcher:         fields.NewSpanFieldFetcher(),
		configs:         configs,
	}, nil
}

type spanProcessor struct {
	processor.CommonProcessor
	fetcher fields.SpanFieldFetcher
	configs *confengine.TierConfig // type: *ConfigHandler
}

func (p *spanProcessor) Name() string {
	return define.ProcessorSpanProcessor
}

func (p *spanProcessor) IsDerived() bool {
	return false
}

func (p *spanProcessor) IsPreCheck() bool {
	return false
}

func (p *spanProcessor) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.fetcher = f.fetcher
	p.configs = f.configs
}

func (p *spanProcessor) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.Drop) > 0 {
		p.dropAction(record, config)
	}
	if len(config.ReplaceValue) > 0 {
		p.replaceValueAction(record, config)
	}

	return nil, nil
}

func (p *spanProcessor) dropAction(record *define.Record, config Config) {
	if record.RecordType != define.RecordTraces {
		return
	}

	pdTraces := record.Data.(ptrace.Traces)
	foreach.SpansWithResourceRemoveIf(pdTraces, func(rs pcommon.Map, span ptrace.Span) bool {
	Loop:
		for _, rule := range config.Drop {
			if len(rule.MatchRules) == 0 {
				continue
			}

			for _, matchRule := range rule.MatchRules {
				if !p.matchField(rs, span, matchRule) {
					continue Loop
				}
			}
			return true
		}
		return false
	})
}

func (p *spanProcessor) matchField(resource pcommon.Map, span ptrace.Span, matchRule MatchRule) bool {
	ff, pk := fields.DecodeFieldFrom(matchRule.Key)

	var actual string
	var ok bool
	switch ff {
	case fields.FieldFromMethod:
		actual, ok = p.fetcher.FetchMethodWithOk(span, pk)
	case fields.FieldFromResource:
		if rv, found := resource.Get(pk); found {
			actual, ok = rv.AsString(), true
		}
	case fields.FieldFromAttributes:
		if attrVal, found := span.Attributes().Get(pk); found {
			actual, ok = attrVal.AsString(), true
		}
	default:
		logger.Warnf("spanProcessor: unsupported match field %s", matchRule.Key)
		return false
	}

	if !ok {
		return false
	}

	if matchRule.Link == LinkOr {
		for _, v := range matchRule.Value {
			if opmatch.Match(actual, v, matchRule.Op) {
				return true
			}
		}
		return false
	}

	// default: "and" — 所有值都必须匹配
	for _, v := range matchRule.Value {
		if !opmatch.Match(actual, v, matchRule.Op) {
			return false
		}
	}
	return true
}

func (p *spanProcessor) replaceValueAction(record *define.Record, config Config) {
	if record.RecordType != define.RecordTraces {
		return
	}

	pdTraces := record.Data.(ptrace.Traces)
	foreach.SpansWithResource(pdTraces, func(rs pcommon.Map, span ptrace.Span) {
		replaceMapping := make(map[string]string)
		for _, fieldRule := range config.ReplaceValue {
			if !p.checkSpanField(span, fieldRule.PredicateKey) {
				continue
			}

		Loop:
			for _, rule := range fieldRule.Rules {
				// 过滤条件不存在时跳过
				if len(rule.Filters) == 0 {
					continue
				}

				for _, filter := range rule.Filters {
					if !p.matchField(rs, span, filter) {
						continue Loop
					}
				}

				replaceMapping[fieldRule.PredicateKey] = p.processReplaceValue(rs, span, rule.From)
				break Loop
			}
		}
		p.updateFieldValue(span, replaceMapping)
	})
}

func (p *spanProcessor) checkSpanField(span ptrace.Span, key string) bool {
	ff, pk := fields.DecodeFieldFrom(key)
	switch ff {
	case fields.FieldFromMethod:
		_, ok := p.fetcher.FetchMethodWithOk(span, pk)
		return ok
	case fields.FieldFromAttributes:
		_, ok := span.Attributes().Get(pk)
		return ok
	default:
		return false
	}
}

func (p *spanProcessor) processReplaceValue(resource pcommon.Map, span ptrace.Span, config ReplaceFrom) string {
	if len(config.Source) == 0 {
		return config.ConstVal
	}

	values := make([]string, 0, len(config.Source))
	for _, field := range config.Source {
		value := config.ConstVal
		ff, pk := fields.DecodeFieldFrom(field)
		switch ff {
		case fields.FieldFromMethod:
			if v, ok := p.fetcher.FetchMethodWithOk(span, pk); ok && v != "" {
				value = v
			}
		case fields.FieldFromResource:
			if rv, ok := resource.Get(pk); ok && rv.AsString() != "" {
				value = rv.AsString()
			}
		case fields.FieldFromAttributes:
			if attrVal, ok := span.Attributes().Get(pk); ok && attrVal.AsString() != "" {
				value = attrVal.AsString()
			}
		}

		values = append(values, value)
	}

	return strings.Join(values, config.Separator)
}

func (p *spanProcessor) updateFieldValue(span ptrace.Span, updateMapping map[string]string) {
	for field, value := range updateMapping {
		ff, pk := fields.DecodeFieldFrom(field)
		switch ff {
		case fields.FieldFromMethod:
			// 目前仅支持 span_name 更新，后续如果有其他需要再增加
			if pk == "span_name" {
				span.SetName(value)
			}
		case fields.FieldFromAttributes:
			span.Attributes().UpsertString(pk, value)
		default:
			logger.Warnf("spanProcessor: unsupported update field %s", field)
		}
	}
}
