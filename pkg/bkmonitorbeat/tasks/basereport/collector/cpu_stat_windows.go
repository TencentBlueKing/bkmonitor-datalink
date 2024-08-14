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
	"context"
	"strings"
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

func getCPUStatUsage(report *CpuReport) error {
	// per stat
	perStat, err := cpu.Times(true)
	if err != nil {
		logger.Error("get CPU Stat fail")
		return err
	}

	// total stat
	totalstat, err := cpu.Times(false)
	if err != nil {
		logger.Error("get CPU Total Stat fail")
		return err
	}
	report.TotalStat = totalstat[0]

	perUsage, err := cpu.Percent(0, true)
	if err != nil {
		logger.Error("get CPU Percent fail")
		return err
	}
	for _, stat := range perStat {
		if strings.Contains(stat.CPU, "_Total") {
			// pass "_Total" filed
			continue
		} else {
			// TODO : gopsutil implement has bug
			// windows TimesStat has only User, System, Idle, Irq
			// perUsage := 100 - stat.Idle
			// report.Usage = append(report.Usage, perUsage)
			report.Stat = append(report.Stat, stat)
		}
	}
	report.Usage = perUsage
	// get total cpu percent
	total, err := cpu.Percent(0, false)
	if err != nil {
		logger.Error("get CPU Total Percent fail")
		return err
	}
	report.TotalUsage = total[0]

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
