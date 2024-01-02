// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auto

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewProcessor :
func NewFlatProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipe := config.PipelineConfigFromContext(ctx)
	schema, err := template.NewSchema(ctx)
	if err != nil {
		return nil, err
	}

	return template.NewRecordProcessor(name, pipe, schema), nil
}

func init() {
	define.RegisterDataProcessor("flat", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		if config.ResultTableConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		return NewFlatProcessor(ctx, pipeConfig.FormatName(name))
	})
}
