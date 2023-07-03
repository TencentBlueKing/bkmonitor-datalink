// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reloader

import (
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	confKubeConfigPath      = "reloader.kubeconfig"
	confPIDPathPath         = "reloader.pid_path"
	confWatchPathPath       = "reloader.watch_path"
	confChildConfigPathPath = "reloader.child_config_path"
	confTaskTypePath        = "reloader.task_type"
)

var (
	ConfPIDPath         string
	ConfChildConfigPath string
	ConfWatchPath       []string
	ConfKubeConfig      string
	ConfNodeName        string
	ConfPodName         string
	ConfNamespace       string
	ConfTaskType        string
)

func initConfig() {
	viper.SetDefault(confChildConfigPathPath, "/data/bkmonitorbeat/config/child_configs")
	viper.SetDefault(confWatchPathPath, []string{"/data/bkmonitorbeat/config/bkmonitorbeat.conf"})
	viper.SetDefault(confPIDPathPath, "/data/pid/bkmonitorbeat.pid")
	viper.SetDefault(confKubeConfigPath, "")
	viper.SetDefault(confTaskTypePath, "")
}

func updateConfig() {
	ConfWatchPath = viper.GetStringSlice(confWatchPathPath)
	ConfPIDPath = viper.GetString(confPIDPathPath)
	ConfChildConfigPath = viper.GetString(confChildConfigPathPath)
	ConfNodeName = viper.GetString(define.EnvNodeName)
	ConfNamespace = viper.GetString(define.EnvNamespace)
	ConfPodName = viper.GetString(define.EnvPodName)
	ConfKubeConfig = viper.GetString(confKubeConfigPath)
	ConfTaskType = viper.GetString(confTaskTypePath)
}

func init() {
	if err := config.EventBus.Subscribe(config.EventSignalConfigPreParse, initConfig); err != nil {
		logger.Errorf("failed to subscribe event [%s], err: %v", config.EventSignalConfigPreParse, err)
	}

	if err := config.EventBus.Subscribe(config.EventSignalConfigPostParse, updateConfig); err != nil {
		logger.Errorf("failed to subscribe event [%s], err: %v", config.EventSignalConfigPostParse, err)
	}
}
