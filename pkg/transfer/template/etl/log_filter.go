// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"context"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	nameLogFilter = "log_filter"
)

type LogFilter struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	rules []*utils.MatchRule
}

func (p *LogFilter) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	if len(p.rules) == 0 {
		outputChan <- d
	}

	var dst define.ETLRecord
	if err := d.To(&dst); err != nil {
		p.CounterFails.Inc()
		logging.Errorf("payload %v to record failed: %v", d, err)
		return
	}

	merged := make(map[string]interface{})
	for k, v := range dst.Dimensions {
		merged[k] = v
	}
	for k, v := range dst.Metrics {
		merged[k] = v
	}
	matched := utils.IsRulesMatch(p.rules, merged)

	// 符合匹配规则才需要向后传递
	if matched {
		outputChan <- d
		p.CounterSuccesses.Inc()
		return
	}
	p.CounterFails.Inc()
}

func NewLogFilter(ctx context.Context, name string) (*LogFilter, error) {
	rtOption := config.PipelineConfigFromContext(ctx).Option
	unmarshal := func() ([]*utils.MatchRule, error) {
		obj, ok := rtOption[config.PipelineConfigOptLogClusterConfig]
		if !ok {
			return nil, nil
		}
		conf, ok := obj.(map[string]interface{})
		if !ok {
			return nil, nil
		}

		var rules []*utils.MatchRule
		err := mapstructure.Decode(conf[nameLogFilter], &rules)
		if err != nil {
			return nil, err
		}

		if len(rules) == 0 {
			return nil, nil
		}

		for i := 0; i < len(rules); i++ {
			if err := rules[i].Init(); err != nil {
				return nil, err
			}
		}
		return rules, nil
	}

	rules, err := unmarshal()
	if err != nil {
		logging.Errorf("failed to unmarshal logfilter config: %v", err)
	}

	return &LogFilter{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		rules:             rules,
	}, nil
}

func init() {
	define.RegisterDataProcessor(nameLogFilter, func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		rtConfig := config.ResultTableConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table config is empty")
		}
		rtName := rtConfig.ResultTable
		name = fmt.Sprintf("%s:%s", name, rtName)
		return NewLogFilter(ctx, pipeConfig.FormatName(name))
	})
}
