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

type FlatBatchHandler struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	ctx    context.Context
	schema etl.Schema
}

const (
	batchKey = "items"
)

// extractFromItems 从 items field 里面提取 containers
func (p *FlatBatchHandler) extractFromItems(originMap etl.MapContainer) ([]etl.Container, bool) {
	obj, ok := originMap[batchKey]
	if !ok {
		return nil, false
	}

	items, ok := obj.([]interface{})
	if !ok {
		return nil, false
	}

	containers := make([]etl.Container, 0, len(items))
	for i := 0; i < len(items); i++ {
		container := originMap.Copy()

		var attrs map[string]interface{}
		switch value := items[i].(type) {
		case etl.Container:
			attrs = etl.ContainerToMap(value)
		case map[string]interface{}:
			attrs = value
		default:
			logging.Warnf("%v got unexpected container type %T", p, items[i])
			continue
		}

		for key, value := range attrs {
			_ = container.Put(key, value)
		}
		containers = append(containers, container)
	}

	return containers, true
}

func (p *FlatBatchHandler) extractContainers(originMap etl.MapContainer) []etl.Container {
	// 优先尝试从 items 里面提取
	containers, ok := p.extractFromItems(originMap)
	if ok {
		return containers
	}

	// 提取不到再尝试走原始 map（日志聚类）
	return []etl.Container{originMap}
}

func (p *FlatBatchHandler) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	originMap := etl.NewMapContainer()
	err := d.To(&originMap)
	if err != nil {
		p.CounterFails.Inc()
		logging.Errorf("%v convert payload %#v error %v", p, d, err)
		return
	}

	containers := p.extractContainers(originMap)
	if len(containers) == 0 {
		logging.Debugf("%v loaded an empty payload %v", p, d)
		p.CounterFails.Inc()
		return
	}

	handled := 0
	for _, from := range containers {
		if bizID, err := from.Get(define.RecordBizID); err == nil {
			if _, ok := p.DisabledBizIDs[conv.String(bizID)]; ok {
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

func NewFlatBatchHandler(ctx context.Context, name string) (*FlatBatchHandler, error) {
	schema, err := NewSchema(ctx)
	if err != nil {
		return nil, err
	}

	return &FlatBatchHandler{
		ctx:               ctx,
		schema:            schema,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("flat_batch_handler", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		return NewFlatBatchHandler(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
