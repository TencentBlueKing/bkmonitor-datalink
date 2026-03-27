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
	"fmt"
	"math"
	"unsafe"

	"github.com/shirou/gopsutil/v3/cpu"
	"golang.org/x/sys/windows"
)

const (
	windowsCPUTimesModeLegacy     = "legacy"
	windowsCPUTimesModeGroupAware = "group-aware"
)

// windowsCPUTimesQueryMeta 描述一次 Windows CPU 时间查询的附加元数据。
type windowsCPUTimesQueryMeta struct {
	// groupCount 表示当前机器检测到的 processor group 数量。
	groupCount uint16
	// mode 表示本次查询最终采用的实现模式，如 legacy 或 group-aware。
	mode string
	// fallbackReason 记录从 group-aware 路径回退到 legacy 路径时的原因。
	fallbackReason string
}

// systemProcessorPerformanceInformation 对应 Windows 底层返回的
// `SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION` 结构。
type systemProcessorPerformanceInformation struct {
	// IdleTime 表示 CPU 处于空闲状态的累计时间。
	IdleTime int64
	// KernelTime 表示 CPU 处于内核态的累计时间，包含空闲时间。
	KernelTime int64
	// UserTime 表示 CPU 处于用户态的累计时间。
	UserTime int64
	// DpcTime 表示 CPU 用于处理延迟过程调用（DPC）的累计时间。
	DpcTime int64
	// InterruptTime 表示 CPU 用于处理中断的累计时间。
	InterruptTime int64
	// InterruptCount 表示 CPU 处理中断的累计次数。
	InterruptCount uint32
}

var (
	kernel32DLL                      = windows.NewLazySystemDLL("kernel32.dll")
	ntdllDLL                         = windows.NewLazySystemDLL("ntdll.dll")
	procGetActiveProcessorGroupCount = kernel32DLL.NewProc("GetActiveProcessorGroupCount")
	procNtQuerySystemInformationEx   = ntdllDLL.NewProc("NtQuerySystemInformationEx")

	// 将这些能力保留为包级函数变量，方便测试时直接打桩 Windows API
	// 的可用性和查询结果，而不需要仅为这个文件额外引入接口。
	// findNtQuerySystemInformationEx 用于探测扩展查询接口是否可用，
	// 这里的 `Ex` 是 extended 的缩写，表示扩展版本。
	findNtQuerySystemInformationEx = func() error { return procNtQuerySystemInformationEx.Find() }
	// getActiveProcessorGroupCount 用于获取当前系统的 processor group 数量。
	getActiveProcessorGroupCount = func() (uint16, error) {
		if err := procGetActiveProcessorGroupCount.Find(); err != nil {
			return 0, fmt.Errorf("GetActiveProcessorGroupCount unavailable: %w", err)
		}

		r0, _, callErr := procGetActiveProcessorGroupCount.Call()
		if r0 == 0 {
			var errno windows.Errno
			if !errors.As(callErr, &errno) {
				return 0, callErr
			}
			if errno != 0 {
				return 0, errno
			}
			// 正常情况下至少会有 1 个 processor group。
			// 返回 0 但又没有明确错误时，按异常结果处理。
			return 0, fmt.Errorf("GetActiveProcessorGroupCount returned 0")
		}
		return uint16(r0), nil
	}
	// queryLegacyProcessorPerformanceInformation 走旧版查询路径，
	// 返回所有 processor group 展平后的 CPU 性能信息。
	queryLegacyProcessorPerformanceInformation = func() ([]systemProcessorPerformanceInformation, error) {
		count := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
		if count == 0 {
			return nil, fmt.Errorf("GetActiveProcessorCount(all groups) returned 0")
		}
		return queryProcessorPerformanceInformation(nil, count, false)
	}
	// queryProcessorPerformanceInformationByGroup 查询单个 processor group 的 CPU 性能信息。
	//
	// 参数：
	// - group：要查询的 processor group 编号。
	queryProcessorPerformanceInformationByGroup = func(group uint16) ([]systemProcessorPerformanceInformation, error) {
		count := windows.GetActiveProcessorCount(group)
		if count == 0 {
			return nil, fmt.Errorf("GetActiveProcessorCount(group=%d) returned 0", group)
		}
		return queryProcessorPerformanceInformation(unsafe.Pointer(&group), count, true)
	}
)

const (
	systemProcessorPerformanceInformationClass = 8
	systemProcessorPerformanceInfoSize         = uint32(unsafe.Sizeof(systemProcessorPerformanceInformation{}))
	windowsClocksPerSec                        = 10000000.0
)

// getWindowsCPUTimesWithMeta 查询 Windows CPU 时间信息，并返回本次查询使用的元数据。
//
// 返回值：
// - []cpu.TimesStat：按 cpuN 编号展开后的 CPU 时间数据。
// - windowsCPUTimesQueryMeta：查询模式、group 数量和 fallback 原因等元信息。
// - error：查询过程中遇到的错误。
func getWindowsCPUTimesWithMeta() ([]cpu.TimesStat, windowsCPUTimesQueryMeta, error) {
	meta := windowsCPUTimesQueryMeta{}
	groupCount, err := getActiveProcessorGroupCount()
	if err != nil {
		// 当获取 processor group 数量的 API 不可用时，回退到 legacy 查询路径。
		// 这样可以继续兼容较老的 Windows 版本，只是无法走按 group 感知的查询流程。
		perCPUTimes, meta, legacyErr := getLegacyWindowsCPUTimes(
			meta,
			fmt.Sprintf("GetActiveProcessorGroupCount failed: %v", err),
		)
		if legacyErr == nil {
			return perCPUTimes, meta, nil
		}
		return nil, meta, fmt.Errorf("get active processor group count: %w", err)
	}
	meta.groupCount = groupCount

	// 单 group 机器不需要使用 NtQuerySystemInformationEx。
	// 这里的 `Ex` 是 extended 的缩写，表示扩展版本。
	// 这里复用 legacy 路径，可以保持实现更简单，也与历史行为一致。
	if groupCount <= 1 {
		return getLegacyWindowsCPUTimes(meta, "")
	}

	if err := findNtQuerySystemInformationEx(); err != nil {
		// 多 group 机器需要依赖 NtQuerySystemInformationEx（`Ex` 即 extended，
		// 表示扩展版本）分别查询每个 processor group。扩展 API 不可用时，
		// 退回 legacy 路径，尽量继续采集数据。
		perCPUTimes, meta, legacyErr := getLegacyWindowsCPUTimes(
			meta,
			fmt.Sprintf("NtQuerySystemInformationEx unavailable for %d processor groups: %v", groupCount, err),
		)
		if legacyErr == nil {
			return perCPUTimes, meta, nil
		}
		return nil, meta, fmt.Errorf(
			"NtQuerySystemInformationEx unavailable for %d processor groups: %v; legacy query failed: %w",
			groupCount,
			err,
			legacyErr,
		)
	}

	var perCPUTimes []cpu.TimesStat
	cpuOffset := 0
	for group := uint16(0); group < groupCount; group++ {
		// CPU 编号需要跨多个 processor group 展平成连续的 cpuN，
		// 所以这里要维护一个累加偏移量。
		processorPerformanceInfos, err := queryProcessorPerformanceInformationByGroup(group)
		if err != nil {
			return nil, meta, fmt.Errorf("query processor performance info for group %d: %w", group, err)
		}
		perCPUTimes = append(perCPUTimes, processorPerformanceInfoToTimes(processorPerformanceInfos, cpuOffset)...)
		cpuOffset += len(processorPerformanceInfos)
	}
	meta.mode = windowsCPUTimesModeGroupAware
	return perCPUTimes, meta, nil
}

// getLegacyWindowsCPUTimes 走 legacy 查询路径，并统一填充返回元数据。
//
// 参数：
// - meta：已有的查询元数据，会在返回前补齐 mode 等字段。
// - fallbackReason：当本次是从新路径回退而来时，记录回退原因；正常单 group 场景可为空。
//
// 返回值：
// - []cpu.TimesStat：legacy 路径返回的 CPU 时间数据。
// - windowsCPUTimesQueryMeta：补齐后的查询元数据。
// - error：legacy 查询失败时返回错误。
func getLegacyWindowsCPUTimes(
	meta windowsCPUTimesQueryMeta, fallbackReason string,
) ([]cpu.TimesStat, windowsCPUTimesQueryMeta, error) {
	// 将 legacy 转换路径集中到这里，确保单 group 处理和所有兼容性
	// fallback 的行为保持一致。
	processorPerformanceInfos, err := queryLegacyProcessorPerformanceInformation()
	if err != nil {
		return nil, meta, err
	}
	meta.mode = windowsCPUTimesModeLegacy
	meta.fallbackReason = fallbackReason
	return processorPerformanceInfoToTimes(processorPerformanceInfos, 0), meta, nil
}

// sumCPUTimes 将多核 CPU 时间逐项累加为一个总时间视图。
//
// 参数：
// - times：每个 CPU 的时间统计列表。
//
// 返回值：
// - cpu.TimesStat：汇总后的 CPU 时间结果。
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

// sumWindowsTotalCPUTimes 计算 Windows 语义下的总 CPU 时间。
//
// 参数：
// - times：每个 CPU 的时间统计列表。
//
// 返回值：
// - cpu.TimesStat：按历史 `GetSystemTimes` 语义修正后的总 CPU 时间结果。
func sumWindowsTotalCPUTimes(times []cpu.TimesStat) cpu.TimesStat {
	total := sumCPUTimes(times)
	// 保留 GetSystemTimes 历史上的 total_cpu 语义，确保聚合后的
	// total_usage 与旧实现保持一致。
	// 在旧逻辑里，system time 已经包含 interrupt time，因此 total 的
	// irq 应保持为 0，而不是把每个核心的 Irq 再累加到总视图里。
	total.Irq = 0
	return total
}

// totalCPUTimes 计算单个 CPU 样本的总时间。
//
// 参数：
// - item：单个 CPU 的时间统计。
//
// 返回值：
// - float64：该样本的总时间。
func totalCPUTimes(item cpu.TimesStat) float64 {
	return item.User + item.System + item.Idle + item.Nice + item.Iowait +
		item.Irq + item.Softirq + item.Steal + item.Guest + item.GuestNice
}

// calculateCPUBusyPercent 根据前后两次采样结果计算单个 CPU 的忙碌百分比。
//
// 参数：
// - prev：上一次采样的 CPU 时间。
// - curr：当前采样的 CPU 时间。
//
// 返回值：
// - float64：范围在 0 到 100 之间的忙碌百分比。
func calculateCPUBusyPercent(prev, curr cpu.TimesStat) float64 {
	prevTotal := totalCPUTimes(prev)
	currTotal := totalCPUTimes(curr)
	prevBusy := prevTotal - prev.Idle - prev.Iowait
	currBusy := currTotal - curr.Idle - curr.Iowait

	// 总时间没有前进，或忙碌时间没有增加时，都视为 0，避免出现负值
	// 和无意义的抖动结果。
	if currTotal <= prevTotal {
		return 0
	}
	if currBusy <= prevBusy {
		return 0
	}
	return math.Min(100, math.Max(0, (currBusy-prevBusy)/(currTotal-prevTotal)*100))
}

// calculateAllCPUBusyPercent 批量计算所有 CPU 的忙碌百分比。
//
// 参数：
// - prev：上一次采样得到的 CPU 时间列表。
// - curr：当前采样得到的 CPU 时间列表。
//
// 返回值：
// - []float64：与 `curr` 一一对应的忙碌百分比结果。
// - error：当两次采样的 CPU 数量不一致时返回错误。
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

// processorPerformanceInfoToTimes 将底层性能信息转换为 `cpu.TimesStat`。
//
// 参数：
// - stats：底层返回的 CPU 性能信息列表。
// - cpuOffset：当前这批 CPU 在全局 cpuN 编号中的起始偏移量。
//
// 返回值：
// - []cpu.TimesStat：转换后的 CPU 时间数据。
func processorPerformanceInfoToTimes(
	stats []systemProcessorPerformanceInformation, cpuOffset int,
) []cpu.TimesStat {
	ret := make([]cpu.TimesStat, 0, len(stats))
	for idx, item := range stats {
		ret = append(ret, cpu.TimesStat{
			CPU:  fmt.Sprintf("cpu%d", cpuOffset+idx),
			User: float64(item.UserTime) / windowsClocksPerSec,
			// Windows 的 KernelTime 含 IdleTime，这里需要减掉空闲时间，
			// 才能得到与 `cpu.TimesStat.System` 对齐的系统忙碌时间。
			System: float64(item.KernelTime-item.IdleTime) / windowsClocksPerSec,
			Idle:   float64(item.IdleTime) / windowsClocksPerSec,
			Irq:    float64(item.InterruptTime) / windowsClocksPerSec,
		})
	}
	return ret
}

// queryProcessorPerformanceInformation 查询底层 CPU 性能信息。
// 根据 `useEx` 决定走 `NtQuerySystemInformation` 还是
// `NtQuerySystemInformationEx`。这里的 `Ex` 是 extended 的缩写，表示
// 带扩展输入参数的版本，可用于按 processor group 查询。
//
// 参数：
// - groupInput：扩展查询时传入的 processor group；legacy 模式下传 nil。
// - expectedCount：预期返回的 CPU 条目数，用于分配接收缓冲区。
// - useEx：是否使用 `NtQuerySystemInformationEx` 执行查询。
//
// 返回值：
// - []systemProcessorPerformanceInformation：底层返回并裁剪后的 CPU 性能信息。
// - error：系统调用失败或返回长度异常时返回错误。
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
		// 某些情况下返回长度可能没有被写回，此时退回到调用前已知的
		// expectedCount，按整块缓冲区解释结果。
		count = len(buf)
	}
	if count < 0 || count > len(buf) {
		return nil, fmt.Errorf("invalid processor performance info size: retLen=%d count=%d", retLen, count)
	}
	return buf[:count], nil
}

// ntQuerySystemInformationEx 是对 `NtQuerySystemInformationEx` 的轻量封装。
// 这里的 `Ex` 是 extended 的缩写，表示带额外输入参数的扩展版本。
//
// 参数：
// - sysInfoClass：要查询的系统信息类别。
// - inputBuffer：传给扩展查询接口的输入缓冲区地址。
// - inputBufferLen：输入缓冲区长度。
// - sysInfo：接收系统信息的输出缓冲区地址。
// - sysInfoLen：输出缓冲区长度。
// - retLen：实际返回字节数，会由系统调用写回。
//
// 返回值：
// - error：当底层 NTSTATUS 非 0 时返回对应错误。
func ntQuerySystemInformationEx(
	sysInfoClass int32,
	inputBuffer unsafe.Pointer,
	inputBufferLen uint32,
	sysInfo unsafe.Pointer,
	sysInfoLen uint32,
	retLen *uint32,
) error {
	r0, _, _ := procNtQuerySystemInformationEx.Call(
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
