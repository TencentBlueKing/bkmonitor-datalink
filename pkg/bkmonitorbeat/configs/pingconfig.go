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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// config类型定义
const (
	ConfigTypePing = define.ModulePing
)

// Target ping目标信息
type Target struct {
	Target     string `config:"target"`
	TargetType string `config:"target_type"`
}

// GetTarget :
func (t *Target) GetTarget() string {
	return t.Target
}

// GetTargetType :
func (t *Target) GetTargetType() string {
	return t.TargetType
}

// PingTaskConfig :
type PingTaskConfig struct {
	BaseTaskParam `config:"_,inline"`
	TargetIPType  IPType `config:"target_ip_type"`
	// 域名检测模式
	DNSCheckMode CheckMode `config:"dns_check_mode"`
	Targets      []*Target `config:"targets"`
	MaxRTT       string    `config:"max_rtt"`
	BatchSize    int       `config:"batch_size"`
	TotalNum     int       `config:"total_num"`
	PingSize     int       `config:"ping_size"`
	CustomReport bool      `config:"custom_report"`
}

// InitIdent :
func (c *PingTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

// Clean :
func (c *PingTaskConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	if c.MaxRTT == "" {
		defaultMaxRTT := "1s"
		logger.Infof("max rtt not configured,set %v by default", defaultMaxRTT)
		c.MaxRTT = defaultMaxRTT
	}
	if c.TotalNum == 0 {
		defaultTotal := 2
		logger.Infof("total num not configured,set %v by default", defaultTotal)
		c.TotalNum = defaultTotal
	}
	if c.PingSize == 0 || c.PingSize < 8 {
		defaultSize := 56
		logger.Infof("ping size not configured or minor than 8,set %v by default", defaultSize)
		c.PingSize = defaultSize
	}

	for _, target := range c.Targets {
		if target.GetTargetType() != "ip" && target.GetTargetType() != "domain" {
			return define.ErrWrongTargetType
		}
	}

	if c.DNSCheckMode == "" {
		// 未配置则使用默认模式
		c.DNSCheckMode = DefaultDNSCheckMode
	}

	return nil
}

// GetType :
func (c *PingTaskConfig) GetType() string {
	return ConfigTypePing
}

// NewPingTaskConfig :
func NewPingTaskConfig() *PingTaskConfig {
	var conf PingTaskConfig
	conf.Timeout = define.DefaultTimeout
	return &conf
}

// PingTaskMetaConfig : associate task config
type PingTaskMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`

	Tasks []*PingTaskConfig `config:"tasks"`
}

// Clean :
func (c *PingTaskMetaConfig) Clean() error {
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
func (c *PingTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewPingTaskMetaConfig :
func NewPingTaskMetaConfig(root *Config) *PingTaskMetaConfig {
	config := &PingTaskMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*PingTaskConfig, 0)

	root.TaskTypeMapping[ConfigTypePing] = config

	return config
}
