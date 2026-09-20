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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/yumaojun03/dmidecode"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type lastTimeSlice struct {
	sync.Mutex
	lastCPUTimes    []cpu.TimesStat
	lastPerCPUTimes []cpu.TimesStat
}

var lastCPUTimeSlice lastTimeSlice

func init() {
	lastCPUTimeSlice.Lock()
	lastCPUTimeSlice.lastCPUTimes, _ = cpu.Times(false)
	lastCPUTimeSlice.lastPerCPUTimes, _ = cpu.Times(true)
	lastCPUTimeSlice.Unlock()
}

type cpuPercentCollector func(interval time.Duration, percpu bool) ([]float64, error)

func collectCPUPercent(collect cpuPercentCollector) (perUsage, totalUsage []float64, valid bool, err error) {
	// 采集整机、逐核 CPU 使用率，并更新 gopsutil 对应基线
	perUsage, perErr := collect(0, true)
	totalUsage, totalErr := collect(0, false)

	// 判断整机、逐核采集是否发生 CPU 累计计数回退
	perRollback := errors.Is(perErr, cpu.ErrCPUTimesCounterRollback)
	totalRollback := errors.Is(totalErr, cpu.ErrCPUTimesCounterRollback)

	// 整机、逐核采集发生非 idle 回退错误时，向上抛错
	if perErr != nil && !perRollback {
		return nil, nil, false, perErr
	}
	if totalErr != nil && !totalRollback {
		return nil, nil, false, totalErr
	}

	// 任一采集发生计数回退时，丢弃本轮数据，不抛错
	if perRollback || totalRollback {
		return nil, nil, false, nil
	}

	// 两次采集均正常时，返回有效的逐核，整机使用率
	return perUsage, totalUsage, true, nil
}

func getCPUStatUsage(report *CpuReport) (bool, error) {
	var err error
	perCPUTimes, err := cpu.Times(true)
	if err != nil {
		return false, err
	}
	// 比较两次获取的时间片的内容的长度,如果不对等直接退出
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()

	// 判断lastPerCPUTimes长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastPerCPUTimes) <= 0 || len(perCPUTimes) != len(lastCPUTimeSlice.lastPerCPUTimes) {
		lastCPUTimeSlice.lastPerCPUTimes, err = cpu.Times(true)
		if err != nil {
			return false, err
		}
	}

	l1, l2 := len(perCPUTimes), len(lastCPUTimeSlice.lastPerCPUTimes)
	if l1 != l2 {
		err = fmt.Errorf("received two CPU counts %d != %d", l1, l2)
		return false, err
	}

	// 校验时间差后继续更新 gopsutil 基线，确保下一轮采样恢复
	timeStateValid := true
	for index, value := range perCPUTimes {
		item := lastCPUTimeSlice.lastPerCPUTimes[index]
		tmp := calcTimeState(item, value)
		if !isValidCPUTimeState(tmp) {
			timeStateValid = false
		}
		report.Stat = append(report.Stat, tmp)
	}

	cpuTimes, err := cpu.Times(false)
	if err != nil {
		return false, err
	}

	// 判断lastCPUTimes的长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastCPUTimes) <= 0 {
		lastCPUTimeSlice.lastCPUTimes, err = cpu.Times(false)
		if err != nil {
			return false, err
		}
	}

	cpuTimeStat := cpuTimes[0]
	lastCpuTimeStat := lastCPUTimeSlice.lastCPUTimes[0]
	report.TotalStat = calcTimeState(lastCpuTimeStat, cpuTimeStat)
	if !isValidCPUTimeState(report.TotalStat) {
		timeStateValid = false
	}

	// 无效样本也更新本地基线，避免下一轮继续使用回退前的旧基线
	lastCPUTimeSlice.lastCPUTimes = cpuTimes
	lastCPUTimeSlice.lastPerCPUTimes = perCPUTimes

	perUsage, totalUsage, valid, err := collectCPUPercent(cpu.Percent)
	if err != nil {
		return false, err
	}
	// idle 回退或 CPU 时间差出现负数时，均丢弃本轮样本
	if !valid || !timeStateValid {
		return false, nil
	}

	report.Usage = perUsage
	for i := range report.Usage {
		if report.Usage[i] < 0 || report.Usage[i] > 100 {
			report.Usage[i] = 0.0
		}
	}

	if len(totalUsage) == 0 {
		return false, fmt.Errorf("empty total CPU usage")
	}
	report.TotalUsage = totalUsage[0]
	if report.TotalUsage < 0 || report.TotalUsage > 100 {
		report.TotalUsage = 0.0
	}
	return true, nil
}

// queryCpuInfo: 查询获取机器的CPU信息
func queryCpuInfo(r *CpuReport, _ time.Duration, _ time.Duration) (err error) {
	if r.Cpuinfo, err = cpu.Info(); err != nil {
		logger.Errorf("failed to get cpu info for: %v", err)
		return err
	}
	// gopsutil查询失败的情况下，利用 dmidecode 命令查询 cpu 基础信息并上报
	if r.Cpuinfo == nil {
		r.Cpuinfo = make([]cpu.InfoStat, 0)
		r.Cpuinfo = append(r.Cpuinfo, cpu.InfoStat{})
	}
	var model string
	var mhz float64
	useDmidecode := false
	if len(r.Cpuinfo) > 0 {
		// 取第一个cpu检查，如果发现存在信息为空的情况，则启用dmidecode进行填充
		if r.Cpuinfo[0].Mhz == 0 || r.Cpuinfo[0].Model == "" || r.Cpuinfo[0].ModelName == "" {
			model, mhz = getDMIDecodeCPUInfo()
			useDmidecode = true
		}
	} else {
		logger.Warn("get empty cpu info, something wrong?")
	}

	// 不需要dmidecode则直接返回即可，cpu信息已经放在r.Cpuinfo
	if !useDmidecode {
		return nil
	}

	// 用dmidecode信息填充所有核 (按需补充信息)
	for index, info := range r.Cpuinfo {
		if info.Mhz == 0 {
			info.Mhz = mhz
		}
		if info.Model == "" {
			info.Model = model
		}
		if info.ModelName == "" {
			info.ModelName = model
		}
		r.Cpuinfo[index] = info
	}

	logger.Debugf("get cpu_info success->[%v]", r.Cpuinfo)
	return nil
}

func getDMIDecodeCPUInfo() (model string, mhz float64) {
	model = "unknown"
	mhz = -1
	dmi, err := dmidecode.New()
	if err != nil {
		logger.Errorf("init dmidecoder error:%s", err)
		return
	}
	processor, err := dmi.Processor()
	if err != nil {
		logger.Errorf("get dmi processor error:%s", err)
		return
	}
	if len(processor) > 0 {
		mhz = float64(processor[0].MaxSpeed)
		model = processor[0].Version
	}
	return
}
