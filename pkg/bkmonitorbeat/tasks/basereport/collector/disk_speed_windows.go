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
	"fmt"
	"syscall"
	"unsafe"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// diskPerformance is an equivalent representation of DISK_PERFORMANCE in the Windows API.
// https://docs.microsoft.com/fr-fr/windows/win32/api/winioctl/ns-winioctl-disk_performance
type diskPerformance struct {
	BytesRead           int64
	BytesWritten        int64
	ReadTime            int64
	WriteTime           int64
	IdleTime            int64
	ReadCount           uint32
	WriteCount          uint32
	QueueDepth          uint32
	SplitCount          uint32
	QueryTime           int64
	StorageDeviceNumber uint32
	StorageManagerName  [8]uint16
	alignmentPadding    uint32 // necessary for 32bit support, see https://github.com/elastic/beats/pull/16553
}

func iOCountersWithContextIgnoreDriveError(ctx context.Context, names ...string) (map[string]disk.IOCountersStat, error) {
	// https://github.com/giampaolo/psutil/blob/544e9daa4f66a9f80d7bf6c7886d693ee42f0a13/psutil/arch/windows/disk.c#L83
	drivemap := make(map[string]disk.IOCountersStat, 0)
	var diskPerformance diskPerformance

	lpBuffer := make([]uint16, 254)
	lpBufferLen, err := windows.GetLogicalDriveStrings(uint32(len(lpBuffer)), &lpBuffer[0])
	if err != nil {
		return drivemap, err
	}
	for _, v := range lpBuffer[:lpBufferLen] {
		if 'A' <= v && v <= 'Z' {
			path := string(rune(v)) + ":"
			typepath, _ := windows.UTF16PtrFromString(path)
			typeret := windows.GetDriveType(typepath)
			if typeret == 0 {
				return drivemap, windows.GetLastError()
			}
			if typeret != windows.DRIVE_FIXED {
				continue
			}
			szDevice := fmt.Sprintf(`\\.\%s`, path)
			const IOCTL_DISK_PERFORMANCE = 0x70020
			h, err := windows.CreateFile(syscall.StringToUTF16Ptr(szDevice), 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				if err == windows.ERROR_FILE_NOT_FOUND {
					continue
				}
				return drivemap, err
			}
			defer windows.CloseHandle(h)

			var diskPerformanceSize uint32
			err = windows.DeviceIoControl(h, IOCTL_DISK_PERFORMANCE, nil, 0, (*byte)(unsafe.Pointer(&diskPerformance)), uint32(unsafe.Sizeof(diskPerformance)), &diskPerformanceSize, nil)
			if err != nil {
				// 忽略错误防止数据全部丢失
				logger.Errorf("get disk performance failed path: %s: %v", path, err)
				continue
			}
			drivemap[path] = disk.IOCountersStat{
				ReadBytes:  uint64(diskPerformance.BytesRead),
				WriteBytes: uint64(diskPerformance.BytesWritten),
				ReadCount:  uint64(diskPerformance.ReadCount),
				WriteCount: uint64(diskPerformance.WriteCount),
				ReadTime:   uint64(diskPerformance.ReadTime / 10000 / 1000), // convert to ms: https://github.com/giampaolo/psutil/issues/1012
				WriteTime:  uint64(diskPerformance.WriteTime / 10000 / 1000),
				Name:       path,
			}
		}
	}
	return drivemap, nil
}

func ioCountersIgnoreDriveError(names ...string) (map[string]disk.IOCountersStat, error) {
	return iOCountersWithContextIgnoreDriveError(context.Background(), names...)
}

func IOCounters() (map[string]BKDiskStats, error) {
	c, err := ioCountersIgnoreDriveError()
	if err != nil {
		return nil, err
	}

	ret := make(map[string]BKDiskStats)
	for k, v := range c {
		ret[k] = BKDiskStats{
			ReadCount:        v.ReadCount,
			MergedReadCount:  v.MergedReadCount,
			WriteCount:       v.WriteCount,
			MergedWriteCount: v.MergedWriteCount,
			ReadBytes:        v.ReadBytes,
			WriteBytes:       v.WriteBytes,
			ReadTime:         v.ReadTime,
			WriteTime:        v.WriteTime,
			IopsInProgress:   v.IopsInProgress,
			IoTime:           v.IoTime,
			WeightedIO:       v.WeightedIO,
			Name:             v.Name,
			SerialNumber:     v.SerialNumber,
			Label:            v.Label,
		}
	}

	return ret, nil
}

type BK_Win32_PerfFormattedData struct {
	Name                    string
	AvgDiskQueueLength      uint64 // avgqu-sz
	AvgDiskBytesPerTransfer uint64 // avgrq-sz
	AvgDiskSecPerTransfer   uint32 // seconds, svctm
	PercentIdleTime         uint64 // = 1 - util
	DiskReadsPerSec         uint32
	DiskReadBytesPerSec     uint64
	DiskWritesPerSec        uint32
	DiskWriteBytesPerSec    uint64
}

// win can get speed from system, no need to calc
func IOSpeed() (map[string]BKDiskStats, error) {
	ret := make(map[string]BKDiskStats, 0)
	var dst []BK_Win32_PerfFormattedData

	err := wmi.Query("SELECT * FROM Win32_PerfFormattedData_PerfDisk_LogicalDisk ", &dst)
	if err != nil {
		return ret, err
	}
	for _, d := range dst {
		if len(d.Name) > 3 { // not get _Total or Harddrive
			continue
		}

		// 避免出现异常值
		u := float64(100-d.PercentIdleTime) / 100.0
		if u > 1 {
			u = 0
		}
		ret[d.Name] = BKDiskStats{
			SpeedIORead:    float64(d.DiskReadsPerSec),
			SpeedByteRead:  float64(d.DiskReadBytesPerSec),
			SpeedIOWrite:   float64(d.DiskWritesPerSec),
			SpeedByteWrite: float64(d.DiskWriteBytesPerSec),
			Util:           u,
			AvgrqSz:        float64(d.AvgDiskBytesPerTransfer),
			AvgquSz:        float64(d.AvgDiskQueueLength),
			Svctm:          float64(d.AvgDiskSecPerTransfer),
		}
	}
	return ret, nil
}

// get speed from origin data
func GetDiskSpeed(last, current map[string]BKDiskStats) {
	iostats, err := IOSpeed()

	if err != nil {
		logger.Error("get Disk IOCounters fail")
		return
	}

	for name, stat := range iostats {
		currentStat := current[name]
		currentStat.SpeedIORead = stat.SpeedIORead
		currentStat.SpeedByteRead = stat.SpeedByteRead
		currentStat.SpeedIOWrite = stat.SpeedIOWrite
		currentStat.SpeedByteWrite = stat.SpeedByteWrite
		currentStat.Util = stat.Util
		currentStat.AvgrqSz = stat.AvgrqSz
		currentStat.AvgquSz = stat.AvgquSz
		currentStat.Await = stat.Await
		currentStat.Svctm = stat.Svctm
		current[name] = currentStat
	}
}
