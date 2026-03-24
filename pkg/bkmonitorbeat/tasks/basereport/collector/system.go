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
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// add systemtype info
type BKInfoStat struct {
	*InfoStat
	SystemType string `json:"systemtype"`
}

type SystemReport struct {
	Info BKInfoStat `json:"info"`
}

// osSystemType 运行时不会发生变更 可以缓存
var osSystemType string

func GetSystemInfo() (*SystemReport, error) {
	var report SystemReport
	var err error

	infoStat, err := host.Info()
	if err != nil {
		logger.Error("get Host Info failed")
		return nil, err
	}
	report.Info.InfoStat = toInfoStat(infoStat)

	procsZombie, _ := numZombieProcs()
	report.Info.InfoStat.ProcsZombie = uint64(procsZombie)

	if osSystemType == "" {
		osSystemType = tasks.GetSystemType()
	}

	// get system type, 32-bit or 64-bit or unknown
	report.Info.SystemType = osSystemType
	return &report, nil
}

func numZombieProcs() (int, error) {
	procs, err := process.Processes()
	if err != nil {
		return 0, err
	}

	var total int
	for _, proc := range procs {
		status, err := proc.Status()
		if err != nil {
			continue
		}
		if len(status) > 0 && status[0] == process.Zombie {
			total++
		}
	}
	return total, nil
}

// InfoStat 从 gopsutil/host.go 中拷贝 补充额外 procsZombie 字段
type InfoStat struct {
	Hostname             string `json:"hostname"`
	Uptime               uint64 `json:"uptime"`
	BootTime             uint64 `json:"bootTime"`
	Procs                uint64 `json:"procs"`           // number of processes
	ProcsZombie          uint64 `json:"procsZombie"`     // number of zombie processes
	OS                   string `json:"os"`              // ex: freebsd, linux
	Platform             string `json:"platform"`        // ex: ubuntu, linuxmint
	PlatformFamily       string `json:"platformFamily"`  // ex: debian, rhel
	PlatformVersion      string `json:"platformVersion"` // version of the complete OS
	KernelVersion        string `json:"kernelVersion"`   // version of the OS kernel (if available)
	KernelArch           string `json:"kernelArch"`      // native cpu architecture queried at runtime, as returned by `uname -m` or empty string in case of error
	VirtualizationSystem string `json:"virtualizationSystem"`
	VirtualizationRole   string `json:"virtualizationRole"` // guest or host
	HostID               string `json:"hostId"`             // ex: uuid
}

func toInfoStat(origin *host.InfoStat) *InfoStat {
	return &InfoStat{
		Hostname:             origin.Hostname,
		Uptime:               origin.Uptime,
		BootTime:             origin.BootTime,
		Procs:                origin.Procs,
		OS:                   origin.OS,
		Platform:             origin.Platform,
		PlatformFamily:       origin.PlatformFamily,
		PlatformVersion:      origin.PlatformVersion,
		KernelVersion:        origin.KernelVersion,
		KernelArch:           origin.KernelArch,
		VirtualizationSystem: origin.VirtualizationSystem,
		VirtualizationRole:   origin.VirtualizationRole,
		HostID:               origin.HostID,
	}
}
