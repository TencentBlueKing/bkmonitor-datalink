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
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/metric/system/memory"
	"github.com/elastic/gosigar"
	shiroups "github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type PortConfigStore struct {
	Pid2Conf map[int32][]string
	Conf2Pid map[string][]int32
}

var ErrSkipCollect = errors.New("skip collect")

type procPerfMgr struct {
	mut      sync.Mutex
	totalMem uint64
	ioCache  map[int32]define.IOStat
	cpuCache map[int32]define.CPUStat
}

func (p *procPerfMgr) GetOneMetaData(pid int32) (define.ProcStat, error) {
	var stat define.ProcStat
	proc, err := shiroups.NewProcess(pid)
	if err != nil {
		return stat, err
	}

	stat.Pid = pid
	stat.PPid, _ = proc.Ppid()

	status, _ := proc.Status()
	var s string
	if len(status) > 0 {
		s = status[0]
	}
	stat.Status = p.getProcState(s)

	stat.Username, _ = proc.Username()
	stat.Name, _ = proc.Name()
	stat.Cmd, _ = proc.Cmdline()
	stat.CmdSlice, _ = proc.CmdlineSlice()
	stat.Exe, _ = proc.Exe()
	stat.Cwd, _ = proc.Cwd()
	stat.Created, _ = proc.CreateTime()
	return stat, nil
}

func (p *procPerfMgr) GetOnePerfStat(pid int32) (define.ProcStat, error) {
	var stat define.ProcStat
	// 确定 pid 是否存在
	_, err := shiroups.NewProcess(pid)
	if err != nil {
		return stat, err
	}

	stat.Mem, _ = p.getMem(pid)
	stat.CPU, _ = p.getCPU(pid)
	stat.IO, _ = p.getIO(pid)
	stat.Fd, _ = p.getFd(pid)
	return stat, nil
}

func (p *procPerfMgr) MergeMetaDataPerfStat(meta, perf define.ProcStat) define.ProcStat {
	meta.Mem = perf.Mem
	meta.CPU = perf.CPU
	meta.IO = perf.IO
	meta.Fd = perf.Fd
	return meta
}

func (p *procPerfMgr) getProcState(s string) string {
	switch s {
	case "S":
		return "sleeping"
	case "R":
		return "running"
	case "D", "I":
		return "idle"
	case "T":
		return "stopped"
	case "Z":
		return "zombie"
	}
	return "unknown"
}

func (p *procPerfMgr) getMem(pid int32) (*define.MemStat, error) {
	memStat, err := p.mem(pid)
	if err != nil {
		return nil, err
	}

	if p.totalMem <= 0 {
		m, err := memory.Get()
		if err != nil {
			return nil, err
		}
		p.totalMem = m.Total
	}

	percent := float64(memStat.Resident) / float64(p.totalMem)
	return &define.MemStat{
		Size:     memStat.Size,
		Resident: memStat.Resident,
		Share:    memStat.Share,
		Percent:  percent,
	}, nil
}

func (p *procPerfMgr) mem(pid int32) (gosigar.ProcMem, error) {
	g := gosigar.ProcMem{}
	return g, g.Get(int(pid))
}

func (p *procPerfMgr) getCPU(pid int32) (*define.CPUStat, error) {
	p.mut.Lock()
	defer p.mut.Unlock()

	cpuStat, err := p.cpu(pid)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	v, ok := p.cpuCache[pid]
	if !ok {
		p.cpuCache[pid] = define.CPUStat{
			Ts:    now,
			Total: cpuStat.Total,
		}
		return nil, ErrSkipCollect
	}

	period := float64(now.Sub(v.Ts).Milliseconds())

	// 进程被重置 过期清理
	if cpuStat.Total < v.Total {
		p.cpuCache[pid] = define.CPUStat{
			Ts:    now,
			Total: cpuStat.Total,
		}
		return nil, ErrSkipCollect
	}

	p.cpuCache[pid] = define.CPUStat{
		Ts:    now,
		Total: cpuStat.Total,
	}

	var percent float64
	if cpuStat.Total > v.Total && period > 0 {
		percent = float64(cpuStat.Total-v.Total) / period
	}

	return &define.CPUStat{
		StartTime:     cpuStat.StartTime,
		User:          cpuStat.User,
		Sys:           cpuStat.Sys,
		Total:         cpuStat.Total,
		Percent:       percent,
		NormalPercent: percent / float64(runtime.NumCPU()),
	}, nil
}

func (p *procPerfMgr) cpu(pid int32) (define.ProcTime, error) {
	proc, err := shiroups.NewProcess(pid)
	if err != nil {
		return define.ProcTime{}, err
	}

	t, err := proc.Times()
	if err != nil {
		return define.ProcTime{}, err
	}

	// 单位均转换为 ms
	ct, _ := proc.CreateTime()
	return define.ProcTime{
		StartTime: uint64(ct),
		User:      uint64(t.User * 1000),
		Sys:       uint64(t.System * 1000),
		Total:     uint64((t.User + t.System) * 1000),
	}, nil
}

func (p *procPerfMgr) getIO(pid int32) (*define.IOStat, error) {
	p.mut.Lock()
	defer p.mut.Unlock()

	iostat, err := p.io(pid)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	v, ok := p.ioCache[pid]
	if !ok {
		p.ioCache[pid] = define.IOStat{
			Ts:         now,
			ReadBytes:  iostat.ReadBytes,
			WriteBytes: iostat.WriteBytes,
		}
		return nil, ErrSkipCollect
	}

	period := now.Sub(v.Ts).Seconds()

	var rs float64
	if iostat.ReadBytes > v.ReadBytes && period > 0 {
		rs = float64(iostat.ReadBytes-v.ReadBytes) / period
	}
	var ws float64
	if iostat.WriteBytes > v.WriteBytes && period > 0 {
		ws = float64(iostat.WriteBytes-v.WriteBytes) / period
	}

	// 进程被重置 过期清理
	if rs < 0 || ws < 0 {
		p.ioCache[pid] = define.IOStat{
			Ts:         now,
			ReadBytes:  iostat.ReadBytes,
			WriteBytes: iostat.WriteBytes,
		}
		return nil, ErrSkipCollect
	}

	p.ioCache[pid] = define.IOStat{
		Ts:         now,
		ReadBytes:  iostat.ReadBytes,
		WriteBytes: iostat.WriteBytes,
	}

	return &define.IOStat{
		ReadBytes:  iostat.ReadBytes,
		WriteBytes: iostat.WriteBytes,
		ReadSpeed:  rs,
		WriteSpeed: ws,
	}, nil
}

func (p *procPerfMgr) io(pid int32) (*shiroups.IOCountersStat, error) {
	proc, err := shiroups.NewProcess(pid)
	if err != nil {
		return nil, err
	}

	return proc.IOCounters()
}

func (p *procPerfMgr) getFd(pid int32) (*define.FdStat, error) {
	fdstat, err := p.fd(pid)
	if err != nil {
		return nil, err
	}

	return &define.FdStat{
		Open:      fdstat.Open,
		SoftLimit: fdstat.SoftLimit,
		HardLimit: fdstat.HardLimit,
	}, nil
}

func (p *procPerfMgr) fd(pid int32) (gosigar.ProcFDUsage, error) {
	g := gosigar.ProcFDUsage{}
	return g, g.Get(int(pid))
}
