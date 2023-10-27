// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package standard

import (
	"context"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type TimestampConvertProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	ctx      context.Context
	timeUnit string
}

type TimestampRecord struct {
	// ETLRecord，后续需要将内容转移到里面
	define.ETLRecord

	Timestamp *float64               `json:"timestamp"` // bkmonitorproxy 上报过来的数据没有 time 字段
	Dimension map[string]interface{} `json:"dimension"` // bkmonitorproxy 上报过来的数据没有 dimension 字段
	Target    string                 `json:"target"`
}

// Process : process json data
func (p *TimestampConvertProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(TimestampRecord)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert record error %v: %v", p, err, d)
		return
	}

	// 通过 bkmonitorproxy 上报过来只有 timestamp 字段
	if record.Timestamp == nil || *record.Timestamp == 0.0 {
		p.CounterFails.Inc()
		logging.Warnf("%v time series record time is empty: %v", p, d)
		return
	}
	tempTime := conv.Int64(*record.Timestamp)
	record.Time = &tempTime

	if p.timeUnit != "" {
		newTs := utils.ConvertTimeUnitAs(*record.Time, p.timeUnit)
		record.Time = &newTs
	}

	if record.Dimensions == nil {
		// 通过 bkmonitorproxy 上报过来只有 dimension 字段
		record.Dimensions = record.Dimension
	}
	if record.Dimensions == nil {
		record.Dimensions = make(map[string]interface{})
	}
	record.Dimensions["target"] = record.Target

	output, err := define.DerivePayload(d, record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v create payload error %v: %v", p, err, d)
		return
	}

	outputChan <- output

	p.CounterSuccesses.Inc()
}

// NewMetricsReportProcessor :
func NewTimestampConvertProcessor(ctx context.Context, name, timeUnit string) (*TimestampConvertProcessor, error) {
	return &TimestampConvertProcessor{
		ctx:               ctx,
		timeUnit:          timeUnit,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("timestamp_converter", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		opts := utils.NewMapHelper(pipe.Option)
		timeUnit, _ := opts.GetString(config.PipelineConfigOptAlignTimeUnit)

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		return NewTimestampConvertProcessor(ctx, pipe.FormatName(rt.FormatName(name)), timeUnit)
	})
}
