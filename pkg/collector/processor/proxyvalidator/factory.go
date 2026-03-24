// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxyvalidator

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorProxyValidator, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*proxyValidator, error) {
	validators := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	validators.SetGlobal(NewValidator(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		validators.Set(custom.Token, custom.Type, custom.ID, NewValidator(cfg))
	}

	return &proxyValidator{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		validators:      validators,
	}, nil
}

type proxyValidator struct {
	processor.CommonProcessor
	validators *confengine.TierConfig // type: Validator
}

func (p *proxyValidator) Name() string {
	return define.ProcessorProxyValidator
}

func (p *proxyValidator) IsDerived() bool {
	return false
}

func (p *proxyValidator) IsPreCheck() bool {
	return true
}

func (p *proxyValidator) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.validators = f.validators
}

func (p *proxyValidator) Process(record *define.Record) (*define.Record, error) {
	validator := p.validators.GetByToken(record.Token.Original).(Validator)

	switch record.RecordType {
	case define.RecordProxy:
		pd := record.Data.(*define.ProxyData)
		return nil, validator.Validate(pd)
	}
	return nil, errors.Errorf("unsupported record type: %s", record.RequestType.S())
}
