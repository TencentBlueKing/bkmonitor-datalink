// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	DefaultFlatField = "__bk_flat_key__"
)

// NewFlatFTAProcessor: 将告警列表打平为多条告警
func NewFlatFTAProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipeConfig := config.PipelineConfigFromContext(ctx)
	helper := utils.NewMapHelper(pipeConfig.Option)

	rawDataKey, ok := helper.GetString(config.PipelineConfigOptFTARawDataKey)
	if !ok {
		// 查找路径为空，上一步会将数据存放到默认字段，因此使用默认的路径名称
		rawDataKey = config.PipelineConfigOptFTADefaultRawDataKey
	}
	extractFn := etl.ExtractByJMESPath(rawDataKey)

	decoder := etl.NewPayloadDecoder().FissionSplitHandler(
		true, extractFn, "", DefaultFlatField)

	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx), etl.NewFunctionalRecord("", func(from etl.Container, to etl.Container) error {
			var v interface{}
			var err error

			v, err = from.Get(DefaultFlatField)

			if err != nil {
				return errors.Errorf("get key->(%s) from data failed: %+v", DefaultFlatField, err)
			}

			var items map[string]interface{}

			switch value := v.(type) {
			case etl.Container:
				items = etl.ContainerToMap(value)
			case map[string]interface{}:
				items = value
			default:
				return errors.Errorf("flat field type error: %T", v)
			}

			for _, key := range from.Keys() {
				if key == DefaultFlatField || key == rawDataKey {
					// 忽略中间处理的key，避免数据冗余
					continue
				}
				v, err := from.Get(key)
				if err != nil {
					return errors.Errorf("get key->(%s) from data failed: %+v", key, err)
				}
				err = to.Put(key, v)
				if err != nil {
					return errors.Errorf("put key->(%s), value->(%+v) into data failed: %+v", key, v, err)
				}
			}

			for key, value := range items {
				err = to.Put(key, value)
				if err != nil {
					return errors.Errorf("put key->(%s), value->(%+v) into data failed: %+v", key, v, err)
				}
			}

			return nil
		}), decoder.Decode,
	), nil
}

func init() {
	define.RegisterDataProcessor("fta-flat", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewFlatFTAProcessor(ctx, pipeConfig.FormatName(name))
	})
}
