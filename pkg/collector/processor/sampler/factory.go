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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/evaluator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorSampler, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*sampler, error) {
	configs := confengine.NewTierConfig()
	evaluators := confengine.NewTierConfig()

	var c evaluator.Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	configs.SetGlobal(c)
	evaluators.SetGlobal(evaluator.New(c))

	for _, custom := range customized {
		var cfg evaluator.Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, cfg)
		evaluators.Set(custom.Token, custom.Type, custom.ID, evaluator.New(cfg))
	}

	return &sampler{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
		evaluators:      evaluators,
	}, nil
}

type sampler struct {
	processor.CommonProcessor
	configs    *confengine.TierConfig // type: Config
	evaluators *confengine.TierConfig // type: Evaluator
}

func (p sampler) Name() string {
	return define.ProcessorSampler
}

func (p sampler) Clean() {
	for _, obj := range p.evaluators.All() {
		obj.(evaluator.Evaluator).Stop()
	}
}

func (p sampler) IsDerived() bool {
	return false
}

func (p sampler) IsPreCheck() bool {
	return false
}

func (p sampler) Process(record *define.Record) (*define.Record, error) {
	eval := p.evaluators.GetByToken(record.Token.Original).(evaluator.Evaluator)
	eval.Evaluate(record)
	return nil, nil
}
