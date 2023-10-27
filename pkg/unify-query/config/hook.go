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

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

// InitConfig 初始化配置
func InitConfig() {
	if CustomConfigFilePath != "" {
		// Use config file from the flag.
		viper.SetConfigFile(CustomConfigFilePath)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".kafka-watcher" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(fmt.Sprintf("./%s.yaml", AppName))
	}

	viper.SetEnvPrefix("unify-query")
	viper.AutomaticEnv() // read in environment variables that match
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// If a config file is found, read it in.
	// 在配置文件读取前，需要先通知全世界做好准备
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPreParse)
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("loading config file:%s failed,error:%s\n", viper.ConfigFileUsed(), err)
	} else {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
	// 配置读取后，通知全世界reload读取新的配置
	eventbus.EventBus.Publish(eventbus.EventSignalConfigPostParse)
}
