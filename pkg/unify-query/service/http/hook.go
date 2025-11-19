// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(IPAddressConfigPath, "127.0.0.1")
	viper.SetDefault(PortConfigPath, 10205)
	viper.SetDefault(UserNameConfigPath, "")
	viper.SetDefault(PasswordConfigPath, "")
	viper.SetDefault(WriteTimeOutConfigPath, "30s")
	viper.SetDefault(ReadTimeOutConfigPath, "3s")
	viper.SetDefault(SlowQueryThresholdConfigPath, "3s")
	viper.SetDefault(SingleflightTimeoutConfigPath, "1m")
	viper.SetDefault(DefaultQueryListLimitPath, 20)

	viper.SetDefault(EnablePrometheusConfigPath, true)
	viper.SetDefault(PrometheusPathConfigPath, "/metrics")

	viper.SetDefault(EnableProfileConfigPath, false)
	viper.SetDefault(ProfilePathConfigPath, "/debug/pprof/")

	viper.SetDefault(FluxHandlePromqlPathConfigPath, "/query/promql")
	viper.SetDefault(ESHandlePathConfigPath, "/query/es")
	viper.SetDefault(TSQueryHandlePathConfigPath, "/query/ts")
	viper.SetDefault(TSQueryExemplarHandlePathConfigPath, "/query/ts/exemplar")
	viper.SetDefault(TSQueryPromQLHandlePathConfigPath, "/query/ts/promql")
	viper.SetDefault(TSQueryReferenceQueryHandlePathConfigPath, "/query/ts/reference")
	viper.SetDefault(TSQueryRawQueryHandlePathConfigPath, "/query/ts/raw")
	viper.SetDefault(TSQueryRawQueryWithScrollHandlePathConfigPath, "/query/ts/raw_with_scroll")
	viper.SetDefault(TSQueryRawMAXLimitConfigPath, 1e2)
	viper.SetDefault(TSQueryInfoHandlePathConfigPath, "/query/ts/info")
	viper.SetDefault(TSQueryStructToPromQLHandlePathConfigPath, "/query/ts/struct_to_promql")
	viper.SetDefault(TSQueryPromQLToStructHandlePathConfigPath, "/query/ts/promql_to_struct")

	viper.SetDefault(TSQueryLabelValuesPathConfigPath, "/query/ts/label/:label_name/values")
	viper.SetDefault(TSQueryClusterMetricsPathConfigPath, "/query/ts/cluster_metrics")

	viper.SetDefault(PrintHandlePathConfigPath, "/print")
	viper.SetDefault(FeatureFlagHandlePathConfigPath, "/ff")
	viper.SetDefault(SpacePrintHandlePathConfigPath, "/space_print")
	viper.SetDefault(SpaceKeyPrintHandlePathConfigPath, "/space_key_print")
	viper.SetDefault(TsDBPrintHandlePathConfigPath, "/tsdb_print")
	viper.SetDefault(InfluxDBPrintHandlePathConfigPath, "/influxdb_print")

	viper.SetDefault(CheckQueryTsConfigPath, "/check/query/ts")
	viper.SetDefault(CheckQueryPromQLConfigPath, "/check/query/ts/promql")
	viper.SetDefault(ProxyConfigPath, "/proxy")

	viper.SetDefault(AlignInfluxdbResultConfigPath, true)
	viper.SetDefault(InfoDefaultLimit, 100)

	// 分段查询配置
	viper.SetDefault(SegmentedEnable, false)
	viper.SetDefault(SegmentedMaxRoutines, 1)
	viper.SetDefault(SegmentedMinInterval, "5m")

	viper.SetDefault(QueryMaxRoutingConfigPath, 4)

	viper.SetDefault(ClusterMetricQueryPrefixConfigPath, "bkmonitor")
	viper.SetDefault(ClusterMetricQueryTimeoutConfigPath, "30s")

	// scroll
	viper.SetDefault(ScrollSliceLimitConfigPath, 10000)
	viper.SetDefault(ScrollSessionLockTimeoutConfigPath, "60s")
	viper.SetDefault(ScrollWindowTimeoutConfigPath, "3m")
}

// LoadConfig
func LoadConfig() {
	TestV = viper.GetBool(AlignInfluxdbResultConfigPath)

	AlignInfluxdbResult = viper.GetBool(AlignInfluxdbResultConfigPath)
	IPAddress = viper.GetString(IPAddressConfigPath)
	Port = viper.GetInt(PortConfigPath)
	Username = viper.GetString(UserNameConfigPath)
	Password = viper.GetString(PasswordConfigPath)
	WriteTimeout = viper.GetDuration(WriteTimeOutConfigPath)
	ReadTimeout = viper.GetDuration(ReadTimeOutConfigPath)
	SingleflightTimeout = viper.GetDuration(SingleflightTimeoutConfigPath)
	SlowQueryThreshold = viper.GetDuration(SlowQueryThresholdConfigPath)
	DefaultQueryListLimit = viper.GetInt(DefaultQueryListLimitPath)

	ScrollSliceLimit = viper.GetInt(ScrollSliceLimitConfigPath)
	ScrollWindowTimeout = viper.GetString(ScrollWindowTimeoutConfigPath)
	ScrollSessionLockTimeout = viper.GetString(ScrollSessionLockTimeoutConfigPath)

	QueryMaxRouting = viper.GetInt(QueryMaxRoutingConfigPath)

	ClusterMetricQueryPrefix = viper.GetString(ClusterMetricQueryPrefixConfigPath)
	ClusterMetricQueryTimeout = viper.GetDuration(ClusterMetricQueryTimeoutConfigPath)

	JwtPublicKey = viper.GetString(JwtPublicKeyConfigPath)
	JwtBkAppCodeSpaces = viper.GetStringMapStringSlice(JwtBkAppCodeSpacesConfigPath)
	JwtEnabled = viper.GetBool(JwtEnabledConfigPath)
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
