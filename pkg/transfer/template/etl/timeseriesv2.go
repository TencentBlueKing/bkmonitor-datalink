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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type TimeseriesV2Pre struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	ctx      context.Context
	timeUnit string
}

func (p *TimeseriesV2Pre) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	records := &define.CustomTimeseries{}
	err := d.To(records)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert payload %#v error %v", p, d, err)
		return
	}

	for _, orgRec := range records.Data {
		// 通过 bkmonitorproxy 上报过来只有 timestamp 字段
		if orgRec.Timestamp == nil || *orgRec.Timestamp == 0.0 {
			p.CounterFails.Inc()
			logging.Warnf("%v time series orgRec time is empty: %v", p, d)
			return
		}

		tempTime := conv.Int64(*orgRec.Timestamp)
		etlRec := &define.ETLRecord{
			Time:    &tempTime,
			Metrics: orgRec.Metrics,
		}

		if p.timeUnit != "" {
			newTs := utils.ConvertTimeUnitAs(*etlRec.Time, p.timeUnit)
			etlRec.Time = &newTs
		}

		etlRec.Dimensions = orgRec.Dimension
		if etlRec.Dimensions == nil {
			etlRec.Dimensions = make(map[string]interface{})
		}
		etlRec.Dimensions["target"] = orgRec.Target

		output, err := define.DerivePayload(d, orgRec)
		if err != nil {
			p.CounterFails.Inc()
			logging.Warnf("%v create payload error %v: %v", p, err, d)
			return
		}

		outputChan <- output
		p.CounterSuccesses.Inc()
	}
}

func NewTimeseriesPre(ctx context.Context, name, timeUnit string) (*TimeseriesV2Pre, error) {
	return &TimeseriesV2Pre{
		ctx:               ctx,
		timeUnit:          timeUnit,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("timeseries_v2_pre", func(ctx context.Context, name string) (define.DataProcessor, error) {
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
		return NewTimeseriesPre(ctx, pipe.FormatName(rt.FormatName(name)), timeUnit)
	})
}
