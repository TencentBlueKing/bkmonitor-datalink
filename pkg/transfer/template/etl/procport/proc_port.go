// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procport

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewPerformanceProcPortProcessor :
func NewPerformanceProcPortProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithDecoderFnWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(BaseDimensionFieldsValue()...).AddDimensions(
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
			"listen",
			etl.ExtractByJMESPath("item.listen"), etl.TransformJSON,
		),
		etl.NewSimpleField(
			"nonlisten",
			etl.ExtractByJMESPath("item.nonlisten"), etl.TransformJSON,
		),
		etl.NewSimpleField(
			"not_accurate_listen",
			etl.ExtractByJMESPath("item.notaccuratelisten"), etl.TransformJSON,
		),
		etl.NewSimpleField(
			"bind_ip",
			etl.ExtractByJMESPath("item.bindip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			"protocol",
			etl.ExtractByJMESPath("item.protocol"), etl.TransformNilString,
		),
	).AddMetrics(
		etl.NewSimpleField(
			"proc_exists",
			etl.ExtractByJMESPath("item.exists"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"port_health",
			etl.ExtractByJMESPath("item.porthealth"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)), etl.NewPayloadDecoder().FissionSplitHandler(true, etl.ExtractByJMESPath(`data.processes`), "", "item").Decode)
}

func init() {
	define.RegisterDataProcessor("system.proc_port", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewPerformanceProcPortProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
