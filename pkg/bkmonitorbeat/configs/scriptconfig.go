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
	"time"

	"github.com/elastic/go-ucfg/yaml"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	ConfigTypeScript = define.ModuleScript
)

// ScriptTaskConfig :
type ScriptTaskConfig struct {
	BaseTaskParam `config:"_,inline"`
	Command       string            `config:"command"`
	TimestampUnit string            `config:"timestamp_unit"`
	UserEnvs      map[string]string `config:"user_env"`
	Username      string            `config:"username"`
	TimeOffset    time.Duration     `config:"time_offset"`
}

// InitIdent :
func (c *ScriptTaskConfig) InitIdent() error {
	// Preserve existing identifiers where gob has no map ordering ambiguity.
	if len(c.UserEnvs) <= 1 && len(c.Tags) <= 1 {
		return c.initIdent(c)
	}
	// Compare labels without regard to order, and let JSON sort the remaining map keys.
	snapshot := *c
	snapshot.convertLabels()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	c.Ident = utils.HashIt(data)
	return nil
}

// Clean :
func (c *ScriptTaskConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	if c.TimestampUnit == "" {
		c.TimestampUnit = "s"
	}
	// 默认可容忍偏移时间为两年
	if c.TimeOffset == 0 {
		c.TimeOffset = 24 * time.Hour * 365 * 2
	}
	return nil
}

// GetType :
func (c *ScriptTaskConfig) GetType() string {
	return ConfigTypeScript
}

// NewScriptTaskConfig :
func NewScriptTaskConfig() *ScriptTaskConfig {
	var conf ScriptTaskConfig
	conf.Timeout = 60 * time.Second
	return &conf
}

// ScriptTaskMetaConfig : tcp task config
type ScriptTaskMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`
	TaskConfigPath    string              `config:"config_path"`
	Tasks             []*ScriptTaskConfig `config:"tasks"`
}

// Clean :
func (c *ScriptTaskMetaConfig) Clean() error {
	err := c.BaseTaskMetaParam.CleanParams()
	if err != nil {
		return err
	}
	// load script config for config path
	if c.TaskConfigPath != "" {
		if err := c.loadExclusiveCfg(); err != nil {
			return err
		}
		logger.Infof("loaded script config %s, tasks: %d", c.TaskConfigPath, len(c.Tasks))
	}
	for _, task := range c.Tasks {
		err = c.CleanTask(task)
		if err != nil {
			return err
		}
		logger.Infof("script task %d: period %s, timeout %s", task.TaskID, task.Period, task.Timeout)
	}
	return nil
}

func (c *ScriptTaskMetaConfig) loadExclusiveCfg() error {
	if !utils.PathExist(c.TaskConfigPath) {
		logger.Errorf("script detail config path %s not exist", c.TaskConfigPath)
		// return nil in order to let other module run
		return nil
	}
	cfg, err := yaml.NewConfigWithFile(c.TaskConfigPath)
	if err != nil {
		logger.Errorf("parse %s to config failed:%s", c.TaskConfigPath, err.Error())
		return err
	}
	err = cfg.Unpack(c)
	if err != nil {
		logger.Errorf("Unpack %s failed:%s", c.TaskConfigPath, err.Error())
		return err
	}
	return nil
}

// GetTaskConfigList :
func (c *ScriptTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewScriptTaskMetaConfig :
func NewScriptTaskMetaConfig(root *Config) *ScriptTaskMetaConfig {
	config := &ScriptTaskMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*ScriptTaskConfig, 0)

	root.TaskTypeMapping[ConfigTypeScript] = config

	return config
}
