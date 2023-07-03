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
)

// Record : 标准上报格式,原则上需要在维度上自带业务id,但考虑到现有主机采集器不知道自己的业务,因此兼容采集器维度
type Record struct {
	define.ETLRecord
	Group      []map[string]interface{} `json:"group_info"`
	IP         *string                  `json:"ip"`
	CloudID    *int32                   `json:"cloudid"`
	SupplierID *int32                   `json:"bizid"`
	CMDBLevel  interface{}              `json:"bk_cmdb_level"`
	BKAgentID  *string                  `json:"bk_agent_id"`
	BKBizID    *int32                   `json:"bk_biz_id"`
	BKHostID   *int32                   `json:"bk_host_id"`
}

// Processor :
type Processor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

// FillDimensions :
func (p *Processor) FillDimensions(record *Record) {
	// 采集器框架上报补充维度

	if record.IP != nil {
		record.Dimensions[define.RecordIPFieldName] = *record.IP
	}
	if record.CloudID != nil {
		record.Dimensions[define.RecordCloudIDFieldName] = conv.String(*record.CloudID)
	}
	if record.SupplierID != nil {
		record.Dimensions[define.RecordSupplierIDFieldName] = conv.String(*record.SupplierID)
	}
	if record.BKAgentID != nil {
		record.Dimensions[define.RecordBKAgentID] = conv.String(*record.BKAgentID)
	}
	if record.BKBizID != nil {
		record.Dimensions[define.RecordBKBizID] = conv.String(*record.BKBizID)
	}
	if record.BKHostID != nil {
		record.Dimensions[define.RecordBKHostID] = conv.String(*record.BKHostID)
	}
}

// Process : process json data
// 此处将PayLoad从原本的JSON数据格式化为ETLRecord格式的内容
func (p *Processor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	record := new(Record)
	err := d.To(record)
	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert record error %v: %v", p, err, d)
		return
	}

	if record.Time == nil {
		p.CounterFails.Inc()
		logging.Warnf("%v record time is empty: %v", p, d)
		return
	}

	if record.Metrics == nil || len(record.Metrics) == 0 {
		p.CounterFails.Inc()
		logging.Warnf("%v record metrics is empty: %v", p, d)
		return
	}

	if record.Dimensions == nil {
		record.Dimensions = map[string]interface{}{}
	}

	p.FillDimensions(record)

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
func NewProcessor(ctx context.Context, name string) *Processor {
	return &Processor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}
}

func init() {
	define.RegisterDataProcessor("standard", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
