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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewSwapProcessor : 存在SWAP 不存在的情况,因此,指标的defaultValue 为 0
func NewSwapProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		etl.NewSimpleFieldWithValue(
			define.RecordIPFieldName, "<unknown>",
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithValue(
			define.RecordTargetIPFieldName, "<unknown>",
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithValue(
			define.RecordSupplierIDFieldName, "<unknown>",
			etl.ExtractByJMESPath("bizid"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithValue(
			define.RecordCloudIDFieldName, "<unknown>",
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithValue(
			define.RecordTargetCloudIDFieldName, "<unknown>",
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
		etl.NewSimpleFieldWithValue(
			"hostname", "<unknown>",
			etl.ExtractByJMESPath(define.HostNameField), etl.TransformNilString,
		),
	).AddMetrics(
		etl.NewSimpleFieldWithValue(
			"free", 0,
			etl.ExtractByJMESPath("data.mem.vmstat.free"), etl.TransformNilFloat64,
		),
		etl.NewSimpleFieldWithValue(
			"total", 0,
			etl.ExtractByJMESPath("data.mem.vmstat.total"), etl.TransformNilFloat64,
		),
		etl.NewSimpleFieldWithValue(
			"used", 0,
			etl.ExtractByJMESPath("data.mem.vmstat.used"), etl.TransformNilFloat64,
		),
		etl.NewSimpleFieldWithValue(
			"swap_in", 0,
			etl.ExtractByJMESPath("data.mem.swap_in"), etl.TransformNilFloat64,
		),
		etl.NewSimpleFieldWithValue(
			"swap_out", 0,
			etl.ExtractByJMESPath("data.mem.swap_out"), etl.TransformNilFloat64,
		),
		etl.NewSimpleFieldWithValue(
			"pct_used", 0,
			etl.ExtractByJMESPath("data.mem.vmstat.usedPercent"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.swap", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewSwapProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
