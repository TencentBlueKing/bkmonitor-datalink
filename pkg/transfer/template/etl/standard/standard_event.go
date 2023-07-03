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

	conv "github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// EventRecord : 标准的事件上报格式
type EventRecord struct {
	// ETLRecord，后续需要将内容转移到里面
	define.ETLRecord
	// 事件特殊的内容
	EventName      string                 `json:"event_name"`
	Event          map[string]interface{} `json:"event"`
	EventDimension map[string]interface{} `json:"dimension"`
	Target         string                 `json:"target"`
	Timestamp      *float64               `json:"timestamp"`
}

// Processor :
type EventProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

// Process: 将PayLoad从基本上报的格式写入到Dimension和Metric中，方便后面使用
func (p *EventProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(EventRecord)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("convert event record failed, processor: %v, record: %+v, err: %+v", p, err, d)
		return
	}

	// 时间是否不存在
	if record.Timestamp == nil || *record.Timestamp == 0.0 {
		p.CounterFails.Inc()
		logging.Warnf("%v event record time is empty: %v", p, d)
		return
	}

	for _, checkElement := range []interface{}{record.Target, record.EventName} {
		// 如果这个不是空接口，那么需要判断string是否为空
		if checkElement == nil || checkElement.(string) == "" {
			p.CounterFails.Inc()
			logging.Warnf("%v event record target/eventName is empty: %v", p, d)
			return
		}
	}

	// 事件内容必须是非空
	for _, checkElement := range []map[string]interface{}{record.Event} {
		if checkElement == nil || len(checkElement) == 0 {
			p.CounterFails.Inc()
			logging.Warnf("%v event record event is empty: %v", p, d)
			return
		}
	}

	// 如果发现事件维度为空，需要增加一个默认的维度补充上去
	if record.EventDimension == nil {
		record.EventDimension = make(map[string]interface{})
	}

	// 检查正常，转移内容
	tempTime := conv.Int64(*record.Timestamp)
	record.Time = &tempTime
	if record.Metrics == nil {
		record.Metrics = make(map[string]interface{})
	}
	record.Metrics["event"] = record.Event

	if record.Dimensions == nil {
		record.Dimensions = make(map[string]interface{})
	}
	record.Dimensions["dimensions"] = record.EventDimension

	// 追加两个维度内容
	record.Dimensions[define.RecordEventTargetName] = record.Target
	record.Dimensions[define.RecordEventEventNameName] = record.EventName

	output, err := define.DerivePayload(d, record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v create payload error %v: %v", p, err, d)
		return
	}

	outputChan <- output

	p.CounterSuccesses.Inc()
}

// NewProcessor :
func NewEventProcessor(ctx context.Context, name string) *EventProcessor {
	return &EventProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}
}

func init() {
	define.RegisterDataProcessor("event_v2_standard", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewEventProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
