// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procstatus

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const secretPlaceHolder = "***"

type ProcessInfo struct {
	Pid      int32
	PPid     int32
	Name     string
	Cwd      string
	Exe      string
	Cmd      []string
	CmdRaw   string
	Status   string
	Username string
	Created  int64
}

type Report struct {
	processes []*ProcessInfo
}

func (r *Report) AsMapStr() common.MapStr {
	ps := make([]common.MapStr, 0, len(r.processes))
	for _, info := range r.processes {
		p := common.MapStr{
			"pid":      info.Pid,
			"ppid":     info.PPid,
			"name":     info.Name,
			"cwd":      info.Cwd,
			"exe":      info.Exe,
			"cmd":      info.Cmd,
			"status":   info.Status,
			"username": info.Username,
			"created":  info.Created,
		}
		ps = append(ps, p)
	}
	return common.MapStr{
		"processes": ps,
	}
}

type Gather struct {
	tasks.BaseTask
	config         *configs.ProcStatusConfig
	nextReportTime time.Time
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcStatusConfig)

	gather.Init()

	logger.Info("New a ProcStatus Task Instance")
	return gather
}

// GetProcessStatus 获取进程信息
var GetProcessStatus = func(ctx context.Context) ([]*ProcessInfo, error) {
	// 获取所有进程信息
	procStats, err := process.ProcCustomPerfCollector.GetAllMetaData()
	if err != nil {
		return nil, err
	}
	return getProcessStatusFromStat(procStats), nil
}

// getProcessStatusFromStat 转换进程状态为上报的信息格式
func getProcessStatusFromStat(stats []define.ProcStat) []*ProcessInfo {
	processes := make([]*ProcessInfo, 0, len(stats))
	for _, stat := range stats {
		p := &ProcessInfo{
			Pid:      stat.Pid,
			PPid:     stat.PPid,
			Name:     stat.Name,
			Cwd:      stat.Cwd,
			Exe:      stat.Exe,
			Cmd:      removeKwargs(stat.CmdSlice),
			CmdRaw:   stat.Cmd,
			Status:   stat.Status,
			Username: stat.Username,
			Created:  stat.Created,
		}
		processes = append(processes, p)
	}
	return processes
}

// removeKwargs 清除所有带名字的参数值
func removeKwargs(cmdlineSlice []string) []string {
	isKwarg := false
	for i := 0; i < len(cmdlineSlice); i++ {
		arg := cmdlineSlice[i]
		if arg != "" {
			if arg[0] == '-' {
				if j := strings.IndexRune(arg, '='); j < 0 {
					isKwarg = true
				} else {
					cmdlineSlice[i] = arg[0:j] + "=" + secretPlaceHolder
					isKwarg = false
				}
			} else if isKwarg {
				cmdlineSlice[i] = secretPlaceHolder
				isKwarg = false
			}
		}
	}
	return cmdlineSlice
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	logger.Info("ProcStatus is running....")
	if !g.shouldReport() {
		return
	}
	processes, err := GetProcessStatus(ctx)
	if err != nil {
		logger.Errorf("get process status failed: %v", err)
		return
	}
	logger.Debugf("got processes: %+v", processes)
	event := NewEvent(g.config.DataID, VERSION, time.Now().Unix(), &Report{processes: processes})
	e <- event
	g.setNextReportTime()
}

// shouldReport 是否上报
func (g *Gather) shouldReport() bool {
	return g.nextReportTime.IsZero() || time.Now().After(g.nextReportTime)
}

// setNextReportTime 更新下一次上报时间
func (g *Gather) setNextReportTime() {
	duration := g.config.ReportPeriod
	if g.nextReportTime.IsZero() {
		// 首次上报后随机设置下一次上报间隔
		duration = time.Duration(rand.Int63n(int64(g.config.ReportPeriod)))
	}
	g.nextReportTime = time.Now().Add(duration)
}
