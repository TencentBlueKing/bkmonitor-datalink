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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
)

const (
	_ = 1 << iota
	DiskRO
	DiskSpace
	Core
	OOM
)

type ExceptionBeatConfig struct {
	BaseTaskParam `config:"_,inline"`

	CheckBit               int           `config:",ignore"`
	CheckMethod            string        `config:"check_bit"`
	CheckDisRoInterval     time.Duration `config:"check_disk_ro_interval"`
	CheckDiskSpaceInterval time.Duration `config:"check_disk_space_interval"`
	CheckOutOfMemInterval  time.Duration `config:"check_oom_interval"`
	OutOfMemReportGap      time.Duration `config:"oom_report_gap"`
	DiskUsagePercent       int           `config:"used_max_disk_space_percent"`
	DiskMinFreeSpace       int           `config:"free_min_disk_space"`
	DiskRoWhiteList        []string      `config:"disk_ro_white_list"`
	DiskRoBlackList        []string      `config:"disk_ro_black_list"`
	CoreFileReportGap      time.Duration `config:"corefile_report_gap"`
	CoreFilePattern        string        `config:"corefile_pattern"`
	CoreFileMatchRegex     string        `config:"corefile_match_regex"`
}

var DefaultExceptionBeatConfig = ExceptionBeatConfig{
	CheckBit:               0,
	CheckMethod:            "",
	CheckDisRoInterval:     time.Hour,
	CheckDiskSpaceInterval: time.Hour,
	CheckOutOfMemInterval:  time.Hour,
	OutOfMemReportGap:      time.Minute, // 默认同一个维度的OOM信息，需要相隔1分钟后才会上报
	DiskUsagePercent:       90,
	DiskMinFreeSpace:       10,
	CoreFileReportGap:      time.Minute, // 默认同一个维度的corefile信息，需要相隔1分钟后才会上报
	CoreFilePattern:        "",
}

func (c *ExceptionBeatConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 说明没有任务 有且仅有一个任务
	if c.DataID == 0 {
		return tasks
	}

	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(define.ModuleExceptionbeat); ok {
		c.DataID = v
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *ExceptionBeatConfig) InitIdent() error {
	return c.initIdent(c)
}

func (c *ExceptionBeatConfig) GetIdent() string {
	return define.ModuleExceptionbeat
}

func (c *ExceptionBeatConfig) GetType() string {
	return define.ModuleExceptionbeat
}

func (c *ExceptionBeatConfig) Clean() error {
	return nil
}

func NewExceptionBeatConfig(root *Config) *ExceptionBeatConfig {
	config := &ExceptionBeatConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleExceptionbeat] = config

	return config
}
