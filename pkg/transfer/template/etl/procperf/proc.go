// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procperf

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewPerformanceProcProcessor :
func NewPerformanceProcProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithDecoderFnWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(ProcBaseDimensionFieldsValue()...).AddDimensions(
		etl.NewSimpleField(
			"proc_name",
			etl.ExtractByJMESPath("item.name"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"display_name",
			etl.ExtractByJMESPath("item.displayname"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"param_regex",
			etl.ExtractByJMESPath("item.paramregex"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"pid",
			etl.ExtractByJMESPath("item.pid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"pgid",
			etl.ExtractByJMESPath("item.pgid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"ppid",
			etl.ExtractByJMESPath("item.ppid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"state",
			etl.ExtractByJMESPath("item.state"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"username",
			etl.ExtractByJMESPath("item.username"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"port",
			etl.ExtractByJMESPath("item.port"), etl.TransformNilString,
		),
	).AddMetrics(
		etl.NewSimpleField(
			"cpu_usage_pct",
			etl.ExtractByJMESPath("item.cpu.total.pct"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cpu_user",
			etl.ExtractByJMESPath("item.cpu.user.ticks"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cpu_system",
			etl.ExtractByJMESPath("item.cpu.system.ticks"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cpu_total_ticks",
			etl.ExtractByJMESPath("item.cpu.total.ticks"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"mem_usage_pct",
			etl.ExtractByJMESPath("item.memory.rss.pct"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"fd_num",
			etl.ExtractByJMESPath("item.fd.open"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"fd_limit_soft",
			etl.ExtractByJMESPath("item.fd.limit.soft"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"fd_limit_hard",
			etl.ExtractByJMESPath("item.fd.limit.hard"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"io_read_bytes",
			etl.ExtractByJMESPath("item.io.read_bytes"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"io_write_bytes",
			etl.ExtractByJMESPath("item.io.write_bytes"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"io_read_speed",
			etl.ExtractByJMESPath("item.io.read_speed"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"io_write_speed",
			etl.ExtractByJMESPath("item.io.write_speed"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"mem_res",
			etl.ExtractByJMESPath("item.memory.rss.bytes"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"mem_virt",
			etl.ExtractByJMESPath("item.memory.size"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"uptime",
			etl.ExtractByJMESPath("item.uptime"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)), etl.NewPayloadDecoder().FissionSplitHandler(true, etl.ExtractByJMESPath(`data.perf`), "", "item").Decode)
}

func init() {
	define.RegisterDataProcessor("system.proc", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceProcProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
