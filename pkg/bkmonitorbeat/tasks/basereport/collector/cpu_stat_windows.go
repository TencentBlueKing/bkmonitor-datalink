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
	"math"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v3/cpu"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	modNtDll                       = syscall.NewLazyDLL("ntdll.dll")
	procNtQuerySystemInformationEx = modNtDll.NewProc("NtQuerySystemInformationEx")
)

// 全局通用的cpu信息获取
var cpuInfo = make([]cpu.InfoStat, 0)

var isCpuInfoUpdating = false

var updateLock sync.RWMutex

var minPeriod = 1 * time.Minute

type LOGICAL_PROCESSOR_RELATIONSHIP uint32

const (
	ClocksPerSec = 10000000.0

	RelationGroup             LOGICAL_PROCESSOR_RELATIONSHIP = 4
	ERROR_INSUFFICIENT_BUFFER                                = syscall.Errno(122)
)

const (
	SystemLogicalProcessorAndGroupInformation = 0x73 // 对应EnumerateProcessorInformation
	SystemProcessorPerformanceInformation     = 0x08
	STATUS_SUCCESS                            = 0x00000000
	STATUS_INFO_LENGTH_MISMATCH               = 0xC0000004
)

type lastTimeSlice struct {
	sync.Mutex
	lastCPUTimes    []cpu.TimesStat
	lastPerCPUTimes []cpu.TimesStat
}

type SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION struct {
	IdleTime       int64
	KernelTime     int64
	UserTime       int64
	DpcTime        int64
	InterruptTime  int64
	InterruptCount uint32
}

type SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX struct {
	Relationship uint32
	Size         uint32
	// 这里为了示例简化了结构体，实际代码需要根据实际情况扩展
	Group struct {
		MaximumGroupCount uint16
		ActiveGroupCount  uint16
		Reserved          [20]byte
		GroupInfo         [256]struct {
			MaximumProcessorCount uint8
			ActiveProcessorCount  uint8
			Reserved              [38]byte
			ActiveProcessorMask   uint64 // 32为下为uint32
		}
	}
}

var lastCPUTimeSlice lastTimeSlice
var numberOfProcessorGroups uint16
var activeProcessorCounts []uint8

func init() {
	// 初始调用以确定缓冲区大小
	var bufferSize uint32
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalProcessorInformationEx").Call(
		uintptr(RelationGroup),
		uintptr(0),
		uintptr(unsafe.Pointer(&bufferSize)),
	)
	fmt.Println(bufferSize)
	if r != 0 && syscall.Errno(r) != ERROR_INSUFFICIENT_BUFFER {
		logger.Fatal("无法确定缓冲区大小")
		return
	}

	buffer := make([]byte, bufferSize)
	r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalProcessorInformationEx").Call(
		uintptr(RelationGroup),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&bufferSize)),
	)
	if r == 0 {
		logger.Fatal("无法获取逻辑处理器信息:", err)
		return
	}

	info := (*SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX)(unsafe.Pointer(&buffer[0]))
	numberOfProcessorGroups = info.Group.ActiveGroupCount
	for uintptr(unsafe.Pointer(info)) < uintptr(unsafe.Pointer(&buffer[0]))+uintptr(bufferSize) {
		if info.Relationship == 4 {
			for i := uint16(0); i < numberOfProcessorGroups; i++ {
				activeProcessorCounts = append(activeProcessorCounts, uint8(info.Group.GroupInfo[i].ActiveProcessorCount))
			}
			break
		}
		info = (*SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX)(unsafe.Pointer(uintptr(unsafe.Pointer(info)) + uintptr(info.Size)))
	}
	logger.Debugf("处理器组数量: %d \n", numberOfProcessorGroups)
	for i := uint16(0); i < numberOfProcessorGroups; i++ {
		logger.Debugf("处理器组 %d 活动处理器数量: %d\n", i, activeProcessorCounts[i])
	}
	lastCPUTimeSlice.Lock()
	lastCPUTimeSlice.lastCPUTimes, _ = perCPUTimes()
	lastCPUTimeSlice.lastPerCPUTimes, _ = totalTimes(lastCPUTimeSlice.lastPerCPUTimes)
	lastCPUTimeSlice.Unlock()
}

func perCPUTimes() ([]cpu.TimesStat, error) {
	var ret []cpu.TimesStat
	stats, err := perfInfo()
	if err != nil {
		return nil, err
	}
	for core, v := range stats {
		c := cpu.TimesStat{
			CPU:    fmt.Sprintf("cpu%d", core),
			User:   float64(v.UserTime) / ClocksPerSec,
			System: float64(v.KernelTime-v.IdleTime) / ClocksPerSec,
			Idle:   float64(v.IdleTime) / ClocksPerSec,
			Irq:    float64(v.InterruptTime) / ClocksPerSec,
		}
		ret = append(ret, c)
	}
	return ret, nil
}

// cpuPercent:计算的是差值后的使用率百分比
func cpuUsagePercent(calcTimeStat []cpu.TimesStat) ([]float64, error) {
	var ret []float64
	for _, v := range calcTimeStat {
		if v.Total() == 0 {
			ret = append(ret, 0)
			continue
		}
		c := math.Min(100, math.Max(0, (1-(v.Idle/v.Total()))*100))
		ret = append(ret, c)
	}
	return ret, nil
}

// Times: 每个cpu的状态
func perfInfo() ([]SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION, error) {

	totalProcessors := uint8(0)
	processorCount := uint8(0)
	var bufferTotal []SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION
	for i := uint16(0); i < numberOfProcessorGroups; i++ {
		activeProcessorCount := activeProcessorCounts[i]
		totalProcessors += activeProcessorCount

		buffer := make([]SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION, activeProcessorCount)
		bufferSize := uint32(int(activeProcessorCount) * int(unsafe.Sizeof(SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION{})))

		r, _, err := procNtQuerySystemInformationEx.Call(
			uintptr(SystemProcessorPerformanceInformation),
			uintptr(unsafe.Pointer(&i)),
			unsafe.Sizeof(uint16(1)),
			uintptr((unsafe.Pointer(&buffer[0]))),
			uintptr(bufferSize),
			uintptr(unsafe.Pointer(nil)),
		)
		if err != nil {
			//fmt.Printf("err%v", err)
		}
		status := syscall.Errno(r)

		if status != STATUS_SUCCESS {
			for j := range buffer {
				buffer[j] = SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION{}
			}
		}

		for s, info := range buffer {
			fmt.Printf("当前处于cpu组【%d】的cpu【%d】KernelTime: %v, UserTime: %v, IdleTime: %v\n", i, s, info.KernelTime, info.UserTime, info.IdleTime)
		}
		bufferTotal = append(bufferTotal, buffer...)
		processorCount += activeProcessorCount
	}

	return bufferTotal, nil
}
func totalTimes(perTimeStat []cpu.TimesStat) ([]cpu.TimesStat, error) {
	var lastCPUTimes []cpu.TimesStat
	lastCPUTimes = append(lastCPUTimes, cpu.TimesStat{
		CPU:    "cpu-total",
		Idle:   float64(0),
		User:   float64(0),
		System: float64(0),
		Irq:    float64(0),
	})
	for _, stat := range perTimeStat {
		lastCPUTimes[0].Idle += stat.Idle
		lastCPUTimes[0].User += stat.User
		lastCPUTimes[0].System += stat.System
		lastCPUTimes[0].Irq += stat.Irq
	}
	return lastCPUTimes, nil
}

func getCPUStatUsage(report *CpuReport) error {
	// per stat
	perStat, err := perCPUTimes()
	if err != nil {
		logger.Error("get CPU Stat fail")
		return err
	}
	// 比较两次获取的时间片的内容的长度,如果不对等直接退出
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()
	// 判断lastPerCPUTimes长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastPerCPUTimes) <= 0 || len(perStat) != len(lastCPUTimeSlice.lastPerCPUTimes) {
		lastCPUTimeSlice.lastPerCPUTimes, err = perCPUTimes()
		if err != nil {
			return err
		}
	}

	l1, l2 := len(perStat), len(lastCPUTimeSlice.lastPerCPUTimes)
	if l1 != l2 {
		err = fmt.Errorf("received two CPU counts %d != %d", l1, l2)
		return err
	}

	for index, value := range perStat {
		item := lastCPUTimeSlice.lastPerCPUTimes[index]
		tmp := calcTimeState(item, value)
		report.Stat = append(report.Stat, tmp)
	}
	// total stat
	totalstat, err := totalTimes(perStat)
	if err != nil {
		logger.Error("get CPU Total Stat fail")
		return err
	}
	// 判断lastCPUTimes的长度，增加重写避免init方法失效的情况
	if len(lastCPUTimeSlice.lastCPUTimes) <= 0 {
		lastCPUTimeSlice.lastCPUTimes, err = cpu.Times(false)
		if err != nil {
			return err
		}
	}
	cpuTimeStat := totalstat[0]
	lastCpuTimeStat := lastCPUTimeSlice.lastCPUTimes[0]
	report.TotalStat = calcTimeState(lastCpuTimeStat, cpuTimeStat)
	// 将此次获取的timeState重新写入公共变量
	lastCPUTimeSlice.lastCPUTimes = totalstat
	lastCPUTimeSlice.lastPerCPUTimes = perStat
	// 手动计算 Percent 通过上报的report.TotalStat report.Stat
	perUsage, err := cpuUsagePercent(report.Stat)
	if err != nil {
		logger.Error("get CPU Percent fail")
		return err
	}

	report.Usage = perUsage
	// get total cpu percent
	total, err := cpuUsagePercent([]cpu.TimesStat{report.TotalStat})
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
