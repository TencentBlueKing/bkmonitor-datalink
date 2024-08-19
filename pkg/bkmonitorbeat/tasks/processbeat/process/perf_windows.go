// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows

package process

import (
	"io"
	"os/exec"
	"strconv"
	"strings"

	shiroups "github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (p *procPerfMgr) listProcs() ([]*define.ProcStat, error) {
	cmd := exec.Command("cmd", "/C", "tasklist /FO CSV")
	std, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		logger.Errorf("tasklist cmd Start err: %s", err)
		return nil, err
	}

	content, err := io.ReadAll(std)
	if err != nil {
		logger.Errorf("tasklist read content err: %s", err)
		return nil, err
	}

	if err = cmd.Wait(); err != nil {
		logger.Errorf("tasklist cmd Wait err: %s", err)
		return nil, err
	}

	var procs []*define.ProcStat
	lines := strings.Split(string(content), "\r\n")
	for idx, line := range lines {
		// tasklist /FO CSV在第一行打印会话名等，最后一行会打印一个空的行
		if idx < 1 || idx == len(lines)-1 {
			continue
		}

		logger.Debugf("proc line: %s", line)
		// 因为是csv的形式，"System Idle Process","0","Services","0","8 K"，将\"去掉
		fields := strings.Split(strings.ReplaceAll(line, "\"", ""), ",")
		if len(fields) < 5 {
			continue
		}
		// 内存使用会带， 如csrss.exe,976,Services,0,3,680 K  所以要向前匹配
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		procs = append(procs, &define.ProcStat{
			Pid:  int32(pid),
			Name: fields[0],
		})
	}

	return procs, nil
}

func (p *procPerfMgr) AllMetaData() ([]define.ProcStat, error) {
	procs, err := p.listProcs()
	if err != nil {
		return nil, err
	}

	for _, proc := range procs {
		if err := p.fillOneMetaData(proc); err != nil {
			logger.Warnf("failed to fill proc metadata: %v", err)
		}
	}

	var ret []define.ProcStat
	for _, proc := range procs {
		ret = append(ret, *proc)
	}

	return ret, nil
}

func (p *procPerfMgr) fillOneMetaData(procStat *define.ProcStat) error {
	proc, err := shiroups.NewProcess(procStat.Pid)
	if err != nil {
		return err
	}

	status, _ := proc.Status()
	var s string
	if len(status) > 0 {
		s = status[0]
	}
	procStat.Status = p.getProcState(s)
	procStat.Username, _ = proc.Username()
	procStat.Cmd, _ = proc.Cmdline()
	procStat.CmdSlice, _ = proc.CmdlineSlice()
	procStat.Exe, _ = proc.Exe()
	procStat.Cwd, _ = proc.Cwd()
	procStat.Created, _ = proc.CreateTime()
	return nil
}
