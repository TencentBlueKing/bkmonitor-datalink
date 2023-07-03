// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris zos

package collector

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var diskLastStatTime time.Time

// get speed from origin data
// default time interval is 1min(60s)
func GetDiskSpeed(last, current map[string]BKDiskStats) {
	now := time.Now()
	interval := now.Sub(diskLastStatTime).Seconds()
	if int(interval) == 0 {
		// in case devide 0
		interval = 1
	}
	logger.Debugf("disk interval=%d", int(interval))
	diskLastStatTime = now

	for name, stat := range last {
		newstat := current[name]
		deltaReadCount := CounterDiff(newstat.ReadCount, stat.ReadCount)
		deltaReadBytes := CounterDiff(newstat.ReadBytes, stat.ReadBytes)
		deltaWriteCount := CounterDiff(newstat.WriteCount, stat.WriteCount)
		deltaWriteBytes := CounterDiff(newstat.WriteBytes, stat.WriteBytes)
		newstat.SpeedIORead = float64(deltaReadCount) / interval
		newstat.SpeedByteRead = float64(deltaReadBytes) / interval
		newstat.SpeedIOWrite = float64(deltaWriteCount) / interval
		newstat.SpeedByteWrite = float64(deltaWriteBytes) / interval
		deltaIOCompleted := deltaReadCount + deltaWriteCount

		deltaIOTime := CounterDiff(newstat.IoTime, stat.IoTime)

		if deltaIOCompleted == 0 { // incase devide 0
			newstat.Svctm = 0
			newstat.Await = 0
			newstat.AvgrqSz = 0
		} else {
			// svctm：delta(time spent doing I/Os)/ (delta(reads completed) + delta(writes completed))
			newstat.Svctm = float64(deltaIOTime) / float64(deltaIOCompleted)

			// await：(delta(time spent reading) + delta(time spent writing)) / (delta(reads completed) + delta(writes completed))
			deltaReadTime := CounterDiff(newstat.ReadTime, stat.ReadTime)
			deltaWriteTime := CounterDiff(newstat.WriteTime, stat.WriteTime)
			newstat.Await = float64(deltaReadTime+deltaWriteTime) / float64(deltaIOCompleted)

			// avgrq-sz：(delta(sectors read) + delta(sectors written)) / (delta(reads completed) + delta(writes completed))
			deltaReadSectors := CounterDiff(newstat.ReadSectors, stat.ReadSectors)
			deltaWriteSectors := CounterDiff(newstat.WriteSectors, stat.WriteSectors)
			newstat.AvgrqSz = float64(deltaReadSectors+deltaWriteSectors) / float64(deltaIOCompleted)
		}

		// avgqu-sz：delta(weighted time spent doing I/Os) / t / 1000
		deltaWeightedIoTime := CounterDiff(newstat.WeightedIO, stat.WeightedIO)
		newstat.AvgquSz = float64(deltaWeightedIoTime) / interval / 1000.0

		// %util：delta(time spent doing I/Os) / t / 1000 * 100%
		// 如果是发现这个节点的IO时间超过了现实时间（部分系统有时间倒流的情况），这个是肯定有问题的，那么此时的使用率将会被设置为0
		if (deltaIOTime / 1000) > uint64(interval) {
			logger.Errorf("got deltaIOTime->[%d] which is larger than interval->[%d] must be something go wrong. utils will set to 0",
				deltaIOTime, interval)
			newstat.Util = 0
		} else {
			newstat.Util = float64(deltaIOTime) / interval / 1000.0
		}

		current[name] = newstat
	}
}
