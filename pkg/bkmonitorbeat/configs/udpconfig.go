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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

const (
	ConfigTypeUDP        = define.ModuleUDP
	defaultUDPRequest    = "X"  // as netcat
	defaultUDPRequestHEX = "58" // hex for defaultUDPRequest
	defaultMaxTimes      = 10
)

// UDPTaskConfig :
type UDPTaskConfig struct {
	NetTaskParam      `config:"_,inline"`
	SimpleMatchParam  `config:"_,inline"`
	SimpleTaskParam   `config:"_,inline"`
	Times             int  `config:"times" validate:"min=1"`
	WaitEmptyResponse bool `config:"wait_empty_response"`
	CustomReport      bool `config:"custom_report"`
}

// InitIdent :
func (c *UDPTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

// Clean :
func (c *UDPTaskConfig) Clean() error {
	err := utils.CleanCompositeParamList(
		&(c.NetTaskParam),
		&(c.SimpleMatchParam),
		&(c.SimpleTaskParam),
	)
	if err != nil {
		return err
	}

	if c.Times < 1 {
		c.Times = 3
	}
	if c.Timeout < time.Nanosecond {
		c.Timeout = define.DefaultTimeout
	}
	return nil
}

// GetType :
func (c *UDPTaskConfig) GetType() string {
	return ConfigTypeUDP
}

// NewUDPTaskConfig :
func NewUDPTaskConfig() *UDPTaskConfig {
	return &UDPTaskConfig{}
}

// UDPTaskMetaConfig : udp task config
type UDPTaskMetaConfig struct {
	NetTaskMetaParam `config:"_,inline"`
	MaxTimes         int `config:"max_times" validate:"min=1"`

	Tasks []*UDPTaskConfig `config:"tasks"`
}

// Clean :
func (c *UDPTaskMetaConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.NetTaskMetaParam)
	if err != nil {
		return err
	}
	if c.MaxTimes == 0 {
		c.MaxTimes = defaultMaxTimes
	}
	for _, task := range c.Tasks {
		err = c.CleanTask(task)
		if c.MaxTimes > 0 {
			if task.Times > c.MaxTimes || task.Times == 0 {
				task.Times = c.MaxTimes
			}
		}
		if task.Request == "" {
			if task.RequestFormat == utils.ConvTypeHex {
				task.Request = defaultUDPRequestHEX
			} else {
				task.Request = defaultUDPRequest
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// GetTaskConfigList :
func (c *UDPTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewUDPTaskMetaConfig :
func NewUDPTaskMetaConfig(root *Config) *UDPTaskMetaConfig {
	config := &UDPTaskMetaConfig{
		NetTaskMetaParam: NewNetTaskMetaParam(),
		MaxTimes:         defaultMaxTimes,
	}
	config.Tasks = make([]*UDPTaskConfig, 0)

	root.TaskTypeMapping[ConfigTypeUDP] = config

	return config
}
