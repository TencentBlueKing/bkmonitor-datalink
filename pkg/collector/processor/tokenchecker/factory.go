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
	"github.com/mitchellh/mapstructure"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
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
	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	return &tokenChecker{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		config:          c,
		decoder:         NewTokenDecoder(c),
	}, nil
}

type tokenChecker struct {
	processor.CommonProcessor
	config  Config
	decoder TokenDecoder
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
	var err error
	switch record.RecordType {
	case define.RecordTraces:
		err = p.processTraces(record)
	case define.RecordMetrics:
		err = p.processMetrics(record)
	case define.RecordLogs:
		err = p.processLogs(record)
	case define.RecordPushGateway, define.RecordRemoteWrite:
		err = p.processCommon(record)
	}
	return nil, err
}

func (p tokenChecker) processTraces(record *define.Record) error {
	pdTraces, ok := record.Data.(ptrace.Traces)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if p.decoder.Skip() {
		record.Token, err = p.decoder.Decode("")
		return err
	}

	var errs []error
	pdTraces.ResourceSpans().RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		v, ok := resourceSpans.Resource().Attributes().Get(p.config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdTraces token key '%s'", p.config.ResourceKey)
			return true
		}
		record.Token, err = p.decoder.Decode(v.AsString())
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

func (p tokenChecker) processMetrics(record *define.Record) error {
	pdMetrics, ok := record.Data.(pmetric.Metrics)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if p.decoder.Skip() {
		record.Token, err = p.decoder.Decode("")
		return err
	}

	var errs []error
	pdMetrics.ResourceMetrics().RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
		v, ok := resourceMetrics.Resource().Attributes().Get(p.config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdMetrics token key '%s'", p.config.ResourceKey)
			return true
		}
		record.Token, err = p.decoder.Decode(v.AsString())
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

func (p tokenChecker) processLogs(record *define.Record) error {
	pdLogs, ok := record.Data.(plog.Logs)
	if !ok {
		return define.ErrUnknownRecordType
	}

	var err error
	if p.decoder.Skip() {
		record.Token, err = p.decoder.Decode("")
		return err
	}

	var errs []error
	pdLogs.ResourceLogs().RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		v, ok := resourceLogs.Resource().Attributes().Get(p.config.ResourceKey)
		if !ok {
			logger.Debugf("failed to get pdLogs token key '%s'", p.config.ResourceKey)
			return true
		}
		record.Token, err = p.decoder.Decode(v.AsString())
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

func (p tokenChecker) processCommon(record *define.Record) error {
	var err error
	if p.decoder.Skip() {
		record.Token, err = p.decoder.Decode("")
		return err
	}

	record.Token, err = p.decoder.Decode(record.Token.Original)
	return err
}
