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
	"time"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// SystemEventProcessor : 自定义字符串处理器
type SystemEventProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	ctx context.Context
}

func (p *SystemEventProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(SystemEventData)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		return
	}

	if record.Values == nil {
		p.CounterFails.Inc()
		return
	}

	var eventRecords []EventRecord

	for _, value := range record.Values {
		extra := value.Extra
		if extra == nil {
			continue
		}
		newEventRecords := parseSystemEvent(extra)
		if newEventRecords == nil {
			continue
		}

		// 时间字段补充
		eventTime := value.EventTime
		if eventTime == "" {
			eventTime = record.Time
		}
		for _, eventRecord := range newEventRecords {
			// 时间格式转换
			parse, err := time.Parse("2006-01-02 15:04:05", eventTime)
			if err != nil {
				p.CounterFails.Inc()
				continue
			}
			timestamp := float64(parse.UnixMilli())
			eventRecord.Timestamp = &timestamp
			eventRecords = append(eventRecords, eventRecord)
		}
	}

	// 补充业务ID
	for _, eventRecord := range eventRecords {
		ipDimension, _ := eventRecord.EventDimension["ip"]
		cloudIDDimension, _ := eventRecord.EventDimension["bk_cloud_id"]
		agentIdDimension, _ := eventRecord.EventDimension["bk_agent_id"]

		ip, _ := ipDimension.(string)
		cloudID, _ := cloudIDDimension.(string)
		agentId, _ := agentIdDimension.(string)

		var bkBizID int
		store := define.StoreFromContext(p.ctx)
		if agentId != "" {
			// 根据agentId获取业务ID
			modelInfo := &models.CCAgentHostInfo{
				AgentID: agentId,
			}
			if err = modelInfo.LoadStore(store); err == nil {
				bkBizID = modelInfo.BizID
				ip = modelInfo.IP
				cloudID = conv.String(modelInfo.CloudID)
			}
		}

		if bkBizID == 0 && ip != "" && cloudID != "" {
			// 根据IP和云区域ID获取业务ID
			modelInfo := &models.CCHostInfo{IP: conv.String(ip), CloudID: conv.Int(cloudID)}
			if err = modelInfo.LoadStore(store); err == nil {
				bkBizID = modelInfo.BizID[0]
			}
		}

		// 业务ID为空则不处理
		if bkBizID == 0 {
			p.CounterFails.Inc()
			continue
		}

		eventRecord.EventDimension["bk_biz_id"] = conv.String(bkBizID)

		if _, exists := eventRecord.EventDimension["bk_target_ip"]; !exists {
			eventRecord.EventDimension["bk_target_ip"] = ip
			eventRecord.EventDimension["bk_target_cloud_id"] = cloudID
			eventRecord.EventDimension["ip"] = ip
			eventRecord.EventDimension["bk_cloud_id"] = cloudID
		}

		output, err := define.DerivePayload(d, eventRecord)
		if err != nil {
			p.CounterFails.Inc()
			logging.Warnf("%v create payload error %v: %v", p, err, d)
			return
		}
		outputChan <- output
	}

	p.CounterSuccesses.Inc()
}

func NewSystemEventProcessor(ctx context.Context, name string) *SystemEventProcessor {
	return &SystemEventProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		ctx:               ctx,
	}
}

func init() {
	define.RegisterDataProcessor("gse_system_event", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		if define.StoreFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "store not found")
		}

		return NewSystemEventProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
