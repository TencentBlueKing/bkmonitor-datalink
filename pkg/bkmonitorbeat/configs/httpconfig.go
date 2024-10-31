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
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	ConfigTypeHTTP = define.ModuleHTTP
)

// HTTPTaskStepConfig :
type HTTPTaskStepConfig struct {
	SimpleMatchParam `config:"_,inline"`

	URL              string            `config:"url"`
	URLList          []string          `config:"url_list"`
	Method           string            `config:"method"`
	Headers          map[string]string `config:"headers"`
	ResponseCode     string            `config:"response_code"`
	ResponseCodeList []int             `config:"response_code_list"`
}

func (c *HTTPTaskStepConfig) URLs() []string {
	if c == nil {
		return nil
	}

	if len(c.URLList) > 0 {
		return c.URLList
	}
	return []string{c.URL}
}

// Clean :
func (c *HTTPTaskStepConfig) Clean() error {
	err := c.SimpleMatchParam.CleanParams()
	if err != nil {
		return err
	}
	if c.Method == "" {
		c.Method = "GET"
	}
	if c.ResponseCode == "" {
		c.ResponseCode = "200"
	}
	if !strings.HasPrefix(c.URL, "http") {
		c.URL = "http://" + c.URL
	}
	// 配置URL列表时格式化列表
	if len(c.URLList) > 0 {
		for i, url := range c.URLList {
			if !strings.HasPrefix(url, "http") {
				c.URLList[i] = "http://" + url
			}
		}
		// 当配置多个URL时忽略单个URL配置
		c.URL = ""
	}
	for _, codeStr := range strings.Split(c.ResponseCode, ",") {
		code, err := strconv.Atoi(codeStr)
		if err != nil {
			logger.Debugf("drop invalid code: %v", codeStr)
			continue
		}
		c.ResponseCodeList = append(c.ResponseCodeList, code)
	}
	return nil
}

// HTTPTaskConfig :
type HTTPTaskConfig struct {
	NetTaskParam       `config:"_,inline"`
	Proxy              string                `config:"proxy"`
	InsecureSkipVerify bool                  `config:"insecure_skip_verify"`
	Steps              []*HTTPTaskStepConfig `config:"steps"`
	CustomReport       bool                  `config:"custom_report"`
}

// InitIdent :
func (c *HTTPTaskConfig) InitIdent() error {
	return c.initIdent(c)
}

// Clean :
func (c *HTTPTaskConfig) Clean() error {
	var err error
	err = utils.CleanCompositeParamList(&c.NetTaskParam)
	if err != nil {
		return err
	}
	for _, step := range c.Steps {
		err = step.Clean()
		if err != nil {
			return err
		}

	}
	return nil
}

// GetType :
func (c *HTTPTaskConfig) GetType() string {
	return ConfigTypeHTTP
}

// HTTPTaskMetaConfig :
type HTTPTaskMetaConfig struct {
	NetTaskMetaParam `config:"_,inline"`

	Tasks []*HTTPTaskConfig `config:"tasks"`
}

// Clean :
func (c *HTTPTaskMetaConfig) Clean() error {
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
func (c *HTTPTaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	count := len(c.Tasks)
	tasks := make([]define.TaskConfig, count)
	for index, task := range c.Tasks {
		tasks[index] = task
	}
	return tasks
}

// NewHTTPTaskMetaConfig :
func NewHTTPTaskMetaConfig(root *Config) *HTTPTaskMetaConfig {
	config := &HTTPTaskMetaConfig{
		NetTaskMetaParam: NewNetTaskMetaParam(),
	}
	config.Tasks = make([]*HTTPTaskConfig, 0)

	root.TaskTypeMapping[ConfigTypeHTTP] = config

	return config
}

// NewHTTPTaskConfig :
func NewHTTPTaskConfig() *HTTPTaskConfig {
	var conf HTTPTaskConfig
	conf.Timeout = define.DefaultTimeout
	conf.BufferSize = DefaultBufferSize
	conf.Steps = make([]*HTTPTaskStepConfig, 0)

	return &conf
}
