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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

type LogFilter struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

func (p *LogFilter) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	outputChan <- d
	logging.Info("mandotest: process in log_filter")
}

// NewLogFilter :
func NewLogFilter(ctx context.Context, name string) (*LogFilter, error) {
	return &LogFilter{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("log_filter", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewLogFilter(ctx, pipeConfig.FormatName(name))
	})
}
