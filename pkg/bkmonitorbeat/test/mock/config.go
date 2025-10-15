// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// TaskConfig :
type TaskConfig struct {
	configs.NetTaskParam
	BizID             int32         `config:"bk_biz_id" validate:"required"`
	TaskID            int32         `config:"task_id" validate:"required"`
	AvailableDuration time.Duration `config:"available_duration"`
	Timeout           time.Duration `config:"timeout" validate:"min=1s"`
	Period            time.Duration `config:"period" validate:"min=1s"`
}

// InitIdent :
func (c *TaskConfig) InitIdent() error {
	c.Ident = "test"
	return nil
}

// GetTimeout :
func (c *TaskConfig) GetTimeout() time.Duration {
	return c.Timeout
}

// GetAvailableDuration :
func (c *TaskConfig) GetAvailableDuration() time.Duration {
	return c.AvailableDuration
}

// GetPeriod :
func (c *TaskConfig) GetPeriod() time.Duration {
	return c.Period
}

// GetType :
func (c *TaskConfig) GetType() string {
	return "test"
}

// GetBizID :
func (c *TaskConfig) GetBizID() int32 {
	return c.BizID
}

// GetTaskID :
func (c *TaskConfig) GetTaskID() int32 {
	return c.TaskID
}

// Clean :
func (c *TaskConfig) Clean() error {
	c.Ident = "test"
	c.TaskID = 1
	c.Timeout = time.Minute
	c.Period = time.Minute
	return nil
}

// TaskMetaConfig :
type TaskMetaConfig struct {
	Task *TaskConfig `inject:""`
}

// GetTaskConfigList :
func (c *TaskMetaConfig) GetTaskConfigList() []define.TaskConfig {
	return []define.TaskConfig{c.Task}
}

// Clean :
func (c *TaskMetaConfig) Clean() error {
	return c.Task.Clean()
}

// Config :
type Config struct {
	Task TaskMetaConfig `inject:"inline"`
}

func (c *Config) GetGatherUpDataID() int32 {
	return 0
}

// GetTaskConfigListByType :
func (c *Config) GetTaskConfigListByType(_ string) []define.TaskConfig {
	return c.Task.GetTaskConfigList()
}

// Clean :
func (c *Config) Clean() error {
	return c.Task.Clean()
}
