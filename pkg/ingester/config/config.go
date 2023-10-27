// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	validator "gopkg.in/validator.v2"
	"gopkg.in/yaml.v2"
)

// IConfiguration : configuration interface
type IConfiguration interface {
	Init()
}

// ConfigurationType : configuration type
type ConfigurationType struct {
	ConfigurationPath string  `yaml:"configuration_path" validate:"nonzero"`
	Http              Http    `yaml:"http"`
	Logging           Logging `yaml:"logging"`
	Consul            Consul  `yaml:"consul"`
}

// Init : init ConfigurationType
func (c *ConfigurationType) Init() {
	c.ConfigurationPath = "ingester.yaml"
	c.Http.Init()
	c.Logging.Init()
	c.Consul.Init()
}

// ReadFrom : read configuration from path
func (c *ConfigurationType) ReadFrom(path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(data, c)
	if err != nil {
		return err
	}
	return nil
}

// Read : read configuration
func (c *ConfigurationType) Read() error {
	var err error
	confPath := c.ConfigurationPath

	err = c.ReadFrom(confPath)
	if err != nil {
		return err
	}
	return nil
}

// Validate :
func (c *ConfigurationType) Validate() error {
	return validator.Validate(c)
}

// Dumps :
func (c *ConfigurationType) Dumps() (string, error) {
	data, err := yaml.Marshal(Configuration)
	return string(data), err
}

// Configuration : global configuration
var Configuration = ConfigurationType{}

func init() {
	Configuration.Init()
}

func Init() (err error) {
	err = Configuration.Read()
	if err != nil {
		return
	}
	err = Configuration.Validate()
	if err != nil {
		return
	}
	confPath, _ := filepath.Abs(Configuration.ConfigurationPath)
	fmt.Printf("Using config file: %s\n", confPath)
	return nil
}
