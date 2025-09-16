// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package servicediscover

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorServiceDiscover, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*serviceDiscover, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Setup()
	configs.SetGlobal(NewConfigHandler(c))

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Setup()
		configs.Set(custom.Token, custom.Type, custom.ID, NewConfigHandler(cfg))
	}

	return &serviceDiscover{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		fetcher:         processor.NewSpanDimensionFetcher(),
		matcher:         NewMatcher(),
		configs:         configs,
	}, nil
}

type serviceDiscover struct {
	processor.CommonProcessor
	matcher Matcher
	fetcher processor.SpanDimensionFetcher
	configs *confengine.TierConfig // type: *ConfigHandler
}

func (p *serviceDiscover) Name() string {
	return define.ProcessorServiceDiscover
}

func (p *serviceDiscover) IsDerived() bool {
	return false
}

func (p *serviceDiscover) IsPreCheck() bool {
	return false
}

func (p *serviceDiscover) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.matcher = f.matcher
	p.fetcher = f.fetcher
	p.configs = f.configs
}

func (p *serviceDiscover) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordTraces:
		p.processTraces(record)
		return nil, nil
	}

	return nil, nil
}

func (p *serviceDiscover) processTraces(record *define.Record) {
	pdTraces := record.Data.(ptrace.Traces)
	ch := p.configs.GetByToken(record.Token.Original).(*ConfigHandler)

	foreach.Spans(pdTraces.ResourceSpans(), func(span ptrace.Span) {
		rules := ch.Get(span.Kind().String())
	loop:
		for _, rule := range rules {
			df, pk := processor.DecodeDimensionFrom(rule.PredicateKey)
			switch df {
			// TODO(mando): 目前 predicateKey 暂时只支持 attributes 后续可能会扩展
			case processor.DimensionFromAttribute:
				// 1) 先判断是否有 predicateKey
				if s := p.fetcher.FetchAttribute(span, pk); s == "" {
					continue
				}
				// 2) 判断是否有 attribute value
				attr := rule.AttributeValue()
				if attr == "" {
					continue
				}
				// 3）判断 attribute value 是否为空
				val := p.fetcher.FetchAttribute(span, attr)
				if val == "" {
					continue
				}

				mappings, matched, _ := rule.Match(val)
				if !matched {
					continue
				}

				p.matcher.Match(span, mappings, rule.ReplaceType)
				break loop

			case processor.DimensionFromMethod:
				// 1) 先判断是否有 predicateKey
				if s := p.fetcher.FetchMethod(span, pk); s == "" {
					continue
				}
				// 2）判断是否有 matchKey
				mkey := rule.MethodValue()
				if mkey == "" {
					continue
				}
				// 3) 判断 matchValue 是否为空
				val := p.fetcher.FetchMethod(span, mkey)
				if val == "" {
					continue
				}

				mappings, matched, _ := rule.Match(val)
				if !matched {
					continue
				}

				p.matcher.Match(span, mappings, rule.ReplaceType)
				break loop
			}
		}
	})
}
