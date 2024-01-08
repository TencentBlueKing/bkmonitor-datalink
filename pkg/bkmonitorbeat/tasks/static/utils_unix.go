// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package static

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// GetDiskStatus linux相比windows,多了去除虚拟挂载的逻辑
var GetDiskStatus = func(ctx context.Context) (*Disk, error) {
	infos, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return nil, err
	}

	// 获取真实磁盘列表
	diskSet := GetDisks()
	var total uint64 = 0
	repeatMap := make(map[string]bool)
	for _, info := range infos {
		// 非真实设备不录入
		if !diskSet.Exist(info.Device) {
			//判断是不是/dev/mapper下的文件
			if !strings.Contains(info.Device, "/dev/mapper") {
				continue
			}
			//获取软连接的源文件
			infoSymlink, err := filepath.EvalSymlinks(info.Device)
			if err != nil {
				logger.Warnf("failed to get file symlink info for: %s", err)
				continue
			}
			// infoSymlink = ../dm-0
			infoSymlink = strings.Replace(infoSymlink, "..", "/dev", 1)
			if !diskSet.Exist(infoSymlink) {
				continue
			}
		}
		// 重复的设备去重
		if _, ok := repeatMap[info.Device]; ok {
			logger.Debugf("get repeat device:%s with mountpoint:%s,skip", info.Device, info.Mountpoint)
			continue
		}
		repeatMap[info.Device] = true
		// 获取total
		usage, err := disk.UsageWithContext(ctx, info.Mountpoint)
		if err != nil {
			logger.Warnf("failed to get usage info for: %s", err)
			continue
		}
		total = total + usage.Total

	}
	return &Disk{
		Total: total,
	}, nil
}

// GetDisks :
var GetDisks = func() common.Set {
	set := common.NewSet()
	path := "/proc/partitions"
	data, err := os.ReadFile(path)
	if err != nil {
		// freebsd无此文件，使用gopsutils封装的方法
		if os.IsNotExist(err) {
			partitions, err := disk.Partitions(true)
			if err != nil {
				return set
			}
			for _, partition := range partitions {
				set.Insert(partition.Device)
			}
			return set
		}
		logger.Errorf("read file:%s,failed,error:%s", path, err)
		return set
	}
	lines := bytes.Split(data, []byte("\n"))
	for idx, line := range lines {
		// 跳过第一行和空行
		if len(line) == 0 || idx == 0 {
			continue
		}

		fields := bytes.Fields(line)
		if len(fields) == 4 {
			set.Insert("/dev/" + string(fields[3]))
		}
	}
	return set
}

// GetVirtualInterfaceSet 获取虚拟网卡列表
func GetVirtualInterfaceSet() (common.Set, error) {
	interfaceSet := common.NewSet()
	fileList, err := os.ReadDir("/sys/devices/virtual/net")
	if err != nil {
		// freebsd无此文件，使用gopsutils封装的方法
		if os.IsNotExist(err) {
			interfaces, err := net.Interfaces()
			if err != nil {
				return interfaceSet, err
			}
			for _, i := range interfaces {
				interfaceSet.Insert(i.Name)
			}
			return interfaceSet, nil
		}
		return interfaceSet, err
	}
	for _, file := range fileList {
		interfaceSet.Insert(file.Name())
	}
	return interfaceSet, nil
}
