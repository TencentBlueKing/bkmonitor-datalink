// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proccustom

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/mapping"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.ProcCustomConfig
	ctr    *process.ProcCollector
	mapper *mapping.Operator
	tasks.BaseTask

	degradeToStdConn bool
	isRunning        bool
}

type Event struct {
	dataid int
	pid    int32
	name   string
	cmd    string
	data   []common.MapStr
}

func (e Event) IgnoreCMDBLevel() bool { return false }

func (e Event) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid":  e.dataid,
		"version": "v2",
		"data":    e.data,
	}
}

func (e Event) GetType() string {
	return define.ModuleProcCustom
}

func (g *Gather) AsPerfEvents(metas []define.ProcStat) []Event {
	var ret []Event

	for _, meta := range metas {
		procname := g.config.ExtractProcessName(meta.Cmd)
		perfstat := g.ctr.GetOnePerfStat(meta.Pid)

		e := perfEvent{
			stat:     g.ctr.MergeMetaDataPerfStat(meta, perfstat),
			procName: procname,
			username: meta.Username,
			dims:     g.config.ExtractDimensions(meta.Cmd),
			tags:     g.config.Tags,
			labels:   g.config.Labels,
			reported: g.config.ProcMetric,
		}
		event := Event{
			dataid: int(g.config.DataID),
			pid:    meta.Pid,
			name:   procname,
			cmd:    meta.Cmd,
			data:   e.AsMapStr(),
		}
		ret = append(ret, event)
	}
	return ret
}

func (g *Gather) AsPortEvents(pid int32, cmd, username string, conns []process.FileSocket) []Event {
	procname := g.config.ExtractProcessName(cmd)
	e := portEvent{
		conns:    conns,
		pid:      pid,
		procName: procname,
		username: username,
		dims:     g.config.ExtractDimensions(cmd),
		tags:     g.config.Tags,
		labels:   g.config.Labels,
	}
	event := Event{
		dataid: g.config.PortDataID,
		pid:    pid,
		name:   procname,
		cmd:    cmd,
		data:   e.AsMapStr(),
	}
	return []Event{event}
}

func (g *Gather) newUpMetricEvent(upCode define.NamedCode) define.Event {
	return tasks.NewGatherUpEvent(g, upCode)
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcCustomConfig)
	gather.config.Setup()
	gather.mapper = mapping.NewOperator()
	gather.ctr = process.ProcCustomPerfCollector
	gather.Init()

	// 快速启动 记录缓存
	_, err := gather.GetAllMetaDataWithCache()
	if err != nil {
		logger.Errorf("failed to get all proc perf detailed: %v", err)
	}
	time.Sleep(time.Second)

	logger.Info("New a ProcCustom Task Instance")
	return gather
}

func (g *Gather) GetAllMetaDataWithCache() ([]define.ProcStat, error) {
	snapshot, updated, err := g.ctr.Snapshot()
	now := time.Now().Unix()
	taskid := g.GetTaskID()

	// 复用缓存
	if now-updated < int64(g.config.Period.Seconds()) {
		logger.Infof("taskid: %v, now: %v, update: %v, delta: %v, [复用]", taskid, now, updated, now-updated)
		return snapshot, err
	}

	logger.Infof("taskid: %v, now: %v, update: %v, delta: %v, [穿透]", taskid, now, updated, now-updated)
	return g.ctr.GetAllMetaData()
}

func (g *Gather) getPidFromPidPath() (int32, error) {
	if g.config.PIDPath == "" {
		return -1, nil
	}

	content, err := os.ReadFile(g.config.PIDPath)
	if err != nil {
		return -1, err
	}

	trimResult := strings.TrimSpace(string(content))
	pid, err := strconv.Atoi(trimResult)
	if err != nil {
		return -1, err
	}

	return int32(pid), nil
}

// aggregateStats 自定义采集时仅使用 pid/exe 作为映射规则
func (g *Gather) aggregateStats(events []Event, refresh bool) []Event {
	curr := make([]mapping.Process, 0)
	if refresh {
		for _, proc := range events {
			curr = append(curr, mapping.NewProcess(int(proc.pid), proc.name, ""))
		}
		g.mapper.RefreshGlobalMap(curr)
	}

	for _, proc := range events {
		fakepid := g.mapper.GetMappingPID(mapping.NewProcess(int(proc.pid), proc.name, ""))
		for i := 0; i < len(proc.data); i++ {
			proc.data[i].Put("dimension.pid", fmt.Sprintf("%d", fakepid))
		}
	}

	return events
}

func (g *Gather) Run(_ context.Context, e chan<- define.Event) {
	logger.Info("ProCustom is running....")
	if g.isRunning {
		return
	}

	g.isRunning = true
	defer func() { g.isRunning = false }()

	pid, err := g.getPidFromPidPath()
	if err != nil {
		logger.Warnf("pid file not found, %v", err)
		return
	}

	var match []define.ProcStat
	if pid >= 0 {
		// 通过PID文件方式读取进程信息分支
		one, err := g.ctr.GetOneMetaData(pid)
		if err != nil {
			logger.Errorf("failed to get one proc perf detailed: %v", err)
			return
		}
		match = append(match, one)
	} else {
		// 通过进程关键字匹配方式读取进程信息分支
		all, err := g.GetAllMetaDataWithCache()
		if err != nil {
			logger.Errorf("failed to get all proc perf detailed: %v", err)
			return
		}
		match = g.config.Match(all)
		if len(match) == 0 {
			logger.Warn("No processes matched keyword pattern")
			return
		}
	}
	// 一次采集全局仅允许刷新一次 Map
	refresh := false
	if g.config.EnablePerfCollected() {
		events := g.AsPerfEvents(match)
		if !g.config.DisableMapping {
			refresh = true
			events = g.aggregateStats(events, true)
		}
		for _, event := range events {
			e <- event
		}
	}

	if g.config.EnablePortCollected() {
		var pids []int32
		for _, m := range match {
			pids = append(pids, m.Pid)
		}
		connDetector := g.getConnDetector()
		conn, err := connDetector.Get(pids)
		if err != nil {
			logger.Errorf(
				"ConnDetector.Get failed, connector: %#v pids: %v, err: %v ", connDetector, pids, err,
			)
			g.degradeToStdConn = true
			return
		} else {
			for _, m := range match {
				socketList := make([]process.FileSocket, 0)
				socketList = append(socketList, conn.TCP[m.Pid]...)
				socketList = append(socketList, conn.UDP[m.Pid]...)
				socketList = append(socketList, conn.TCP6[m.Pid]...)
				socketList = append(socketList, conn.UDP6[m.Pid]...)
				events := g.AsPortEvents(m.Pid, m.Cmd, m.Username, socketList)
				if !g.config.DisableMapping {
					events = g.aggregateStats(events, !refresh)
				}

				for _, event := range events {
					e <- event
				}
			}
		}
	}

	upEvent := g.newUpMetricEvent(define.CodeOK)
	e <- upEvent
}

func (g *Gather) getConnDetector() process.ConnDetector {
	if configs.DisableNetlink || g.degradeToStdConn {
		return process.StdDetector{}
	}
	return process.NetlinkDetector{}
}
