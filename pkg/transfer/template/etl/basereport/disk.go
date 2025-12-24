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

// PerformanceDiskProcessor :
type PerformanceDiskProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	schema            *etl.TSSchemaRecord
	perUsageJPath     etl.ExtractFn
	perPartitionJPath etl.ExtractFn
	ctx               context.Context
}

// NewPerformanceDiskProcessor :
func NewPerformanceDiskProcessor(ctx context.Context, name string) *PerformanceDiskProcessor {
	return &PerformanceDiskProcessor{
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
				"device_type",
				etl.ExtractByJMESPath("usage.fstype"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"mount_point",
				etl.ExtractByJMESPath("usage.mountpoint"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"bk_cmdb_level",
				etl.ExtractByJMESPath("root.bk_cmdb_level"), etl.TransformJSON,
			),
		).AddMetrics(
			etl.NewSimpleField(
				"free",
				etl.ExtractByJMESPath("item.free"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"total",
				etl.ExtractByJMESPath("item.total"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"used",
				etl.ExtractByJMESPath("item.used"), etl.TransformNilFloat64,
			),
			etl.NewFutureFieldWithFn("in_use", func(name string, to etl.Container) (interface{}, error) {
				used, err := to.Get("used")
				if err != nil {
					return nil, err
				}
				free, err := to.Get("free")
				if err != nil {
					return nil, err
				}
				usedValue, err := conv.DefaultConv.Float64(used)
				if err != nil {
					return nil, err
				}
				freeValue, err := conv.DefaultConv.Float64(free)
				if err != nil {
					return nil, err
				}
				inUse, err := utils.DivNumber(usedValue, freeValue+usedValue)
				if math.IsInf(inUse, 1) {
					return nil, nil
				} else if err != nil {
					return nil, err
				}
				return inUse * 100.0, nil
			}),
		).AddTime(etl.NewSimpleField(
			"time", etl.ExtractByJMESPath("root.data.utctime"),
			etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
		)),
		perUsageJPath:     etl.ExtractByJMESPath(`data.disk.usage`),
		perPartitionJPath: etl.ExtractByJMESPath(`data.disk.partition`),
		ctx:               ctx,
	}
}

// Process : process json data
func (p *PerformanceDiskProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var (
		err    error
		output define.Payload
	)
	root := etl.NewMapContainer()
	err = d.To(&root)
	if err != nil {
		logging.Warnf("%v load %#v disk payload error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}

	if bizID, err := root.Get(define.RecordBizID); err == nil {
		if _, ok := p.DisabledBizIDs[conv.String(bizID)]; ok {
			p.CounterSkip.Inc()
			return
		}
	}

	perUsage, err := p.perUsageJPath(root)
	if err != nil {
		logging.Warnf("%v extract cpu usage %#v error %#v", p, root, err)
		p.CounterFails.Inc()
		return
	}
	perStat, err := p.perPartitionJPath(root)
	if err != nil {
		logging.Warnf("%v extract cpu stat %#v error %v", p, root, err)
		p.CounterFails.Inc()
		return
	}

	usageValue, ok := perUsage.([]interface{})
	if !ok {
		logging.Warnf("%v convert value excepted []interface{} but %#v", p, perUsage)
		p.CounterFails.Inc()
		return
	}
	statValue, ok := perStat.([]interface{})
	if !ok {
		logging.Warnf("%v convert value excepted []interface{} but %#v", p, perStat)
		p.CounterFails.Inc()
		return
	}

	if len(usageValue) != len(statValue) {
		logging.Warnf("usageValue length(%d) is not equal with statValue(%d)", len(usageValue), len(statValue))
		p.CounterFails.Inc()
		return
	}

	for key, value := range usageValue {
		from := etl.NewMapContainer()
		from["root"] = root.AsMapStr()
		var usage interface{}
		if perStat != nil {
			usage = statValue[key]
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
			logging.Errorf("create output payload %#v error: %v", to, err)
			return
		}

		outputChan <- output
	}
	if err != nil {
		logging.Warnf("%v handle %#v disk stat error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	p.CounterSuccesses.Inc()
}

func init() {
	define.RegisterDataProcessor("system.disk", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConf := config.PipelineConfigFromContext(ctx)
		if pipeConf == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceDiskProcessor(ctx, pipeConf.FormatName(name)), nil
	})
}
