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
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(TimeoutConfigPath, "30s")
	viper.SetDefault(PerQueryMaxGoroutineConfigPath, 2)
	viper.SetDefault(ContentTypeConfigPath, "application/x-msgpack")
	// influxdb 先根据series分流之后，再每个series下的数量分流
	viper.SetDefault(ChunkSizeConfigPath, 20000)

	viper.SetDefault(ToleranceConfigPath, 5)
	viper.SetDefault(MaxLimitConfigPath, 5e6)
	viper.SetDefault(MaxSLimitConfigPath, 2e5)

	viper.SetDefault(PrefixConfigPath, "bkmonitorv3:influxdb")
	viper.SetDefault(RouterIntervalConfigPath, "30m")

	viper.SetDefault(SpaceRouterPrefixConfigPath, "bkmonitorv3:spaces")
	viper.SetDefault(SpaceRouterBboltPathConfigPath, "boltV2.db")
	viper.SetDefault(SpaceRouterBboltBucketNameConfigPath, "space")
	viper.SetDefault(SpaceRouterBboltWriteBatchSizeConfigPath, 1000)

	viper.SetDefault(GrpcMaxCallRecvMsgSizeConfigPath, 1024*1024*10)
	viper.SetDefault(GrpcMaxCallSendMsgSizeConfigPath, 1024*1024*10)
}

// LoadConfig
func LoadConfig() {
	Tolerance = viper.GetInt(ToleranceConfigPath)
	MaxLimit = viper.GetInt(MaxLimitConfigPath)
	MaxSLimit = viper.GetInt(MaxSLimitConfigPath)

	Timeout = viper.GetString(TimeoutConfigPath)
	PerQueryMaxGoroutine = viper.GetInt(PerQueryMaxGoroutineConfigPath)
	ContentType = viper.GetString(ContentTypeConfigPath)
	ChunkSize = viper.GetInt(ChunkSizeConfigPath)

	RouterPrefix = viper.GetString(PrefixConfigPath)
	RouterInterval = viper.GetDuration(RouterIntervalConfigPath)

	SpaceRouterPrefix = viper.GetString(SpaceRouterPrefixConfigPath)
	SpaceRouterBboltPath = viper.GetString(SpaceRouterBboltPathConfigPath)
	SpaceRouterBboltBucketName = viper.GetString(SpaceRouterBboltBucketNameConfigPath)
	SpaceRouterBboltWriteBatchSize = viper.GetInt(SpaceRouterBboltWriteBatchSizeConfigPath)

	GrpcMaxCallRecvMsgSize = viper.GetInt(GrpcMaxCallRecvMsgSizeConfigPath)
	GrpcMaxCallSendMsgSize = viper.GetInt(GrpcMaxCallSendMsgSizeConfigPath)
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for http module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
