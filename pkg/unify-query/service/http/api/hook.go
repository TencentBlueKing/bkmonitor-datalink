// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

func setDefaultConfig() {
	viper.SetDefault(RelationMultiResourceConfigPath, "/api/v1/relation/multi_resource")
	viper.SetDefault(RelationMultiResourceRangeConfigPath, "/api/v1/relation/multi_resource_range")
	viper.SetDefault(RelationMaxRoutingConfigPath, 5)
	viper.SetDefault(RelationMultiResourceV2ConfigPath, "/api/v2/relation/multi_resource")
	viper.SetDefault(RelationMultiResourceRangeV2ConfigPath, "/api/v2/relation/multi_resource_range")
}

func loadConfig() {
	RelationMultiResource = viper.GetString(RelationMultiResourceConfigPath)
	RelationMultiResourceRange = viper.GetString(RelationMultiResourceRangeConfigPath)
	RelationMaxRouting = viper.GetInt(RelationMaxRoutingConfigPath)
	RelationMultiResourceV2 = viper.GetString(RelationMultiResourceV2ConfigPath)
	RelationMultiResourceRangeV2 = viper.GetString(RelationMultiResourceRangeV2ConfigPath)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for api module for default config, maybe api module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, loadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for api module for new config, maybe api module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
