// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package textspliter

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorTextSpliter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*textSpliter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &textSpliter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type textSpliter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p *textSpliter) Name() string {
	return define.ProcessorTextSpliter
}

func (p *textSpliter) IsDerived() bool {
	return false
}

func (p *textSpliter) IsPreCheck() bool {
	return false
}

func (p *textSpliter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *textSpliter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.Separator) == 0 {
		return nil, nil
	}

	switch record.RecordType {
	case define.RecordLogPush:
		data := record.Data.(*define.LogPushData)
		if len(data.Data) == 0 {
			return nil, nil
		}
		split := strings.Split(data.Data[0], config.Separator)
		data.Data = make([]string, 0, len(split))
		for i := 0; i < len(split); i++ {
			// 仅保留有内容的数据
			if len(split[i]) > 0 {
				data.Data = append(data.Data, split[i])
			}
		}
	}
	return nil, nil
}
