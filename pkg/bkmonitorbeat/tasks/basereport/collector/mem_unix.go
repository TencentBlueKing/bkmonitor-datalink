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
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func PhysicalMemoryInfo(specialSource bool) (*mem.VirtualMemoryStat, error) {
	// 原本的方案 usedPercent = total-available/total
	info, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	// 内部特殊富容器的监控支持
	getIEGMemInfo(info)
	if !specialSource {
		return info, nil
	}

	// 使用特殊方案时，usedPercent = Used/total
	info.UsedPercent = float64(info.Used) / float64(info.Total) * 100.0
	return info, nil
}

func GetSwapInfo() (float64, float64, error) {
	first, err := getSwapInfo()
	if err != nil {
		return 0, 0, err
	}

	time.Sleep(1 * time.Second)
	second, err := getSwapInfo()
	if err != nil {
		return 0, 0, err
	}

	logger.Debugf("first swapinfo: %+v, second swapinfo: %+v", first, second)
	in := float64(second.Sin - first.Sin)
	out := float64(second.Sout - first.Sout)
	return in, out, nil
}

type swapInfo struct {
	Sin  uint64
	Sout uint64
}

func getSwapInfo() (*swapInfo, error) {
	var si swapInfo
	swapMemoryStat, err := mem.SwapMemory()
	if err != nil {
		if strings.Contains(err.Error(), "no swap devices") {
			return &si, nil
		}
		return nil, err
	}

	const kb uint64 = 1024
	si.Sin = swapMemoryStat.Sin / kb
	si.Sout = swapMemoryStat.Sout / kb
	return &si, nil
}
