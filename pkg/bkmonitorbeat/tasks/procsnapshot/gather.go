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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcMeta struct {
	Pid      int32
	PPid     int32
	Name     string
	Cwd      string
	Exe      string
	Cmd      string
	CmdSlice []string
	Username string
	Created  int64
	Uids     []int32
}

const (
	socketPerformanceThreshold = 1000
	socketPerformanceSleep     = 10
)

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
	meta.CmdSlice, _ = proc.CmdlineSlice()
	meta.Exe, _ = proc.Exe()
	meta.Cwd, _ = proc.Cwd()
	meta.Created, _ = proc.CreateTime()
	meta.Uids, _ = proc.Uids()
	return meta, nil
}

//func allProcsInodes() {
//	//for _, pid := range
//	process.GetProcInodes("/proc", 1)
//
//	process.NetlinkDetector{}.Get(allpids)
//}
