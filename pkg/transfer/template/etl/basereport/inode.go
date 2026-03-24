// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basereport

import (
	"context"
	"math"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// PerformanceInodeProcessor :
type PerformanceInodeProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	schema             *etl.TSSchemaRecord
	perInodeUsageJPath etl.ExtractFn
	perPartitionJPath  etl.ExtractFn
	ctx                context.Context
}

// NewPerformanceInodeProcessor :
func NewPerformanceInodeProcessor(ctx context.Context, name string) *PerformanceInodeProcessor {
	return &PerformanceInodeProcessor{
		BaseDataProcessor: define.NewBaseDataProcessorWith(name, config.ResultTableConfigFromContext(ctx).DisabledBizID()),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
		schema: etl.NewTSSchemaRecord(name).AddDimensions(
			etl.NewSimpleField(
				define.RecordIPFieldName,
				etl.ExtractByJMESPath("root.ip"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordTargetIPFieldName,
				etl.ExtractByJMESPath("root.ip"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordSupplierIDFieldName,
				etl.ExtractByJMESPath("root.bizid"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordCloudIDFieldName,
				etl.ExtractByJMESPath("root.cloudid"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordTargetCloudIDFieldName,
				etl.ExtractByJMESPath("root.cloudid"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordBKAgentID,
				etl.ExtractByJMESPath("root.bk_agent_id"), etl.TransformNilString,
			),
			etl.NewSimpleFieldWithCheck(
				define.RecordBKBizID,
				etl.ExtractByJMESPath("root.bk_biz_id"), etl.TransformNilString, func(v interface{}) bool {
					return !etl.IfEmptyStringField(v)
				},
			),
			etl.NewSimpleField(
				define.RecordBKHostID,
				etl.ExtractByJMESPath("root.bk_host_id"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordTargetHostIDFieldName,
				etl.ExtractByJMESPath("root.bk_host_id"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordHostNameFieldName,
				etl.ExtractByJMESPath(define.BaseHostNameField), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"device_name",
				etl.ExtractByJMESPath("usage.device"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"mountpoint",
				etl.ExtractByJMESPath("usage.mountpoint"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"bk_cmdb_level",
				etl.ExtractByJMESPath("root.bk_cmdb_level"), etl.TransformJSON,
			),
		).AddMetrics(
			etl.NewSimpleField(
				"free",
				etl.ExtractByJMESPath("item.inodesFree"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"total",
				etl.ExtractByJMESPath("item.inodesTotal"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"used",
				etl.ExtractByJMESPath("item.inodesUsed"), etl.TransformNilFloat64,
			),
			etl.NewFutureFieldWithFn("in_use", func(name string, to etl.Container) (interface{}, error) {
				total, err := to.Get("total")
				if err != nil {
					return nil, err
				}

				used, err := to.Get("used")
				if err != nil {
					return nil, err
				}

				inUsed, err := utils.DivNumber(used, total)
				if math.IsInf(inUsed, 1) {
					return nil, nil
				} else if err != nil {
					return nil, err
				}

				return inUsed * 100.0, nil
			}),
		).AddTime(etl.NewSimpleField(
			"time", etl.ExtractByJMESPath("root.data.utctime"),
			etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
		)),
		perInodeUsageJPath: etl.ExtractByJMESPath(`data.disk.usage`),
		perPartitionJPath:  etl.ExtractByJMESPath(`data.disk.partition`),
		ctx:                ctx,
	}
}

// Process : process json data
func (p *PerformanceInodeProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var (
		err    error
		output define.Payload
	)
	root := etl.NewMapContainer()
	err = d.To(&root)
	if err != nil {
		logging.Warnf("%v load %#v inode payload error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}

	if bizID, err := root.Get("bizid"); err == nil {
		if _, ok := p.DisabledBizIDs[conv.String(bizID)]; ok {
			return
		}
	}

	perPart, err := p.perPartitionJPath(root)
	if err != nil {
		logging.Warnf("%v extract perPart %#v  error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	perInodes, err := p.perInodeUsageJPath(root)
	if err != nil {
		logging.Warnf("%v extract perInodes %#v error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	partValue, ok := perPart.([]interface{})
	if !ok {
		logging.Warnf("%v convert value excepted []interface{} but %#v", p, partValue)
		p.CounterFails.Inc()
		return
	}
	inodeValue, ok := perInodes.([]interface{})
	if !ok {
		logging.Warnf("%v convert value excepted []interface{} but %#v", p, inodeValue)
		p.CounterFails.Inc()
		return
	}
	for key, value := range inodeValue {
		from := etl.NewMapContainer()
		from["root"] = root.AsMapStr()
		var usage interface{}
		if partValue != nil {
			usage = partValue[key]
		}
		logging.WarnIf("put usage", from.Put("usage", usage))
		logging.WarnIf("put item", from.Put("item", value.(map[string]interface{})))

		to := etl.NewMapContainer()
		err = p.schema.Transform(from, to)
		if err != nil {
			logging.Errorf("%v transform %#v error %v", p, from, err)
			return
		}

		output, err = define.DerivePayload(d, &to)
		if err != nil {
			logging.Errorf("%v create output payload %#v error: %v", p, to, err)
			return
		}

		outputChan <- output
	}

	if err != nil {
		logging.Warnf("%v extract %v inode stat error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	p.CounterSuccesses.Inc()
}

func init() {
	define.RegisterDataProcessor("system.inode", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceInodeProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
