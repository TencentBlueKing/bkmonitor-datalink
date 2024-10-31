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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

const (
	ConfigTypeTCP = define.ModuleTCP
)

// TCPTaskConfig :
type TCPTaskConfig struct {
	NetTaskParam     `config:"_,inline"`
	SimpleMatchParam `config:"_,inline"`
	SimpleTaskParam  `config:"_,inline"`
	CustomReport     bool `config:"custom_report"`
}

// InitIdent :
func (c *TCPTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

// Clean :
func (c *TCPTaskConfig) Clean() error {
	return utils.CleanCompositeParamList(
		&c.NetTaskParam,
		&c.SimpleMatchParam,
		&c.SimpleTaskParam,
	)
}

// GetType :
func (c *TCPTaskConfig) GetType() string {
	return ConfigTypeTCP
}

// NewTCPTaskConfig :
func NewTCPTaskConfig() *TCPTaskConfig {
	var conf TCPTaskConfig
	conf.Timeout = define.DefaultTimeout
	conf.ResponseFormat = DefaultResponseFormat
	conf.BufferSize = DefaultBufferSize
	return &conf
}

// TCPTaskMetaConfig : tcp task config
type TCPTaskMetaConfig struct {
	NetTaskMetaParam `config:"_,inline"`

	Tasks []*TCPTaskConfig `config:"tasks"`
}

// Clean :
func (c *TCPTaskMetaConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.NetTaskMetaParam)
	if err != nil {
		return err
	}
	for _, task := range c.Tasks {
		err = c.CleanTask(task)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetTaskConfigList :
func (c *TCPTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewTCPTaskMetaConfig :
func NewTCPTaskMetaConfig(root *Config) *TCPTaskMetaConfig {
	config := &TCPTaskMetaConfig{
		NetTaskMetaParam: NewNetTaskMetaParam(),
	}
	config.Tasks = make([]*TCPTaskConfig, 0)

	root.TaskTypeMapping[ConfigTypeTCP] = config

	return config
}
