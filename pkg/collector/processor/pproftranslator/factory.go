// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pproftranslator

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorPprofTranslator, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*pprofTranslator, error) {
	configs := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}

	configs.SetGlobal(NewPprofTranslator(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, NewPprofTranslator(cfg))
	}

	return &pprofTranslator{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type pprofTranslator struct {
	processor.CommonProcessor
	configs *confengine.TierConfig
}

func (p *pprofTranslator) Name() string {
	return define.ProcessorPprofTranslator
}

func (p *pprofTranslator) IsDerived() bool {
	return false
}

func (p *pprofTranslator) IsPreCheck() bool {
	return false
}

func (p *pprofTranslator) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *pprofTranslator) Process(record *define.Record) (*define.Record, error) {
	translator := p.configs.GetByToken(record.Token.Original).(PprofTranslator)

	rawProfile, ok := record.Data.(define.ProfilesRawData)
	if !ok {
		return nil, errors.Errorf("invalid profile data type: %T", record.Data)
	}

	profileData, err := translator.Translate(rawProfile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to translate data to pprof format")
	}

	record.Data = profileData
	return nil, nil
}
