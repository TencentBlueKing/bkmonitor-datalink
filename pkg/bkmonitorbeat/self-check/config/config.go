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
	"path/filepath"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

type BkmonitorbeatConf struct {
	Path   PathConfig
	OutPut OutPutConfig
}

type PathConfig struct {
	Pid  string `config:"pid"`  // pid 文件路径
	Data string `config:"data"` // data 路径
}

type OutPutConfig struct {
	Endpoint string `config:"endpoint"` // socket 文件路径
}

var bkmonitorConf = BkmonitorbeatConf{}

// GetConfInfo 对外返回解析后的配置项
func GetConfInfo() BkmonitorbeatConf {
	return bkmonitorConf
}

// ParseConfiguration 解析配置文件对 confInfo 进行赋值操作
func ParseConfiguration() {
	rowConfig := beat.GetRawConfig()

	// 对于无法获取配置文件的情况，视情况决定是否退出
	if rowConfig == nil {
		fmt.Printf("unable to get bkmonitorbeat configuration, please check config.\n")
		return
	}

	// 解析 Path 相关的配置
	if cfg, err := rowConfig.Child("path", -1); err == nil {
		var pathCfg PathConfig
		if err = cfg.Unpack(&pathCfg); err == nil {
			bkmonitorConf.Path = pathCfg
		}
	}

	// 解析 OutPut 相关的配置
	if cfg, err := rowConfig.Child("output.bkpipe", -1); err == nil {
		var outputCfg OutPutConfig
		if err = cfg.Unpack(&outputCfg); err == nil {
			bkmonitorConf.OutPut = outputCfg
		}
	}

}

// GetProcessPid 获取进程的 Pid
func GetProcessPid() string {
	var pid string
	pidPath := bkmonitorConf.Path.Pid
	if pidPath == "" {
		fmt.Println("unable to get the path of pid file")
		return pid
	}
	pidFile := filepath.Join(pidPath, "bkmonitorbeat.pid")
	if f, err := os.ReadFile(pidFile); err == nil {
		// 删除尾部换行符
		pid = strings.TrimRight(string(f), "\n")
	}
	return pid
}
