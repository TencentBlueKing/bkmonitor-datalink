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
)

type ProcSnapshotConfig struct {
	BaseTaskParam `config:"_,inline"`
}

func (c *ProcSnapshotConfig) GetTaskConfigList() []define.TaskConfig {
	if c.DataID == 0 {
		return []define.TaskConfig{}
	}
	return []define.TaskConfig{c}
}

func (c *ProcSnapshotConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *ProcSnapshotConfig) GetIdent() string {
	return define.ModuleProcSnapshot
}

func (c *ProcSnapshotConfig) GetType() string {
	return define.ModuleProcSnapshot
}

func (c *ProcSnapshotConfig) Clean() error {
	return nil
}

func NewProcSnapshotConfig(root *Config) *ProcSnapshotConfig {
	config := &ProcSnapshotConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcSnapshot] = config

	return config
}
