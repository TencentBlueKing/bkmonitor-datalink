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
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withWindowsCPUTimesTestHooks(t *testing.T) {
	originalGetActiveProcessorGroupCount := getActiveProcessorGroupCount
	originalQueryLegacyProcessorPerformanceInformation := queryLegacyProcessorPerformanceInformation
	originalQueryProcessorPerformanceInformationByGroup := queryProcessorPerformanceInformationByGroup
	originalFindNtQuerySystemInformationEx := findNtQuerySystemInformationEx

	t.Cleanup(func() {
		getActiveProcessorGroupCount = originalGetActiveProcessorGroupCount
		queryLegacyProcessorPerformanceInformation = originalQueryLegacyProcessorPerformanceInformation
		queryProcessorPerformanceInformationByGroup = originalQueryProcessorPerformanceInformationByGroup
		findNtQuerySystemInformationEx = originalFindNtQuerySystemInformationEx
	})
}

func TestProcessorPerformanceInfoToTimes(t *testing.T) {
	stats := []systemProcessorPerformanceInformation{
		{
			UserTime:      int64(12 * windowsClocksPerSec),
			KernelTime:    int64(10 * windowsClocksPerSec),
			IdleTime:      int64(3 * windowsClocksPerSec),
			InterruptTime: int64(2 * windowsClocksPerSec),
		},
		{
			UserTime:      int64(20 * windowsClocksPerSec),
			KernelTime:    int64(17 * windowsClocksPerSec),
			IdleTime:      int64(5 * windowsClocksPerSec),
			InterruptTime: int64(1 * windowsClocksPerSec),
		},
	}

	perCPUTimes := processorPerformanceInfoToTimes(stats, 64)
	require.Len(t, perCPUTimes, 2)

	assert.Equal(t, "cpu64", perCPUTimes[0].CPU)
	assert.InDelta(t, 12.0, perCPUTimes[0].User, 0.0001)
	assert.InDelta(t, 7.0, perCPUTimes[0].System, 0.0001)
	assert.InDelta(t, 3.0, perCPUTimes[0].Idle, 0.0001)
	assert.InDelta(t, 2.0, perCPUTimes[0].Irq, 0.0001)

	assert.Equal(t, "cpu65", perCPUTimes[1].CPU)
	assert.InDelta(t, 20.0, perCPUTimes[1].User, 0.0001)
	assert.InDelta(t, 12.0, perCPUTimes[1].System, 0.0001)
	assert.InDelta(t, 5.0, perCPUTimes[1].Idle, 0.0001)
	assert.InDelta(t, 1.0, perCPUTimes[1].Irq, 0.0001)
}

func TestSumWindowsTotalCPUTimesPreservesHistoricalIRQSemantics(t *testing.T) {
	times := []cpu.TimesStat{
		{
			CPU:     "cpu0",
			User:    10,
			System:  5,
			Idle:    20,
			Iowait:  2,
			Irq:     3,
			Softirq: 1,
		},
		{
			CPU:     "cpu1",
			User:    6,
			System:  4,
			Idle:    10,
			Iowait:  1,
			Irq:     7,
			Softirq: 2,
		},
	}

	totalCPUTimes := sumWindowsTotalCPUTimes(times)
	assert.Equal(t, "cpu-total", totalCPUTimes.CPU)
	assert.Equal(t, 16.0, totalCPUTimes.User)
	assert.Equal(t, 9.0, totalCPUTimes.System)
	assert.Equal(t, 30.0, totalCPUTimes.Idle)
	assert.Equal(t, 3.0, totalCPUTimes.Iowait)
	assert.Equal(t, 3.0, totalCPUTimes.Softirq)
	assert.Equal(t, 0.0, totalCPUTimes.Irq)
}

func TestCalculateCPUBusyPercent(t *testing.T) {
	prev := cpu.TimesStat{
		CPU:    "cpu0",
		User:   10,
		System: 10,
		Idle:   80,
	}
	curr := cpu.TimesStat{
		CPU:    "cpu0",
		User:   30,
		System: 20,
		Idle:   100,
	}

	got := calculateCPUBusyPercent(prev, curr)
	assert.InDelta(t, 60.0, got, 0.0001)
}

func TestCalculateCPUBusyPercentBounds(t *testing.T) {
	t.Run("busy_does_not_increase", func(t *testing.T) {
		prev := cpu.TimesStat{CPU: "cpu0", User: 10, System: 10, Idle: 80}
		curr := cpu.TimesStat{CPU: "cpu0", User: 10, System: 10, Idle: 90}
		assert.Equal(t, 0.0, calculateCPUBusyPercent(prev, curr))
	})

	t.Run("total_does_not_increase_but_busy_does", func(t *testing.T) {
		prev := cpu.TimesStat{CPU: "cpu0", User: 10, System: 10, Idle: 80}
		curr := cpu.TimesStat{CPU: "cpu0", User: 20, System: 20, Idle: 60}
		assert.Equal(t, 0.0, calculateCPUBusyPercent(prev, curr))
	})
}

func TestCalculateAllCPUBusyPercent(t *testing.T) {
	prev := []cpu.TimesStat{
		{CPU: "cpu0", User: 10, System: 10, Idle: 80},
		{CPU: "cpu1", User: 20, System: 10, Idle: 70},
	}
	curr := []cpu.TimesStat{
		{CPU: "cpu0", User: 30, System: 20, Idle: 100},
		{CPU: "cpu1", User: 30, System: 20, Idle: 90},
	}

	got, err := calculateAllCPUBusyPercent(prev, curr)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.InDelta(t, 60.0, got[0], 0.0001)
	assert.InDelta(t, 50.0, got[1], 0.0001)
}

func TestCalculateAllCPUBusyPercentLengthMismatch(t *testing.T) {
	_, err := calculateAllCPUBusyPercent(
		[]cpu.TimesStat{{CPU: "cpu0"}},
		[]cpu.TimesStat{{CPU: "cpu0"}, {CPU: "cpu1"}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "received two CPU counts")
}

func TestGetWindowsCPUTimesWithMetaGroupAware(t *testing.T) {
	withWindowsCPUTimesTestHooks(t)

	getActiveProcessorGroupCount = func() (uint16, error) {
		return 2, nil
	}
	findNtQuerySystemInformationEx = func() error {
		return nil
	}
	queryProcessorPerformanceInformationByGroup = func(group uint16) ([]systemProcessorPerformanceInformation, error) {
		switch group {
		case 0:
			return []systemProcessorPerformanceInformation{
				{UserTime: int64(1 * windowsClocksPerSec), KernelTime: int64(3 * windowsClocksPerSec), IdleTime: int64(1 * windowsClocksPerSec)},
				{UserTime: int64(2 * windowsClocksPerSec), KernelTime: int64(4 * windowsClocksPerSec), IdleTime: int64(1 * windowsClocksPerSec)},
			}, nil
		case 1:
			return []systemProcessorPerformanceInformation{
				{UserTime: int64(3 * windowsClocksPerSec), KernelTime: int64(6 * windowsClocksPerSec), IdleTime: int64(2 * windowsClocksPerSec)},
			}, nil
		default:
			return nil, errors.New("unexpected group")
		}
	}

	perCPUTimes, meta, err := getWindowsCPUTimesWithMeta()
	require.NoError(t, err)
	require.Len(t, perCPUTimes, 3)
	assert.Equal(t, windowsCPUTimesModeGroupAware, meta.mode)
	assert.Equal(t, uint16(2), meta.groupCount)
	assert.Empty(t, meta.fallbackReason)
	assert.Equal(t, "cpu0", perCPUTimes[0].CPU)
	assert.Equal(t, "cpu1", perCPUTimes[1].CPU)
	assert.Equal(t, "cpu2", perCPUTimes[2].CPU)
}

func TestGetWindowsCPUTimesWithMetaFallbackWhenNtQuerySystemInformationExUnavailable(t *testing.T) {
	withWindowsCPUTimesTestHooks(t)

	getActiveProcessorGroupCount = func() (uint16, error) {
		return 2, nil
	}
	findNtQuerySystemInformationEx = func() error {
		return errors.New("not supported")
	}
	queryLegacyProcessorPerformanceInformation = func() ([]systemProcessorPerformanceInformation, error) {
		return []systemProcessorPerformanceInformation{
			{UserTime: int64(1 * windowsClocksPerSec), KernelTime: int64(2 * windowsClocksPerSec), IdleTime: int64(1 * windowsClocksPerSec)},
			{UserTime: int64(2 * windowsClocksPerSec), KernelTime: int64(3 * windowsClocksPerSec), IdleTime: int64(1 * windowsClocksPerSec)},
		}, nil
	}

	perCPUTimes, meta, err := getWindowsCPUTimesWithMeta()
	require.NoError(t, err)
	require.Len(t, perCPUTimes, 2)
	assert.Equal(t, windowsCPUTimesModeLegacy, meta.mode)
	assert.Equal(t, uint16(2), meta.groupCount)
	assert.Contains(t, meta.fallbackReason, "NtQuerySystemInformationEx unavailable")
	assert.Equal(t, "cpu0", perCPUTimes[0].CPU)
	assert.Equal(t, "cpu1", perCPUTimes[1].CPU)
}
