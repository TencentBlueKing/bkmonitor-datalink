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

// NewEnvProcessor :
func NewEnvProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		BaseDimensionBaseReportFieldsValue()...).AddDimensions(
		etl.NewSimpleField(
			"timezone",
			etl.ExtractByJMESPath("data.timezone"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"city",
			etl.ExtractByJMESPath("data.city"), etl.TransformNilString,
		),
	).AddMetrics(
		etl.NewSimpleField(
			"procs",
			etl.ExtractByJMESPath("data.system.info.procs"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"procs_zombie",
			etl.ExtractByJMESPath("data.system.info.procsZombie"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"uptime",
			etl.ExtractByJMESPath("data.system.info.uptime"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"maxfiles",
			etl.ExtractByJMESPath("data.env.maxfiles"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"allocated_files",
			etl.ExtractByJMESPath("data.env.allocated_files"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"uname",
			etl.ExtractByJMESPath("data.env.uname"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"login_user",
			etl.ExtractByJMESPath("data.env.login_user"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"proc_running_current",
			etl.ExtractByJMESPath("data.env.proc_running_current"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"procs_blocked_current",
			etl.ExtractByJMESPath("data.env.procs_blocked_current"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"procs_processes_total",
			etl.ExtractByJMESPath("data.env.procs_processes_total"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"procs_ctxt_total",
			etl.ExtractByJMESPath("data.env.procs_ctxt_total"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.env", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConf := config.PipelineConfigFromContext(ctx)
		if pipeConf == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewEnvProcessor(ctx, pipeConf.FormatName(name)), nil
	})
}
