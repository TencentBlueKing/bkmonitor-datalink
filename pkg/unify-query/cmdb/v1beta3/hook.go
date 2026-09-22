// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

func setDefaultConfig() {
	viper.SetDefault(MaxHopsConfigPath, 2)
	viper.SetDefault(MaxAllowedHopsConfigPath, 5)
	viper.SetDefault(DefaultLimitConfigPath, 100)
	viper.SetDefault(MaxRangePointsConfigPath, 11000)
	viper.SetDefault(MaxTargetsConfigPath, 5000)
	viper.SetDefault(MaxGraphNodesConfigPath, 100000)
	viper.SetDefault(MaxGraphEdgesConfigPath, 200000)
	viper.SetDefault(MaxGraphResultsConfigPath, 10000)
	viper.SetDefault(MaxGraphNodeInfosConfigPath, 1000000)
	viper.SetDefault(MaxSharedTopologyPointsConfigPath, 60)
	viper.SetDefault(MaxSharedTopologyConcurrencyConfigPath, 2)
	viper.SetDefault(MaxSharedTopologyBackendBytesConfigPath, 16*1024*1024)
	viper.SetDefault(MaxSharedTopologyMatrixPointsConfigPath, 1000000)
	viper.SetDefault(MaxSharedTopologyOutputElementsConfigPath, 200000)
	viper.SetDefault(MaxSharedTopologyOutputBytesConfigPath, 64*1024*1024)
	viper.SetDefault(DefaultLookBackDeltaConfigPath, 86400000) // 24小时（毫秒）
}

func LoadConfig() {
	DefaultMaxHops = viper.GetInt(MaxHopsConfigPath)
	MaxAllowedHops = viper.GetInt(MaxAllowedHopsConfigPath)
	DefaultLimit = viper.GetInt(DefaultLimitConfigPath)
	MaxRangePoints = viper.GetInt(MaxRangePointsConfigPath)
	MaxTargets = viper.GetInt(MaxTargetsConfigPath)
	MaxGraphNodes = viper.GetInt(MaxGraphNodesConfigPath)
	MaxGraphEdges = viper.GetInt(MaxGraphEdgesConfigPath)
	MaxGraphResults = viper.GetInt(MaxGraphResultsConfigPath)
	MaxGraphNodeInfos = viper.GetInt(MaxGraphNodeInfosConfigPath)
	MaxSharedTopologyPoints = viper.GetInt(MaxSharedTopologyPointsConfigPath)
	MaxSharedTopologyConcurrency = viper.GetInt(MaxSharedTopologyConcurrencyConfigPath)
	MaxSharedTopologyBackendBytes = viper.GetInt(MaxSharedTopologyBackendBytesConfigPath)
	MaxSharedTopologyMatrixPoints = viper.GetInt(MaxSharedTopologyMatrixPointsConfigPath)
	MaxSharedTopologyOutputElements = viper.GetInt(MaxSharedTopologyOutputElementsConfigPath)
	MaxSharedTopologyOutputBytes = viper.GetInt(MaxSharedTopologyOutputBytesConfigPath)
	DefaultLookBackDelta = viper.GetInt64(DefaultLookBackDeltaConfigPath)
}

func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for cmdb v1beta3 module for default config, maybe cmdb v1beta3 module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for cmdb v1beta3 module for new config, maybe cmdb v1beta3 module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
