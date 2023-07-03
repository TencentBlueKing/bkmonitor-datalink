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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
	"golang.org/x/sys/unix"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var partitionExp = regexp.MustCompile("^.*?([A-Za-z]+)[0-9]+$")

var devPartitionExp = regexp.MustCompile("^/dev/(.+)$")

var basicSysDevPath = "/sys/dev/block"

var partitionPath = "partition"

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

func isPartition(diskStat BKDiskStats) bool {
	deviceCode := strconv.FormatUint(diskStat.MajorNum, 10) + ":" + strconv.FormatUint(diskStat.MinorNum, 10)
	devPath := filepath.Join(basicSysDevPath, deviceCode, partitionPath)
	// 存在partition文件，说明是分区
	if fileExist(devPath) {
		return true
	}
	// 否则根据设备名称判断是否为分区
	return partitionExp.MatchString(diskStat.Name)
}

// FilterDiskIoStats 过滤磁盘io信息
func FilterDiskIoStats(diskStats map[string]BKDiskStats, config configs.DiskConfig) map[string]BKDiskStats {
	resultDiskStats := make(map[string]BKDiskStats)
	partitions, err := disk.Partitions(true)
	if err != nil {
		logger.Errorf("get partitions failed,error:%s", err)
	}

diskStatsLoop:
	for name, diskStat := range diskStats {
		// 判断是否是分区，不是分区则默认为设备
		if !isPartition(diskStat) {
			// 先通过黑白名单过滤设备层级数据
			if !CheckBlackWhiteList(name, config.DiskWhiteList, config.DiskBlackList) {
				logger.Debugf("filtered disk io status by black-white list:%s", name)
				continue
			}
		} else {
			// 针对分区io上报做处理
			// 是否跳过分区上报
			if config.IOSkipPartition {
				logger.Debugf("filtered partition level disk io status:%s", name)
				continue
			}
			// 过滤所属设备
			// sda1 => sda
			diskName := getDiskName(name)
			if !CheckBlackWhiteList(diskName, config.DiskWhiteList, config.DiskBlackList) {
				logger.Debugf("filtered disk io status by black-white list:%s", name)
				continue
			}
			// 不跳过partition的话，就要过滤对应黑白名单
			if !CheckBlackWhiteList(name, config.PartitionWhiteList, config.PartitionBlackList) {
				logger.Debugf("filtered disk io status by black-white list:%s", name)
				continue
			}

			// 校验分区类型
			for _, partition := range partitions {
				// /dev/sda1 => sda1
				devName := getDevName(partition.Device)
				// 过滤文件系统类型黑白名单
				if devName == name && !CheckBlackWhiteList(partition.Fstype, config.FSTypeWhiteList, config.FSTypeBlackList) {
					logger.Debugf("filtered disk partition and usage by fs type black-white list,device:%s,mountpoint:%s", partition.Device, partition.Mountpoint)
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
	submatchList := partitionExp.FindStringSubmatch(name)
	// 遇到匹配失败的异常情况，则忽略掉disk过滤
	if len(submatchList) == 2 {
		diskName := submatchList[1]
		// disk层级黑白名单
		return diskName
	}
	logger.Warnf("get disk name from device name failed,submatch length not as expected,device name:%s", name)
	return ""
}

func getDevName(name string) string {
	// 使用正则匹配partition名 例: /dev/sda1 => sda1
	submatchList := devPartitionExp.FindStringSubmatch(name)
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

func getDiskSerialNumberWithContext(ctx context.Context, name string) string {
	var stat unix.Stat_t
	err := unix.Stat(name, &stat)
	if err != nil {
		return ""
	}
	major := unix.Major(uint64(stat.Rdev))
	minor := unix.Minor(uint64(stat.Rdev))

	// Try to get the serial from udev data
	udevDataPath := hostRun(fmt.Sprintf("udev/data/b%d:%d", major, minor))
	if udevdata, err := os.ReadFile(udevDataPath); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(udevdata))
		for scanner.Scan() {
			values := strings.Split(scanner.Text(), "=")
			if len(values) == 2 && values[0] == "E:ID_SERIAL" {
				return values[1]
			}
		}
	}

	// Try to get the serial from sysfs, look at the disk device (minor 0) directly
	// because if it is a partition it is not going to contain any device information
	devicePath := hostSys(fmt.Sprintf("dev/block/%d:0/device", major))
	model, _ := os.ReadFile(filepath.Join(devicePath, "model"))
	serial, _ := os.ReadFile(filepath.Join(devicePath, "serial"))
	if len(model) > 0 && len(serial) > 0 {
		return fmt.Sprintf("%s_%s", string(model), string(serial))
	}
	return ""
}

// ioCountersWithContextBySyscall 通过系统调用获取io信息
func ioCountersWithContextBySyscall(ctx context.Context, names ...string) (map[string]BKDiskStats, error) {
	stats, err := disk.IOCountersWithContext(ctx, names...)
	if err != nil {
		return nil, err
	}
	ret := make(map[string]BKDiskStats)
	for s, stat := range stats {
		ret[s] = BKDiskStats{
			ReadCount:        stat.ReadCount,
			MergedReadCount:  stat.MergedReadCount,
			WriteCount:       stat.WriteCount,
			MergedWriteCount: stat.MergedWriteCount,
			ReadBytes:        stat.ReadBytes,
			WriteBytes:       stat.WriteBytes,
			ReadTime:         stat.ReadTime,
			WriteTime:        stat.WriteTime,
			IopsInProgress:   stat.IopsInProgress,
			IoTime:           stat.IoTime,
			WeightedIO:       stat.WeightedIO,
			Name:             stat.Name,
			SerialNumber:     stat.SerialNumber,
			Label:            stat.Label,
		}
	}
	return ret, nil
}

func parseBKDistStatsFromLine(line string, names []string) (BKDiskStats, string, error) {
	empty := BKDiskStats{}
	fields := strings.Fields(line)
	if len(fields) < 14 {
		// malformed line in /proc/diskstats, avoid panic by ignoring.
		return empty, "", nil
	}
	name := fields[2]

	if len(names) > 0 && !stringsHas(names, name) {
		return empty, "", nil
	}

	major, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return empty, "", err
	}
	minor, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return empty, "", err
	}
	reads, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return empty, "", err
	}
	mergedReads, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return empty, "", err
	}
	rbytes, err := strconv.ParseUint(fields[5], 10, 64)
	if err != nil {
		return empty, "", err
	}
	rtime, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		return empty, "", err
	}
	writes, err := strconv.ParseUint(fields[7], 10, 64)
	if err != nil {
		return empty, "", err
	}
	mergedWrites, err := strconv.ParseUint(fields[8], 10, 64)
	if err != nil {
		return empty, "", err
	}
	wbytes, err := strconv.ParseUint(fields[9], 10, 64)
	if err != nil {
		return empty, "", err
	}
	wtime, err := strconv.ParseUint(fields[10], 10, 64)
	if err != nil {
		return empty, "", err
	}
	iopsInProgress, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return empty, "", err
	}
	iotime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return empty, "", err
	}
	weightedIO, err := strconv.ParseUint(fields[13], 10, 64)
	if err != nil {
		return empty, "", err
	}
	d := BKDiskStats{
		ReadSectors:      rbytes,
		WriteSectors:     wbytes,
		ReadBytes:        rbytes * SectorSize,
		WriteBytes:       wbytes * SectorSize,
		ReadCount:        reads,
		WriteCount:       writes,
		MergedReadCount:  mergedReads,
		MergedWriteCount: mergedWrites,
		ReadTime:         rtime,
		WriteTime:        wtime,
		IopsInProgress:   iopsInProgress,
		IoTime:           iotime,
		WeightedIO:       weightedIO,
	}
	if d == empty {
		return empty, "", err
	}
	d.Name = name
	d.MajorNum = major
	d.MinorNum = minor

	d.SerialNumber = getDiskSerialNumberWithContext(context.Background(), name)
	d.Label = getLabel(name)
	return d, name, nil
}

func IOCountersWithContext(ctx context.Context, names ...string) (map[string]BKDiskStats, error) {
	filename := hostProc("diskstats")
	// freebsd无此文件，使用gopsutils封装的系统调用获取
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return ioCountersWithContextBySyscall(ctx, names...)
	}
	lines, err := readLinesOffsetN(filename, 0, -1)
	if err != nil {
		return nil, err
	}
	ret := make(map[string]BKDiskStats, 0)

	// use only basename such as "/dev/sda1" to "sda1"
	for i, name := range names {
		names[i] = filepath.Base(name)
	}

	for _, line := range lines {
		d, name, err := parseBKDistStatsFromLine(line, names)
		if err != nil {
			return ret, err
		}
		if name == "" {
			continue
		}

		ret[name] = d
	}
	return ret, nil
}

func IOCounters(names ...string) (map[string]BKDiskStats, error) {
	return IOCountersWithContext(context.Background(), names...)
}
