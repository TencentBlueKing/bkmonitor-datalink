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
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	SectorSize = 512
)

type DiskStats struct {
	ReadCount        uint64  `json:"readCount"`
	MergedReadCount  uint64  `json:"mergedReadCount"`
	WriteCount       uint64  `json:"writeCount"`
	MergedWriteCount uint64  `json:"mergedWriteCount"`
	ReadBytes        uint64  `json:"readBytes"`
	WriteBytes       uint64  `json:"writeBytes"`
	ReadTime         uint64  `json:"readTime"`
	WriteTime        uint64  `json:"writeTime"`
	IopsInProgress   uint64  `json:"iopsInProgress"`
	IoTime           uint64  `json:"ioTime"`
	WeightedIO       uint64  `json:"weightedIO"`
	Name             string  `json:"name"`
	SerialNumber     string  `json:"serialNumber"`
	Label            string  `json:"label"`
	MajorNum         uint64  `json:"major"`
	MinorNum         uint64  `json:"minor"`
	ReadSectors      uint64  `json:"readSectors"`
	WriteSectors     uint64  `json:"writeSectors"`
	SpeedIORead      float64 `json:"speedIORead"`
	SpeedByteRead    float64 `json:"speedByteRead"`
	SpeedIOWrite     float64 `json:"speedIOWrite"`
	SpeedByteWrite   float64 `json:"speedByteWrite"`
	Util             float64 `json:"util"`
	AvgrqSz          float64 `json:"avgrq_sz"`
	AvgquSz          float64 `json:"avgqu_sz"`
	Await            float64 `json:"await"`
	Svctm            float64 `json:"svctm"`
}

func ToDiskStats(stats disk.IOCountersStat) DiskStats {
	return DiskStats{
		ReadCount:        stats.ReadCount,
		MergedReadCount:  stats.MergedReadCount,
		WriteCount:       stats.WriteCount,
		MergedWriteCount: stats.MergedWriteCount,
		ReadBytes:        stats.ReadBytes,
		WriteBytes:       stats.WriteBytes,
		ReadTime:         stats.ReadTime,
		WriteTime:        stats.WriteTime,
		IopsInProgress:   stats.IopsInProgress,
		IoTime:           stats.IoTime,
		WeightedIO:       stats.WeightedIO,
		Name:             stats.Name,
		SerialNumber:     stats.SerialNumber,
		Label:            stats.Label,
		MajorNum:         stats.Major,
		MinorNum:         stats.Minor,
		ReadSectors:      stats.ReadBytes / SectorSize,
		WriteSectors:     stats.WriteBytes / SectorSize,
	}
}

func IOCounters(names ...string) (map[string]DiskStats, error) {
	stats, err := disk.IOCountersWithContext(context.Background(), names...)
	if err != nil {
		return nil, err
	}

	ret := make(map[string]DiskStats)
	for k, v := range stats {
		ret[k] = ToDiskStats(v)
	}
	return ret, nil
}

type DiskReport struct {
	DiskStats  map[string]DiskStats `json:"diskstat"`
	Partitions []disk.PartitionStat `json:"partition"`
	Usage      []disk.UsageStat     `json:"usage"`
}

func (report *DiskReport) AssignDiskStats(stats map[string]DiskStats) {
	report.DiskStats = make(map[string]DiskStats)
	for name, stat := range stats {
		report.DiskStats[name] = stat
	}
}

// diskLast 无并发 不加锁
var diskLast map[string]DiskStats

func GetDiskInfo(config configs.DiskConfig) (*DiskReport, error) {
	var report DiskReport
	var err error

	// TODO 采样多次，取最大值
	count := config.StatTimes
	ticker := time.NewTicker(config.StatPeriod)
	defer ticker.Stop()
	for {
		logger.Debug("collect disk io")

		var once DiskReport
		iostats, err := IOCounters()
		if err != nil {
			return nil, err
		}

		once.AssignDiskStats(iostats)

		// get speed
		// TODO 待修复：应该按照上个周期计算而不是第一个周期
		GetDiskSpeed(diskLast, once.DiskStats)
		diskLast = once.DiskStats

		// TODO select max iostat report by partition
		// 过滤黑白名单
		// 过滤以分区形式上报的数据
		report.DiskStats = FilterDiskIoStats(once.DiskStats, config)

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}

	partitions, err := disk.Partitions(config.CollectAllDev)
	if err != nil {
		return nil, err
	}
	// 分区过滤
	partitions = FilterPartitions(partitions, config)

	report.Partitions = make([]disk.PartitionStat, 0, len(partitions))
	report.Usage = make([]disk.UsageStat, 0, len(partitions))
	for _, partition := range partitions {
		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			logger.Errorf("get disk usage failed, mountpoint=%v, err: %v", partition.Mountpoint, err)
			continue
		}

		if usage.UsedPercent < 0 || usage.UsedPercent > 100 {
			continue
		}

		report.Usage = append(report.Usage, *usage)
		report.Partitions = append(report.Partitions, partition)
	}

	return &report, nil
}

func GetDiskSpeed(last, current map[string]DiskStats) {
	deltaCount(last, current)
}

var diskLastStatTime time.Time

func deltaCount(last, current map[string]DiskStats) {
	now := time.Now()
	interval := now.Sub(diskLastStatTime).Seconds()
	if int(interval) == 0 {
		interval = 1
	}

	logger.Debugf("disk interval=%ds", int(interval))
	diskLastStatTime = now

	for name, stat := range last {
		newstat := current[name]
		deltaReadCount := calcDelta(newstat.ReadCount, stat.ReadCount)
		deltaReadBytes := calcDelta(newstat.ReadBytes, stat.ReadBytes)
		deltaWriteCount := calcDelta(newstat.WriteCount, stat.WriteCount)
		deltaWriteBytes := calcDelta(newstat.WriteBytes, stat.WriteBytes)
		newstat.SpeedIORead = float64(deltaReadCount) / interval
		newstat.SpeedByteRead = float64(deltaReadBytes) / interval
		newstat.SpeedIOWrite = float64(deltaWriteCount) / interval
		newstat.SpeedByteWrite = float64(deltaWriteBytes) / interval
		deltaIOCompleted := deltaReadCount + deltaWriteCount

		deltaIOTime := calcDelta(newstat.IoTime, stat.IoTime)

		if deltaIOCompleted == 0 {
			newstat.Svctm = 0
			newstat.Await = 0
			newstat.AvgrqSz = 0
		} else {
			// svctm：delta(time spent doing I/Os)/ (delta(reads completed) + delta(writes completed))
			newstat.Svctm = float64(deltaIOTime) / float64(deltaIOCompleted)

			// await：(delta(time spent reading) + delta(time spent writing)) / (delta(reads completed) + delta(writes completed))
			deltaReadTime := calcDelta(newstat.ReadTime, stat.ReadTime)
			deltaWriteTime := calcDelta(newstat.WriteTime, stat.WriteTime)
			newstat.Await = float64(deltaReadTime+deltaWriteTime) / float64(deltaIOCompleted)

			// avgrq-sz：(delta(sectors read) + delta(sectors written)) / (delta(reads completed) + delta(writes completed))
			deltaReadSectors := calcDelta(newstat.ReadSectors, stat.ReadSectors)
			deltaWriteSectors := calcDelta(newstat.WriteSectors, stat.WriteSectors)
			newstat.AvgrqSz = float64(deltaReadSectors+deltaWriteSectors) / float64(deltaIOCompleted)
		}

		// avgqu-sz：delta(weighted time spent doing I/Os) / t / 1000
		deltaWeightedIoTime := calcDelta(newstat.WeightedIO, stat.WeightedIO)
		newstat.AvgquSz = float64(deltaWeightedIoTime) / interval / 1000.0

		// %util：delta(time spent doing I/Os) / t / 1000 * 100%
		// 如果是发现这个节点的IO时间超过了现实时间（部分系统有时间倒流的情况），这个是肯定有问题的，那么此时的使用率将会被设置为0
		if (deltaIOTime / 1000) > uint64(interval) {
			logger.Errorf("deltaIOTime->[%d] should not larger than interval->[%f], set to 0", deltaIOTime, interval)
			newstat.Util = 0
		} else {
			newstat.Util = float64(deltaIOTime) / interval / 1000.0
		}

		current[name] = newstat
	}
}
