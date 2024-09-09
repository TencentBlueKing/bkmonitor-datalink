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
	"os"

	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/env"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Logger struct {
	Level string `yaml:"level"`
}

type Config struct {
	KubeConfig      string       `yaml:"kubeconfig"`
	PidPath         string       `yaml:"pid_path"`
	WatchPath       []string     `yaml:"watch_path"`
	ChildConfigPath string       `yaml:"child_config_path"`
	TaskType        string       `yaml:"task_type"`
	Logger          Logger       `yaml:"logger"`
	MetaEnv         env.Metadata `yaml:"meta_env"`
}

func (c *Config) setup() {
	logger.SetOptions(logger.Options{
		Stdout: true,
		Format: "logfmt",
		Level:  c.Logger.Level,
	})
	c.MetaEnv = env.Load()
}

var gConfig = &Config{}

// G 返回全局加载的 Config
func G() *Config {
	return gConfig
}

// Load 从文件中加载 Config
func Load(p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	newConfig := &Config{}
	if err := yaml.Unmarshal(b, newConfig); err != nil {
		return err
	}

	newConfig.setup()
	gConfig = newConfig
	return nil
}
