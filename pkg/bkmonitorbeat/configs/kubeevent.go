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
)

const (
	ConfigTypeKubeevent = "kubeevent"
)

type KubeEventConfig struct {
	BaseTaskParam `config:"_,inline"`

	Interval        time.Duration `config:"interval"`
	TailFiles       []string      `config:"tail_files"`
	UpMetricsDataID int32         `config:"upmetrics_dataid"` // 自监控 dataid
}

func (c *KubeEventConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 说明没有任务
	if len(c.TailFiles) == 0 {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *KubeEventConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *KubeEventConfig) GetType() string {
	return define.ModuleKubeevent
}

func (c *KubeEventConfig) Clean() error {
	return nil
}

func NewKubeEventConfig(root *Config) *KubeEventConfig {
	config := &KubeEventConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleKubeevent] = config

	return config
}
