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
	"strings"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	FilePath     = "./config.yaml"
	EnvKeyPrefix = "api-server"
)

// HttpConfig http config
type HttpConfig struct {
	Mode string `yaml:"mode"`
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LogConfig log config
type LogConfig struct {
	EnableStdout bool   `yaml:"enable_stdout"`
	Level        string `yaml:"level"`
	Path         string `yaml:"path"`
	MaxSize      int    `yaml:"max_size"`
	MaxAge       int    `yaml:"max_age"`
	MaxBackups   int    `yaml:"max_backups"`
}

// ConfigInfo api server config
type ConfigInfo struct {
	Http HttpConfig `yaml:"http"`
	Log  LogConfig  `yaml:"log"`
}

var Config ConfigInfo

// InitConfig This method is used to refresh the configuration
func InitConfig() {
	viper.SetConfigFile(FilePath)

	// environment variable override all config file
	viper.AutomaticEnv()
	viper.SetEnvPrefix(EnvKeyPrefix)
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	if err := viper.ReadInConfig(); err != nil {
		logger.Fatalf("read config file: %s error: %s", FilePath, err)
	}

	err := viper.Unmarshal(&Config)
	if err != nil {
		logger.Fatalf("error unmarshal config file: %v", err)
	}
}
