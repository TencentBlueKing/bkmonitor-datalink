// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"time"

	"golang.org/x/sys/unix"
)

// cpuClockSched 是内核 CPUCLOCK_SCHED：按调度器统计的纳秒级 CPU 时间。
const cpuClockSched = 2

// processCPUTime 读取进程（整个线程组）累计消耗的 CPU 时间，含已退出的线程。
// clock id 的编码与 glibc clock_getcpuclockid 相同；内核对进程级 CPU 时钟不做权限
// 检查，只要求 pid 在当前 PID 命名空间内且是线程组 leader。
func processCPUTime(pid int32) (time.Duration, error) {
	// 编码里 pid 0 表示调用者自己，会把 Beat 的 CPU 时间当成目标的。
	if pid <= 0 {
		return 0, unix.EINVAL
	}
	clockID := (^pid << 3) | cpuClockSched
	var ts unix.Timespec
	if err := unix.ClockGettime(clockID, &ts); err != nil {
		return 0, err
	}
	return time.Duration(ts.Nano()), nil
}

// readTargetCPUTimes 记录各目标进程当前的累计 CPU 时间，读不到的进程直接跳过。
// 它只服务于没有样本时的失败提示，不能影响采集本身。
func readTargetCPUTimes(pids []int32) map[int32]time.Duration {
	times := make(map[int32]time.Duration, len(pids))
	for _, pid := range pids {
		if value, err := processCPUTime(pid); err == nil {
			times[pid] = value
		}
	}
	return times
}
