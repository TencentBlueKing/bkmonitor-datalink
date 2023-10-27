// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"regexp"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

var DefaultBasereportConfig = configs.BasereportConfig{
	Cpu: configs.CpuConfig{
		StatTimes:     4,
		InfoPeriod:    1 * time.Minute,
		InfoTimeout:   30 * time.Second,
		ReportCpuFlag: false,
	},
	Disk: configs.DiskConfig{
		StatTimes:           1,
		DiskWhiteList:       []*regexp.Regexp{},
		DiskBlackList:       []*regexp.Regexp{},
		PartitionWhiteList:  []*regexp.Regexp{},
		PartitionBlackList:  []*regexp.Regexp{},
		MountpointWhiteList: []*regexp.Regexp{},
		MountpointBlackList: []*regexp.Regexp{},
	},
	Mem: configs.MemConfig{
		InfoTimes:     1,
		SpecialSource: false,
	},
	Net: configs.NetConfig{
		StatTimes:           4,
		InterfaceWhiteList:  []*regexp.Regexp{},
		InterfaceBlackList:  []*regexp.Regexp{},
		RevertProtectNumber: 100,
	},
	ReportCrontab: false,
	ReportHosts:   false,
	ReportRoute:   false,
}

func TestGetCPUStatUsageUnix(t *testing.T) {
	report := &CpuReport{}
	err := getCPUStatUsage(report)
	assert.NoError(t, err)
	assert.NotNil(t, report.Stat)
	assert.NotNil(t, report.Usage)
}

func TestQueryCpuInfoUnix(t *testing.T) {
	report := &CpuReport{}
	cfg := configs.FastBasereportConfig
	err := queryCpuInfo(report, cfg.Cpu.InfoPeriod, cfg.Cpu.InfoTimeout)
	assert.NoError(t, err)
}

func TestCalcTimeState(t *testing.T) {
	t1, err := cpu.Times(false)
	assert.NoError(t, err)
	t2, err := cpu.Times(false)
	assert.NoError(t, err)
	t1TimeState := t1[0]
	t2TimeState := t2[0]
	res := calcTimeState(t1TimeState, t2TimeState)
	assert.NotNil(t, res)
}
