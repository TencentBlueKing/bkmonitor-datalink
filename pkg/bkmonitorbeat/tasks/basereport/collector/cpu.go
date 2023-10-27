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
	"time"

	"github.com/shirou/gopsutil/v3/cpu"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type CpuReport struct {
	Cpuinfo    []cpu.InfoStat  `json:"cpuinfo"`
	Usage      []float64       `json:"per_usage"`
	TotalUsage float64         `json:"total_usage"`
	Stat       []cpu.TimesStat `json:"per_stat"`
	TotalStat  cpu.TimesStat   `json:"total_stat"`
}

func GetCPUInfo(config configs.CpuConfig) (*CpuReport, error) {
	var report CpuReport
	var err error

	// 采样多次，取最大值
	// 规定的采集时间和采集次数，优先达到的为准
	var maxTotalUsage float64
	count := config.StatTimes
	ticker := time.NewTicker(config.StatPeriod)
	defer ticker.Stop()

	for {
		logger.Debug("collect cpu stat")

		var once CpuReport
		err := getCPUStatUsage(&once)
		if err != nil {
			logger.Errorf("get cpu usage stat fail")
			return nil, err
		}

		// select max cpu total usage report
		if once.TotalUsage >= maxTotalUsage {
			report = once
			maxTotalUsage = report.TotalUsage
		}

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}

	// collect once
	err = queryCpuInfo(&report, config.InfoPeriod, config.InfoTimeout)
	if err != nil {
		logger.Errorf("get CPU Info fail for->[%#v] but will still upload cpu usage info.", err)
	}

	// 判断是否需要将CPU的指令集去掉，降低上报的数据长度
	if !config.ReportCpuFlag {

		// 如果不必上报指令集，那么将所有的上报数据统一都改为空
		emptySlice := make([]string, 0)
		for index, cpuInfo := range report.Cpuinfo {
			cpuInfo.Flags = emptySlice
			// 需要将修改后的内容放回到slice当中
			report.Cpuinfo[index] = cpuInfo
		}

		logger.Debug("cpu flags is all clean.")
	}

	return &report, nil
}
