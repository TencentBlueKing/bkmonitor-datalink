// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package methodfilter

import (
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
	processor.Register(define.ProcessorMethodFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*methodFilter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	configs.SetGlobal(NewConfigHandler(c))

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, NewConfigHandler(cfg))
	}

	return &methodFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		fetcher:         fields.NewSpanFieldFetcher(),
		configs:         configs,
	}, nil
}

type methodFilter struct {
	processor.CommonProcessor
	fetcher fields.SpanFieldFetcher
	configs *confengine.TierConfig // type: *ConfigHandler
}

func (p *methodFilter) Name() string {
	return define.ProcessorMethodFilter
}

func (p *methodFilter) IsDerived() bool {
	return false
}

func (p *methodFilter) IsPreCheck() bool {
	return false
}

func (p *methodFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.fetcher = f.fetcher
	p.configs = f.configs
}

func (p *methodFilter) Process(record *define.Record) (*define.Record, error) {
	ch := p.configs.GetByToken(record.Token.Original).(*ConfigHandler)
	if len(ch.dropSpanRules) > 0 {
		p.dropSpanAction(record)
	}

	return nil, nil
}

func (p *methodFilter) dropSpanAction(record *define.Record) {
	switch record.RecordType {
	case define.RecordTraces:
		ch := p.configs.GetByToken(record.Token.Original).(*ConfigHandler)
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansRemoveIf(pdTraces, func(span ptrace.Span) bool {
			rules := ch.Get(span.Kind().String())
			for _, rule := range rules {
				val := p.fetcher.FetchMethod(span, rule.PredicateKey)
				if val == "" {
					continue
				}

				if !opmatch.Match(val, rule.MatchConfig.Value, rule.MatchConfig.Op) {
					continue
				}
				return true
			}
			return false
		})
	}
}
