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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	partitionRegex    = regexp.MustCompile("^.*?([A-Za-z]+)[0-9]+$")
	devPartitionRegex = regexp.MustCompile("^/dev/(.+)$")
)

const (
	basicSysDevPath = "/sys/dev/block"
	partitionPath   = "partition"
)

func fileExist(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}

func formatMountPoint(mountpoint string) string {
	spaceCode := "\\040"
	if strings.Index(mountpoint, spaceCode) != -1 {
		mountpoint = strings.ReplaceAll(mountpoint, spaceCode, " ")
	}
	return mountpoint
}

func isPartition(diskStat DiskStats) bool {
	deviceCode := strconv.FormatUint(diskStat.MajorNum, 10) + ":" + strconv.FormatUint(diskStat.MinorNum, 10)
	devPath := filepath.Join(basicSysDevPath, deviceCode, partitionPath)
	// 存在partition文件，说明是分区
	if fileExist(devPath) {
		return true
	}
	// 否则根据设备名称判断是否为分区
	return partitionRegex.MatchString(diskStat.Name)
}

// FilterDiskIoStats 过滤磁盘io信息
func FilterDiskIoStats(diskStats map[string]DiskStats, config configs.DiskConfig) map[string]DiskStats {
	resultDiskStats := make(map[string]DiskStats)
	partitions, err := disk.Partitions(true)
	if err != nil {
		logger.Errorf("get partitions failed, error: %s", err)
	}

diskStatsLoop:
	for name, diskStat := range diskStats {
		// 判断是否是分区，不是分区则默认为设备
		if !isPartition(diskStat) {
			// 先通过黑白名单过滤设备层级数据
			if !CheckBlackWhiteList(name, config.DiskWhiteList, config.DiskBlackList) {
				logger.Debugf("filtered disk io status by black-white list: %s", name)
				continue
			}
		} else {
			// 针对分区io上报做处理
			// 是否跳过分区上报
			if config.IOSkipPartition {
				logger.Debugf("filtered partition level disk io status: %s", name)
				continue
			}
			// 过滤所属设备
			// sda1 => sda
			diskName := getDiskName(name)
			if !CheckBlackWhiteList(diskName, config.DiskWhiteList, config.DiskBlackList) {
				logger.Debugf("filtered disk io status by black-white list: %s", name)
				continue
			}
			// 不跳过partition的话，就要过滤对应黑白名单
			if !CheckBlackWhiteList(name, config.PartitionWhiteList, config.PartitionBlackList) {
				logger.Debugf("filtered disk io status by black-white list: %s", name)
				continue
			}

			// 校验分区类型
			for _, partition := range partitions {
				// /dev/sda1 => sda1
				devName := getDevName(partition.Device)
				// 过滤文件系统类型黑白名单
				if devName == name && !CheckBlackWhiteList(partition.Fstype, config.FSTypeWhiteList, config.FSTypeBlackList) {
					logger.Debugf("filtered disk partition by black-white list, device: %s, mountpoint: %s", partition.Device, partition.Mountpoint)
					continue diskStatsLoop
				}
			}
		}
		resultDiskStats[name] = diskStat
	}
	return resultDiskStats
}

func getDiskName(name string) string {
	// 使用正则匹配disk名 例: sda1 => sda
	submatchList := partitionRegex.FindStringSubmatch(name)
	// 遇到匹配失败的异常情况，则忽略掉disk过滤
	if len(submatchList) == 2 {
		diskName := submatchList[1]
		// disk层级黑白名单
		return diskName
	}
	logger.Warnf("get disk name from device name failed, submatch length not as expected, device name: %s", name)
	return ""
}

func getDevName(name string) string {
	// 使用正则匹配partition名 例: /dev/sda1 => sda1
	submatchList := devPartitionRegex.FindStringSubmatch(name)
	// 遇到匹配失败的异常情况，则忽略掉disk过滤
	if len(submatchList) == 2 {
		partitionName := submatchList[1]
		// disk层级黑白名单
		return partitionName
	}
	return name
}

// FilterPartitions :
func FilterPartitions(partitionStats []disk.PartitionStat, config configs.DiskConfig) []disk.PartitionStat {
	deviceMap := make(map[string]bool)
	resultPartitionStats := make([]disk.PartitionStat, 0, len(partitionStats))
	for _, partition := range partitionStats {
		// 处理mountpoint路径上有特殊字符的情况
		mountpoint := formatMountPoint(partition.Mountpoint)
		partition.Mountpoint = mountpoint

		// sda1 => sda
		diskName := getDiskName(partition.Device)
		if diskName != "" {
			if !CheckBlackWhiteList(diskName, config.DiskWhiteList, config.DiskBlackList) {
				logger.Debugf("filtered disk partition and usage by disk black-white list,device:%s,mountpoint:%s", partition.Device, partition.Mountpoint)
				continue
			}
		}

		// /dev/sda1 => sda1
		devName := getDevName(partition.Device)
		// device(partition)层级黑白名单
		if !CheckBlackWhiteList(devName, config.PartitionWhiteList, config.PartitionBlackList) {
			logger.Debugf("filtered disk partition and usage by partition black-white list,device:%s,mountpoint:%s", partition.Device, partition.Mountpoint)
			continue
		}
		// mountpoint层级黑白名单
		if !CheckBlackWhiteList(partition.Mountpoint, config.MountpointWhiteList, config.MountpointBlackList) {
			logger.Debugf("filtered disk partition and usage by mountpoint black-white list,device:%s,mountpoint:%s", partition.Device, partition.Mountpoint)
			continue
		}

		// 过滤文件系统类型黑白名单
		if !CheckBlackWhiteList(partition.Fstype, config.FSTypeWhiteList, config.FSTypeBlackList) {
			logger.Debugf("filtered disk partition and usage by fs type black-white list,device:%s,mountpoint:%s", partition.Device, partition.Mountpoint)
			continue
		}

		// device上报去重
		if config.DropDuplicateDevice {
			if _, ok := deviceMap[partition.Device]; ok {
				logger.Debugf("duplicate device name:%s,which mountpoint is:%s,dropped", partition.Device, partition.Mountpoint)
				continue
			}
			deviceMap[partition.Device] = true
		}
		resultPartitionStats = append(resultPartitionStats, partition)
	}
	return resultPartitionStats
}
