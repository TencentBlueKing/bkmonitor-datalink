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

type CustomTimeseriesRecord struct {
	Target    string                 `json:"target"`
	Metrics   map[string]interface{} `json:"metrics"`
	Dimension map[string]interface{} `json:"dimension"`
	Timestamp *int64                 `json:"timestamp"`
}

type CustomTimeseries struct {
	Data      []CustomTimeseriesRecord `json:"data"`
	Target    string                   `json:"target"`
	Timestamp *int64                   `json:"timestamp"`
}

type TimeseriesV2Handler struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor

	ctx             context.Context
	timeUnit        string
	metricsReporter *MetricsReportProcessor
}

func (p *TimeseriesV2Handler) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	records := &CustomTimeseries{}
	err := d.To(records)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert payload %#v error %v", p, d, err)
		return
	}

	// 兼容逻辑
	// 日志指标上报存在一种看起来像自定义指标但实际上又不是自定义指标的数据格式
	for i := 0; i < len(records.Data); i++ {
		item := records.Data[i]
		if item.Timestamp == nil {
			item.Timestamp = records.Timestamp
		}
		if item.Target == "" {
			item.Target = records.Target
		}
	}

	for _, item := range records.Data {
		// 通过 bkmonitorproxy 上报过来只有 timestamp 字段
		if item.Timestamp == nil || *item.Timestamp == 0.0 {
			p.CounterFails.Inc()
			logging.Warnf("%v time series item time is empty: %v", p, d)
			return
		}

		record := &define.ETLRecord{
			Time:    item.Timestamp,
			Metrics: item.Metrics,
		}

		if p.timeUnit != "" {
			newTs := utils.ConvertTimeUnitAs(*record.Time, p.timeUnit)
			record.Time = &newTs
		}

		record.Dimensions = item.Dimension
		if record.Dimensions == nil {
			record.Dimensions = make(map[string]interface{})
		}
		record.Dimensions["target"] = item.Target

		p.metricsReporter.process(d, record, outputChan, killChan)
		output, err := define.DerivePayload(d, record)
		if err != nil {
			p.CounterFails.Inc()
			logging.Warnf("%v create payload error %v: %v", p, err, d)
			return
		}
		outputChan <- output
		p.CounterSuccesses.Inc()
	}
}

func NewTimeseriesV2Handler(ctx context.Context, name, timeUnit string) (*TimeseriesV2Handler, error) {
	metricReporter, err := NewMetricsReportProcessor(ctx, name)
	if err != nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "create metricreporter failed")
	}

	return &TimeseriesV2Handler{
		ctx:               ctx,
		timeUnit:          timeUnit,
		metricsReporter:   metricReporter,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("timeseries_v2_handler", func(ctx context.Context, name string) (define.DataProcessor, error) {
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

		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}

		return NewTimeseriesV2Handler(ctx, pipe.FormatName(rt.FormatName(name)), timeUnit)
	})
}
