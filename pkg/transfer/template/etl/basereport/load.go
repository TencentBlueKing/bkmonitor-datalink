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

// NewLoadProcessor :
func NewLoadProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		BaseDimensionBaseReportFieldsValue()...).AddMetrics(
		etl.NewSimpleField(
			"load1",
			etl.ExtractByJMESPath("data.load.load_avg.load1"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"load15",
			etl.ExtractByJMESPath("data.load.load_avg.load15"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"load5",
			etl.ExtractByJMESPath("data.load.load_avg.load5"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"per_cpu_load",
			etl.ExtractByJMESPath("data.load.per_cpu_load"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.load", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConf := config.PipelineConfigFromContext(ctx)
		if pipeConf == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewLoadProcessor(ctx, pipeConf.FormatName(name)), nil
	})
}
