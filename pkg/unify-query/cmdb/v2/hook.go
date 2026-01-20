// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

func setDefaultConfig() {
	viper.SetDefault(MaxHopsConfigPath, 2)
	viper.SetDefault(MaxAllowedHopsConfigPath, 5)
	viper.SetDefault(DefaultLimitConfigPath, 100)
	viper.SetDefault(DefaultLookBackDeltaConfigPath, 86400000) // 24小时（毫秒）

	viper.SetDefault(BKBaseSurrealDBUrlConfigPath, DefaultBKBaseSurrealDBUrl)
	viper.SetDefault(BKBaseSurrealDBResultTableIDConfigPath, DefaultBKBaseSurrealDBResultTableID)
	viper.SetDefault(BKBaseSurrealDBPreferStorageConfigPath, DefaultBKBaseSurrealDBPreferStorage)
	viper.SetDefault(BKBaseSurrealDBAuthMethodConfigPath, DefaultBKBaseSurrealDBAuthMethod)
	viper.SetDefault(BKBaseSurrealDBUsernameConfigPath, DefaultBKBaseSurrealDBUsername)
	viper.SetDefault(BKBaseSurrealDBAppCodeConfigPath, DefaultBKBaseSurrealDBAppCode)
	viper.SetDefault(BKBaseSurrealDBAppSecretConfigPath, DefaultBKBaseSurrealDBAppSecret)
	viper.SetDefault(BKBaseSurrealDBTimeoutConfigPath, DefaultBKBaseSurrealDBTimeout)
}

func LoadConfig() {
	DefaultMaxHops = viper.GetInt(MaxHopsConfigPath)
	MaxAllowedHops = viper.GetInt(MaxAllowedHopsConfigPath)
	DefaultLimit = viper.GetInt(DefaultLimitConfigPath)
	DefaultLookBackDelta = viper.GetInt64(DefaultLookBackDeltaConfigPath)

	BKBaseSurrealDBUrl = viper.GetString(BKBaseSurrealDBUrlConfigPath)
	BKBaseSurrealDBResultTableID = viper.GetString(BKBaseSurrealDBResultTableIDConfigPath)
	BKBaseSurrealDBPreferStorage = viper.GetString(BKBaseSurrealDBPreferStorageConfigPath)
	BKBaseSurrealDBAuthMethod = viper.GetString(BKBaseSurrealDBAuthMethodConfigPath)
	BKBaseSurrealDBUsername = viper.GetString(BKBaseSurrealDBUsernameConfigPath)
	BKBaseSurrealDBAppCode = viper.GetString(BKBaseSurrealDBAppCodeConfigPath)
	BKBaseSurrealDBAppSecret = viper.GetString(BKBaseSurrealDBAppSecretConfigPath)
	BKBaseSurrealDBTimeout = viper.GetDuration(BKBaseSurrealDBTimeoutConfigPath)
}

func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for cmdb v2 module for default config, maybe cmdb v2 module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for cmdb v2 module for new config, maybe cmdb v2 module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}
