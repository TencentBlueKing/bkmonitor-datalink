// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procsnapshot

import (
	"time"

	shiroups "github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcMeta struct {
	Pid      int32   `json:"pid"`
	PPid     int32   `json:"ppid"`
	Name     string  `json:"name"`
	Cwd      string  `json:"cwd"`
	Exe      string  `json:"exe"`
	Cmd      string  `json:"cmd"`
	Username string  `json:"username"`
	Created  int64   `json:"created"`
	Uids     []int32 `json:"uids"`
}

type ProcConn struct {
	Pid       int32  `json:"pid"`
	State     string `json:"state"`
	Protocol  string `json:"protocol"`
	LocalAddr string `json:"local_addr"`
	LocalPort uint32 `json:"local_port"`
}

const (
	socketPerformanceThreshold = 1000
	socketPerformanceSleep     = 10
)

func getProcMeta(pid int32) (ProcMeta, error) {
	var meta ProcMeta
	proc, err := shiroups.NewProcess(pid)
	if err != nil {
		return meta, err
	}

	meta.Pid = pid
	meta.PPid, _ = proc.Ppid()
	meta.Username, _ = proc.Username()
	meta.Name, _ = proc.Name()
	meta.Cmd, _ = proc.Cmdline()
	meta.Exe, _ = proc.Exe()
	meta.Cwd, _ = proc.Cwd()
	meta.Created, _ = proc.CreateTime()
	meta.Uids, _ = proc.Uids()
	return meta, nil
}

func allProcsMeta() ([]ProcMeta, error) {
	var ret []ProcMeta
	pids, err := shiroups.Pids()
	if err != nil {
		return ret, err
	}

	for idx, pid := range pids {
		if (idx+1)%socketPerformanceThreshold == 0 {
			time.Sleep(time.Millisecond * socketPerformanceSleep)
		}

		stat, err := getProcMeta(pid)
		if err != nil {
			logger.Warnf("get process meta data failed, pid: %d, err: %v", pid, err)
			continue
		}
		ret = append(ret, stat)
	}

	return ret, nil
}

func allProcsConn(pids []int32) ([]ProcConn, error) {
	var ret []ProcConn
	d := getConnDetector()
	listenPs, err := d.GetState(pids, process.StateListen)
	if err != nil {
		return nil, err
	}
	estabPs, err := d.GetState(pids, process.StateEstab)
	if err != nil {
		return nil, err
	}

	appendConn := func(sockets map[int32][]process.FileSocket) {
		for pid, items := range sockets {
			for _, item := range items {
				ret = append(ret, ProcConn{
					Pid:       pid,
					State:     item.Status,
					Protocol:  item.Protocol,
					LocalAddr: item.ConnLaddr,
					LocalPort: item.ConnLport,
				})
			}
		}
	}

	handleFs := func(ps process.PidSockets) {
		appendConn(ps.TCP)
		appendConn(ps.TCP6)
		appendConn(ps.UDP)
		appendConn(ps.UDP6)
	}

	handleFs(listenPs)
	handleFs(estabPs)

	return ret, nil
}

func getConnDetector() process.ConnDetector {
	if configs.DisableNetlink {
		return process.StdDetector{}
	}
	return process.NetlinkDetector{}
}
