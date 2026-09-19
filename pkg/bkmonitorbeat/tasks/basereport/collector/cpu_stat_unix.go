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
	"math"
	"runtime"
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

// cpuTimesReader 用于替换 CPU 快照读取函数，便于测试时注入固定数据而不读取 /proc/stat。
var cpuTimesReader = cpu.Times

func getCPUStatUsage(report *CpuReport) error {
	// 将快照读取和基线更新一起加锁，避免并发读取的旧快照覆盖较新的基线。
	lastCPUTimeSlice.Lock()
	defer lastCPUTimeSlice.Unlock()
	per, err := cpuTimesReader(true)
	if err != nil {
		return err
	}
	total, err := cpuTimesReader(false)
	if err != nil {
		return err
	}
	previousPer, previousTotal := lastCPUTimeSlice.lastPerCPUTimes, lastCPUTimeSlice.lastCPUTimes
	// 即使本次采样区间无效，也更新基线，便于下一次采样恢复正常计算。
	lastCPUTimeSlice.lastPerCPUTimes = per
	lastCPUTimeSlice.lastCPUTimes = total
	if len(total) != 1 || len(previousTotal) != 1 || len(per) == 0 || len(per) != len(previousPer) {
		return fmt.Errorf("%w: empty snapshot or CPU topology changed", errInvalidCPUSample)
	}
	previous := make(map[string]cpu.TimesStat, len(previousPer))
	for _, stat := range previousPer {
		previous[stat.CPU] = stat
	}
	if len(previous) != len(previousPer) {
		return fmt.Errorf("%w: duplicate CPU names", errInvalidCPUSample)
	}
	seen := make(map[string]bool, len(per))
	for _, stat := range per {
		if _, ok := previous[stat.CPU]; !ok || seen[stat.CPU] {
			return fmt.Errorf("%w: CPU topology changed", errInvalidCPUSample)
		}
		seen[stat.CPU] = true
	}
	var sample CpuReport
	sample.TotalStat, sample.TotalUsage, err = diffCPUUsage(previousTotal[0], total[0])
	if err != nil {
		return err
	}
	for _, current := range per {
		stat, usage, err := diffCPUUsage(previous[current.CPU], current)
		if err != nil {
			// 跳过异常核心，保留健康核心和已独立校验通过的整机数据。
			// stat 和 usage 同步追加，确保下游按位置关联时保持一致。
			logger.Debugf("drop invalid per-CPU sample: %v", err)
			continue
		}
		sample.Stat = append(sample.Stat, stat)
		sample.Usage = append(sample.Usage, usage)
	}
	*report = sample
	return nil
}

// diffCPUUsage 保持 gopsutil 的忙碌时间口径（不包含 iowait），
// 对无效采样区间返回错误，不将其伪装成有效的 0% 或 100%。
func diffCPUUsage(previous, current cpu.TimesStat) (cpu.TimesStat, float64, error) {
	delta := calcTimeState(previous, current)
	invalid := func(reason string) (cpu.TimesStat, float64, error) {
		return cpu.TimesStat{}, 0, fmt.Errorf("%w: cpu=%s %s previous=%+v current=%+v", errInvalidCPUSample, current.CPU, reason, previous, current)
	}
	if previous.CPU != current.CPU {
		return invalid("CPU name changed")
	}
	fields := func(s cpu.TimesStat) []float64 {
		return []float64{s.User, s.System, s.Idle, s.Nice, s.Iowait, s.Irq, s.Softirq, s.Steal, s.Guest, s.GuestNice}
	}
	for _, s := range []cpu.TimesStat{previous, current, delta} {
		for _, v := range fields(s) {
			// 对 iowait 回退也采取保守丢弃策略，不引入任意的修正阈值，
			// 也不改变正常样本的指标计算口径。
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return invalid("non-finite or regressed counter")
			}
		}
	}
	total := delta.User + delta.System + delta.Idle + delta.Nice + delta.Iowait + delta.Irq + delta.Softirq + delta.Steal
	if runtime.GOOS != "linux" {
		total += delta.Guest + delta.GuestNice
	}
	busy := total - delta.Idle - delta.Iowait
	if total <= 0 || busy < 0 || busy > total || math.IsInf(total, 0) {
		return invalid("invalid total/busy delta")
	}
	usage := busy / total * 100
	if math.IsNaN(usage) || math.IsInf(usage, 0) || usage < 0 || usage > 100 {
		return invalid("invalid usage")
	}
	return delta, usage, nil
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
