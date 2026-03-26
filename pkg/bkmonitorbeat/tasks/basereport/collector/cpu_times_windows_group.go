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
	"fmt"
	"math"
	"syscall"
	"unsafe"

	"github.com/shirou/gopsutil/v3/cpu"
	"golang.org/x/sys/windows"
)

const (
	windowsCPUTimesModeLegacy     = "legacy"
	windowsCPUTimesModeGroupAware = "group-aware"
)

type windowsCPUTimesQueryMeta struct {
	groupCount     uint16
	mode           string
	fallbackReason string
}

type systemProcessorPerformanceInformation struct {
	IdleTime       int64
	KernelTime     int64
	UserTime       int64
	DpcTime        int64
	InterruptTime  int64
	InterruptCount uint32
}

var (
	kernel32DLL                      = windows.NewLazySystemDLL("kernel32.dll")
	ntdllDLL                         = windows.NewLazySystemDLL("ntdll.dll")
	procGetActiveProcessorGroupCount = kernel32DLL.NewProc("GetActiveProcessorGroupCount")
	procNtQuerySystemInformationEx   = ntdllDLL.NewProc("NtQuerySystemInformationEx")
	findNtQuerySystemInformationEx   = func() error { return procNtQuerySystemInformationEx.Find() }
	getActiveProcessorGroupCountFn   = getActiveProcessorGroupCount
	queryLegacyProcessorTimesFn      = queryLegacyProcessorPerformanceInformation
	queryProcessorTimesByGroupFn     = queryProcessorPerformanceInformationByGroup
)

const (
	systemProcessorPerformanceInformationClass = 8
	systemProcessorPerformanceInfoSize         = uint32(unsafe.Sizeof(systemProcessorPerformanceInformation{}))
	windowsClocksPerSec                        = 10000000.0
)

func getWindowsCPUTimes() ([]cpu.TimesStat, error) {
	times, _, err := getWindowsCPUTimesWithMeta()
	return times, err
}

func getWindowsCPUTimesWithMeta() ([]cpu.TimesStat, windowsCPUTimesQueryMeta, error) {
	meta := windowsCPUTimesQueryMeta{}
	groupCount, err := getActiveProcessorGroupCountFn()
	if err != nil {
		stats, legacyErr := queryLegacyProcessorTimesFn()
		if legacyErr == nil {
			meta.mode = windowsCPUTimesModeLegacy
			meta.fallbackReason = fmt.Sprintf("GetActiveProcessorGroupCount failed: %v", err)
			return processorPerformanceInfoToTimes(stats, 0), meta, nil
		}
		return nil, meta, fmt.Errorf("get active processor group count: %w", err)
	}
	meta.groupCount = groupCount

	// Single-group machines can still use the old code path safely.
	if groupCount <= 1 {
		stats, err := queryLegacyProcessorTimesFn()
		if err != nil {
			return nil, meta, err
		}
		meta.mode = windowsCPUTimesModeLegacy
		return processorPerformanceInfoToTimes(stats, 0), meta, nil
	}

	if err := findNtQuerySystemInformationEx(); err != nil {
		stats, legacyErr := queryLegacyProcessorTimesFn()
		if legacyErr == nil {
			meta.mode = windowsCPUTimesModeLegacy
			meta.fallbackReason = fmt.Sprintf(
				"NtQuerySystemInformationEx unavailable for %d processor groups: %v",
				groupCount,
				err,
			)
			return processorPerformanceInfoToTimes(stats, 0), meta, nil
		}
		return nil, meta, fmt.Errorf(
			"NtQuerySystemInformationEx unavailable for %d processor groups: %v; legacy query failed: %w",
			groupCount,
			err,
			legacyErr,
		)
	}

	var all []cpu.TimesStat
	cpuOffset := 0
	for group := uint16(0); group < groupCount; group++ {
		stats, err := queryProcessorTimesByGroupFn(group)
		if err != nil {
			return nil, meta, fmt.Errorf("query processor performance info for group %d: %w", group, err)
		}
		all = append(all, processorPerformanceInfoToTimes(stats, cpuOffset)...)
		cpuOffset += len(stats)
	}
	meta.mode = windowsCPUTimesModeGroupAware
	return all, meta, nil
}

func sumCPUTimes(times []cpu.TimesStat) cpu.TimesStat {
	total := cpu.TimesStat{CPU: "cpu-total"}
	for _, item := range times {
		total.User += item.User
		total.System += item.System
		total.Idle += item.Idle
		total.Nice += item.Nice
		total.Iowait += item.Iowait
		total.Irq += item.Irq
		total.Softirq += item.Softirq
		total.Steal += item.Steal
		total.Guest += item.Guest
		total.GuestNice += item.GuestNice
	}
	return total
}

func sumWindowsTotalCPUTimes(times []cpu.TimesStat) cpu.TimesStat {
	total := sumCPUTimes(times)
	// Preserve the historical total_cpu semantics from GetSystemTimes so the
	// aggregated total_usage stays aligned with the previous implementation.
	// There, system time already included interrupt time, so total irq should
	// remain 0 rather than summing per-core Irq into the total view.
	total.Irq = 0
	return total
}

func calculateCPUBusyPercent(prev, curr cpu.TimesStat) float64 {
	prevTotal := prev.Total()
	currTotal := curr.Total()
	prevBusy := prevTotal - prev.Idle - prev.Iowait
	currBusy := currTotal - curr.Idle - curr.Iowait

	if currTotal <= prevTotal {
		return 0
	}
	if currBusy <= prevBusy {
		return 0
	}
	return math.Min(100, math.Max(0, (currBusy-prevBusy)/(currTotal-prevTotal)*100))
}

func calculateAllCPUBusyPercent(prev, curr []cpu.TimesStat) ([]float64, error) {
	if len(prev) != len(curr) {
		return nil, fmt.Errorf("received two CPU counts: %d != %d", len(prev), len(curr))
	}

	ret := make([]float64, len(curr))
	for i := range curr {
		ret[i] = calculateCPUBusyPercent(prev[i], curr[i])
	}
	return ret, nil
}

func processorPerformanceInfoToTimes(
	stats []systemProcessorPerformanceInformation, cpuOffset int,
) []cpu.TimesStat {
	ret := make([]cpu.TimesStat, 0, len(stats))
	for idx, item := range stats {
		ret = append(ret, cpu.TimesStat{
			CPU:    fmt.Sprintf("cpu%d", cpuOffset+idx),
			User:   float64(item.UserTime) / windowsClocksPerSec,
			System: float64(item.KernelTime-item.IdleTime) / windowsClocksPerSec,
			Idle:   float64(item.IdleTime) / windowsClocksPerSec,
			Irq:    float64(item.InterruptTime) / windowsClocksPerSec,
		})
	}
	return ret
}

func getActiveProcessorGroupCount() (uint16, error) {
	if err := procGetActiveProcessorGroupCount.Find(); err != nil {
		return 0, fmt.Errorf("GetActiveProcessorGroupCount unavailable: %w", err)
	}

	r0, _, callErr := syscall.Syscall(procGetActiveProcessorGroupCount.Addr(), 0, 0, 0, 0)
	if r0 == 0 {
		if callErr != syscall.Errno(0) {
			return 0, callErr
		}
		return 0, fmt.Errorf("GetActiveProcessorGroupCount returned 0")
	}
	return uint16(r0), nil
}

func queryLegacyProcessorPerformanceInformation() ([]systemProcessorPerformanceInformation, error) {
	count := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	if count == 0 {
		return nil, fmt.Errorf("GetActiveProcessorCount(all groups) returned 0")
	}
	return queryProcessorPerformanceInformation(nil, count, false)
}

func queryProcessorPerformanceInformationByGroup(group uint16) ([]systemProcessorPerformanceInformation, error) {
	count := windows.GetActiveProcessorCount(group)
	if count == 0 {
		return nil, fmt.Errorf("GetActiveProcessorCount(group=%d) returned 0", group)
	}
	return queryProcessorPerformanceInformation(unsafe.Pointer(&group), count, true)
}

func queryProcessorPerformanceInformation(
	groupInput unsafe.Pointer, expectedCount uint32, useEx bool,
) ([]systemProcessorPerformanceInformation, error) {
	buf := make([]systemProcessorPerformanceInformation, expectedCount)
	retLen := uint32(0)

	var err error
	if useEx {
		err = ntQuerySystemInformationEx(
			systemProcessorPerformanceInformationClass,
			groupInput,
			uint32(unsafe.Sizeof(uint16(0))),
			unsafe.Pointer(&buf[0]),
			systemProcessorPerformanceInfoSize*expectedCount,
			&retLen,
		)
	} else {
		err = windows.NtQuerySystemInformation(
			systemProcessorPerformanceInformationClass,
			unsafe.Pointer(&buf[0]),
			systemProcessorPerformanceInfoSize*expectedCount,
			&retLen,
		)
	}
	if err != nil {
		return nil, err
	}

	count := int(retLen / systemProcessorPerformanceInfoSize)
	if retLen == 0 && len(buf) > 0 {
		count = len(buf)
	}
	if count < 0 || count > len(buf) {
		return nil, fmt.Errorf("invalid processor performance info size: retLen=%d count=%d", retLen, count)
	}
	return buf[:count], nil
}

func ntQuerySystemInformationEx(
	sysInfoClass int32,
	inputBuffer unsafe.Pointer,
	inputBufferLen uint32,
	sysInfo unsafe.Pointer,
	sysInfoLen uint32,
	retLen *uint32,
) error {
	r0, _, _ := syscall.Syscall6(
		procNtQuerySystemInformationEx.Addr(),
		6,
		uintptr(sysInfoClass),
		uintptr(inputBuffer),
		uintptr(inputBufferLen),
		uintptr(sysInfo),
		uintptr(sysInfoLen),
		uintptr(unsafe.Pointer(retLen)),
	)
	if r0 != 0 {
		return windows.NTStatus(r0)
	}
	return nil
}
