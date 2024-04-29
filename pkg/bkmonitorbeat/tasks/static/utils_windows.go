// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows
// +build windows

package static

import (
	"context"
	"regexp"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// GetVirtualInterfaceSet 获取虚拟网卡列表,windows下只拿个空列表
func GetVirtualInterfaceSet() (common.Set, error) {
	interfaceSet := common.NewSet()
	return interfaceSet, nil
}

// GetDiskStatus :
var GetDiskStatus = func(ctx context.Context, cfg *configs.StaticTaskConfig) (*Disk, error) {
	infos, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		logger.Errorf("failed to get disk info for: %s", err)
		return nil, err
	}

	var blacklist []*regexp.Regexp
	for i := 0; i < len(cfg.MountpointBlackList); i++ {
		r, err := regexp.Compile(cfg.MountpointBlackList[i])
		if err != nil {
			continue
		}
		blacklist = append(blacklist, r)
	}

	var total uint64 = 0
	partitions := make([]DiskPartition, 0)
	repeatMap := make(map[string]bool)
	for _, info := range infos {
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

		// 黑名单过滤不需要的挂载点
		var matched bool
		for _, rule := range blacklist {
			if rule.MatchString(info.Mountpoint) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		partitions = append(partitions, DiskPartition{
			Total:      usage.Total,
			MountPoint: info.Mountpoint,
			FileSystem: info.Fstype,
		})
	}
	return &Disk{
		Total:      total,
		Partitions: partitions,
	}, nil
}
