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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// cpuInfo 缓存最近一次成功查询到的 CPU 基础信息。
var cpuInfo = make([]cpu.InfoStat, 0)

// cpuInfoWithContext 保留为包级函数变量，方便测试时替换查询实现。
var cpuInfoWithContext = cpu.InfoWithContext

// cpuInfoUpdaterRunning 标记后台 CPU 信息刷新协程是否正在运行。
var cpuInfoUpdaterRunning atomic.Bool

// updateLock 用于保护 `cpuInfo` 的并发读写。
var updateLock sync.RWMutex

// minPeriod 限制 CPU 信息后台刷新任务的最小执行周期。
var minPeriod = 1 * time.Minute

// lastTimeSlice 缓存上一次 CPU 时间采样结果，用于计算本次增量。
type lastTimeSlice struct {
	sync.Mutex
	// lastPerCPUTimes 保存上一次逐核 CPU 时间采样结果。
	lastPerCPUTimes []cpu.TimesStat
}

// lastCPUTimeSlice 保存 Windows CPU 使用率计算所需的基线时间片。
var lastCPUTimeSlice lastTimeSlice

// logWindowsCPUTimesQuery 记录 Windows CPU 时间查询路径及回退信息。
//
// 参数：
// - stage：当前日志对应的阶段，如 init、collect 或 reset_baseline。
// - meta：本次查询返回的元数据，包含模式、group 数量和回退原因。
// - cpuCount：本次查询得到的 CPU 数量。
func logWindowsCPUTimesQuery(stage string, meta windowsCPUTimesQueryMeta, cpuCount int) {
	if meta.fallbackReason != "" {
		logger.Warnw(
			"windows cpu times fallback to legacy query",
			"stage", stage,
			"group_count", meta.groupCount,
			"cpu_count", cpuCount,
			"mode", meta.mode,
			"used_group_query", false,
			"fallback_reason", meta.fallbackReason,
		)
		return
	}

	logger.Debugw(
		"windows cpu times query",
		"stage", stage,
		"group_count", meta.groupCount,
		"cpu_count", cpuCount,
		"mode", meta.mode,
		"used_group_query", meta.mode == windowsCPUTimesModeGroupAware,
	)
}

// init 初始化 Windows CPU 采样基线，供后续使用率计算复用。
func init() {
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()

	perCPUTimes, meta, err := getWindowsCPUTimesWithMeta()
	if err != nil {
		logger.Errorf("init windows cpu baseline failed: %v", err)
		return
	}

	lastCPUTimeSlice.lastPerCPUTimes = cloneCPUTimesStats(perCPUTimes)
	logWindowsCPUTimesQuery("init", meta, len(perCPUTimes))
}

// getCPUStatUsage 采集 Windows CPU 时间信息，并计算逐核及总 CPU 使用率。
//
// 参数：
// - report：CPU 采集结果的输出对象，会被填充逐核统计、总统计和使用率。
//
// 返回值：
// - error：当 CPU 时间查询、基线重置或使用率计算失败时返回错误。
func getCPUStatUsage(report *CpuReport) error {
	report.Stat = report.Stat[:0]
	report.Usage = report.Usage[:0]
	report.TotalStat = cpu.TimesStat{}
	report.TotalUsage = 0

	// 采集逐核 CPU 时间统计。
	perCPUTimes, meta, err := getWindowsCPUTimesWithMeta()
	if err != nil {
		logger.Errorf("get CPU Stat fail: %v", err)
		return err
	}
	logWindowsCPUTimesQuery("collect", meta, len(perCPUTimes))

	var previousPerCPUTimes []cpu.TimesStat
	lastCPUTimeSlice.Lock()
	// 判断lastPerCPUTimes长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastPerCPUTimes) <= 0 || len(perCPUTimes) != len(lastCPUTimeSlice.lastPerCPUTimes) {
		logger.Warnw(
			"reset windows cpu baseline before usage calculation",
			"previous_cpu_count", len(lastCPUTimeSlice.lastPerCPUTimes),
			"current_cpu_count", len(perCPUTimes),
		)

		lastCPUTimeSlice.lastPerCPUTimes, meta, err = getWindowsCPUTimesWithMeta()
		if err != nil {
			lastCPUTimeSlice.Unlock()
			logger.Errorf("reset windows cpu baseline failed: %v", err)
			return err
		}
		logWindowsCPUTimesQuery("reset_baseline", meta, len(lastCPUTimeSlice.lastPerCPUTimes))
	}

	l1, l2 := len(perCPUTimes), len(lastCPUTimeSlice.lastPerCPUTimes)
	if l1 != l2 {
		lastCPUTimeSlice.Unlock()
		err = fmt.Errorf("received two CPU counts %d != %d", l1, l2)
		logger.Errorf("windows cpu baseline length mismatch: %v", err)
		return err
	}

	previousPerCPUTimes = cloneCPUTimesStats(lastCPUTimeSlice.lastPerCPUTimes)
	lastCPUTimeSlice.lastPerCPUTimes = cloneCPUTimesStats(perCPUTimes)
	lastCPUTimeSlice.Unlock()

	for index, currentCPUTimes := range perCPUTimes {
		previousCPUTimes := previousPerCPUTimes[index]
		tmp := calcTimeState(previousCPUTimes, currentCPUTimes)
		report.Stat = append(report.Stat, tmp)
	}
	// 计算总 CPU 统计信息。
	currentTotalCPUTimes := sumWindowsTotalCPUTimes(perCPUTimes)
	lastTotalCPUTimes := sumWindowsTotalCPUTimes(previousPerCPUTimes)
	report.TotalStat = calcTimeState(lastTotalCPUTimes, currentTotalCPUTimes)
	perUsage, err := calculateAllCPUBusyPercent(previousPerCPUTimes, perCPUTimes)
	if err != nil {
		logger.Errorf("get CPU Percent fail: %v", err)
		return err
	}

	report.Usage = perUsage
	// 计算总 CPU 使用率。
	report.TotalUsage = calculateCPUBusyPercent(lastTotalCPUTimes, currentTotalCPUTimes)

	// 对使用率结果做边界保护。
	for i := range report.Usage {
		if report.Usage[i] < 0 {
			logger.Errorf("get invalid cpu usage %f", report.Usage[i])
			report.Usage[i] = 0.0
		}
		if report.Usage[i] > 100 {
			logger.Errorf("get invalid cpu usage %f", report.Usage[i])
			report.Usage[i] = 100.0
		}
	}

	if report.TotalUsage < 0 || report.TotalUsage > 100 {
		report.TotalUsage = 0.0
	}

	logger.Debugw(
		"windows cpu usage collected",
		"per_stat_len", len(report.Stat),
		"per_usage_len", len(report.Usage),
		"total_usage", report.TotalUsage,
	)
	return nil
}

func cloneCPUTimesStats(src []cpu.TimesStat) []cpu.TimesStat {
	if src == nil {
		return nil
	}

	cloned := make([]cpu.TimesStat, len(src))
	copy(cloned, src)
	return cloned
}

// cloneCPUInfo 复制 CPU 信息切片，避免调用方修改结果时污染全局缓存。
func cloneCPUInfo(src []cpu.InfoStat) []cpu.InfoStat {
	if src == nil {
		return nil
	}

	cloned := make([]cpu.InfoStat, len(src))
	copy(cloned, src)
	for i := range cloned {
		if src[i].Flags != nil {
			cloned[i].Flags = append([]string(nil), src[i].Flags...)
		}
	}
	return cloned
}

func fetchCPUInfo(timeout time.Duration) ([]cpu.InfoStat, error) {
	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	return cpuInfoWithContext(ctx)
}

func ensureCPUInfoUpdater(period time.Duration, timeout time.Duration) {
	if !cpuInfoUpdaterRunning.CompareAndSwap(false, true) {
		return
	}

	if period < minPeriod {
		period = minPeriod
	}

	go func(period time.Duration, timeout time.Duration) {
		defer cpuInfoUpdaterRunning.Store(false)

		timer := time.NewTicker(period)
		defer timer.Stop()
		logger.Debugf("going to period update config->[%s]", period)
		logger.Debugf("cpu info gather timeout->[%s]", timeout)

		for {
			tempCpuInfo, err := fetchCPUInfo(timeout)
			if err != nil {
				logger.Errorf("failed to get cpu info for->[%#v]", err)
			} else {
				logger.Debug("cpu info update success and going to update global state")
				updateLock.Lock()
				cpuInfo = cloneCPUInfo(tempCpuInfo)
				updateLock.Unlock()
				logger.Debug("update global state success")
			}

			select {
			case <-timer.C:
			}
		}
	}(period, timeout)
}

// queryCpuInfo 查询并缓存机器的 CPU 基础信息。
//
// 注意：
// - 部分 Windows 机器因为 CPU 核数过多，查询 WMI 接口可能超时。
// - 因此这里提供兼容方案，允许 Windows 上报的 CPU info 数量与 CPU usage 数量不完全对应。
// - Linux 侧因为该信息更容易稳定获取，因此仍要求两边个数保持一致。
//
// 参数：
// - r：CPU 采集结果的输出对象，会写入当前缓存的 CPU 基础信息。
// - period：后台刷新 CPU 信息的周期，实际执行时不会低于 `minPeriod`。
// - timeout：单次查询 CPU 信息的超时时间。
//
// 返回值：
// - error：当缓存为空且同步查询失败时返回错误；若命中缓存则通常为 nil。
func queryCpuInfo(r *CpuReport, period time.Duration, timeout time.Duration) error {
	ensureCPUInfoUpdater(period, timeout)

	updateLock.RLock()
	cachedCPUInfo := cloneCPUInfo(cpuInfo)
	updateLock.RUnlock()
	if len(cachedCPUInfo) > 0 {
		r.Cpuinfo = cachedCPUInfo
		return nil
	}

	tempCpuInfo, err := fetchCPUInfo(timeout)
	if err != nil {
		logger.Errorf("failed to get cpu info for->[%#v]", err)
		return err
	}

	clonedCPUInfo := cloneCPUInfo(tempCpuInfo)
	updateLock.Lock()
	cpuInfo = cloneCPUInfo(clonedCPUInfo)
	updateLock.Unlock()
	r.Cpuinfo = clonedCPUInfo
	return nil
}
