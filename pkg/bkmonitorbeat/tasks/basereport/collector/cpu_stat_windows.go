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
	"time"

	"github.com/shirou/gopsutil/v3/cpu"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 全局通用的cpu信息获取
var cpuInfo = make([]cpu.InfoStat, 0)

var isCpuInfoUpdating = false

var updateLock sync.RWMutex

var minPeriod = 1 * time.Minute

type lastTimeSlice struct {
	sync.Mutex
	lastCPUTimes    []cpu.TimesStat
	lastPerCPUTimes []cpu.TimesStat
}

var lastCPUTimeSlice lastTimeSlice

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

func init() {
	lastCPUTimeSlice.Lock()
	perTimes, meta, err := getWindowsCPUTimesWithMeta()
	if err != nil {
		logger.Errorf("init windows cpu baseline failed: %v", err)
	} else {
		lastCPUTimeSlice.lastPerCPUTimes = perTimes
		logWindowsCPUTimesQuery("init", meta, len(perTimes))
	}
	if len(lastCPUTimeSlice.lastPerCPUTimes) > 0 {
		lastCPUTimeSlice.lastCPUTimes = []cpu.TimesStat{sumWindowsTotalCPUTimes(lastCPUTimeSlice.lastPerCPUTimes)}
	} else {
		lastCPUTimeSlice.lastCPUTimes, err = cpu.Times(false)
		if err != nil {
			logger.Errorf("init windows total cpu baseline fallback failed: %v", err)
		} else {
			logger.Warn("init windows total cpu baseline fell back to cpu.Times(false)")
		}
	}
	lastCPUTimeSlice.Unlock()
}

func getCPUStatUsage(report *CpuReport) error {
	// per stat
	perStat, meta, err := getWindowsCPUTimesWithMeta()
	if err != nil {
		logger.Errorf("get CPU Stat fail: %v", err)
		return err
	}
	logWindowsCPUTimesQuery("collect", meta, len(perStat))
	// 比较两次获取的时间片的内容的长度,如果不对等直接退出
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()
	// 判断lastPerCPUTimes长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastPerCPUTimes) <= 0 || len(perStat) != len(lastCPUTimeSlice.lastPerCPUTimes) {
		logger.Warnw(
			"reset windows cpu baseline before usage calculation",
			"previous_cpu_count", len(lastCPUTimeSlice.lastPerCPUTimes),
			"current_cpu_count", len(perStat),
		)

		lastCPUTimeSlice.lastPerCPUTimes, meta, err = getWindowsCPUTimesWithMeta()
		if err != nil {
			logger.Errorf("reset windows cpu baseline failed: %v", err)
			return err
		}
		logWindowsCPUTimesQuery("reset_baseline", meta, len(lastCPUTimeSlice.lastPerCPUTimes))
	}

	l1, l2 := len(perStat), len(lastCPUTimeSlice.lastPerCPUTimes)
	if l1 != l2 {
		err = fmt.Errorf("received two CPU counts %d != %d", l1, l2)
		logger.Errorf("windows cpu baseline length mismatch: %v", err)
		return err
	}

	for index, value := range perStat {
		item := lastCPUTimeSlice.lastPerCPUTimes[index]
		tmp := calcTimeState(item, value)
		report.Stat = append(report.Stat, tmp)
	}
	// total stat
	cpuTimeStat := sumWindowsTotalCPUTimes(perStat)
	lastCpuTimeStat := sumWindowsTotalCPUTimes(lastCPUTimeSlice.lastPerCPUTimes)
	report.TotalStat = calcTimeState(lastCpuTimeStat, cpuTimeStat)
	perUsage, err := calculateAllCPUBusyPercent(lastCPUTimeSlice.lastPerCPUTimes, perStat)
	if err != nil {
		logger.Errorf("get CPU Percent fail: %v", err)
		return err
	}
	// 将此次获取的timeState重新写入公共变量
	lastCPUTimeSlice.lastCPUTimes = []cpu.TimesStat{cpuTimeStat}
	lastCPUTimeSlice.lastPerCPUTimes = perStat

	report.Usage = perUsage
	// get total cpu percent
	report.TotalUsage = calculateCPUBusyPercent(lastCpuTimeStat, cpuTimeStat)

	// protect code
	for i := range report.Usage {
		if report.Usage[i] < 0 {
			logger.Errorf("get invalid cpu useage %f", report.Usage[i])
			report.Usage[i] = 0.0
		}
		if report.Usage[i] > 100 {
			logger.Errorf("get invalid cpu useage %f", report.Usage[i])
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

// queryCpuInfo: 查询获取机器的CPU信息
// 注意，由于发现在部分windows机器上存在CPU核数过多，导致查询WMI接口超时的问题
// 所以在此会提供兼容方案，windows允许CPU info上报的核数信息与CPU usage不对应
// 但是由于linux机器上获取该配置方便，因此存在控制，要求两边的个数必须一致
func queryCpuInfo(r *CpuReport, period time.Duration, timeout time.Duration) error {
	// 异常存储，如果有任何异常存储在此处
	var err error

	// 判断是否已经有在工作的goroutines
	if !isCpuInfoUpdating {
		// 如果没有，需要新增一个
		go func() {
			var tempCpuInfo []cpu.InfoStat
			// 标识已经正在处理中
			isCpuInfoUpdating = true

			// 退出时，需要将标志位改为
			defer func() { isCpuInfoUpdating = false }()

			// CPU的定时更新时间，不能低于1分钟一次，防止导致频繁更新
			if period < minPeriod {
				period = minPeriod
			}

			timer := time.NewTicker(period)
			logger.Debugf("going to period update config->[%d]", period)
			logger.Debugf("cpu info gather timeout->[%d]", timeout)
			// 定时的更新CPU INFO
			for {
				timeoutCtx, _ := context.WithTimeout(context.Background(), timeout)
				if tempCpuInfo, err = cpu.InfoWithContext(timeoutCtx); err != nil {
					logger.Errorf("failed to get cpu info for->[%#v]", err)
					return
				}

				logger.Debug("cpu info update success and going to update global state")
				updateLock.Lock()
				cpuInfo = tempCpuInfo
				updateLock.Unlock()
				logger.Debug("update global state success")

				// 更新完成，需要sleep
				select {
				case <-timer.C:
					continue
				}
			}
		}()
	}

	// 将获取的CPU信息返回到Report中
	updateLock.RLock()
	r.Cpuinfo = cpuInfo
	updateLock.RUnlock()
	return err
}
