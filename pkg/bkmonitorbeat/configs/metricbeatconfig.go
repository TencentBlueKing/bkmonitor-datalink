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
	"encoding/json"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// config类型定义
const (
	ConfigTypeMetric = "metricbeat"
)

// ModulesConfig modules结构体，只用于获取ident过程
type ModulesConfig struct {
	Task   BaseTaskParam
	Module string // 将modules序列化成json后存储
}

// MetricBeatConfig :
type MetricBeatConfig struct {
	BaseTaskParam `config:"_,inline"`

	Module *common.Config `config:"module"`
	// 是否使用自定义指标格式上报
	CustomReport bool `config:"custom_report"`

	Workers        int
	SpreadWorkload bool
	EnableAlignTs  bool
}

// InitIdent 覆盖BaseTaskParam的同名方法，因为metricbeat直接调用原方法会panic
func (c *MetricBeatConfig) InitIdent() error {
	c.convertLabels()
	// 这里由于metricbeat的特殊性做了修改
	err := c.getHashWithModule()
	if err != nil {
		return err
	}
	c.resetLabels()
	return nil
}

// Clean :
func (c *MetricBeatConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	if c.Module == nil {
		return define.ErrorNoTask
	}

	return nil
}

func (c *MetricBeatConfig) getHashWithModule() error {
	// 收集module信息
	mods := new(ModulesConfig)

	var mod map[string]interface{}
	err := c.Module.Unpack(&mod)
	if err != nil {
		logger.Errorf("unpack module failed,err:%v", err)
		return err
	}
	buf, err := json.Marshal(mod)
	if err != nil {
		logger.Errorf("Marshal module failed,err:%v", err)
		return err
	}
	mods.Module = string(buf)

	// 在modules信息后面追加基础任务信息
	mods.Task = c.BaseTaskParam

	// 用收集到的信息建立唯一变量Ident
	c.Ident = utils.HashIt(mods)
	return nil
}

// GetType :
func (c *MetricBeatConfig) GetType() string {
	return ConfigTypeMetric
}

// NewMetricBeatConfig :
func NewMetricBeatConfig() *MetricBeatConfig {
	var conf MetricBeatConfig
	conf.Timeout = define.DefaultTimeout
	return &conf
}

// MetricBeatMetaConfig : associate task config
type MetricBeatMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`

	Tasks []*MetricBeatConfig `config:"tasks"`
}

// Clean :
func (c *MetricBeatMetaConfig) Clean() error {
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
func (c *MetricBeatMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewMetricBeatMetaConfig :
func NewMetricBeatMetaConfig(root *Config) *MetricBeatMetaConfig {
	config := &MetricBeatMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*MetricBeatConfig, 0)
	root.TaskTypeMapping[ConfigTypeMetric] = config

	return config
}
