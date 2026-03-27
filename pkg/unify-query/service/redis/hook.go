// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

func setDefaultConfig() {
	viper.SetDefault(ModeConfigPath, "standalone")
	viper.SetDefault(HostConfigPath, "127.0.0.1")
	viper.SetDefault(PortConfigPath, 6379)
	viper.SetDefault(PasswordConfigPath, "")

	viper.SetDefault(MasterNameConfigPath, "")
	viper.SetDefault(SentinelAddressConfigPath, []string{})
	viper.SetDefault(SentinelPasswordConfigPath, "")

	viper.SetDefault(DialTimeoutConfigPath, time.Second)
	viper.SetDefault(ReadTimeoutConfigPath, time.Second*30)
	viper.SetDefault(ServiceNameConfigPath, "bkmonitorv3:spaces")
	viper.SetDefault(KVBasePathConfigPath, "bkmonitorv3:unify-query")
}

func LoadConfig() {
	Mode = viper.GetString(ModeConfigPath)
	Host = viper.GetString(HostConfigPath)
	Port = viper.GetInt(PortConfigPath)
	Password = viper.GetString(PasswordConfigPath)

	MasterName = viper.GetString(MasterNameConfigPath)
	SentinelAddress = viper.GetStringSlice(SentinelAddressConfigPath)
	SentinelPassword = viper.GetString(SentinelPasswordConfigPath)
	DataBase = viper.GetInt(DataBaseConfigPath)

	DialTimeout = viper.GetDuration(DialTimeoutConfigPath)
	ReadTimeout = viper.GetDuration(ReadTimeoutConfigPath)
	ServiceName = viper.GetString(ServiceNameConfigPath)
	KVBasePath = viper.GetString(KVBasePathConfigPath)
}

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
