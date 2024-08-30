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
	confLoggerLevelPath = "logger.level"
)

func initConfig() {
	viper.SetDefault(confLoggerLevelPath, "info")
}

func updateConfig() {
	logger.SetOptions(logger.Options{
		Stdout: true,
		Format: "logfmt",
		Level:  viper.GetString(confLoggerLevelPath),
	})
}

func init() {
	if err := config.EventBus.Subscribe(config.EventConfigPreParse, initConfig); err != nil {
		fmt.Printf("failed to subscribe event %s, err: %v\n", config.EventConfigPreParse, err)
	}

	if err := config.EventBus.Subscribe(config.EventConfigPostParse, updateConfig); err != nil {
		fmt.Printf("failed to subscribe event %s, err: %v\n", config.EventConfigPostParse, err)
	}
}
