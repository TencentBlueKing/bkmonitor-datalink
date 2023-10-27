// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

const (
	MetricRouterConfPath      = "consul.metric_path"
	MetadataConfigPath        = "consul.metadata_path"
	BCSInfoConfigPath         = "consul.bcs_path"
	MetadataConfigPathVersion = "consul.metadata_path_version"
	CheckUpdatePeriod         = "consul.check_update_period"
	DelayUpdateTime           = "consul.delay_update_time"
)

var (
	MetricRouterPath    string
	MetadataPath        string
	BCSInfoPath         string
	MetadataPathVersion string
	checkUpdatePeriod   time.Duration
	delayUpdateTime     time.Duration
)

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(MetadataConfigPath, "bkmonitorv3/metadata")
	viper.SetDefault(MetadataConfigPathVersion, "v1")
	viper.SetDefault(BCSInfoConfigPath, "bkmonitorv3/metadata/project_id")
	viper.SetDefault(CheckUpdatePeriod, "1s")
	viper.SetDefault(DelayUpdateTime, "5s")
	viper.SetDefault(MetricRouterConfPath, "bkmonitorv3/metadata/influxdb_metrics")
}

// LoadConfig
func LoadConfig() {
	MetricRouterPath = viper.GetString(MetricRouterConfPath)
	MetadataPath = viper.GetString(MetadataConfigPath)
	BCSInfoPath = viper.GetString(BCSInfoConfigPath)
	MetadataPathVersion = viper.GetString(MetadataConfigPathVersion)
	checkUpdatePeriod = viper.GetDuration(CheckUpdatePeriod)
	delayUpdateTime = viper.GetDuration(DelayUpdateTime)
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
