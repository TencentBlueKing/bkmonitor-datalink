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
	"regexp"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
)

type CpuConfig struct {
	// collector times in one period
	StatTimes   int           `config:"stat_times"`
	StatPeriod  time.Duration `config:",ignore"`
	InfoPeriod  time.Duration `config:"info_period"`
	InfoTimeout time.Duration `config:"info_timeout"`

	ReportCpuFlag bool `config:"report_flag"`
}

type DiskConfig struct {
	// collector times in one period
	StatTimes           int           `config:"stat_times"`
	StatPeriod          time.Duration `config:",ignore"`
	CollectAllDev       bool          `config:"collect_all_device"`
	DropDuplicateDevice bool          `config:"drop_duplicate_device"`
	IOSkipPartition     bool          `config:"io_skip_partition"`

	DiskWhiteListPattern       []string         `config:"disk_white_list"`
	DiskWhiteList              []*regexp.Regexp `config:",ignore"`
	DiskBlackListPattern       []string         `config:"disk_black_list"`
	DiskBlackList              []*regexp.Regexp `config:",ignore"`
	PartitionWhiteListPattern  []string         `config:"partition_white_list"`
	PartitionWhiteList         []*regexp.Regexp `config:",ignore"`
	PartitionBlackListPattern  []string         `config:"partition_black_list"`
	PartitionBlackList         []*regexp.Regexp `config:",ignore"`
	MountpointWhiteListPattern []string         `config:"mountpoint_white_list"`
	MountpointWhiteList        []*regexp.Regexp `config:",ignore"`
	MountpointBlackListPattern []string         `config:"mountpoint_black_list"`
	MountpointBlackList        []*regexp.Regexp `config:",ignore"`
	FSTypeBlackListPattern     []string         `config:"fs_type_black_list"`
	FSTypeBlackList            []*regexp.Regexp `config:",ignore"`
	FSTypeWhiteListPattern     []string         `config:"fs_type_white_list"`
	FSTypeWhiteList            []*regexp.Regexp `config:",ignore"`
}

type MemConfig struct {
	// collector times in one period
	InfoTimes     int           `config:"info_times"`
	InfoPeriod    time.Duration `config:",ignore"`
	SpecialSource bool          `config:"special_source"`
}

type NetConfig struct {
	// collector times in one period
	StatTimes            int           `config:"stat_times"`
	StatPeriod           time.Duration `config:",ignore"`
	SkipVirtualInterface bool          `config:"skip_virtual_interface"`
	RevertProtectNumber  uint64        `config:"revert_protect_number"`

	ForceReportList           []*regexp.Regexp `config:",ignore"`
	ForceReportListPattern    []string         `config:"force_report_list"`
	InterfaceWhiteListPattern []string         `config:"interface_white_list"`
	InterfaceWhiteList        []*regexp.Regexp `config:",ignore"`
	InterfaceBlackListPattern []string         `config:"interface_black_list"`
	InterfaceBlackList        []*regexp.Regexp `config:",ignore"`
}

// BasereportConfig
type BasereportConfig struct {
	BaseTaskParam `config:"_,inline"`
	TimeTolerate  int64 `config:"time_tolerate"`

	// module detail configs
	Cpu  CpuConfig  `config:"cpu"`
	Disk DiskConfig `config:"disk"`
	Mem  MemConfig  `config:"mem"`
	Net  NetConfig  `config:"net"`

	// 环境信息的上报开关
	ReportCrontab bool `config:"report_crontab"`
	ReportHosts   bool `config:"report_hosts"`
	ReportRoute   bool `config:"report_route"`
}

func (c *BasereportConfig) GetTaskConfigList() []define.TaskConfig {
	tasks := make([]define.TaskConfig, 0)
	// 说明没有任务 有且仅有一个任务
	if c.DataID == 0 {
		return tasks
	}

	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(define.ModuleBasereport); ok {
		c.DataID = v
	}

	tasks = append(tasks, c)
	return tasks
}

func (c *BasereportConfig) InitIdent() error { return c.initIdent(c) }

func (c *BasereportConfig) GetIdent() string { return define.ModuleBasereport }

func (c *BasereportConfig) GetType() string { return define.ModuleBasereport }

func (c *BasereportConfig) Clean() error { return nil }

var _ define.TaskConfig = &BasereportConfig{}

var DefaultBasereportConfig = BasereportConfig{
	Cpu: CpuConfig{
		StatTimes:     4,
		InfoPeriod:    1 * time.Minute,
		InfoTimeout:   30 * time.Second,
		ReportCpuFlag: false,
	},
	Disk: DiskConfig{
		StatTimes:           1,
		DiskWhiteList:       []*regexp.Regexp{},
		DiskBlackList:       []*regexp.Regexp{},
		PartitionWhiteList:  []*regexp.Regexp{},
		PartitionBlackList:  []*regexp.Regexp{},
		MountpointWhiteList: []*regexp.Regexp{},
		MountpointBlackList: []*regexp.Regexp{},
	},
	Mem: MemConfig{
		InfoTimes:     1,
		SpecialSource: false,
	},
	Net: NetConfig{
		StatTimes:           4,
		InterfaceWhiteList:  []*regexp.Regexp{},
		InterfaceBlackList:  []*regexp.Regexp{},
		RevertProtectNumber: 100,
	},
	ReportCrontab: false,
	ReportHosts:   false,
	ReportRoute:   false,
}

var FastBasereportConfig = BasereportConfig{
	Cpu: CpuConfig{
		StatTimes:     1,
		StatPeriod:    1,
		InfoPeriod:    1 * time.Minute,
		InfoTimeout:   30 * time.Second,
		ReportCpuFlag: false,
	},
	Disk: DiskConfig{
		StatTimes:           1,
		StatPeriod:          1,
		DiskWhiteList:       []*regexp.Regexp{},
		DiskBlackList:       []*regexp.Regexp{},
		PartitionWhiteList:  []*regexp.Regexp{},
		PartitionBlackList:  []*regexp.Regexp{},
		MountpointWhiteList: []*regexp.Regexp{},
		MountpointBlackList: []*regexp.Regexp{},
	},
	Mem: MemConfig{
		InfoTimes:     1,
		InfoPeriod:    1,
		SpecialSource: false,
	},
	Net: NetConfig{
		StatTimes:           1,
		StatPeriod:          1,
		InterfaceWhiteList:  []*regexp.Regexp{},
		InterfaceBlackList:  []*regexp.Regexp{},
		RevertProtectNumber: 100,
	},
	ReportCrontab: false,
	ReportHosts:   false,
	ReportRoute:   false,
}

func init() {
	DefaultBasereportConfig.DataID = 0
	DefaultBasereportConfig.Period = 1 * time.Minute
	FastBasereportConfig.Period = 5 * time.Second
}

func NewBasereportConfig(root *Config) *BasereportConfig {
	config := &BasereportConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleBasereport] = config
	return config
}
