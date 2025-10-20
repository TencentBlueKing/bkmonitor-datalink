// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorTracesDeriver, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*tracesDeriver, error) {
	operators := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	operators.SetGlobal(NewOperator(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		operators.Set(custom.Token, custom.Type, custom.ID, NewOperator(cfg))
	}

	return &tracesDeriver{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		operators:       operators,
	}, nil
}

type tracesDeriver struct {
	processor.CommonProcessor
	operators *confengine.TierConfig // type: Operator
}

func (p *tracesDeriver) Name() string {
	return define.ProcessorTracesDeriver
}

func (p *tracesDeriver) IsDerived() bool {
	return true
}

func (p *tracesDeriver) IsPreCheck() bool {
	return false
}

func (p *tracesDeriver) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	equal := processor.DiffMainConfig(p.MainConfig(), config)
	if equal {
		f.operators.GetGlobal().(Operator).Clean()
	} else {
		p.operators.GetGlobal().(Operator).Clean()
		p.operators.SetGlobal(f.operators.GetGlobal())
	}

	diffRet := processor.DiffCustomizedConfig(p.SubConfigs(), customized)
	for _, obj := range diffRet.Keep {
		f.operators.Get(obj.Token, obj.Type, obj.ID).(Operator).Clean()
	}

	for _, obj := range diffRet.Updated {
		p.operators.Get(obj.Token, obj.Type, obj.ID).(Operator).Clean()
		newOperator := f.operators.Get(obj.Token, obj.Type, obj.ID)
		p.operators.Set(obj.Token, obj.Type, obj.ID, newOperator)
	}

	for _, obj := range diffRet.Deleted {
		p.operators.Get(obj.Token, obj.Type, obj.ID).(Operator).Clean()
		p.operators.Del(obj.Token, obj.Type, obj.ID)
	}

	p.CommonProcessor = f.CommonProcessor
}

func (p *tracesDeriver) Clean() {
	for _, obj := range p.operators.All() {
		obj.(Operator).Clean()
	}
}

func (p *tracesDeriver) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordTraces:
		operator := p.operators.GetByToken(record.Token.Original).(Operator)
		r := operator.Operate(record)
		return r, nil
	}

	return nil, nil
}
