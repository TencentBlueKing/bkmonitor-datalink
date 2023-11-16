// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse_event

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"
	"time"
)

// CustomStringProcessor : 自定义字符串处理器
type CustomStringProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	ctx context.Context
}

// CustomStringEvent : 自定义字符串事件
/*
{
  "_bizid_" : 0,
  "_cloudid_" : 0,
  "_server_" : "127.0.0.1",
  "_time_" : "2019-03-02 15:29:24",
  "_utctime_" : "2019-03-02 07:29:24",
  "_value_" : [ "This service is offline" ]
}
*/
type CustomStringEvent struct {
	CloudID int      `json:"_cloud_id_"`
	IP      string   `json:"_server_"`
	Time    string   `json:"_utctime_"`
	Values  []string `json:"_value_"`
}

type EventRecord struct {
	EventName      string                 `json:"event_name"`
	Event          map[string]interface{} `json:"event"`
	EventDimension map[string]interface{} `json:"dimension"`
	Target         string                 `json:"target"`
	Timestamp      *float64               `json:"timestamp"`
}

func (p *CustomStringProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(CustomStringEvent)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		return
	}

	// IP为空则不处理
	if record.IP == "" {
		p.CounterFails.Inc()
		return
	}

	event := new(EventRecord)
	event.EventName = "custom_string"
	event.Target = record.IP

	// 根据IP和云区域ID获取业务ID
	store := define.StoreFromContext(p.ctx)
	modelInfo := &models.CCHostInfo{IP: conv.String(record.IP), CloudID: conv.Int(record.CloudID)}
	err = modelInfo.LoadStore(store)
	if err != nil {
		p.CounterFails.Inc()
		return
	}

	ccTopo, ok := modelInfo.GetInfo().(*models.CCTopoBaseModelInfo)
	if !ok {
		p.CounterFails.Inc()
		return
	}

	event.EventDimension = map[string]interface{}{
		"bk_target_cloud_id": conv.String(record.CloudID),
		"bk_target_ip":       record.IP,
		"ip":                 record.IP,
		"bk_cloud_id":        conv.String(record.CloudID),
		"bk_biz_id":          conv.String(ccTopo.BizID[0]),
	}

	// 时间格式转换
	parse, err := time.Parse("2006-01-02 15:04:05", record.Time)
	if err != nil {
		p.CounterFails.Inc()
		return
	}
	timestamp := float64(parse.UnixMilli())
	event.Timestamp = &timestamp

	// 将多个值转换为多个事件
	for _, value := range record.Values {
		if value == "" {
			continue
		}
		newEvent := event
		newEvent.Event = map[string]interface{}{
			"content": value,
		}
		output, err := define.DerivePayload(d, newEvent)
		if err != nil {
			p.CounterFails.Inc()
			logging.Warnf("%v create payload error %v: %v", p, err, d)
			return
		}
		outputChan <- output
	}
	p.CounterSuccesses.Inc()
}

func NewCustomEventProcessor(ctx context.Context, name string) *CustomStringProcessor {
	return &CustomStringProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		ctx:               ctx,
	}
}

func init() {
	define.RegisterDataProcessor("gse_custom_string", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		if define.StoreFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "store not found")
		}

		return NewCustomEventProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
