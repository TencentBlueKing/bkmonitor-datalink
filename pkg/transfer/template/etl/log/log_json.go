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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/conv"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

func NewJSONLogProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	return NewLogProcessor(ctx, name, func(record *etl.TSSchemaRecord, decoder *etl.PayloadDecoder) {
		record.AddMetrics(etl.NewFunctionField(name, func(name string, from etl.Container, to etl.Container) error {
			value, err := from.Get(FieldName)
			if err != nil {
				return err
			}
			jsonValue, err := conv.DefaultConv.String(value)
			if err != nil {
				return err
			}
			var logJSONValue map[string]interface{}
			err = json.Unmarshal([]byte(jsonValue), &logJSONValue)
			if err != nil {
				return err
			}
			for key, value := range logJSONValue {
				err = from.Put(key, value)
				if err != nil {
					return err
				}
			}
			return nil
		}))
	})
}

func init() {
	define.RegisterDataProcessor("json_log", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewJSONLogProcessor(ctx, pipeConfig.FormatName(name))
	})
}
