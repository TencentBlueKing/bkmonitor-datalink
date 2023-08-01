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
	"github.com/mitchellh/mapstructure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorTracesDeriver, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*tracesDeriver, error) {
	configs := confengine.NewTierConfig()
	operators := confengine.NewTierConfig()

	var c Config
	if err := mapstructure.Decode(conf, &c); err != nil {
		return nil, err
	}
	configs.SetGlobal(c)
	operators.SetGlobal(NewTracesOperator(c))

	for _, custom := range customized {
		var cfg Config
		if err := mapstructure.Decode(custom.Config.Config, &cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, cfg)
		operators.Set(custom.Token, custom.Type, custom.ID, NewTracesOperator(cfg))
	}

	return &tracesDeriver{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
		operators:       operators,
	}, nil
}

type tracesDeriver struct {
	processor.CommonProcessor
	configs   *confengine.TierConfig // type: Config
	operators *confengine.TierConfig // type: Operator
}

func (p tracesDeriver) Name() string {
	return define.ProcessorTracesDeriver
}

func (p tracesDeriver) IsDerived() bool {
	return true
}

func (p tracesDeriver) IsPreCheck() bool {
	return false
}

func (p tracesDeriver) Clean() {
	for _, obj := range p.operators.All() {
		obj.(Operator).Clean()
	}
}

func (p tracesDeriver) Process(record *define.Record) (*define.Record, error) {
	switch record.RecordType {
	case define.RecordTraces:
		operator := p.operators.GetByToken(record.Token.Original).(Operator)
		r := operator.Operate(record)
		return r, nil
	}

	return nil, nil
}
