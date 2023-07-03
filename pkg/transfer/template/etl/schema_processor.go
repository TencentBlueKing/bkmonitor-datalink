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

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// PayloadDecoder :
type Decoder func(d define.Payload) ([]etl.Container, error)

// RecordProcessor :
type RecordProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	Decode Decoder
	schema etl.Transformer
}

// Process : process json data
func (p *RecordProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	containers, err := p.Decode(d)
	if err != nil {
		logging.MinuteErrorfSampling(p.String(), "%v load %#v error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	if len(containers) == 0 {
		logging.Debugf("%v loaded an empty payload %v", p, d)
		p.CounterFails.Inc()
		return
	}

	handled := 0
	for _, from := range containers {
		if bizID, err := from.Get(define.RecordBizID); err == nil {
			if _, ok := p.DisabledBizIDs[conv.String(bizID)]; ok {
				p.CounterSkip.Inc()
				return
			}
		}

		to := etl.NewMapContainer()
		err = p.schema.Transform(from, to)
		if err != nil {
			logging.MinuteErrorfSampling(p.String(), "%v transform %v error %v", p, d, err)
			continue
		}

		output, err := define.DerivePayload(d, &to)
		if err != nil {
			logging.Errorf("%v create payload from %v error: %+v", p, d, err)
			continue
		}

		outputChan <- output
		handled++
	}

	if handled == 0 {
		logging.Warnf("%v handle %#v failed", p, d)
		p.CounterFails.Inc()
	} else {
		logging.Debugf("%v push %d items from %v", p, handled, d)
		p.CounterSuccesses.Inc()
	}
}

// NewRecordProcessor :
func NewRecordProcessor(name string, pipeConfig *config.PipelineConfig, schema etl.Transformer) *RecordProcessor {
	return NewRecordProcessorWithDecoderFn(name, pipeConfig, schema, etl.NewPayloadDecoder().Decode)
}

func NewRecordProcessorWithContext(ctx context.Context, name string, pipeConfig *config.PipelineConfig, schema etl.Transformer) *RecordProcessor {
	recordProcessor := NewRecordProcessorWithDecoderFn(name, pipeConfig, schema, etl.NewPayloadDecoder().Decode)
	recordProcessor.DisabledBizIDs = config.ResultTableConfigFromContext(ctx).DisabledBizID()
	return recordProcessor
}

func NewRecordProcessorWithDecoderFnWithContext(ctx context.Context, name string, pipeConfig *config.PipelineConfig, schema etl.Transformer, fn Decoder) *RecordProcessor {
	recordProcessor := NewRecordProcessorWithDecoderFn(name, pipeConfig, schema, fn)
	recordProcessor.DisabledBizIDs = config.ResultTableConfigFromContext(ctx).DisabledBizID()
	return recordProcessor
}

// NewRecordProcessorWithDecoderFn :
func NewRecordProcessorWithDecoderFn(name string, pipeConfig *config.PipelineConfig, schema etl.Transformer, fn Decoder) *RecordProcessor {
	return &RecordProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, pipeConfig),
		schema:            schema,
		Decode:            fn,
	}
}
