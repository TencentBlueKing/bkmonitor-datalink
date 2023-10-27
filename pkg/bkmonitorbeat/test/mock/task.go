// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"context"

	"github.com/facebookgo/inject"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// Task :
type Task struct {
	GlobalConfig *Config     `inject:""`
	TaskConfig   *TaskConfig `inject:""`
}

// GetTaskID :
func (t *Task) GetTaskID() int32 {
	return t.TaskConfig.GetTaskID()
}

// GetStatus :
func (t *Task) GetStatus() define.Status {
	return define.TaskReady
}

// SetConfig :
func (t *Task) SetConfig(config define.TaskConfig) {
	t.TaskConfig = config.(*TaskConfig)
}

// GetConfig :
func (t *Task) GetConfig() define.TaskConfig {
	return t.TaskConfig
}

// SetGlobalConfig :
func (t *Task) SetGlobalConfig(config define.Config) {
	t.GlobalConfig = config.(*Config)
}

// GetGlobalConfig :
func (t *Task) GetGlobalConfig() define.Config {
	return t.GlobalConfig
}

// Reload :
func (t *Task) Reload() {
}

// Wait :
func (t *Task) Wait() {
}

func (t *Task) Stop() {}

// Run :
func (t *Task) Run(ctx context.Context, e chan<- define.Event) {
	e <- nil
}

// NewTask :
func NewTask() define.Task {
	var (
		g    inject.Graph
		task Task
		err  error
	)
	err = g.Provide(
		&inject.Object{Value: &task},
	)
	if err != nil {
		panic(err)
	}

	err = g.Populate()
	if err != nil {
		panic(err)
	}

	task.GlobalConfig.Task.Task = task.TaskConfig

	return &task
}
