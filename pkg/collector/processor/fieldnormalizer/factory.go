// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fieldnormalizer

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
	processor.Register(define.ProcessorFieldNormalizer, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*fieldNormalizer, error) {
	normalizers := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	normalizers.SetGlobal(NewSpanFieldNormalizer(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		normalizers.Set(custom.Token, custom.Type, custom.ID, NewSpanFieldNormalizer(cfg))
	}

	return &fieldNormalizer{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		normalizers:     normalizers,
	}, nil
}

type fieldNormalizer struct {
	processor.CommonProcessor
	normalizers *confengine.TierConfig // type: *SpanFieldNormalizer
}

func (p *fieldNormalizer) Name() string {
	return define.ProcessorFieldNormalizer
}

func (p *fieldNormalizer) IsDerived() bool {
	return false
}

func (p *fieldNormalizer) IsPreCheck() bool {
	return false
}

func (p *fieldNormalizer) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.normalizers = f.normalizers
}

func (p *fieldNormalizer) Process(record *define.Record) (derivedRecord *define.Record, err error) {
	normalizer := p.normalizers.GetByToken(record.Token.Original).(*SpanFieldNormalizer)
	if normalizer.Keys() == 0 {
		return nil, nil
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.Spans(pdTraces, func(span ptrace.Span) {
			normalizer.Normalize(span, span.Kind().String())
			normalizer.Normalize(span, "")
		})
	}

	return nil, nil
}
