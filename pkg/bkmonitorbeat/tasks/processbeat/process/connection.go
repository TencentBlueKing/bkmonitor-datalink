// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"fmt"
	stdnet "net"
	"sort"
	"syscall"

	"github.com/shirou/gopsutil/v3/net"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	socketPerformanceThreshold = 1000
	socketPerformanceSleep     = 10

	ProtocolUnspecified = ""
	ProtocolTCP         = "tcp"
	ProtocolUDP         = "udp"
	ProtocolTCP6        = "tcp6"
	ProtocolUDP6        = "udp6"
)

type PidSockets struct {
	TCP  map[int32][]FileSocket
	UDP  map[int32][]FileSocket
	TCP6 map[int32][]FileSocket
	UDP6 map[int32][]FileSocket
}

// Get 获取指定 protocol 的 FileSocket
// 既要支持 ipv6 新场景的 也要兼容原先的 tcp/udp 检测逻辑
func (ps PidSockets) Get(pid int32, protocol string) []FileSocket {
	var ret []FileSocket
	switch protocol {
	case ProtocolTCP:
		ret = append(ret, ps.TCP[pid]...)
		ret = append(ret, ps.TCP6[pid]...)
	case ProtocolUDP:
		ret = append(ret, ps.UDP[pid]...)
		ret = append(ret, ps.UDP6[pid]...)
	case ProtocolTCP6:
		ret = ps.TCP6[pid]
	case ProtocolUDP6:
		ret = ps.UDP6[pid]
	}
	return ret
}

type PortStat struct {
	ProcName          string   `json:"name"`
	Status            int      `json:"exists"`
	Protocol          string   `json:"protocol"`
	Listen            []uint16 `json:"listen"`
	NonListen         []uint16 `json:"nonlisten"`
	NotAccurateListen []string `json:"notaccuratelisten"`
	BindIP            string   `json:"bindip"`
	ParamRegex        string   `json:"paramregex"`
	DisplayName       string   `json:"displayname"`
	PortHealthy       int      `json:"porthealth"`
}

func (s *PortStat) sortPorts() {
	sort.Slice(s.Listen, func(i, j int) bool { return s.Listen[i] <= s.Listen[j] })
	sort.Slice(s.NonListen, func(i, j int) bool { return s.NonListen[i] <= s.NonListen[j] })
	sort.Slice(s.NotAccurateListen, func(i, j int) bool { return s.NotAccurateListen[i] <= s.NotAccurateListen[j] })
}

type ConnDetector interface {
	Get(pids []int32) (PidSockets, error)
}

type StdConnector struct{}

var _ ConnDetector = StdConnector{}

func (StdConnector) Get(pids []int32) (PidSockets, error) {
	ret := PidSockets{
		TCP: map[int32][]FileSocket{},
		UDP: map[int32][]FileSocket{},
	}

	pidset := make(map[int32]struct{})
	for _, pid := range pids {
		pidset[pid] = struct{}{}
	}

	tcp, err := net.Connections("tcp")
	if err != nil {
		return ret, err
	}
	for _, conn := range tcp {
		if conn.Status != "LISTEN" {
			continue
		}
		if _, ok := pidset[conn.Pid]; !ok {
			continue
		}
		logger.Debugf("pid %d listening tcp %+v", conn.Pid, conn.Laddr)
		for _, listenIP := range tasks.GetListeningIPs(conn.Laddr.IP) {
			s := FileSocket{
				Status:    conn.Status,
				Type:      int(conn.Type),
				Pid:       conn.Pid,
				Family:    conn.Family,
				ConnLaddr: listenIP,
				ConnLport: conn.Laddr.Port,
			}

			if s.Family == syscall.AF_INET6 && len(listenIP) == stdnet.IPv6len {
				// IPv6
				s.Protocol = ProtocolTCP6
				ret.TCP6[conn.Pid] = append(ret.TCP6[conn.Pid], s)
			} else {
				s.Protocol = ProtocolTCP
				ret.TCP[conn.Pid] = append(ret.TCP[conn.Pid], s)
			}
		}
	}

	udp, err := net.Connections("udp")
	if err != nil {
		return ret, err
	}

	for _, conn := range udp {
		if _, ok := pidset[conn.Pid]; !ok {
			continue
		}
		logger.Debugf("pid %d listening udp %+v", conn.Pid, conn.Laddr)
		for _, listenIP := range tasks.GetListeningIPs(conn.Laddr.IP) {
			s := FileSocket{
				Status:    conn.Status,
				Type:      int(conn.Type),
				Pid:       conn.Pid,
				Family:    conn.Family,
				ConnLaddr: listenIP,
				ConnLport: conn.Laddr.Port,
			}
			if s.Family == syscall.AF_INET6 && len(listenIP) == stdnet.IPv6len {
				// IPv6
				s.Protocol = ProtocolUDP6
				ret.UDP6[conn.Pid] = append(ret.UDP6[conn.Pid], s)
			} else {
				s.Protocol = ProtocolUDP
				ret.UDP[conn.Pid] = append(ret.UDP[conn.Pid], s)
			}
		}
	}

	return ret, nil
}

func (pc *ProcCollector) getConnDetector() ConnDetector {
	if configs.DisableNetlink || pc.degradeToStdConn {
		return StdConnector{}
	}
	return NetlinkConnector{}
}

type PortStats struct {
	Processes []PortStat `json:"processes"`
}

func (pc *ProcCollector) CollectPortStat(pcs PortConfigStore) (PortStats, error) {
	var pids []int32
	for k := range pcs.Pid2Conf {
		pids = append(pids, k)
	}

	var ret PortStats
	sockets, err := pc.getConnDetector().Get(pids)
	// 一旦获取失败 就使用 stdConnDetector
	if err != nil {
		pc.degradeToStdConn = true
		return ret, err
	}
	logger.Debugf("get connection stats: %+v", sockets)

	getSockets := func(conf configs.ProcessbeatPortConfig) []FileSocket {
		// 获取协议集合
		protocolSet := make(map[string]struct{})
		for _, bind := range conf.GetBindDetailed() {
			if bind.Protocol != "" {
				protocolSet[bind.Protocol] = struct{}{}
			}
		}
		// 无配置协议则视为所有协议均可
		// TCP = TCP4+TCP6
		// UDP = UDP4+UDP6
		if len(protocolSet) == 0 {
			logger.Debug("no protocol found, mark as TCP+UDP")
			protocolSet = map[string]struct{}{
				ProtocolTCP: {},
				ProtocolUDP: {},
			}
		}

		// 获取对应进程和协议的监听端口信息
		sks := make([]FileSocket, 0)
		for protocol := range protocolSet {
			for _, pid := range pcs.Conf2Pid[conf.ID()] {
				sks = append(sks, sockets.Get(pid, protocol)...)
			}
		}
		logger.Debugf("processbeat port conf: %+v, found %d FileSocks", conf, len(sks))
		return sks
	}

	for _, conf := range pc.cmdbConf.Processes {
		n := len(pcs.Conf2Pid[conf.ID()]) // 配置对应进程数 用于判断进程是否存在

		if len(conf.GetBindDetailed()) <= 0 {
			// 如果没有配置端口上报 也需要上报端口数据 ╮(╯▽╰)╭ 因为进程是否存在的指标在端口数据中
			var exist int
			if n > 0 {
				exist = 1
			}
			ret.Processes = append(ret.Processes, PortStat{
				ProcName:    conf.Name,
				Status:      exist,
				ParamRegex:  conf.ParamRegex,
				DisplayName: conf.DisplayName,
				PortHealthy: 1,
			})
			continue
		}

		ret.Processes = append(ret.Processes, calcPortStat(conf, getSockets(conf), n)...)
	}

	return ret, nil
}

func calcPortStat(conf configs.ProcessbeatPortConfig, sockets []FileSocket, pidcnt int) []PortStat {
	socketSet := make(map[string]struct{})
	portSet := make(map[uint32]struct{})
	for _, socket := range sockets {
		portSet[socket.ConnLport] = struct{}{}
		socketSet[socket.Listen()] = struct{}{}
	}

	binds := conf.GetBindDetailed()
	psList := make([]PortStat, 0)
	for _, bind := range binds {
		var ps PortStat
		ps.Listen = make([]uint16, 0)
		ps.NonListen = make([]uint16, 0)
		ps.NotAccurateListen = make([]string, 0)
		var others []uint16
		for _, port := range bind.Ports {
			// nolisten
			if _, ok := portSet[uint32(port)]; !ok {
				ps.NonListen = append(ps.NonListen, port)
				continue
			}
			// listen / notaccuratelisten
			others = append(others, port)
		}

		notaccuratelisten := make(map[uint16]struct{})
		for _, other := range others {
			// 如果已经有绑定的 ip 则以配置为准
			guessIPs := []string{stdnet.ParseIP(bind.IP).String()}

			// 如果没有绑定的 ip 信息 则需要判断 protocol 类型
			if bind.IP == "" {
				// TCP6/UDP6
				guessIPs = []string{stdnet.IPv6loopback.String(), stdnet.IPv6zero.String()}
				guessIPs = append(guessIPs, tasks.DefaultIPs(configs.IPv6)...)

				// TCP=TCP4+TCP6;
				// UDP=UDP4+UDP6;
				switch bind.Protocol {
				case ProtocolUnspecified, ProtocolTCP, ProtocolUDP:
					guessIPs = append(guessIPs, "127.0.0.1", stdnet.IPv4zero.String())
					guessIPs = append(guessIPs, tasks.DefaultIPs(configs.IPv4)...)
				}
			}

			var found bool
			for _, guessip := range guessIPs {
				_, ok := socketSet[fmt.Sprintf("%s:%d", guessip, other)]
				// listen
				if ok {
					ps.Listen = append(ps.Listen, other)
					found = true
					break
				}
			}

			// notaccuratelisten
			if !found {
				notaccuratelisten[other] = struct{}{}
			}
		}

		for _, socket := range sockets {
			if _, ok := notaccuratelisten[uint16(socket.ConnLport)]; !ok {
				continue
			}
			ps.NotAccurateListen = append(ps.NotAccurateListen, socket.Listen())
		}
		if len(ps.NotAccurateListen) > 0 {
			ps.NotAccurateListen = tasks.UniqueSlice(ps.NotAccurateListen)
		}

		// process exists
		if pidcnt > 0 {
			ps.Status = 1
		}

		// port health
		if len(ps.NotAccurateListen) == 0 && len(ps.NonListen) == 0 {
			ps.PortHealthy = 1
		}

		ps.ProcName = conf.Name
		ps.DisplayName = conf.DisplayName
		ps.ParamRegex = conf.ParamRegex
		ps.Protocol = bind.Protocol
		ps.BindIP = bind.IP
		ps.sortPorts()
		psList = append(psList, ps)
	}

	return psList
}
