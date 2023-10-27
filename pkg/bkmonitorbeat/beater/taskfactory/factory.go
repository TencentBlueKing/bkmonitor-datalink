// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskfactory

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type taskNew func(define.Config, define.TaskConfig) define.Task

var mappings = make(map[string]taskNew)

// Register :
func Register(name string, f taskNew) {
	mappings[name] = f
}

// New :
func New(globalConf define.Config, taskConf define.TaskConfig) define.Task {
	taskType := taskConf.GetType()
	f, ok := mappings[taskType]
	if !ok {
		panic(fmt.Errorf("task(%s) create failed: %+v", taskType, taskConf))
	}
	return f(globalConf, taskConf)
}

// ConfigFunc :
type ConfigFunc func() define.TaskMetaConfig

// ConfigMap :
var configMap = make(map[string]ConfigFunc)

// SetTaskConfigByName :
func SetTaskConfigByName(name string, taskFunc ConfigFunc) {
	configMap[name] = taskFunc
}

// GetTaskConfigByName :
func GetTaskConfigByName(name string) (define.TaskMetaConfig, error) {
	taskFunc, ok := configMap[name]
	if !ok {
		return nil, fmt.Errorf("no task found")
	}
	return taskFunc(), nil
}
