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
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
	"go.opentelemetry.io/collector/pdata/pcommon"
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
		p.asStringAction(record, config)
	}
	if len(config.AsInt.Keys) > 0 {
		p.asIntAction(record, config)
	}
	if config.FromToken.BizId != "" || config.FromToken.AppName != "" {
		p.fromTokenAction(record, config)
	}
	if len(config.Assemble) > 0 {
		p.assembleAction(record, config)
	}
	if len(config.Drop) > 0 {
		p.dropAction(record, config)
	}
	if len(config.Cut) > 0 {
		p.cutAction(record, config)
	}

	return nil, nil
}

func (p attributeFilter) fromTokenAction(record *define.Record, config Config) {
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
		foreach.Metrics(pdMetrics.ResourceMetrics(), func(metric pmetric.Metric) {
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
		})
	}
}

func (p attributeFilter) asStringAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansWithResource(pdTraces.ResourceSpans(), func(resource pcommon.Resource, span ptrace.Span) {
			for _, key := range config.AsString.Keys {
				rsAttrs := resource.Attributes()
				if v, ok := rsAttrs.Get(key); ok {
					rsAttrs.UpsertString(key, v.AsString())
				}

				attributes := span.Attributes()
				if v, ok := attributes.Get(key); ok {
					attributes.UpsertString(key, v.AsString())
				}
			}
		})
	}
}

func processAsIntAction(attributes pcommon.Map, key string) {
	v, ok := attributes.Get(key)
	if !ok {
		return
	}

	i, err := strconv.ParseInt(v.AsString(), 10, 64)
	if err != nil {
		logger.Debugf("parse attribute key '%s' as int failed, error: %s", key, err)
		return
	}
	attributes.UpsertInt(key, i)
}

func (p attributeFilter) asIntAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansWithResource(pdTraces.ResourceSpans(), func(resource pcommon.Resource, span ptrace.Span) {
			for _, key := range config.AsInt.Keys {
				processAsIntAction(resource.Attributes(), key)
				processAsIntAction(span.Attributes(), key)
			}
		})
	}
}

const unknownVal = "Unknown"

func (p attributeFilter) assembleAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		foreach.Spans(resourceSpansSlice, func(span ptrace.Span) {
			for _, action := range config.Assemble {
				if !processAssembleAction(span, action) {
					if _, ok := span.Attributes().Get(action.Destination); !ok {
						span.Attributes().UpsertString(action.Destination, unknownVal)
					}
				}
			}
		})
	}
}

func processAssembleAction(span ptrace.Span, action AssembleAction) bool {
	attrs := span.Attributes()
	if _, ok := attrs.Get(action.PredicateKey); !ok {
		// 没有匹配的情况下直接返回，进入下一个循环
		return false
	}

	spanKind := span.Kind().String()
	for _, rule := range action.Rules {
		// 匹配规则中不要求 Kind 类型或 Kind 类型符合要求的时候进行操作
		if rule.Kind == "" || spanKind == rule.Kind {
			fields := make([]string, 0, len(rule.Keys))
			for _, key := range rule.Keys {
				d := unknownVal

				// 常量不需要判断是否存在
				if strings.HasPrefix(key, constPrefix) {
					d = key[len(constPrefix):]
					fields = append(fields, d)
					continue
				}

				// 处理 attributes 属性 支持首字母大写
				if v, ok := attrs.Get(key); ok && v.AsString() != "" {
					if _, exist := rule.upper[key]; exist {
						d = firstUpper(v.AsString())
					} else {
						d = v.AsString()
					}
				}
				fields = append(fields, d)
			}
			// 匹配到直接插入返回，不再进行后续 rule 匹配
			span.Attributes().UpsertString(action.Destination, strings.Join(fields, rule.Separator))
			return true
		}
	}

	// Kind 条件不匹配直接返回，进入下一个循环
	return false
}

// firstUpper 首字母大写
func firstUpper(s string) string {
	if s == "" {
		return unknownVal
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (p attributeFilter) dropAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		foreach.Spans(resourceSpansSlice, func(span ptrace.Span) {
			for _, action := range config.Drop {
				v, ok := span.Attributes().Get(action.PredicateKey)
				if !ok {
					continue
				}

				// 取到 key，但是判定条件不符合的时候
				_, ok = action.match[v.AsString()]
				if len(action.Match) > 0 && !ok {
					continue
				}
				for _, k := range action.Keys {
					span.Attributes().Remove(k)
				}
			}
		})
	}
}

func (p attributeFilter) cutAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		foreach.Spans(resourceSpansSlice, func(span ptrace.Span) {
			for _, action := range config.Cut {
				v, ok := span.Attributes().Get(action.PredicateKey)
				if !ok {
					continue
				}

				// 不符合匹配条件的时候跳过
				_, ok = action.match[v.AsString()]
				if len(action.Match) > 0 && !ok {
					continue
				}

				// preKey 取值 ok 并且 无匹配条件 或 匹配条件符合的情况下
				for _, k := range action.Keys {
					// 无法获取到 key 的值 则跳过
					if v, ok = span.Attributes().Get(k); !ok {
						continue
					}
					// 对于长度超出的情况，进行裁剪
					value := v.AsString()
					if len(value) > action.MaxLength {
						span.Attributes().UpsertString(k, value[:action.MaxLength])
					}
				}
			}
		})
	}
}
