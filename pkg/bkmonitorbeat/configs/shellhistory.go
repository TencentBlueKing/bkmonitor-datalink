// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"

type ShellHistoryConfig struct {
	BaseTaskParam `config:"_,inline"`
	LastBytes     int64    `config:"last_bytes"`
	HistoryFiles  []string `config:"history_files"`
}

func (c *ShellHistoryConfig) GetTaskConfigList() []define.TaskConfig {
	if c.DataID == 0 {
		return []define.TaskConfig{}
	}
	return []define.TaskConfig{c}
}

func (c *ShellHistoryConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *ShellHistoryConfig) GetIdent() string {
	return define.ModuleShellHistory
}

func (c *ShellHistoryConfig) GetType() string {
	return define.ModuleShellHistory
}

func (c *ShellHistoryConfig) Clean() error {
	return nil
}

func NewShellHistoryConfig(root *Config) *ShellHistoryConfig {
	config := &ShellHistoryConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleShellHistory] = config

	return config
}
