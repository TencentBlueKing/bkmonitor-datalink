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

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// PerformanceCPUDetailProcessor :
type PerformanceCPUDetailProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	schema        *etl.TSSchemaRecord
	perStatJPath  etl.ExtractFn
	perUsageJPath etl.ExtractFn
	ctx           context.Context
}

// NewPerformanceCPUDetailProcessor :
func NewPerformanceCPUDetailProcessor(ctx context.Context, name string) *PerformanceCPUDetailProcessor {
	return &PerformanceCPUDetailProcessor{
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
				define.RecordTargetCloudIDFieldName,
				etl.ExtractByJMESPath("root.cloudid"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				define.RecordHostNameFieldName,
				etl.ExtractByJMESPath(define.BaseHostNameField), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"device_name",
				etl.ExtractByJMESPath("item.cpu"), etl.TransformNilString,
			),
			etl.NewSimpleField(
				"bk_cmdb_level",
				etl.ExtractByJMESPath("root.bk_cmdb_level"), etl.TransformJSON,
			),
		).AddMetrics(
			etl.NewSimpleField(
				"user",
				etl.ExtractByJMESPath("item.user"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"system",
				etl.ExtractByJMESPath("item.system"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"nice",
				etl.ExtractByJMESPath("item.nice"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"idle",
				etl.ExtractByJMESPath("item.idle"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"iowait",
				etl.ExtractByJMESPath("item.iowait"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"interrupt",
				etl.ExtractByJMESPath("item.irq"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"softirq",
				etl.ExtractByJMESPath("item.softirq"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"stolen",
				etl.ExtractByJMESPath("item.stolen"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"usage",
				etl.ExtractByJMESPath("usage"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"guest",
				etl.ExtractByJMESPath("item.guest"), etl.TransformNilFloat64,
			),
			etl.NewFutureField("pct", func(name string, from etl.Container, to etl.Container) error {
				names := []string{
					"user", "system", "nice", "idle", "iowait", "interrupt", "softirq", "stolen", "guest",
				}
				values := make(map[string]float64)
				sum := 0.0
				for _, name := range names {
					v, err := to.Get(name)
					if err != nil {
						continue
					}

					switch value := v.(type) {
					case float64:
						sum += value
						values[name] = value
					}
				}

				if sum == 0.0 {
					return nil
				}

				for name, value := range values {
					err := to.Put(name, value/sum)
					if err != nil {
						continue
					}
				}
				return nil
			}),
		).AddTime(etl.NewSimpleField(
			"time", etl.ExtractByJMESPath("root.data.utctime"),
			etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
		)),
		perStatJPath:  etl.ExtractByJMESPath(`data.cpu.per_stat`),
		perUsageJPath: etl.ExtractByJMESPath(`data.cpu.per_usage`),
		ctx:           ctx,
	}
}

// Process : process json data
func (p *PerformanceCPUDetailProcessor) Process(d define.Payload, outputChan chan<- define.Payload, _ chan<- error) {
	var (
		err    error
		output define.Payload
	)
	root := etl.NewMapContainer()
	err = d.To(&root)
	if err != nil {
		logging.Warnf("%v load %#v error %v", p, d, err)
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
		logging.Warnf("%v extract cpu usage %#v error %v", p, root, err)
		p.CounterFails.Inc()
		return
	}
	perStat, err := p.perStatJPath(root)
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

	for key, value := range statValue {
		from := etl.NewMapContainer()
		from["root"] = root.AsMapStr()
		usage := -1.0
		if perUsage != nil {
			usage = usageValue[key].(float64)
		}
		logging.WarnIf("put usage", from.Put("usage", usage))
		logging.WarnIf("put item", from.Put("item", value.(map[string]interface{})))

		to := etl.NewMapContainer()
		err = p.schema.Transform(from, to)
		if err != nil {
			logging.Errorf("%v transform %#v error %v", p, from, err)
			continue
		}

		output, err = define.DerivePayload(d, &to)
		if err != nil {
			logging.Errorf("%v create output payload %#v error: %v", p, to, err)
			continue
		}
		outputChan <- output
	}

	if err != nil {
		logging.Warnf("%v extract cpu stat error %#v: %v", p, root, err)
		p.CounterFails.Inc()
		return
	}
}

func init() {
	define.RegisterDataProcessor("system.cpu_detail", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConf := config.PipelineConfigFromContext(ctx)
		if pipeConf == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceCPUDetailProcessor(ctx, pipeConf.FormatName(name)), nil
	})
}
