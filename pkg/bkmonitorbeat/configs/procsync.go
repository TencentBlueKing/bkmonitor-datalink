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
	minProcsyncHash int32 = 1 << 29 // 哈希算法计算出的task id最小值，用以区分是否真实下发任务id
)

func EnsureProcsyncHash(i int32) int32 {
	if i <= minProcsyncHash {
		i += minProcsyncHash
	}
	return i
}

func IsProcsyncHash(i int32) bool {
	return i > minProcsyncHash
}

type ProcSyncConfig struct {
	BaseTaskParam `config:"_,inline"`
	TaskID        int32  `config:"task_id"`
	DstDir        string `config:"dst_dir"`
}

func NewProcSyncConfig(root *Config) *ProcSyncConfig {
	config := &ProcSyncConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcSync] = config
	return config
}

func (c *ProcSyncConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 如果不存在采集 dataid 则没必要生成采集配置
	if c.TaskID == 0 {
		return tasks
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *ProcSyncConfig) GetPeriod() time.Duration { return c.Period }

func (c *ProcSyncConfig) InitIdent() error { return c.initIdent(c) }

func (c *ProcSyncConfig) GetIdent() string { return define.ModuleProcSync }

func (c *ProcSyncConfig) GetType() string { return define.ModuleProcSync }

func (c *ProcSyncConfig) Clean() error { return nil }
