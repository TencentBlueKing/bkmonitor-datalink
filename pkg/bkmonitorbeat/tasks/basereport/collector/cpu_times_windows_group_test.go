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
	"testing"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	got := processorPerformanceInfoToTimes(stats, 64)
	require.Len(t, got, 2)

	assert.Equal(t, "cpu64", got[0].CPU)
	assert.InDelta(t, 12.0, got[0].User, 0.0001)
	assert.InDelta(t, 7.0, got[0].System, 0.0001)
	assert.InDelta(t, 3.0, got[0].Idle, 0.0001)
	assert.InDelta(t, 2.0, got[0].Irq, 0.0001)

	assert.Equal(t, "cpu65", got[1].CPU)
	assert.InDelta(t, 20.0, got[1].User, 0.0001)
	assert.InDelta(t, 12.0, got[1].System, 0.0001)
	assert.InDelta(t, 5.0, got[1].Idle, 0.0001)
	assert.InDelta(t, 1.0, got[1].Irq, 0.0001)
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

	got := sumWindowsTotalCPUTimes(times)
	assert.Equal(t, "cpu-total", got.CPU)
	assert.Equal(t, 16.0, got.User)
	assert.Equal(t, 9.0, got.System)
	assert.Equal(t, 30.0, got.Idle)
	assert.Equal(t, 3.0, got.Iowait)
	assert.Equal(t, 3.0, got.Softirq)
	assert.Equal(t, 0.0, got.Irq)
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
		assert.Equal(t, 100.0, calculateCPUBusyPercent(prev, curr))
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
