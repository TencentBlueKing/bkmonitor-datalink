// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

var DefaultBasereportConfigWin = configs.BasereportConfig{
	Cpu: configs.CpuConfig{
		StatTimes:     4,
		InfoPeriod:    1 * time.Minute,
		InfoTimeout:   30 * time.Second,
		ReportCpuFlag: false,
	},
	ReportCrontab: false,
	ReportHosts:   false,
	ReportRoute:   false,
}

func TestGetCPUStatUsageWin(t *testing.T) {
	report := &CpuReport{}
	for i := 0; i <= 4; i++ {
		err := getCPUStatUsage(report)
		t.Log(report.TotalStat.Idle)
		t.Log(report.TotalStat.System)
		t.Log(report.TotalStat.User)
		assert.NoError(t, err)
		assert.NotNil(t, report.Stat)
		assert.NotNil(t, report.Usage)
		time.Sleep(1 * time.Second)
	}
}

func TestQueryCpuInfoWin(t *testing.T) {
	report := &CpuReport{}
	cfg := configs.FastBasereportConfig
	err := queryCpuInfo(report, cfg.Cpu.InfoPeriod, cfg.Cpu.InfoTimeout)
	assert.NoError(t, err)
}

func TestGetCPUStatUsageWinResetsReusableReport(t *testing.T) {
	report := &CpuReport{
		Stat:  []cpu.TimesStat{{CPU: "stale"}},
		Usage: []float64{1, 2, 3},
	}

	err := getCPUStatUsage(report)
	require.NoError(t, err)
	require.NotEmpty(t, report.Stat)
	require.NotEmpty(t, report.Usage)
	assert.NotEqual(t, "stale", report.Stat[0].CPU)
	assert.NotContains(t, report.Usage, float64(3))
}

func TestQueryCpuInfoWinReturnsClonedCache(t *testing.T) {
	originalCPUInfoWithContext := cpuInfoWithContext
	originalMinPeriod := minPeriod
	originalUpdaterRunning := cpuInfoUpdaterRunning.Load()
	originalCachedCPUInfo := cloneCPUInfo(cpuInfo)

	updateLock.Lock()
	cpuInfo = nil
	updateLock.Unlock()
	cpuInfoUpdaterRunning.Store(false)
	minPeriod = time.Millisecond
	cpuInfoWithContext = func(ctx context.Context) ([]cpu.InfoStat, error) {
		return []cpu.InfoStat{{ModelName: "cached-model", Flags: []string{"f1", "f2"}}}, nil
	}

	t.Cleanup(func() {
		cpuInfoWithContext = originalCPUInfoWithContext
		minPeriod = originalMinPeriod
		cpuInfoUpdaterRunning.Store(originalUpdaterRunning)
		updateLock.Lock()
		cpuInfo = originalCachedCPUInfo
		updateLock.Unlock()
	})

	report := &CpuReport{}
	err := queryCpuInfo(report, time.Millisecond, time.Second)
	require.NoError(t, err)
	require.Len(t, report.Cpuinfo, 1)
	require.Equal(t, "cached-model", report.Cpuinfo[0].ModelName)
	require.Equal(t, []string{"f1", "f2"}, report.Cpuinfo[0].Flags)

	report.Cpuinfo[0].ModelName = "mutated"
	report.Cpuinfo[0].Flags[0] = "changed"

	updateLock.RLock()
	cached := cloneCPUInfo(cpuInfo)
	updateLock.RUnlock()
	require.Len(t, cached, 1)
	assert.Equal(t, "cached-model", cached[0].ModelName)
	assert.Equal(t, []string{"f1", "f2"}, cached[0].Flags)
	assert.NotSame(t, cached[0].Flags, report.Cpuinfo[0].Flags)
}

func TestQueryCpuInfoWinReturnsSyncErrorWhenCacheEmpty(t *testing.T) {
	originalCPUInfoWithContext := cpuInfoWithContext
	originalUpdaterRunning := cpuInfoUpdaterRunning.Load()
	originalCachedCPUInfo := cloneCPUInfo(cpuInfo)
	expectedErr := context.DeadlineExceeded

	updateLock.Lock()
	cpuInfo = nil
	updateLock.Unlock()
	cpuInfoUpdaterRunning.Store(true)
	cpuInfoWithContext = func(ctx context.Context) ([]cpu.InfoStat, error) {
		return nil, expectedErr
	}

	t.Cleanup(func() {
		cpuInfoWithContext = originalCPUInfoWithContext
		cpuInfoUpdaterRunning.Store(originalUpdaterRunning)
		updateLock.Lock()
		cpuInfo = originalCachedCPUInfo
		updateLock.Unlock()
	})

	report := &CpuReport{}
	err := queryCpuInfo(report, time.Millisecond, time.Millisecond)
	require.ErrorIs(t, err, expectedErr)
	assert.Nil(t, report.Cpuinfo)
}
