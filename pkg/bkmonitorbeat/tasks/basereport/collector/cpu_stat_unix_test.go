// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"math"
	"regexp"
	"runtime"
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
	oldReader := cpuTimesReader
	oldPer, oldTotal := lastCPUTimeSlice.lastPerCPUTimes, lastCPUTimeSlice.lastCPUTimes
	t.Cleanup(func() {
		cpuTimesReader = oldReader
		lastCPUTimeSlice.lastPerCPUTimes = oldPer
		lastCPUTimeSlice.lastCPUTimes = oldTotal
	})
	lastCPUTimeSlice.lastPerCPUTimes = []cpu.TimesStat{{CPU: "cpu0", User: 10, Idle: 90}}
	lastCPUTimeSlice.lastCPUTimes = []cpu.TimesStat{{CPU: "cpu-total", User: 10, Idle: 90}}
	cpuTimesReader = func(per bool) ([]cpu.TimesStat, error) {
		name := "cpu-total"
		if per {
			name = "cpu0"
		}
		return []cpu.TimesStat{{CPU: name, User: 20, Idle: 180}}, nil
	}
	report := &CpuReport{}
	assert.NoError(t, getCPUStatUsage(report))
	assert.Len(t, report.Stat, 1)
	assert.Equal(t, []float64{10}, report.Usage)
	assert.Equal(t, 10.0, report.TotalUsage)
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

func TestDiffCPUUsageValidation(t *testing.T) {
	before := cpu.TimesStat{CPU: "cpu-total", User: 100, System: 100, Idle: 10000, Iowait: 10}
	tests := []struct {
		name  string
		delta cpu.TimesStat
		valid bool
		want  float64
	}{
		{"normal", cpu.TimesStat{User: 10, Idle: 90}, true, 10},
		{"real full load", cpu.TimesStat{User: 10}, true, 100},
		{"idle only", cpu.TimesStat{Idle: 10}, true, 0},
		{"aggregate idle regression", cpu.TimesStat{User: .82, System: .38, Idle: -4175.78}, false, 0},
		{"core idle regression", cpu.TimesStat{User: 1, Idle: -4279.42}, false, 0},
		{"zero interval", cpu.TimesStat{}, false, 0},
		{"iowait regression", cpu.TimesStat{User: 1, Idle: 10, Iowait: -.01}, false, 0},
		{"busy regression", cpu.TimesStat{User: -1, Idle: 10}, false, 0},
		{"nan", cpu.TimesStat{User: math.NaN(), Idle: 10}, false, 0},
		{"infinity", cpu.TimesStat{User: math.Inf(1)}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after := before
			after.User += tt.delta.User
			after.System += tt.delta.System
			after.Idle += tt.delta.Idle
			after.Iowait += tt.delta.Iowait
			stat, usage, err := diffCPUUsage(before, after)
			if !tt.valid {
				assert.ErrorIs(t, err, errInvalidCPUSample)
				return
			}
			assert.NoError(t, err)
			assert.InDelta(t, tt.want, usage, 1e-9)
			assert.InDelta(t, tt.delta.Idle, stat.Idle, 1e-9)
		})
	}
	after := before
	after.CPU = "cpu1"
	after.User++
	_, _, err := diffCPUUsage(before, after)
	assert.ErrorIs(t, err, errInvalidCPUSample)
	if runtime.GOOS == "linux" {
		after = before
		after.User += 10
		after.Idle += 90
		after.Guest += 5
		_, usage, err := diffCPUUsage(before, after)
		assert.NoError(t, err)
		assert.InDelta(t, 10, usage, 1e-9)
	}
}

func TestCPUSnapshotRecoveryAndTopology(t *testing.T) {
	oldReader := cpuTimesReader
	oldPer, oldTotal := lastCPUTimeSlice.lastPerCPUTimes, lastCPUTimeSlice.lastCPUTimes
	t.Cleanup(func() {
		cpuTimesReader = oldReader
		lastCPUTimeSlice.lastPerCPUTimes = oldPer
		lastCPUTimeSlice.lastCPUTimes = oldTotal
	})
	core := func(name string, user, idle float64) cpu.TimesStat {
		return cpu.TimesStat{CPU: name, User: user, Idle: idle}
	}
	total := []cpu.TimesStat{core("cpu-total", 100, 10000)}
	per := []cpu.TimesStat{core("cpu0", 50, 5000), core("cpu1", 50, 5000)}
	lastCPUTimeSlice.lastPerCPUTimes = per
	lastCPUTimeSlice.lastCPUTimes = total
	cpuTimesReader = func(p bool) ([]cpu.TimesStat, error) {
		if p {
			return per, nil
		}
		return total, nil
	}
	// 整机累计计数回退时丢弃本次样本，但将当前快照作为下一次基线。
	total = []cpu.TimesStat{core("cpu-total", 101, 5000)}
	var report CpuReport
	assert.ErrorIs(t, getCPUStatUsage(&report), errInvalidCPUSample)
	total = []cpu.TimesStat{core("cpu-total", 102, 5099)}
	per = []cpu.TimesStat{core("cpu1", 51, 5099), core("cpu0", 51, 5099)}
	assert.NoError(t, getCPUStatUsage(&report))
	assert.InDelta(t, 1, report.TotalUsage, 1e-9)
	assert.Equal(t, "cpu1", report.Stat[0].CPU)
	assert.InDelta(t, 1, report.Usage[0], 1e-9)
	// 核心数量相同但名称集合变化时必须重置基线，不能按下标直接相减。
	per = []cpu.TimesStat{core("cpu1", 52, 5198), core("cpu2", 1, 1)}
	assert.ErrorIs(t, getCPUStatUsage(&report), errInvalidCPUSample)
	per = []cpu.TimesStat{core("cpu1", 53, 5297)}
	assert.ErrorIs(t, getCPUStatUsage(&report), errInvalidCPUSample)
	// 异常核心不得影响已校验通过的整机数据及其他健康核心。
	lastCPUTimeSlice.lastPerCPUTimes = []cpu.TimesStat{core("cpu0", 1, 100), core("cpu1", 1, 100)}
	per = []cpu.TimesStat{core("cpu0", 2, 10), core("cpu1", 2, 199)}
	total = []cpu.TimesStat{core("cpu-total", 103, 5198)}
	assert.NoError(t, getCPUStatUsage(&report))
	assert.Len(t, report.Stat, 1)
	assert.Len(t, report.Usage, 1)
	assert.Equal(t, "cpu1", report.Stat[0].CPU)
	total = nil
	assert.ErrorIs(t, getCPUStatUsage(&report), errInvalidCPUSample)
}

func TestCPUSelectionSkipsInvalidSamples(t *testing.T) {
	for _, allInvalid := range []bool{false, true} {
		calls := 0
		cfg := configs.CpuConfig{StatTimes: 4, StatPeriod: time.Millisecond}
		report, err := getCPUInfoWithSampler(cfg, func(r *CpuReport) error {
			values := []float64{10, 100, 20, 15}
			r.TotalUsage = values[calls]
			calls++
			if allInvalid || calls == 2 {
				return errInvalidCPUSample
			}
			return nil
		})
		assert.Equal(t, 4, calls)
		if allInvalid {
			assert.ErrorIs(t, err, errInvalidCPUSample)
			assert.Nil(t, report)
		} else {
			assert.NoError(t, err)
			assert.Equal(t, 20.0, report.TotalUsage)
		}
	}
}
