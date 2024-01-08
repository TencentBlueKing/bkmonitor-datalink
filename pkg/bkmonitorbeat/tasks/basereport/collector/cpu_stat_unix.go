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

func getCPUStatUsage(report *CpuReport) error {
	var err error
	perCPUTimes, err := cpu.Times(true)
	if err != nil {
		return err
	}
	// 比较两次获取的时间片的内容的长度,如果不对等直接退出
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()

	// 判断lastPerCPUTimes长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastPerCPUTimes) <= 0 {
		lastCPUTimeSlice.lastPerCPUTimes, err = cpu.Times(true)
		if err != nil {
			return err
		}
	}

	l1, l2 := len(perCPUTimes), len(lastCPUTimeSlice.lastPerCPUTimes)
	if l1 != l2 {
		err = fmt.Errorf("received two CPU counts %d != %d", l1, l2)
		return err
	}

	for index, value := range perCPUTimes {
		item := lastCPUTimeSlice.lastPerCPUTimes[index]
		tmp := calcTimeState(item, value)
		report.Stat = append(report.Stat, tmp)
	}

	cpuTimes, err := cpu.Times(false)
	if err != nil {
		return err
	}

	// 判断lastCPUTimes的长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastCPUTimes) <= 0 {
		lastCPUTimeSlice.lastCPUTimes, err = cpu.Times(false)
		if err != nil {
			return err
		}
	}

	cpuTimeStat := cpuTimes[0]
	lastCpuTimeStat := lastCPUTimeSlice.lastCPUTimes[0]
	report.TotalStat = calcTimeState(lastCpuTimeStat, cpuTimeStat)

	// 将此次获取的timeState重新写入公共变量
	lastCPUTimeSlice.lastCPUTimes = cpuTimes
	lastCPUTimeSlice.lastPerCPUTimes = perCPUTimes

	// per usage
	report.Usage, err = cpu.Percent(0, true)
	if err != nil {
		return err
	}

	for i := range report.Usage {
		if report.Usage[i] < 0 || int(report.Usage[i]) > 100 {
			report.Usage[i] = 0.0
		}
	}
	// total usage
	total, err := cpu.Percent(0, false)
	if err != nil {
		return err
	}

	report.TotalUsage = total[0]
	if report.TotalUsage < 0 || report.TotalUsage > 100 {
		report.TotalUsage = 0.0
	}
	return nil
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
		if r.Cpuinfo[0].Mhz == 0 || r.Cpuinfo[0].Model == "" {
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

	// 用dmidecode信息填充所有核
	for index, info := range r.Cpuinfo {
		info.Mhz = mhz
		info.Model = model
		info.ModelName = model
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

func calcTimeState(t1, t2 cpu.TimesStat) cpu.TimesStat {
	return cpu.TimesStat{
		CPU:       t2.CPU,
		User:      t2.User - t1.User,
		System:    t2.System - t1.System,
		Idle:      t2.Idle - t1.Idle,
		Nice:      t2.Nice - t1.Nice,
		Iowait:    t2.Iowait - t1.Iowait,
		Irq:       t2.Irq - t1.Irq,
		Softirq:   t2.Softirq - t1.Softirq,
		Steal:     t2.Steal - t1.Steal,
		Guest:     t2.Guest - t1.Guest,
		GuestNice: t2.GuestNice - t1.GuestNice,
	}
}
