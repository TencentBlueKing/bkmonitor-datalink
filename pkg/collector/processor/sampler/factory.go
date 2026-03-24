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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/evaluator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorSampler, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*sampler, error) {
	evaluators := confengine.NewTierConfig()

	var c evaluator.Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	evaluators.SetGlobal(evaluator.New(c))

	for _, custom := range customized {
		var cfg evaluator.Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		evaluators.Set(custom.Token, custom.Type, custom.ID, evaluator.New(cfg))
	}

	return &sampler{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		evaluators:      evaluators,
	}, nil
}

type sampler struct {
	processor.CommonProcessor
	evaluators *confengine.TierConfig // type: Evaluator
}

func (p *sampler) Name() string {
	return define.ProcessorSampler
}

func (p *sampler) IsDerived() bool {
	return false
}

func (p *sampler) IsPreCheck() bool {
	return false
}

func (p *sampler) Clean() {
	for _, obj := range p.evaluators.All() {
		obj.(evaluator.Evaluator).Stop()
	}
}

func (p *sampler) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	equal := processor.DiffMainConfig(p.MainConfig(), config)
	if equal {
		f.evaluators.GetGlobal().(evaluator.Evaluator).Stop()
	} else {
		p.evaluators.GetGlobal().(evaluator.Evaluator).Stop()
		p.evaluators.SetGlobal(f.evaluators.GetGlobal())
	}

	diffRet := processor.DiffCustomizedConfig(p.SubConfigs(), customized)
	for _, obj := range diffRet.Keep {
		f.evaluators.Get(obj.Token, obj.Type, obj.ID).(evaluator.Evaluator).Stop()
	}

	for _, obj := range diffRet.Updated {
		p.evaluators.Get(obj.Token, obj.Type, obj.ID).(evaluator.Evaluator).Stop()
		newEval := f.evaluators.Get(obj.Token, obj.Type, obj.ID)
		p.evaluators.Set(obj.Token, obj.Type, obj.ID, newEval)
	}

	for _, obj := range diffRet.Deleted {
		p.evaluators.Get(obj.Token, obj.Type, obj.ID).(evaluator.Evaluator).Stop()
		p.evaluators.Del(obj.Token, obj.Type, obj.ID)
	}

	p.CommonProcessor = f.CommonProcessor
}

func (p *sampler) Process(record *define.Record) (*define.Record, error) {
	eval := p.evaluators.GetByToken(record.Token.Original).(evaluator.Evaluator)
	err := eval.Evaluate(record)
	return nil, err
}
