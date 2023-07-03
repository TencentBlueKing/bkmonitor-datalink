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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// GroupedRecord
type GroupedRecord struct {
	define.ETLRecord
	Groups []map[string]string `json:"group_info"`
}

// GroupInjector
type GroupInjector struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

// Process
func (p *GroupInjector) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	raw := new(GroupedRecord)
	err := d.To(raw)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert payload %#v error %v", p, d, err)
		return
	}

	// skip when groups is empty
	if raw.Groups == nil {
		outputChan <- d
		p.CounterSuccesses.Inc()
		return
	}

	record := define.ETLRecord{
		Time:     raw.Time,
		Metrics:  raw.Metrics,
		Exemplar: raw.Exemplar,
	}
	for _, group := range raw.Groups {
		if group == nil {
			continue
		}

		dimensions := make(map[string]interface{})
		for key, value := range raw.Dimensions {
			dimensions[key] = value
		}
		for key, value := range group {
			dimensions[key] = value
		}
		record.Dimensions = dimensions

		payload, err := define.DerivePayload(d, record)
		if err == nil {
			outputChan <- payload
		} else {
			logging.Warnf("%v derive payload %#v error %v", p, d, err)
		}
	}

	p.CounterSuccesses.Inc()
}

// NewGroupInjector :
func NewGroupInjector(ctx context.Context, name string) (*GroupInjector, error) {
	return &GroupInjector{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("group_injector", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewGroupInjector(ctx, pipeConfig.FormatName(name))
	})
}
