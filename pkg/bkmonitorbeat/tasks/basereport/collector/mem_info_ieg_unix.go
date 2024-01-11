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
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// getIEGMemInfo: 从/dev/shm/ieg_dockervm_meminfo文件读取内存信息，格式如下：
// MemTotal: 62914560 kB
// MemFree: 3803636 kB
// MemUsed: 59110924 kB
// MemApp: 33181796 kB

const iegDockervmMeminfo = "/dev/shm/ieg_dockervm_meminfo"

func getIEGMemInfo(info *mem.VirtualMemoryStat) *mem.VirtualMemoryStat {
	// 判断文件是否存在，如果不存在直接退出返回信息
	if _, err := os.Stat(iegDockervmMeminfo); os.IsNotExist(err) {
		logger.Debugf("internal docker mem file->[%s] not exists, nothing will do", iegDockervmMeminfo)
		return info
	}

	file, err := os.Open(iegDockervmMeminfo)
	if err != nil {
		logger.Errorf("failed to open internal docker men file->[%s], err: %v", iegDockervmMeminfo, err)
		return info
	}
	defer func() {
		_ = file.Close()
	}()

	// 逐行的进行解析
	var (
		total        uint64
		free         uint64
		totalUsed    uint64
		appUsed      uint64
		successCount int
	)

	for scanner := bufio.NewScanner(file); scanner.Scan(); {
		line := scanner.Text()
		parts := strings.Fields(line)

		// 如果每行内容少于2的，直接跳过处理
		if len(parts) != 2 && len(parts) != 3 {
			logger.Errorf("internal mem got invalid value in mem info->[%s] which is not 2 and 3 parts", line)
			continue
		}

		// 是否可以正常转换
		memValueKB, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			logger.Errorf("internal mem got wrong mem value->[%s] which cannot transfer for->[%s]", parts[1], err)
			continue
		}
		memValueByte := memValueKB * 1024
		logger.Debugf("internal mem got line->[%s] transfer to value->[%d] byte and key->[%s]", line, memValueByte, parts[0])

		// 3. 将文件中的内容解析并放置到info中
		switch parts[0] {
		case "MemTotal:":
			total = memValueByte
			successCount += 1
		case "MemFree:":
			free = memValueByte
			successCount += 1
		case "MemUsed:":
			// 此处内存包含了 进程内存 + buff + cache
			totalUsed = memValueByte
			successCount += 1
		case "MemApp:":
			// 此处内存仅包含有 进程内存
			appUsed = memValueByte
			successCount += 1
		}
	}

	if successCount != 4 {
		logger.Errorf("failed to get success count to 4 but->[%d] will not update mem info", successCount)
		return info
	}

	// 需要额外对部分数据进行重新的处理
	info.Total = total
	info.Free = free
	// 此处的内存 = Free + buff + cache
	info.Available = info.Free + (totalUsed - appUsed)
	// 内存其他的依赖项需要重新计算
	info.Used = info.Total - info.Available
	info.UsedPercent = float64(info.Total-info.Available) / float64(info.Total) * 100.0
	logger.Infof("mem info updated, info=%+v", info)

	return info
}
