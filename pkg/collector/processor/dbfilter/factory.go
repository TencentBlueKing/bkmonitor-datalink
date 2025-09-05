// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dbfilter

import (
	"go.opentelemetry.io/collector/pdata/ptrace"
	semconv "go.opentelemetry.io/collector/semconv/v1.8.0"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorDbFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*dbFilter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Setup()
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Setup()
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &dbFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

const (
	flagNotSlowQuery = iota
	flagSlowQuery
)

type dbFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p *dbFilter) Name() string {
	return define.ProcessorDbFilter
}

func (p *dbFilter) IsDerived() bool {
	return false
}

func (p *dbFilter) IsPreCheck() bool {
	return false
}

func (p *dbFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *dbFilter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.SlowQuery.Rules) > 0 {
		p.processSlowQuery(record, config)
	}
	return nil, nil
}

func (p *dbFilter) processSlowQuery(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces.ResourceSpans(), func(span ptrace.Span) {
			attrs := span.Attributes()

			// 先确定 db.system 属性是否存在 不存在代表非 db 类型 span 则无需处理
			dbSystem, ok := attrs.Get(semconv.AttributeDBSystem)
			if !ok {
				return
			}

			// 判断是否存在处理规则 如若不存在 也不需要处理
			threshold, ok := config.GetSlowQueryConf(dbSystem.AsString())
			if !ok {
				return
			}

			duration := int64(span.EndTimestamp() - span.StartTimestamp())
			if duration > threshold.Nanoseconds() {
				attrs.UpsertInt(config.SlowQuery.Destination, flagSlowQuery)
			} else {
				attrs.UpsertInt(config.SlowQuery.Destination, flagNotSlowQuery)
			}
		})
	}
}
