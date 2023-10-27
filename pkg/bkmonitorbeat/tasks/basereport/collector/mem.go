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
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

type MemReport struct {
	Swapin   float64                `json:"swap_in"`
	Swapout  float64                `json:"swap_out"`
	Info     *mem.VirtualMemoryStat `json:"meminfo"`
	SwapInfo *mem.SwapMemoryStat    `json:"vmstat"`
}

func GetMemInfo(config configs.MemConfig) (*MemReport, error) {
	var report MemReport
	var err error

	// 采样多次，取最大值
	var maxUsedPercent float64
	count := config.InfoTimes
	ticker := time.NewTicker(config.InfoPeriod)
	defer ticker.Stop()

	for {
		var once MemReport
		once.Info, err = PhysicalMemoryInfo(config.SpecialSource)
		if err != nil {
			return nil, err
		}

		if once.Info.UsedPercent < 0 || once.Info.UsedPercent > 100 {
			once.Info.UsedPercent = 0
		}

		// select max usage report
		if once.Info.UsedPercent >= maxUsedPercent {
			report = once
			maxUsedPercent = once.Info.UsedPercent
			report.SwapInfo, err = mem.SwapMemory()
			if err != nil {
				if !strings.Contains(err.Error(), "no swap devices") {
					return nil, err
				}
			}
		}

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}

	report.Swapin, report.Swapout, err = GetSwapInfo()
	if err != nil {
		return &report, err
	}

	return &report, nil
}
