// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"fmt"
	"strings"
)

const (
	// StaticReportFieldAll 表示上报当前全部静态资源字段。
	StaticReportFieldAll = "*"

	StaticReportFieldCPU      = "cpu"
	StaticReportFieldCPUTotal = "cpu.total"
	StaticReportFieldCPUModel = "cpu.model"

	StaticReportFieldDisk      = "disk"
	StaticReportFieldDiskTotal = "disk.total"

	StaticReportFieldMem      = "mem"
	StaticReportFieldMemTotal = "mem.total"

	StaticReportFieldNet               = "net"
	StaticReportFieldNetInterface      = "net.interface"
	StaticReportFieldNetInterfaceName  = "net.interface.name"
	StaticReportFieldNetInterfaceMac   = "net.interface.mac"
	StaticReportFieldNetInterfaceAddrs = "net.interface.addrs"

	StaticReportFieldSystem              = "system"
	StaticReportFieldSystemHostname      = "system.hostname"
	StaticReportFieldSystemOS            = "system.os"
	StaticReportFieldSystemArch          = "system.arch"
	StaticReportFieldSystemPlatform      = "system.platform"
	StaticReportFieldSystemPlatVer       = "system.platVer"
	StaticReportFieldSystemSysType       = "system.sysType"
	StaticReportFieldSystemKernelVersion = "system.kernelVersion"
)

var staticReportFieldExpansions = map[string][]string{
	StaticReportFieldAll: nil,

	StaticReportFieldCPU: {
		StaticReportFieldCPUTotal,
		StaticReportFieldCPUModel,
	},
	StaticReportFieldCPUTotal: {StaticReportFieldCPUTotal},
	StaticReportFieldCPUModel: {StaticReportFieldCPUModel},

	StaticReportFieldDisk:      {StaticReportFieldDiskTotal},
	StaticReportFieldDiskTotal: {StaticReportFieldDiskTotal},

	StaticReportFieldMem:      {StaticReportFieldMemTotal},
	StaticReportFieldMemTotal: {StaticReportFieldMemTotal},

	StaticReportFieldNet: {
		StaticReportFieldNetInterfaceName,
		StaticReportFieldNetInterfaceMac,
		StaticReportFieldNetInterfaceAddrs,
	},
	StaticReportFieldNetInterface: {
		StaticReportFieldNetInterfaceName,
		StaticReportFieldNetInterfaceMac,
		StaticReportFieldNetInterfaceAddrs,
	},
	StaticReportFieldNetInterfaceName:  {StaticReportFieldNetInterfaceName},
	StaticReportFieldNetInterfaceMac:   {StaticReportFieldNetInterfaceMac},
	StaticReportFieldNetInterfaceAddrs: {StaticReportFieldNetInterfaceAddrs},

	StaticReportFieldSystem: {
		StaticReportFieldSystemHostname,
		StaticReportFieldSystemOS,
		StaticReportFieldSystemArch,
		StaticReportFieldSystemPlatform,
		StaticReportFieldSystemPlatVer,
		StaticReportFieldSystemSysType,
		StaticReportFieldSystemKernelVersion,
	},
	StaticReportFieldSystemHostname:      {StaticReportFieldSystemHostname},
	StaticReportFieldSystemOS:            {StaticReportFieldSystemOS},
	StaticReportFieldSystemArch:          {StaticReportFieldSystemArch},
	StaticReportFieldSystemPlatform:      {StaticReportFieldSystemPlatform},
	StaticReportFieldSystemPlatVer:       {StaticReportFieldSystemPlatVer},
	StaticReportFieldSystemSysType:       {StaticReportFieldSystemSysType},
	StaticReportFieldSystemKernelVersion: {StaticReportFieldSystemKernelVersion},
}

// NormalizeStaticReportFields 将配置的字段路径展开为叶子字段集合。
// 返回 nil 表示上报全部字段。
func NormalizeStaticReportFields(fields []string) (map[string]struct{}, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	selected := make(map[string]struct{})
	all := false
	for _, raw := range fields {
		field := strings.TrimSpace(raw)
		if field == "" {
			continue
		}

		expanded, ok := staticReportFieldExpansions[field]
		if !ok {
			return nil, fmt.Errorf("unknown static report field %q", field)
		}
		if field == StaticReportFieldAll {
			all = true
			continue
		}
		for _, leaf := range expanded {
			selected[leaf] = struct{}{}
		}
	}

	if all {
		return nil, nil
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return selected, nil
}
