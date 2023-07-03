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
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewPerformanceNetProcessor :
func NewPerformanceNetProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithDecoderFnWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		etl.NewSimpleField(
			define.RecordIPFieldName,
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetIPFieldName,
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordSupplierIDFieldName,
			etl.ExtractByJMESPath("bizid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordCloudIDFieldName,
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetCloudIDFieldName,
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordBKAgentID,
			etl.ExtractByJMESPath("bk_agent_id"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithCheck(
			define.RecordBKBizID,
			etl.ExtractByJMESPath("bk_biz_id"), etl.TransformNilString, func(v interface{}) bool {
				return !etl.IfEmptyStringField(v)
			},
		),
		etl.NewSimpleField(
			define.RecordBKHostID,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetHostIDFieldName,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordHostNameFieldName,
			etl.ExtractByJMESPath(define.HostNameField), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"device_name",
			etl.ExtractByJMESPath("item.name"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"bk_cmdb_level",
			etl.ExtractByJMESPath("bk_cmdb_level"), etl.TransformJSON,
		),
	).AddMetrics(
		etl.NewSimpleField(
			"speed_recv",
			etl.ExtractByJMESPath("item.speedRecv"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"speed_sent",
			etl.ExtractByJMESPath("item.speedSent"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"speed_packets_recv",
			etl.ExtractByJMESPath("item.speedPacketsRecv"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"speed_packets_sent",
			etl.ExtractByJMESPath("item.speedPacketsSent"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"packets_recv",
			etl.ExtractByJMESPath("item.packetsRecv"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"packets_sent",
			etl.ExtractByJMESPath("item.packetsSent"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"bytes_recv",
			etl.ExtractByJMESPath("item.bytesRecv"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"bytes_sent",
			etl.ExtractByJMESPath("item.bytesSent"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"errors",
			etl.ExtractByJMESPath("item.errors"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"dropped",
			etl.ExtractByJMESPath("item.dropped"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"overruns",
			etl.ExtractByJMESPath("item.overruns"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"carrier",
			etl.ExtractByJMESPath("item.carrier"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"collisions",
			etl.ExtractByJMESPath("item.collisions"), etl.TransformNilFloat64,
		),
		etl.NewFutureFieldWithFn("speed_recv_bit", func(name string, to etl.Container) (interface{}, error) {
			speedRecvTemp, err := to.Get("speed_recv")
			if err != nil {
				return nil, err
			}
			speedRecv, err := conv.DefaultConv.Float64(speedRecvTemp)
			if err != nil {
				return nil, err
			}
			return speedRecv * 8.0, nil
		}),
		etl.NewFutureFieldWithFn("speed_sent_bit", func(name string, to etl.Container) (interface{}, error) {
			speedSentTemp, err := to.Get("speed_sent")
			if err != nil {
				return nil, err
			}
			speedSent, err := conv.DefaultConv.Float64(speedSentTemp)
			if err != nil {
				return nil, err
			}

			return speedSent * 8.0, nil
		}),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)), etl.NewPayloadDecoder().FissionSplitHandler(true, etl.ExtractByJMESPath(`data.net.dev`), "", "item").Decode)
}

func init() {
	define.RegisterDataProcessor("system.net", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceNetProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
