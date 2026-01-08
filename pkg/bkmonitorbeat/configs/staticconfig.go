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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

// config类型定义
const (
	ConfigTypeStatic = define.ModuleStatic
)

// StaticTaskConfig :
type StaticTaskConfig struct {
	BaseTaskParam         `config:"_,inline"`
	CheckPeriod           time.Duration `config:"check_period"`
	ReportPeriod          time.Duration `config:"report_period"`
	VirtualIfaceWhitelist []string      `config:"virtual_iface_whitelist"`
	VirtualIfaceBlacklist []string      `config:"virtual_iface_blacklist"`
}

// InitIdent :
func (c *StaticTaskConfig) InitIdent() error {
	info, _ := gse.GetAgentInfo()
	if info.StaticDataID != 0 {
		c.DataID = info.StaticDataID
	}
	return c.initIdent(c)
}

// Clean :
func (c *StaticTaskConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	// 小于1分钟按1分钟算
	if c.CheckPeriod < (1 * time.Minute) {
		c.CheckPeriod = 1 * time.Minute
	}
	// 小于1小时按6小时(默认值)算
	if c.ReportPeriod < (1 * time.Hour) {
		c.ReportPeriod = 6 * time.Hour
	}

	return nil
}

// GetType :
func (c *StaticTaskConfig) GetType() string {
	return ConfigTypeStatic
}

// NewStaticTaskConfig :
func NewStaticTaskConfig() *StaticTaskConfig {
	var conf StaticTaskConfig
	conf.Timeout = define.DefaultTimeout
	return &conf
}

// StaticTaskMetaConfig : associate task config
type StaticTaskMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`

	Tasks []*StaticTaskConfig `config:"tasks"`
}

// Clean :
func (c *StaticTaskMetaConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskMetaParam)
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
func (c *StaticTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewStaticTaskMetaConfig :
func NewStaticTaskMetaConfig(root *Config) *StaticTaskMetaConfig {
	config := &StaticTaskMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*StaticTaskConfig, 0)
	root.TaskTypeMapping[ConfigTypeStatic] = config

	return config
}
