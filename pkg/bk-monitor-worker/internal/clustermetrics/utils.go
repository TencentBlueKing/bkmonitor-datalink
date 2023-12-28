// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package clustermetrics

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

func InitTestConfig() {
	pc, filename, x, ok := runtime.Caller(0)
	fmt.Println("Caller: ", pc, filename, x)
	if !ok {
		panic("Failed to get current file information")
	}
	path := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
	absPath, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	configFilePath := absPath + "/bmw.yaml"
	fmt.Println("Current config file path:", configFilePath)
	config.FilePath = configFilePath
	config.InitConfig()
}
