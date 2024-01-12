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
	"strings"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// FilterDiskIoStats 过滤磁盘 io 信息
func FilterDiskIoStats(diskStats map[string]DiskStats, config configs.DiskConfig) map[string]DiskStats {
	resultDiskStats := make(map[string]DiskStats)
	partitions, err := disk.Partitions(true)
	if err != nil {
		logger.Errorf("get partitions failed, err: %s", err)
	}
diskStatsLoop:
	// 过滤掉 partition，只留下设备
	for name, diskStat := range diskStats {
		// windows 上报的是分区数据，所以这里的黑白名单是分区黑白名单
		if !checkBlackWhiteList(name, config.PartitionWhiteList, config.PartitionBlackList) {
			logger.Debugf("filtered disk io status by black-white list:%s", name)
			continue
		}
		// 校验分区类型
		for _, partition := range partitions {
			// 过滤文件系统类型黑白名单
			if partition.Device == name && !checkBlackWhiteList(strings.ToLower(partition.Fstype), config.FSTypeWhiteList, config.FSTypeBlackList) {
				logger.Debugf("filtered disk stats by fs type black-white list, device=%s, mountpoint=%s", partition.Device, partition.Mountpoint)
				continue diskStatsLoop
			}
		}
		resultDiskStats[name] = diskStat
	}
	return resultDiskStats
}

// FilterPartitions 过滤分区以及分区使用率信息
func FilterPartitions(partitionStats []disk.PartitionStat, config configs.DiskConfig) []disk.PartitionStat {
	resultPartitionStats := make([]disk.PartitionStat, 0, len(partitionStats))
	for _, partition := range partitionStats {

		// windows 上报数据只有分区概念，所以只验证分区黑白名单
		if !checkBlackWhiteList(partition.Device, config.PartitionWhiteList, config.PartitionBlackList) {
			logger.Debugf("filtered disk stats by partition black-white list, device=%s, mountpoint=%s", partition.Device, partition.Mountpoint)
			continue
		}
		// 过滤文件系统类型黑白名单
		if !checkBlackWhiteList(strings.ToLower(partition.Fstype), config.FSTypeWhiteList, config.FSTypeBlackList) {
			logger.Debugf("filtered disk stats by fs type black-white list, device=%s, mountpoint=%s", partition.Device, partition.Mountpoint)
			continue
		}

		resultPartitionStats = append(resultPartitionStats, partition)
	}
	return resultPartitionStats
}
