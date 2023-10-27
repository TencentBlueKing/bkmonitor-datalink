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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// EncodingHandler
type EncodingHandler struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	decoder define.CharSetDecoder
	strict  bool
}

// Process : process json data
func (p *EncodingHandler) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var data []byte
	err := d.To(&data)
	if err != nil {
		logging.Warnf("%v load %#v error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}

	data, err = p.decoder.Bytes(data)
	if err != nil && p.strict {
		logging.Warnf("%v decode %#v error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}

	err = d.From(data)
	if err != nil {
		logging.Errorf("%v dump payload from %v error: %v", p, d, err)
		return
	}

	outputChan <- d
	p.CounterSuccesses.Inc()
}

// NewEncodingHandler
func NewEncodingHandler(ctx context.Context, name string, charset string, strict bool) (*EncodingHandler, error) {
	decoder, err := define.NewCharSetDecoder(charset)
	if err != nil {
		return nil, err
	}
	return &EncodingHandler{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		strict:            strict,
		decoder:           decoder,
	}, nil
}

func init() {
	define.RegisterDataProcessor("encoding", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		helper := utils.NewMapHelper(pipeConfig.Option)
		charset := helper.MustGetString(config.PipelineConfigOptPayloadEncoding)
		strict, _ := helper.GetBool(config.PipelineConfigOptPayloadEncodingStrict)
		return NewEncodingHandler(ctx, name, charset, strict)
	})
}
