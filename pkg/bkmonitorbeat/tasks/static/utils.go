// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package static

import (
	"context"
	"math/rand"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/monitoring/report/bkpipe"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Report 主机静态数据集合
type Report struct {
	CPU    *CPU
	Memory *Memory
	Disk   *Disk
	Net    *Net
	System *System

	reportFields map[string]struct{}
}

// NewReport :
func NewReport(cpu *CPU, mem *Memory, disk *Disk, net *Net, system *System) *Report {
	return &Report{
		CPU:    cpu,
		Memory: mem,
		Disk:   disk,
		Net:    net,
		System: system,
	}
}

// AsMapStr :
func (r *Report) AsMapStr() common.MapStr {
	result := make(common.MapStr)
	if r.CPU != nil {
		cpu := make(common.MapStr)
		if r.includeReportField(configs.StaticReportFieldCPUTotal) {
			cpu["total"] = r.CPU.Total
		}
		if r.includeReportField(configs.StaticReportFieldCPUModel) {
			cpu["model"] = r.CPU.Model
		}
		if len(cpu) > 0 {
			result["cpu"] = cpu
		}
	}

	if r.Disk != nil && r.includeReportField(configs.StaticReportFieldDiskTotal) {
		result["disk"] = common.MapStr{
			"total": r.Disk.Total,
		}
	}

	if r.Memory != nil && r.includeReportField(configs.StaticReportFieldMemTotal) {
		result["mem"] = common.MapStr{
			"total": r.Memory.Total,
		}
	}

	if r.Net != nil {
		interfaces := make([]common.MapStr, 0, len(r.Net.Interface))
		for _, inter := range r.Net.Interface {
			item := make(common.MapStr)
			if r.includeReportField(configs.StaticReportFieldNetInterfaceAddrs) {
				item["addrs"] = inter.Addrs
			}
			if r.includeReportField(configs.StaticReportFieldNetInterfaceMac) {
				item["mac"] = inter.Mac
			}
			if r.includeReportField(configs.StaticReportFieldNetInterfaceName) {
				item["name"] = inter.Name
			}
			if len(item) > 0 {
				interfaces = append(interfaces, item)
			}
		}
		if r.includeReportField(configs.StaticReportFieldNetInterfaceAddrs) ||
			r.includeReportField(configs.StaticReportFieldNetInterfaceMac) ||
			r.includeReportField(configs.StaticReportFieldNetInterfaceName) {
			result["net"] = common.MapStr{
				"interface": interfaces,
			}
		}
	}

	if r.System != nil {
		system := make(common.MapStr)
		if r.includeReportField(configs.StaticReportFieldSystemHostname) {
			system["hostname"] = r.System.HostName
		}
		if r.includeReportField(configs.StaticReportFieldSystemOS) {
			system["os"] = r.System.OS
		}
		if r.includeReportField(configs.StaticReportFieldSystemArch) {
			arch := "x86"
			if r.System.Arch == "arm" || r.System.Arch == "aarch64" {
				arch = "arm"
			}
			system["arch"] = arch
		}
		if r.includeReportField(configs.StaticReportFieldSystemPlatform) {
			system["platform"] = r.System.Platform
		}
		if r.includeReportField(configs.StaticReportFieldSystemPlatVer) {
			system["platVer"] = r.System.PlatVer
		}
		if r.includeReportField(configs.StaticReportFieldSystemSysType) {
			system["sysType"] = r.System.SysType
		}
		if r.includeReportField(configs.StaticReportFieldSystemKernelVersion) {
			system["kernelVersion"] = r.System.KernelVersion
		}
		if len(system) > 0 {
			result["system"] = system
		}
	}
	return result
}

// SetReportFields 设置需要上报的字段。传空或包含 * 时上报全部字段。
func (r *Report) SetReportFields(fields []string) error {
	selected, err := configs.NormalizeStaticReportFields(fields)
	if err != nil {
		return err
	}
	r.reportFields = selected
	return nil
}

func (r *Report) includeReportField(field string) bool {
	if r.reportFields == nil {
		return true
	}
	_, ok := r.reportFields[field]
	return ok
}

// CPU :
type CPU struct {
	Total int
	Model string
}

// Memory :
type Memory struct {
	Total uint64
}

// Disk :
type Disk struct {
	Total uint64
}

// Net :
type Net struct {
	Interface []Interface
}

// Interface :
type Interface struct {
	Addrs []string
	Mac   string
	Name  string
}

// System :
type System struct {
	HostName      string
	OS            string
	Platform      string
	PlatVer       string
	SysType       string
	BKAgentID     string
	Arch          string
	KernelVersion string
}

// GetData 采集全部静态数据
var GetData = func(ctx context.Context, cfg *configs.StaticTaskConfig) (*Report, error) {
	cpu, err := GetCPUStatus(ctx)
	if err != nil {
		logger.Errorf("failed to get cpu status: %v", err)
	}

	mem, err := GetMemoryStatus(ctx)
	if err != nil {
		logger.Errorf("failed to get mem status: %v", err)
	}

	disk, err := GetDiskStatus(ctx)
	if err != nil {
		logger.Errorf("failed to get disk status: %v", err)
	}

	net, err := GetNetStatus(ctx, cfg)
	if err != nil {
		logger.Errorf("failed to get net status: %v", err)
	}

	system, err := GetSystemStatus(ctx)
	if err != nil {
		logger.Errorf("failed to get system status: %v", err)
	}
	logger.Debug("collect report data success")
	report := NewReport(cpu, mem, disk, net, system)
	if cfg != nil {
		if err = report.SetReportFields(cfg.ReportFields); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// GetCPUStatus :
var GetCPUStatus = func(ctx context.Context) (*CPU, error) {
	infos, err := cpu.InfoWithContext(ctx)
	if err != nil {
		return nil, err
	}

	total := len(infos)
	model := ""
	if total == 0 {
		model = "unknown"
	} else {
		model = infos[0].ModelName
	}

	// gopsutil cpu 返回的信息 在不同平台不一样 需要进行区别操作
	if runtime.GOOS == "windows" {
		n := 0
		for _, info := range infos {
			n += int(info.Cores) // NumberOfLogicalProcessors
		}
		total = n
	}
	return &CPU{Total: total, Model: model}, nil
}

// GetMemoryStatus :
var GetMemoryStatus = func(ctx context.Context) (*Memory, error) {
	info, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, err
	}
	return &Memory{Total: info.Total}, nil
}

func checkBlacklist(name string, blacklist []string) bool {
	for _, pattern := range blacklist {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

// GetNetStatus :
var GetNetStatus = func(ctx context.Context, cfg *configs.StaticTaskConfig) (*Net, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	virtualInterfaces, err := GetVirtualInterfaceSet()
	if err != nil {
		return nil, err
	}

	whiteList := make(map[string]struct{})
	if cfg != nil && len(cfg.VirtualIfaceWhitelist) > 0 {
		logger.Debug("VirtualIfaceWhitelist: %v", cfg.VirtualIfaceWhitelist)

		for _, iface := range cfg.VirtualIfaceWhitelist {
			whiteList[iface] = struct{}{}
		}
	}

	items := make([]Interface, 0, len(interfaces))
	for _, inter := range interfaces {
		// 排除虚拟网卡（有额外白名单机制）
		if virtualInterfaces.Exist(inter.Name) {
			if _, ok := whiteList[inter.Name]; !ok {
				continue
			}

			logger.Debug("Hit whitelist: %s", inter.Name)
		}

		// 排除黑名单网卡
		if cfg != nil && len(cfg.VirtualIfaceBlacklist) > 0 {
			logger.Debug("VirtualIfaceBlacklist: %v", cfg.VirtualIfaceBlacklist)
			if checkBlacklist(inter.Name, cfg.VirtualIfaceBlacklist) {
				logger.Debug("Hit blacklist: %s", inter.Name)
				continue
			}
		}

		addrs, err := inter.Addrs()
		if err != nil {
			logger.Warnf("failed to get net addr info for: %s", err)
			continue
		}
		addrList := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			addrList = append(addrList, addr.String())
		}
		item := Interface{
			Addrs: addrList,
			Mac:   inter.HardwareAddr.String(),
			Name:  inter.Name,
		}
		items = append(items, item)
	}
	return &Net{Interface: items}, nil
}

var osSystemType string

// GetSystemStatus :
var GetSystemStatus = func(ctx context.Context) (*System, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, err
	}

	if osSystemType == "" {
		osSystemType = tasks.GetSystemType()
	}

	bkAgentID := GetBKAgentID()
	return &System{
		HostName:      info.Hostname,
		SysType:       osSystemType,
		OS:            info.OS,
		Platform:      info.Platform,
		PlatVer:       info.PlatformVersion,
		BKAgentID:     bkAgentID,
		Arch:          info.KernelArch,
		KernelVersion: info.KernelVersion,
	}, nil
}

// GetBKAgentID 从gse获取bk_agent_id
func GetBKAgentID() string {
	return bkpipe.GetAgentInfo().BKAgentID
}

// GetRandomDuration 获取一个0～3600秒之间的数字
var GetRandomDuration = func() time.Duration {
	randomSecond := rand.Intn(3600)
	return time.Duration(randomSecond) * time.Second
}
