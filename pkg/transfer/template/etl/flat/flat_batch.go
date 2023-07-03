// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flat

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ItemsName
const (
	ItemsName = "items"
)

// NewBatchProcessor
func NewBatchProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	// 通过pipeline的整体option中，获取该次需要flat偏平化的字段内容
	pipeConfig := config.PipelineConfigFromContext(ctx)
	util := utils.MapHelper{Data: pipeConfig.Option}
	pipeLineItemName := util.GetOrDefault(config.PipelineConfigOptFlatBatchKey, ItemsName).(string)

	var decoder *etl.PayloadDecoder
	if pipeConfig.ETLConfig == pipeline.TypeFlatBatch {
		// 如果是日志上报的流水线，可以接受item为空的情况，但是依然需要需要返回数据
		// 这里只要其拆分的功能
		decoder = etl.NewPayloadDecoder().FissionSplitHandler(
			false, etl.ExtractByJMESPath(pipeLineItemName), "", pipeLineItemName,
		)
	} else {
		// 如果是自定义事件和自定义时序的上报，必须要提取到字段，否则丢弃数据
		decoder = etl.NewPayloadDecoder().FissionSplitHandler(
			true, etl.ExtractByJMESPath(pipeLineItemName), "", pipeLineItemName,
		)
	}

	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx), etl.NewFunctionalRecord("", func(from etl.Container, to etl.Container) error {
			for _, key := range from.Keys() {
				v, err := from.Get(key)
				if err != nil {
					return err
				}
				err = to.Put(key, v)
				if err != nil {
					return err
				}
			}

			v, err := from.Get(pipeLineItemName)
			if err != nil {
				// 没有pipeLineItemName field 可以直接跳过
				return nil
			}

			var items map[string]interface{}

			switch value := v.(type) {
			case etl.Container:
				items = etl.ContainerToMap(value)
			case map[string]interface{}:
				items = value
			default:
				return errors.Wrapf(define.ErrType, "%T", v)
			}

			for key, value := range items {
				err = to.Put(key, value)
				if err != nil {
					return err
				}
			}

			return nil
		}), decoder.Decode,
	), nil
}

func init() {
	define.RegisterDataProcessor("flat-batch", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewBatchProcessor(ctx, pipeConfig.FormatName(name))
	})
}
