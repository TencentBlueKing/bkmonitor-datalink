// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"context"
	"regexp"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// NewRegexpLogProcessor
func NewRegexpLogProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	return NewLogProcessor(ctx, name, func(record *etl.TSSchemaRecord, decoder *etl.PayloadDecoder) {
		pipe := config.PipelineConfigFromContext(ctx)
		options := utils.NewMapHelper(pipe.Option)
		pattern, ok := options.GetString(config.PipelineConfigOptLogSeparatorRegexp)
		if !ok {
			panic(errors.Wrapf(define.ErrOperationForbidden, "regexp not set"))
		}

		record.AddMetrics(
			// 分割
			etl.NewPrepareField(
				FieldSeparatorValues, etl.ExtractByPath(FieldName),
				etl.TransformMapByRegexp(pattern),
			),
			// 合并
			etl.NewMergeField(FieldSeparatorValues),
		)
	})
}

func init() {
	define.RegisterDataProcessor("regexp_log", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		options := utils.NewMapHelper(pipeConfig.Option)
		pattern, ok := options.GetString(config.PipelineConfigOptLogSeparatorRegexp)
		if !ok {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "regexp not set")
		}

		_, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}

		return NewRegexpLogProcessor(ctx, pipeConfig.FormatName(name))
	})
}
