// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorTokenChecker, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*tokenChecker, error) {
	decoders := confengine.NewTierConfig()
	configs := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	decoders.SetGlobal(NewTokenDecoder(c))
	configs.SetGlobal(c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		decoders.Set(custom.Token, custom.Type, custom.ID, NewTokenDecoder(*cfg))
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &tokenChecker{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		decoders:        decoders,
		configs:         configs,
	}, nil
}

type tokenChecker struct {
	processor.CommonProcessor
	decoders *confengine.TierConfig // type: Decoder
	configs  *confengine.TierConfig // type: Config
}

func (p tokenChecker) Name() string {
	return define.ProcessorTokenChecker
}

func (p tokenChecker) IsDerived() bool {
	return false
}

func (p tokenChecker) IsPreCheck() bool {
	return true
}

func (p tokenChecker) Process(record *define.Record) (*define.Record, error) {
	decoder := p.decoders.GetByToken(record.Token.Original).(TokenDecoder)
	config := p.configs.GetByToken(record.Token.Original).(Config)

	var err error
	switch record.RecordType {
	case define.RecordTraces:
		err = p.processTraces(decoder, config, record)
	case define.RecordMetrics:
		err = p.processMetrics(decoder, config, record)
	case define.RecordLogs:
		err = p.processLogs(decoder, config, record)
	case define.RecordProxy:
		err = p.processProxy(decoder, record)
	default:
		err = p.processCommon(decoder, record)
	}
	return nil, err
}

func (p tokenChecker) processTraces(decoder TokenDecoder, config Config, record *define.Record) error {
	pdTraces, ok := record.Data.(ptrace.Traces)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdTraces.ResourceSpans().RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		v, ok := resourceSpans.Resource().Attributes().Get(config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdTraces token key '%s'", config.ResourceKey)
			return true
		}
		record.Token, err = decoder.Decode(v.AsString())
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdTraces token=%v, err: %v", v.AsString(), err)
			return true
		}
		return false
	})

	if len(errs) > 0 {
		return errs[0]
	}

	if pdTraces.ResourceSpans().Len() == 0 {
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p tokenChecker) processMetrics(decoder TokenDecoder, config Config, record *define.Record) error {
	pdMetrics, ok := record.Data.(pmetric.Metrics)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdMetrics.ResourceMetrics().RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
		v, ok := resourceMetrics.Resource().Attributes().Get(config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdMetrics token key '%s'", config.ResourceKey)
			return true
		}
		record.Token, err = decoder.Decode(v.AsString())
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdMetrics token=%v, err: %v", v.AsString(), err)
			return true
		}
		return false
	})

	if len(errs) > 0 {
		return errs[0]
	}

	if pdMetrics.ResourceMetrics().Len() == 0 {
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p tokenChecker) processLogs(decoder TokenDecoder, config Config, record *define.Record) error {
	pdLogs, ok := record.Data.(plog.Logs)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	var errs []error
	pdLogs.ResourceLogs().RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		v, ok := resourceLogs.Resource().Attributes().Get(config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdLogs token key '%s'", config.ResourceKey)
			return true
		}
		record.Token, err = decoder.Decode(v.AsString())
		if err != nil {
			errs = append(errs, err)
			logger.Errorf("failed to parse pdLogs token=%v, err: %v", v.AsString(), err)
			return true
		}
		return false
	})

	if len(errs) > 0 {
		return errs[0]
	}

	if pdLogs.ResourceLogs().Len() == 0 {
		return define.ErrSkipEmptyRecord
	}
	return nil
}

func (p tokenChecker) processProxy(decoder TokenDecoder, record *define.Record) error {
	var err error
	record.Token, err = decoder.Decode(define.WrapProxyToken(record.Token))
	return err
}

func (p tokenChecker) processCommon(decoder TokenDecoder, record *define.Record) error {
	var err error
	if decoder.Skip() {
		record.Token, err = decoder.Decode("")
		return err
	}

	record.Token, err = decoder.Decode(record.Token.Original)
	return err
}
