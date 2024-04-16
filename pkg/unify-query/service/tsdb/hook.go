// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

// setDefaultConfig 配置初始化参数
func setDefaultConfig() {
	// influxDB 基础配置
	viper.SetDefault(InfluxDBPerQueryMaxGoroutineConfigPath, 2)

	viper.SetDefault(InfluxDBTimeoutConfigPath, "1m")
	viper.SetDefault(InfluxDBContentTypeConfigPath, "application/x-msgpack")
	// influxdb 先根据series分流之后，再每个series下的数量分流
	viper.SetDefault(InfluxDBChunkSizeConfigPath, 20000)

	// influxdb 直查配置
	viper.SetDefault(InfluxDBQueryRawUriPathConfigPath, "api/v1/raw/read")
	viper.SetDefault(InfluxDBQueryRawAcceptConfigPath, "application/x-protobuf")
	viper.SetDefault(InfluxDBQueryRawAcceptEncodingConfigPath, "snappy")

	viper.SetDefault(InfluxDBQueryReadRateLimitConfigPath, 1e6)
	viper.SetDefault(InfluxDBMaxLimitConfigPath, 1e8)
	viper.SetDefault(InfluxDBMaxSLimitConfigPath, 2e5)
	viper.SetDefault(InfluxDBToleranceConfigPath, 5)

	viper.SetDefault(InfluxDBRouterPrefixConfigPath, "bkmonitorv3:influxdb")

	// victoriaMetrics 配置
	viper.SetDefault(VmTimeoutConfigPath, "30s")
	viper.SetDefault(VmContentTypeConfigPath, "application/json")
	viper.SetDefault(VmMaxConditionNumConfigPath, 2e4)

	// vm 支持 influxdb 的查询配置
	viper.SetDefault(VmInfluxCompatibleConfigPath, true)
	viper.SetDefault(VmUseNativeOrConfigPath, true)

	viper.SetDefault(VmAuthenticationMethodConfigPath, "token")

	viper.SetDefault(OfflineDataArchiveAddressConfigPath, "bk-datalink-offline-data-archive:8089")
	viper.SetDefault(OfflineDataArchiveTimeoutConfigPath, "10m")
	viper.SetDefault(OfflineDataArchiveGrpcMaxCallRecvMsgSizeConfigPath, 1024*1024*10)
	viper.SetDefault(OfflineDataArchiveGrpcMaxCallSendMsgSizeConfigPath, 1024*1024*10)

	viper.SetDefault(BkSqlTimeoutConfigPath, "30s")
	viper.SetDefault(BkSqlIntervalTimeConfigPath, "300ms")
	viper.SetDefault(BkSqlLimitConfigPath, 2e6)
	viper.SetDefault(BkSqlToleranceConfigPath, 5)
	viper.SetDefault(BkSqlContentTypeConfigPath, "application/json")
	viper.SetDefault(BkSqlAuthenticationMethodConfigPath, "token")

	viper.SetDefault(EsTimeoutConfigPath, "30s")
	viper.SetDefault(EsMaxSizeConfigPath, 1e4)
	viper.SetDefault(EsMaxRoutingConfigPath, 10)
}

// initConfig 加载配置
func initConfig() {
	InfluxDBTimeout = viper.GetDuration(InfluxDBTimeoutConfigPath)
	InfluxDBContentType = viper.GetString(InfluxDBContentTypeConfigPath)
	InfluxDBChunkSize = viper.GetInt(InfluxDBChunkSizeConfigPath)

	InfluxDBQueryRawUriPath = viper.GetString(InfluxDBQueryRawUriPathConfigPath)
	InfluxDBQueryRawAccept = viper.GetString(InfluxDBQueryRawAcceptConfigPath)
	InfluxDBQueryRawAcceptEncoding = viper.GetString(InfluxDBQueryRawAcceptEncodingConfigPath)

	InfluxDBMaxLimit = viper.GetInt(InfluxDBMaxLimitConfigPath)
	InfluxDBMaxSLimit = viper.GetInt(InfluxDBMaxSLimitConfigPath)
	InfluxDBTolerance = viper.GetInt(InfluxDBToleranceConfigPath)

	InfluxDBRouterPrefix = viper.GetString(InfluxDBRouterPrefixConfigPath)

	// victoriaMetrics 配置
	VmAddress = viper.GetString(VmAddressConfigPath)
	VmTimeout = viper.GetDuration(VmTimeoutConfigPath)
	VmUriPath = viper.GetString(VmUriPathConfigPath)
	VmContentType = viper.GetString(VmContentTypeConfigPath)
	VmCode = viper.GetString(VmCodeConfigPath)
	VmSecret = viper.GetString(VmSecretConfigPath)
	VmToken = viper.GetString(VmTokenConfigPath)
	VmAuthenticationMethod = viper.GetString(VmAuthenticationMethodConfigPath)
	VmMaxConditionNum = viper.GetInt(VmMaxConditionNumConfigPath)

	VmInfluxCompatible = viper.GetBool(VmInfluxCompatibleConfigPath)
	VmUseNativeOr = viper.GetBool(VmUseNativeOrConfigPath)

	// bksql 配置
	BkSqlAddress = viper.GetString(BkSqlAddressConfigPath)
	BkSqlTimeout = viper.GetDuration(BkSqlTimeoutConfigPath)
	BkSqlIntervalTime = viper.GetDuration(BkSqlIntervalTimeConfigPath)
	BkSqlLimit = viper.GetInt(BkSqlLimitConfigPath)
	BkSqlTolerance = viper.GetInt(BkSqlToleranceConfigPath)
	BkSqlAuthenticationMethod = viper.GetString(BkSqlAuthenticationMethodConfigPath)
	BkSqlContentType = viper.GetString(BkSqlContentTypeConfigPath)
	BkSqlCode = viper.GetString(BkSqlCodeConfigPath)
	BkSqlSecret = viper.GetString(BkSqlSecretConfigPath)
	BkSqlToken = viper.GetString(BkSqlTokenConfigPath)

	OfflineDataArchiveAddress = viper.GetString(OfflineDataArchiveAddressConfigPath)
	OfflineDataArchiveTimeout = viper.GetDuration(OfflineDataArchiveTimeoutConfigPath)
	OfflineDataArchiveGrpcMaxCallRecvMsgSize = viper.GetInt(OfflineDataArchiveGrpcMaxCallRecvMsgSizeConfigPath)
	OfflineDataArchiveGrpcMaxCallSendMsgSize = viper.GetInt(OfflineDataArchiveGrpcMaxCallSendMsgSizeConfigPath)

	EsTimeout = viper.GetDuration(EsTimeoutConfigPath)
	EsMaxRouting = viper.GetInt(EsMaxRoutingConfigPath)
	EsMaxSize = viper.GetInt(EsMaxSizeConfigPath)
}

// init 初始化，通过 eventBus 加载配置读取前和读取后操作
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for trace module for default config, maybe http module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, initConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for trace module for new config, maybe http module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
