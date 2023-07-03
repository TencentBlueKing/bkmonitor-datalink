// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sampler

import (
	"github.com/mitchellh/mapstructure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorSampler, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*handler, error) {
	configs := confengine.NewTierConfig()
	samplers := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	configs.SetGlobal(c)
	samplers.SetGlobal(NewSampler(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, cfg)
		samplers.Set(custom.Token, custom.Type, custom.ID, NewSampler(cfg))
	}

	return &handler{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
		samplers:        samplers,
	}, nil
}

type handler struct {
	processor.CommonProcessor
	configs  *confengine.TierConfig // type: Config
	samplers *confengine.TierConfig // type: Sampler
}

func (p handler) Name() string {
	return define.ProcessorSampler
}

func (p handler) Clean() {}

func (p handler) IsDerived() bool {
	return false
}

func (p handler) IsPreCheck() bool {
	return false
}

func (p handler) Process(record *define.Record) (*define.Record, error) {
	sampler := p.samplers.GetByToken(record.Token.Original).(Sampler)
	sampler.Sample(record)
	return nil, nil
}
