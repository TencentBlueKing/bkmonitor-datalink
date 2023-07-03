// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procconf

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/elastic/beats/libbeat/cfgfile"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	tcpProtocol  = "1"
	udpProtocol  = "2"
	tcp6Protocol = "3"
	udp6Protocol = "4"

	// CC不再下发该选项，现有选项仅用于兼容旧版，不再更新
	bindLoopBack     = "1"
	bindAll          = "2"
	bindFirstInnerIP = "3"
	bindFirstOuterIP = "4"

	procConfDir  = "bkmonitorbeat"
	procConfName = "bkmonitorbeat_processbeat.conf"
)

type BindInfo struct {
	Enable   *bool  `json:"enable"`
	IP       string `json:"ip"`
	Ports    string `json:"port"`
	Protocol string `json:"protocol"`
}

type CmdbProcessInfo struct {
	ProcessName  string     `json:"bk_func_name"`
	DisplayName  string     `json:"bk_process_name"`
	BindIP       string     `json:"bind_ip"`
	Ports        string     `json:"port"`
	Protocol     string     `json:"protocol"`
	ParamRegex   string     `json:"bk_start_param_regex"`
	EnablePort   *bool      `json:"bk_enable_port"`
	BindInfoList []BindInfo `json:"bind_info"`
}

type CmdbInfo struct {
	InnerIPs     string            `json:"bk_host_innerip"`
	OuterIPs     string            `json:"bk_host_outerip"`
	InnerIP6s    string            `json:"bk_host_innerip_v6"`
	OuterIP6s    string            `json:"bk_host_outerip_v6"`
	ProcessInfos []CmdbProcessInfo `json:"process"`
}

type Gather struct {
	dstfile string

	config *configs.ProcConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcConfig)

	dstdir := gather.config.DstDir
	if dstdir == "" {
		dstdir = filepath.Join(filepath.Dir(cfgfile.GetDefaultCfgfile()), procConfDir)
	}

	if !utils.PathExist(dstdir) {
		if err := os.MkdirAll(dstdir, 0x666); err != nil {
			logger.Errorf("failed to create processbeat dst dir: %s, %v", dstdir, err)
		}
	}

	gather.dstfile = filepath.Join(dstdir, procConfName)
	logger.Info("New a ProcConf Task Instance")
	return gather
}

func (g *Gather) Run(_ context.Context, _ chan<- define.Event) {
	logger.Info("ProcConf is running....")
	srcCfgs, err := g.readSrcCfgs()
	if err != nil {
		logger.Errorf("failed to get config from cmdb dir: %v", err)
		return
	}

	dstCfgs, err := g.readDstCfgs()
	if err != nil {
		logger.Errorf("failed to get config from cmdb dir: %v", err)
		return
	}

	if g.IsModify(srcCfgs, dstCfgs) {
		g.writeDstCfgs(srcCfgs)
	}
}

func (g *Gather) readSrcCfgs() ([]byte, error) {
	var content []byte
	var srccfg configs.ProcessbeatConfig

	bs, err := os.ReadFile(g.getHostFileIndex())
	if err != nil {
		return content, err
	}

	var cmdbinfo CmdbInfo
	if err := json.Unmarshal(bs, &cmdbinfo); err != nil {
		return content, err
	}

	srccfg.Processes = make([]configs.ProcessbeatPortConfig, 0)
	set := make(map[string]bool)
	for _, proItem := range cmdbinfo.ProcessInfos {
		if proItem.ProcessName == "" {
			continue
		}

		var portCfg configs.ProcessbeatPortConfig
		portCfg.Name = proItem.ProcessName
		portCfg.ParamRegex = proItem.ParamRegex
		portCfg.DisplayName = proItem.DisplayName

		if proItem.BindInfoList != nil && len(proItem.BindInfoList) != 0 {
			portCfg.BindInfoList = make([]configs.ProcessbeatBindInfo, 0)
			// 如果是新的 CMDB 配置格式，则需要使用新的方式进行解析
			for _, bindInfo := range proItem.BindInfoList {
				if bindInfo.Enable != nil && !*bindInfo.Enable {
					continue
				}

				portCfg.BindInfoList = append(portCfg.BindInfoList,
					configs.ProcessbeatBindInfo{
						IP:       g.convertBindIP(bindInfo.IP, cmdbinfo),
						Ports:    g.convertPorts(bindInfo.Ports),
						Protocol: g.convertProtocol(bindInfo.Protocol),
					},
				)
			}
		} else {
			// 否则是旧的方式
			portCfg.BindIP = g.convertBindIP(proItem.BindIP, cmdbinfo)
			portCfg.Protocol = g.convertProtocol(proItem.Protocol)
			portList := make([]uint16, 0)
			if proItem.Ports != "" {
				portList = g.convertPorts(proItem.Ports)
			}

			if proItem.EnablePort != nil && !*proItem.EnablePort {
				portCfg.Ports = make([]uint16, 0)
			} else {
				portCfg.Ports = portList
			}
		}

		if set[portCfg.ID()] {
			continue
		}
		set[portCfg.ID()] = true
		srccfg.Processes = append(srccfg.Processes, portCfg)
	}

	// 无需改动
	srccfg.Type = "processbeat"
	srccfg.Name = "processbeat_task"
	srccfg.Version = "1.0.0"

	srccfg.Period = g.config.Period
	srccfg.PerfDataId = g.config.PerfDataId
	srccfg.TopDataId = g.config.TopDataId
	srccfg.PortDataId = g.config.PortDataId
	srccfg.ConvergePID = g.config.ConvergePID
	return yaml.Marshal(srccfg)
}

func (g *Gather) readDstCfgs() ([]byte, error) {
	if !utils.PathExist(g.dstfile) {
		return []byte{}, nil
	}

	return os.ReadFile(g.dstfile)
}

func (g *Gather) IsModify(src, dst []byte) bool {
	if len(src) != len(dst) {
		return true
	}

	if len(src) == 0 {
		return false
	}

	return bytes.Compare(src, dst) != 0
}

func (g *Gather) writeDstCfgs(content []byte) {
	if err := os.WriteFile(g.dstfile, content, 0666); err != nil {
		logger.Errorf("failed to write file: %s, err: %v", g.dstfile, err)
		return
	}

	beat.ReloadChan <- true // 通知调度器 reload
}

func (g *Gather) convertProtocol(proto string) string {
	switch proto {
	case tcpProtocol:
		return process.ProtocolTCP
	case udpProtocol:
		return process.ProtocolUDP
	case tcp6Protocol:
		return process.ProtocolTCP6
	case udp6Protocol:
		return process.ProtocolUDP6
	}
	return proto
}

func getFirstIP(s string) string {
	ip := strings.Split(s, ",")
	if len(ip) == 0 {
		return ""
	}
	return ip[0]
}

func (g *Gather) convertBindIP(bindIP string, cmdb CmdbInfo) string {
	switch bindIP {
	case bindLoopBack:
		return "127.0.0.1"
	case bindAll:
		return net.IPv4zero.String()
	case bindFirstInnerIP:
		return getFirstIP(cmdb.InnerIPs)
	case bindFirstOuterIP:
		return getFirstIP(cmdb.OuterIPs)
	default:
		// 实际现有版本都下发实际IP，枚举选项仅存在于部分旧版本
		return bindIP
	}
}

func (g *Gather) convertPorts(s string) []uint16 {
	ports := make(map[uint16]struct{})
	for _, p := range strings.Split(s, ",") {
		if !strings.Contains(p, "-") {
			i, err := strconv.Atoi(p)
			if err != nil {
				continue
			}
			ports[uint16(i)] = struct{}{}
			continue
		}

		portRange := strings.Split(p, "-")
		if len(portRange) != 2 {
			continue
		}

		left, err := strconv.Atoi(portRange[0])
		if err != nil {
			continue
		}
		right, err := strconv.Atoi(portRange[1])
		if err != nil {
			continue
		}

		for i := left; i <= right; i++ {
			ports[uint16(i)] = struct{}{}
		}
	}

	var ret []uint16
	for k := range ports {
		ret = append(ret, k)
	}
	return ret
}

func (g *Gather) getHostFileIndex() string {
	if g.config.HostFilePath != "" {
		return g.config.HostFilePath
	}

	p := "/var/lib/gse/host/hostid"
	if runtime.GOOS == "windows" {
		p = "c:\\gse\\data\\host\\hostid"
	}
	return p
}
