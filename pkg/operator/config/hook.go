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
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

const (
	bkMonitorOperatorPrefix = "BKMONITOR"

	appName = "bkmonitor-operator"
)

var CustomConfigFilePath string

func InitConfig() error {
	if CustomConfigFilePath != "" {
		// Use config file from the flag.
		viper.SetConfigFile(CustomConfigFilePath)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			return err
		}

		// Search config in home directory with name $AppName (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(fmt.Sprintf("./%s.yaml", appName))
	}
	viper.SetEnvPrefix(bkMonitorOperatorPrefix)
	viper.AutomaticEnv() // read in environment variables that match
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	EventBus.Publish(EventSignalConfigPreParse)
	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	fmt.Println("using config file:", viper.ConfigFileUsed())
	fmt.Println("settings", viper.AllSettings())
	EventBus.Publish(EventSignalConfigPostParse)
	return nil
}
