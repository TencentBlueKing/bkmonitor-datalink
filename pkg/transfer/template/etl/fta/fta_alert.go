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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type Alert struct {
	Trigger
	Name string `json:"name"`
}

// NewAlertFTAProcessor: 根据告警匹配规则，设置告警名称
func NewAlertFTAProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipeConfig := config.PipelineConfigFromContext(ctx)
	helper := utils.NewMapHelper(pipeConfig.Option)

	// 获取告警名称匹配配置
	alertsConfig, _ := helper.Get(config.PipelineConfigOptFTAAlertsKey)

	var alerts []*Alert

	alertsConfigJSON, err := json.Marshal(alertsConfig)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(alertsConfigJSON, &alerts)
	if err != nil {
		return nil, err
	}

	for _, alert := range alerts {
		// 初始化告警匹配对象
		err = alert.Init()
		if err != nil {
			return nil, err
		}
	}

	decoder := etl.NewPayloadDecoder()

	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx),
		etl.NewFunctionalRecord("", func(from etl.Container, to etl.Container) error {
			for _, key := range from.Keys() {
				v, err := from.Get(key)
				if err != nil {
					return errors.Errorf("get key->(%s) from data failed: %+v", key, err)
				}
				err = to.Put(key, v)
				if err != nil {
					return errors.Errorf("put key->(%s), value->(%+v) into data failed: %+v", key, v, err)
				}
			}

			data := etl.ContainerToMap(from)
			for _, alert := range alerts {
				// 对满足匹配规则的数据，设置告警名称
				if alert.IsMatch(data) {
					err = to.Put(config.PipelineConfigOptFTAAlertNameKey, alert.Name)
					if err != nil {
						return errors.Errorf("put key->(%s), value->(%+v) into data failed: %+v",
							config.PipelineConfigOptFTAAlertNameKey, alert.Name, err)
					}
					logging.Debugf("fta alert name matched->(%s) data->(%+v)", alert.Name, data)
					break
				}
			}
			return nil
		}), decoder.Decode,
	), nil
}

func init() {
	define.RegisterDataProcessor("fta-alert", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewAlertFTAProcessor(ctx, pipeConfig.FormatName(name))
	})
}
