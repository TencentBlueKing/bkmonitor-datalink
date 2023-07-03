// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/eventbus"
)

// InitConfigPath 设置配置读取路径
func InitConfigPath() error {
	if CustomConfigFilePath != "" {
		// Use config file from the flag.
		viper.SetConfigFile(CustomConfigFilePath)
	} else {
		dir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("init config error: %+v \n", err)
		}

		// Search config in home directory with name ".kafka-watcher" (without extension).
		viper.AddConfigPath(dir)
		viper.SetConfigName("config")
	}

	v := viper.GetViper()
	v.SetEnvPrefix(AppName)
	v.AutomaticEnv() // read in environment variables that match
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// If a config file is found, read it in.
	// 在配置文件读取前，需要先通知全世界做好准备
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPreParse)

	setDefault()
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("loading config file: %s failed, error: %s\n", viper.ConfigFileUsed(), err)
	}
	// 配置读取后，通知全世界reload读取新的配置
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPostParse)
	return nil
}

func setDefault() {
	viper.SetDefault(MoveMaxPoolConfigPath, 10)
	viper.SetDefault(MoveIntervalConfigPath, "1h")
	viper.SetDefault(MoveDistributedLockExpiration, "1h")
	viper.SetDefault(MoveDistributedLockRenewalDuration, "1m")
	viper.SetDefault(MoveClusterNameConfigPath, "default")
	viper.SetDefault(MoveSourceDirConfigPath, "/data")
	viper.SetDefault(MoveTargetNameConfigPath, "cos")
	viper.SetDefault(MoveTargetDirConfigPath, "/move")

	viper.SetDefault(RebuildMaxPoolConfigPath, 10)
	viper.SetDefault(RebuildIntervalConfigPath, "1h")
	viper.SetDefault(RebuildDistributedLockExpiration, "1h")
	viper.SetDefault(RebuildDistributedLockRenewalDuration, "1m")
	viper.SetDefault(RebuildFinalNameConfigPath, "cos")
	viper.SetDefault(RebuildFinalDirConfigPath, "/rebuild")

	viper.SetDefault(QueryHttpHostConfigPath, "0.0.0.0")
	viper.SetDefault(QueryHttpPortConfigPath, "8089")
	viper.SetDefault(QueryHttpReadTimeoutConfigPath, "30s")
	viper.SetDefault(QueryHttpMetricConfigPath, "/metric")
	viper.SetDefault(QueryHttpDIrConfigPath, "/data/influxdb")
}
