// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// RecordHandlers :
type RecordHandlers []define.ETLRecordChainingHandler

// Product :
func (f RecordHandlers) Handle(record *define.ETLRecord, callback define.ETLRecordHandler) error {
	for i := len(f) - 1; i >= 0; i-- {
		fn := f[i]
		if fn != nil {
			callback = define.ETLRecordHandlerWrapper(f[i], callback)
		}
	}
	if err := callback(record); err != nil {
		logging.Debugf("hadler run failed, handler: %v, record: %v, err: %+v", f, record, err)
		return err
	}
	return nil
}

// Processor :
type Processor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	pipelineConfig *config.PipelineConfig
	handlers       RecordHandlers
}

// Process : process json data
func (p *Processor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(define.ETLRecord)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Errorf("%v convert payload %#v error %v", p, d, err)
		return
	}

	err = p.handlers.Handle(record, func(record *define.ETLRecord) error {
		payload, err := define.DerivePayload(d, record)
		if err != nil {
			logging.Warnf("%v handle payload %#v failed: %v", p, d, err)
			return err
		}

		outputChan <- payload
		return nil
	})

	if err != nil {
		p.CounterFails.Inc()
		logging.MinuteErrorfSampling(p.String(), "%v handle payload %#v failed: %v", p, d, err)
		return
	}
	p.CounterSuccesses.Inc()
}

// NewProcessor :
func NewProcessor(ctx context.Context, name string, handlers RecordHandlers) *Processor {
	pipeConf := config.PipelineConfigFromContext(ctx)
	p := &Processor{
		pipelineConfig:    pipeConf,
		BaseDataProcessor: define.NewBaseDataProcessor(pipeConf.FormatName(name)),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		handlers:          handlers,
	}

	return p
}
