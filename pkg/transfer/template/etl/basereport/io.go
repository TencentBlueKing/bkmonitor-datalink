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

// PerformanceIoProcessor :
type PerformanceIoProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	schema         *etl.TSSchemaRecord
	perIostatJPath etl.ExtractFn
	ctx            context.Context
}

// NewPerformanceIoProcessor :
func NewPerformanceIoProcessor(ctx context.Context, name string) *PerformanceIoProcessor {
	return &PerformanceIoProcessor{
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
				"bk_cmdb_level",
				etl.ExtractByJMESPath("root.bk_cmdb_level"), etl.TransformJSON,
			),
		).AddMetrics(
			etl.NewSimpleField(
				"await",
				etl.ExtractByJMESPath("item.await"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"svctm",
				etl.ExtractByJMESPath("item.svctm"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"r_s",
				etl.ExtractByJMESPath("item.speedIORead"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"w_s",
				etl.ExtractByJMESPath("item.speedIOWrite"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"util",
				etl.ExtractByJMESPath("item.util"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"avgrq_sz",
				etl.ExtractByJMESPath("item.avgrq_sz"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"avgqu_sz",
				etl.ExtractByJMESPath("item.avgqu_sz"), etl.TransformNilFloat64,
			),
			etl.NewSimpleField(
				"wkb_s",
				etl.ExtractByJMESPath("item.speedByteWrite"), etl.TransformChain(etl.TransformNilFloat64, etl.TransformDivideByFloat64(1024.0)),
			),
			etl.NewSimpleField(
				"rkb_s",
				etl.ExtractByJMESPath("item.speedByteRead"), etl.TransformChain(etl.TransformNilFloat64, etl.TransformDivideByFloat64(1024.0)),
			),
		).AddTime(etl.NewSimpleField(
			"time", etl.ExtractByJMESPath("root.data.utctime"),
			etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
		)),
		perIostatJPath: etl.ExtractByJMESPath(`data.disk.diskstat`),
		ctx:            ctx,
	}
}

// Process : process json data
func (p *PerformanceIoProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var (
		err    error
		output define.Payload
	)
	root := etl.NewMapContainer()
	err = d.To(&root)
	if err != nil {
		logging.Warnf("%v load %#v io payload error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}

	if bizID, err := root.Get(define.RecordBizID); err == nil {
		if _, ok := p.DisabledBizIDs[conv.String(bizID)]; ok {
			return
		}
	}

	perIo, err := p.perIostatJPath(root)
	if err != nil {
		logging.Warnf("%v extract %#v io usage error %v", p, d, err)
		p.CounterFails.Inc()
		return
	}
	v, ok := perIo.(map[string]interface{})
	if !ok {
		logging.Warnf("%v convert value excepted map[string]interface{} but %#v", p, perIo)
		p.CounterFails.Inc()
		return
	}

	handled := 0
	for key, value := range v {
		from := etl.NewMapContainer()
		from["root"] = root.AsMapStr()
		logging.WarnIf("put item", from.Put("item", value))

		to := etl.NewMapContainer()
		err = p.schema.Transform(from, to)
		if err != nil {
			logging.Errorf("%v transform %#v error %v", p, from, err)
			return
		}
		vDimensions, err := to.Get(define.RecordDimensionsFieldName)
		if err != nil {
			logging.Errorf("%v get dimensions %#v error %v", p, to, err)
			return
		}

		itemDimensions := vDimensions.(etl.Container)
		logging.WarnIf("set device_name error", itemDimensions.Put("device_name", key))

		output, err = define.DerivePayload(d, &to)
		if err != nil {
			logging.Errorf("%v create output payload %#v error: %v", p, to, err)
			return
		}

		outputChan <- output
		handled++
	}

	if handled == 0 {
		logging.Warnf("%v handle %#v failed", p, d)
		p.CounterFails.Inc()
		return
	}
	p.CounterSuccesses.Inc()
}

func init() {
	define.RegisterDataProcessor("system.io", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceIoProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
