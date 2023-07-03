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

type ProcConfig struct {
	BaseTaskParam `config:"_,inline" yaml:"-"`
	TaskID        int32         `config:"task_id" yaml:"task_id"`
	Period        time.Duration `config:"period" yaml:"period"`
	PortDataId    int32         `config:"portdataid" yaml:"portdataid"`
	TopDataId     int32         `config:"topdataid" yaml:"topdataid"`
	PerfDataId    int32         `config:"perfdataid" yaml:"perfdataid"`
	ConvergePID   bool          `config:"converge_pid" yaml:"converge_pid"`
	HostFilePath  string        `config:"hostfilepath" yaml:"hostfilepath"`
	DstDir        string        `config:"dst_dir" yaml:"dst_dir"`
}

func NewProcConf(root *Config) *ProcConfig {
	config := &ProcConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcConf] = config
	return config
}

func (c *ProcConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 如果不存在采集 dataid 则没必要生成采集配置
	if c.PortDataId == 0 && c.TopDataId == 0 && c.PerfDataId == 0 {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *ProcConfig) GetPeriod() time.Duration { return c.Period }

func (c *ProcConfig) InitIdent() error { return c.initIdent(c) }

func (c *ProcConfig) GetIdent() string { return define.ModuleProcConf }

func (c *ProcConfig) GetType() string { return define.ModuleProcConf }

func (c *ProcConfig) Clean() error { return nil }
