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

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
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
	processor.Register(define.ProcessorAttributeFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*attributeFilter, error) {
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

func (p *attributeFilter) Name() string {
	return define.ProcessorAttributeFilter
}

func (p *attributeFilter) IsDerived() bool {
	return false
}

func (p *attributeFilter) IsPreCheck() bool {
	return false
}

func (p *attributeFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *attributeFilter) Process(record *define.Record) (*define.Record, error) {
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

func (p *attributeFilter) fromTokenAction(record *define.Record, config Config) {
	handle := func(attrs pcommon.Map, action FromTokenAction) {
		if action.BizId != "" {
			attrs.UpsertInt(action.BizId, int64(record.Token.BizId))
		}
		if action.AppName != "" {
			attrs.UpsertString(action.AppName, record.Token.AppName)
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			handle(span.Attributes(), config.FromToken)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.Logs(pdLogs, func(logRecord plog.LogRecord) {
			handle(logRecord.Attributes(), config.FromToken)
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsDataPointWithResource(pdMetrics, func(metric pmetric.Metric, _, attrs pcommon.Map) {
			handle(attrs, config.FromToken)
		})
	}
}

func (p *attributeFilter) asStringAction(record *define.Record, config Config) {
	handle := func(attrs pcommon.Map, key string) {
		v, ok := attrs.Get(key)
		if !ok {
			return
		}
		attrs.UpsertString(key, v.AsString())
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			for _, key := range config.AsString.Keys {
				handle(span.Attributes(), key)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.Logs(pdLogs, func(logRecord plog.LogRecord) {
			for _, key := range config.AsString.Keys {
				handle(logRecord.Attributes(), key)
			}
		})
	}
}

func (p *attributeFilter) asIntAction(record *define.Record, config Config) {
	handle := func(attrs pcommon.Map, key string) {
		v, ok := attrs.Get(key)
		if !ok {
			return
		}

		i, err := strconv.ParseInt(v.AsString(), 10, 64)
		if err != nil {
			logger.Debugf("parse attribute key '%s' as int failed, error: %s", key, err)
			return
		}
		attrs.UpsertInt(key, i)
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			for _, key := range config.AsInt.Keys {
				handle(span.Attributes(), key)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.Logs(pdLogs, func(logRecord plog.LogRecord) {
			for _, key := range config.AsInt.Keys {
				handle(logRecord.Attributes(), key)
			}
		})
	}
}

func (p *attributeFilter) assembleAction(record *define.Record, config Config) {
	handle := func(span ptrace.Span, action AssembleAction) bool {
		attrs := span.Attributes()
		if _, ok := attrs.Get(action.PredicateKey); !ok {
			// 没有匹配的情况下直接返回，进入下一个循环
			return false
		}

		spanKind := span.Kind().String()
		for _, rule := range action.Rules {
			// 匹配规则中不要求 Kind 类型或 Kind 类型符合要求的时候进行操作
			if rule.Kind != "" && spanKind != rule.Kind {
				continue
			}

			keys := make([]string, 0, len(rule.Keys))
			for _, key := range rule.Keys {
				// 常量不需要判断是否存在
				if strings.HasPrefix(key, fields.PrefixConst) {
					keys = append(keys, key[len(fields.PrefixConst):])
					continue
				}

				// 处理 attributes 属性 支持首字母大写
				d := rule.Placeholder
				if v, ok := attrs.Get(key); ok && v.AsString() != "" {
					if _, exist := rule.upper[key]; exist {
						d = utils.FirstUpper(v.AsString(), d)
						attrs.UpdateString(key, d)
					} else {
						d = v.AsString()
					}
				}
				keys = append(keys, d)
			}
			// 匹配到直接插入返回，不再进行后续 rule 匹配
			attrs.UpsertString(action.Destination, strings.Join(keys, rule.Separator))
			return true
		}

		// Kind 条件不匹配直接返回，进入下一个循环
		return false
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			for _, action := range config.Assemble {
				if handle(span, action) {
					continue
				}

				attrs := span.Attributes()
				_, ok := attrs.Get(action.Destination)
				if ok {
					continue
				}

				key := action.DefaultFrom
				// 判定常量情况直接插入
				if strings.HasPrefix(key, fields.PrefixConst) {
					attrs.UpsertString(action.Destination, key[len(fields.PrefixConst):])
					continue
				}
				// 匹配到 span_name 插入 span.Name(), 否则直接跳过
				switch key {
				case "span_name":
					attrs.UpsertString(action.Destination, span.Name())
				}
			}
		})
	}
}

func (p *attributeFilter) dropAction(record *define.Record, config Config) {
	handle := func(attrs pcommon.Map, action DropAction) {
		v, ok := attrs.Get(action.PredicateKey)
		if !ok {
			return
		}

		// 取到 key，但是判定条件不符合的时候
		_, ok = action.match[v.AsString()]
		if len(action.Match) > 0 && !ok {
			return
		}
		for _, k := range action.Keys {
			attrs.Remove(k)
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			for _, action := range config.Drop {
				handle(span.Attributes(), action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.Logs(pdLogs, func(logRecord plog.LogRecord) {
			for _, action := range config.Drop {
				handle(logRecord.Attributes(), action)
			}
		})
	}
}

func (p *attributeFilter) cutAction(record *define.Record, config Config) {
	handle := func(attrs pcommon.Map, action CutAction) {
		v, ok := attrs.Get(action.PredicateKey)
		if !ok {
			return
		}

		// 不符合匹配条件的时候跳过
		_, ok = action.match[v.AsString()]
		if len(action.Match) > 0 && !ok {
			return
		}

		// preKey 取值 ok 并且 无匹配条件 或 匹配条件符合的情况下
		for _, k := range action.Keys {
			// 无法获取到 key 的值 则跳过
			if v, ok = attrs.Get(k); !ok {
				continue
			}
			// 对于长度超出的情况，进行裁剪
			value := v.AsString()
			if len(value) > action.MaxLength {
				attrs.UpsertString(k, value[:action.MaxLength])
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			for _, action := range config.Cut {
				handle(span.Attributes(), action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.Logs(pdLogs, func(logRecord plog.LogRecord) {
			for _, action := range config.Cut {
				handle(logRecord.Attributes(), action)
			}
		})
	}
}
