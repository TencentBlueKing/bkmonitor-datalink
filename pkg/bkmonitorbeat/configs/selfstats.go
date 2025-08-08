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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	ConfigTypeSelfStats = define.ModuleSelfStats
)

type SelfStatsConfig struct {
	BaseTaskParam `config:"_,inline"`
}

func (c *SelfStatsConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 说明没有任务
	if c.DataID <= 0 {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *SelfStatsConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *SelfStatsConfig) GetType() string {
	return ConfigTypeSelfStats
}

func (c *SelfStatsConfig) Clean() error {
	return nil
}

func (c *SelfStatsConfig) GetIdent() string {
	return ConfigTypeSelfStats
}

func NewSelfStatsConfig(root *Config) *SelfStatsConfig {
	config := &SelfStatsConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[ConfigTypeSelfStats] = config

	return config
}
