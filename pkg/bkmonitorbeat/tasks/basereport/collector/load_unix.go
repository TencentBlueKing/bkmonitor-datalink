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
	"github.com/pkg/errors"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/load"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// GetLoadInfo 获取 cpu 信息
func GetLoadInfo() (*LoadReport, error) {
	var report LoadReport
	var err error

	report.LoadAvg, err = load.Avg()
	if err != nil {
		return nil, err
	}

	// per_cpu_load = load1/cpu 总核数
	cores, err := GetCpuCores()
	if cores == 0 || err != nil {
		return &report, errors.Wrap(err, "failed to get cpu cores")
	}
	report.PerCpuLoad = report.LoadAvg.Load1 / float64(cores)

	return &report, nil
}

// GetCpuCores 获取 CPU 核心数
func GetCpuCores() (int32, error) {
	var cores int32
	infos, err := cpu.Info()
	if err != nil {
		return 0, err
	}
	for _, info := range infos {
		cores += info.Cores
	}
	logger.Debugf("cpu cores: %v", cores)
	return cores, nil
}
