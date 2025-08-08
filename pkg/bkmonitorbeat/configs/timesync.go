// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
)

const (
	ConfigTypeTimeSync = define.ModuleTimeSync
)

type TimeSyncConfig struct {
	BaseTaskParam `config:"_,inline"`

	Env           string        `config:"env"`
	QueryTimeout  time.Duration `config:"query_timeout"`
	NtpdPath      string        `config:"ntpd_path"`
	ChronyAddress string        `config:"chrony_address"`
}

func (c *TimeSyncConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 说明没有任务
	if len(c.NtpdPath) == 0 && len(c.ChronyAddress) == 0 {
		return tasks
	}

	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(ConfigTypeTimeSync); ok {
		c.DataID = v
	}
	tasks = append(tasks, c)
	return tasks
}

func (c *TimeSyncConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *TimeSyncConfig) GetType() string {
	return ConfigTypeTimeSync
}

func (c *TimeSyncConfig) Clean() error {
	return nil
}

func (c *TimeSyncConfig) GetIdent() string {
	return ConfigTypeTimeSync
}

func NewTimeSyncConfig(root *Config) *TimeSyncConfig {
	config := &TimeSyncConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[ConfigTypeTimeSync] = config

	return config
}
