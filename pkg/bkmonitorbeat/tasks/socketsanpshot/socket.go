// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package socketsanpshot

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
)

func mappingTcpFamily(n int) string {
	mapping := map[int]string{
		syscall.AF_INET6: "AF_INET6",
		syscall.AF_INET:  "AF_INET",
	}

	v, ok := mapping[n]
	if ok {
		return v
	}
	return fmt.Sprintf("%d", n)
}

func mappingUdpFamily(n int) string {
	mapping := map[int]string{
		syscall.SOCK_DGRAM: "SOCK_DGRAM",
	}

	v, ok := mapping[n]
	if ok {
		return v
	}
	return fmt.Sprintf("%d", n)
}

type ProcSocket struct {
	Pid      int32  `json:"pid"`
	Protocol string `json:"protocol"`
	State    string `json:"state"`
	Saddr    string `json:"saddr"`
	Sport    uint32 `json:"sport"`
	Daddr    string `json:"daddr"`
	Dport    uint32 `json:"dport"`
	Family   string `json:"family"`
}

func AllProcsSocket(pids []int32, mode string) ([]ProcSocket, error) {
	var ret []ProcSocket
	detector := getConnDetector(mode)
	ps, err := detector.GetState(pids, process.StateListenEstab)
	if err != nil {
		return nil, err
	}

	appendConn := func(sockets map[int32][]process.FileSocket) {
		for pid, items := range sockets {
			for _, item := range items {
				var family string
				if strings.HasPrefix(item.Protocol, "tcp") {
					family = mappingTcpFamily(int(item.Family))
				} else {
					family = mappingUdpFamily(int(item.Family))
				}
				ret = append(ret, ProcSocket{
					Pid:      pid,
					State:    item.Status,
					Protocol: item.Protocol,
					Saddr:    item.Saddr,
					Sport:    item.Sport,
					Daddr:    item.Daddr,
					Dport:    item.Dport,
					Family:   family,
				})
			}
		}
	}

	appendConn(ps.TCP)
	appendConn(ps.TCP6)
	appendConn(ps.UDP)
	appendConn(ps.UDP6)

	return ret, nil
}

func getConnDetector(mode string) process.ConnDetector {
	switch mode {
	case "netlink":
		return process.NetlinkDetector{}
	default:
		return process.StdDetector{}
	}
}
