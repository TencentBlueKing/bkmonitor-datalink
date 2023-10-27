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
)

var (
	ServiceName       string
	ConfigPath        string
	defaultConfigPath = "./bmw.yaml"
)

func init() {
	// 如果service name为空，则赋值为 `bmw`
	if ServiceName == "" {
		ServiceName = "bmw"
	}
}

// InitConfig init the service config
func InitConfig() {
	// 如果没有指定，则使用默认路径配置
	if ConfigPath == "" {
		viper.SetConfigFile(defaultConfigPath)
	} else {
		// 指定的配置文件
		viper.SetConfigFile(ConfigPath)
	}

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("load config: %s failed, err: %s", viper.ConfigFileUsed(), err)
		os.Exit(1)
	}

	// 读取环境变量，会覆盖配置文件中的值
	viper.AutomaticEnv()

	viper.SetEnvPrefix(ServiceName)
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	fmt.Println("load config: ", viper.ConfigFileUsed())
}
