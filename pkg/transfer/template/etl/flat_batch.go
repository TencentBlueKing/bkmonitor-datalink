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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

type FlatBatchPre struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	ctx    context.Context
	schema etl.Schema
}

const (
	batchKey = "items"
)

func (p *FlatBatchPre) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	batch := etl.NewMapContainer()
	err := d.To(&batch)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert payload %#v error %v", p, d, err)
		return
	}

	obj, ok := batch[batchKey]
	if !ok {
		logging.Warnf("%v no 'items' field in flat.batch %v", p, batch)
		return
	}

	items, ok := obj.([]interface{})
	if !ok {
		logging.Warnf("%v got unexpected type %T", p, obj)
		return
	}

	containers := make([]etl.Container, 0, len(items))
	for i := 0; i < len(items); i++ {
		container := batch.Copy()
		_ = container.Put(batchKey, items[i])
		containers = append(containers, container)
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

func NewFlatBatchPre(ctx context.Context, name string) (*FlatBatchPre, error) {
	schema, err := NewSchema(ctx)
	if err != nil {
		return nil, err
	}

	return &FlatBatchPre{
		ctx:               ctx,
		schema:            schema,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("flat_batch_pre", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		return NewFlatBatchPre(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
