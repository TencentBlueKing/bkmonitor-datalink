// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func setDefaultConfig() {
	viper.SetDefault(MultiTenantModeConfigPath, false)
}

func LoadConfig() {
	MultiTenantMode = viper.GetBool(MultiTenantModeConfigPath)

	log.Debugf(context.TODO(), "reload influxdb config: system_tenant_with_suffix=%v", MultiTenantMode)
}

func init() {
	eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig)
	eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig)
}
