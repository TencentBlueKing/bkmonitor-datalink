// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package logconf

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	confStdoutPath    = "log.stdout"
	confFormatPath    = "log.format"
	confFileNamePath  = "log.filename"
	confMaxAgePath    = "log.max_age"
	confMaxSizePath   = "log.max_size"
	confMaxBackupPath = "log.max_backup"
	confLogLevelPath  = "log.level"
)

func initConfig() {
	viper.SetDefault(confStdoutPath, false)
	viper.SetDefault(confFormatPath, "logfmt")
	viper.SetDefault(confFileNamePath, "bkmonitor-operator.log")
	viper.SetDefault(confMaxAgePath, 3)
	viper.SetDefault(confMaxSizePath, 512)
	viper.SetDefault(confMaxBackupPath, 5)
	viper.SetDefault(confLogLevelPath, "error")
}

func updateConfig() {
	logger.SetOptions(logger.Options{
		Stdout:     viper.GetBool(confStdoutPath),
		Format:     viper.GetString(confFormatPath),
		Filename:   viper.GetString(confFileNamePath),
		MaxAge:     viper.GetInt(confMaxAgePath),
		MaxSize:    viper.GetInt(confMaxSizePath),
		MaxBackups: viper.GetInt(confMaxBackupPath),
		Level:      viper.GetString(confLogLevelPath),
	})
}

func init() {
	if err := config.EventBus.Subscribe(config.EventSignalConfigPreParse, initConfig); err != nil {
		fmt.Printf("failed to subscribe event [%s], err: %v\n", config.EventSignalConfigPreParse, err)
	}

	if err := config.EventBus.Subscribe(config.EventSignalConfigPostParse, updateConfig); err != nil {
		fmt.Printf("failed to subscribe event [%s], err: %v\n", config.EventSignalConfigPostParse, err)
	}
}
