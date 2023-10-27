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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// Passer 一个简单的传输节点，主要用来处理一些特殊操作
type Passer struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

// Process
func (p *Passer) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	outputChan <- d
	p.CounterSuccesses.Inc()
}

// NewGroupInjector :
func NewPasser(ctx context.Context, name string) (*Passer, error) {
	return &Passer{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("passer", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
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
		return NewPasser(ctx, pipeConfig.FormatName(name))
	})
}
