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
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

// config类型定义
const (
	ConfigTypeTrap = define.ModuleTrap
)

// TrapConfig :
type TrapConfig struct {
	BaseTaskParam   `config:"_,inline"`
	IP              string        `config:"listen_ip"`
	Port            int           `config:"listen_port"`
	Version         string        `config:"snmp_version"`
	Community       string        `config:"community"`
	IsAggregate     bool          `config:"aggregate"`
	Concurrency     int           `config:"concurrency"`
	AggregatePeriod time.Duration `config:"aggregate_period"`

	// oid翻译字典
	OIDS map[string]string `config:"oids"`

	// 用户指定的需要作为维度上报的oid
	ReportOIDDimensions []string `config:"report_oid_dimensions"`
	// 用户指定的value不需要解析为字符的oid
	RawByteOIDs []string `config:"raw_byte_oids"`
	Target      string   `config:"target"`
	Label       []Label  `config:"labels"`
	// v3参数
	UsmInfos []UsmInfo `config:"usm_info"`
	// oids排序拼成string 用于hash
	Tags define.Tags

	// 翻译oid value时若是中文，则可能需要该配置
	Encode string `config:"encode"`
	// 是否将oid维度转换为翻译后的英文名再上报
	UseDisplayNameOID bool `config:"use_display_name_oid"`
	// 是否关闭agent_port维度上报，该维度可以看作是一个无意义随机值
	HideAgentPort bool `config:"hide_agent_port"`
}

// InitIdent :
func (c *TrapConfig) InitIdent() error {
	// map影响hash结果，将map排序拼成string进行hash
	oids := c.OIDS

	oidList := make([]define.Tag, 0, len(oids))
	for key, val := range oids {
		oidList = append(oidList, define.Tag{
			Key:   key,
			Value: val,
		})
	}
	c.Tags = oidList
	sort.Sort(c.Tags)

	c.OIDS = make(map[string]string, 0)
	err := c.initIdent(c)
	// 恢复oids 释放内存
	c.OIDS = oids
	c.Tags = nil
	return err
}

// Clean :
func (c *TrapConfig) Clean() error {
	err := utils.CleanCompositeParamList(&c.BaseTaskParam)
	if err != nil {
		return err
	}
	if c.Port == 0 {
		c.Port = 162
	}
	if c.Version == "" {
		c.Version = "2c"
	}
	if c.Concurrency == 0 {
		c.Concurrency = 2000
	}

	return nil
}

// GetType :
func (c *TrapConfig) GetType() string {
	return ConfigTypeTrap
}

// NewTrapConfig :
func NewTrapConfig() *TrapConfig {
	var conf TrapConfig
	conf.Timeout = define.DefaultTimeout
	return &conf
}

// TrapMetaConfig : associate task config
type TrapMetaConfig struct {
	BaseTaskMetaParam `config:"_,inline"`

	Tasks []*TrapConfig `config:"tasks"`
}

// Clean :
func (c *TrapMetaConfig) Clean() error {
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
func (c *TrapMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewTrapMetaConfig :
func NewTrapMetaConfig(root *Config) *TrapMetaConfig {
	config := &TrapMetaConfig{
		BaseTaskMetaParam: NewBaseTaskMetaParam(),
	}
	config.Tasks = make([]*TrapConfig, 0)
	root.TaskTypeMapping[ConfigTypeTrap] = config

	return config
}

// USMConfig snmp usm参数
type USMConfig struct {
	UserName                 string `config:"username"`
	AuthenticationProtocol   string `config:"authentication_protocol"`
	AuthenticationPassphrase string `config:"authentication_passphrase"`
	PrivacyProtocol          string `config:"privacy_protocol"`
	PrivacyPassphrase        string `config:"privacy_passphrase"`
	AuthoritativeEngineBoots uint32 `config:"authoritative_engineboots"`
	AuthoritativeEngineTime  uint32 `config:"authoritative_enginetime"`
	AuthoritativeEngineID    string `config:"authoritative_engineID"`
}

// usmInfo 多用户参数
type UsmInfo struct {
	MsgFlags    string    `config:"msg_flags"`
	ContextName string    `config:"context_name"`
	USMConfig   USMConfig `config:"usm_config"`
}
