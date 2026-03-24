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
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/mapping"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type FileSocket struct {
	Status   string
	Inode    uint32
	Type     int
	Pid      int32
	Family   uint32
	Saddr    string
	Sport    uint32
	Daddr    string
	Dport    uint32
	Protocol string
}

func (fs FileSocket) Listen() string {
	return fmt.Sprintf("%s:%d", fs.Saddr, fs.Sport)
}

// socketNetInfo inode -> socket
type socketNetInfo struct {
	TCP  map[uint32]FileSocket
	UDP  map[uint32]FileSocket
	TCP6 map[uint32]FileSocket
	UDP6 map[uint32]FileSocket
}

type ProcCollector struct {
	degradeToStdConn bool
	mgr              *procPerfMgr
	cmdbConf         *configs.ProcessbeatConfig
	mapper           *mapping.Operator

	snapshotMut sync.Mutex
	snapshot    []define.ProcStat
	updatedTs   int64
	updatedErr  error
}

func NewProcCollector() *ProcCollector {
	return &ProcCollector{
		mgr:      &procPerfMgr{ioCache: map[int32]define.IOStat{}, cpuCache: map[int32]define.CPUStat{}},
		cmdbConf: &configs.ProcessbeatConfig{},
		mapper:   mapping.NewOperator(),
	}
}

func (pc *ProcCollector) UpdateConf(conf *configs.ProcessbeatConfig) {
	pc.cmdbConf = conf
	pc.cmdbConf.Setup()
}

func (pc *ProcCollector) AsOneCmdbConfMapStr(stat define.ProcStat) common.MapStr {
	mstr := common.MapStr{}
	mstr.Put("pid", stat.Pid)
	mstr.Put("ppid", stat.PPid)
	mstr.Put("name", stat.Name)
	mstr.Put("state", stat.Status)
	mstr.Put("username", stat.Username)
	mstr.Put("cmdline", stat.Cmd)
	mstr.Put("cwd", stat.Cwd)
	mstr.Put("exe", stat.Exe)

	if stat.Created != 0 {
		mstr.Put("uptime", time.Now().Unix()-stat.Created/1000)
	}

	if stat.Mem != nil {
		mstr.Put("memory", common.MapStr{
			"size": stat.Mem.Size,
			"rss": common.MapStr{
				"bytes": stat.Mem.Resident,
				"pct":   stat.Mem.Percent,
			},
			"share": stat.Mem.Share,
		})
	}

	if stat.CPU != nil {
		startTime := time.Unix(0, int64(stat.CPU.StartTime*1000000)) // ms -> ns

		mstr.Put("cpu", common.MapStr{
			"total": common.MapStr{
				"value": stat.CPU.Total,
				"pct":   stat.CPU.Percent,
				"norm": common.MapStr{
					"pct": stat.CPU.NormalPercent,
				},
			},
			"start_time": startTime,
		})

		mstr.Put("cpu.user.ticks", stat.CPU.User)
		mstr.Put("cpu.system.ticks", stat.CPU.Sys)
		mstr.Put("cpu.total.ticks", stat.CPU.Total)
	}

	if stat.Fd != nil {
		mstr.Put("fd", common.MapStr{
			"open": stat.Fd.Open,
			"limit": common.MapStr{
				"soft": stat.Fd.SoftLimit,
				"hard": stat.Fd.HardLimit,
			},
		})
	}

	if stat.IO != nil {
		mstr.Put("io", common.MapStr{
			"read_speed":  stat.IO.ReadSpeed,
			"write_speed": stat.IO.WriteSpeed,
			"read_bytes":  stat.IO.ReadBytes,
			"write_bytes": stat.IO.WriteBytes,
		})
	}

	return mstr
}

// aggregateStats CMDB 采集时使用 pid/exe/params 作为映射规则
func (pc *ProcCollector) aggregateStats(stats []common.MapStr) []common.MapStr {
	curr := make([]mapping.Process, 0)
	for _, proc := range stats {
		curr = append(curr, mapping.NewProcess(
			int(proc["pid"].(int32)),
			proc["name"].(string),
			proc["paramregex"].(string),
		))
	}
	pc.mapper.RefreshGlobalMap(curr)

	// 用映射替换真实 pid 去掉一些额外的参数上报 如 ppid 等
	for _, proc := range stats {
		// 基本内容先进行抹零
		proc["ppid"] = 0
		proc["pgid"] = 0
		proc["state"] = "sleeping"

		pid, ok := proc["pid"].(int32)
		if !ok {
			continue
		}

		name, ok := proc["name"].(string)
		if !ok {
			continue
		}

		params, ok := proc["paramregex"].(string)
		if !ok {
			continue
		}

		fakepid := pc.mapper.GetMappingPID(mapping.NewProcess(int(pid), name, params))
		define.GlobalPidStore.Set(int(pid), fakepid, proc["cmdline"].(string))
		proc["pid"] = fakepid
	}

	return stats
}

// CollectProcStat collects process performance statistics
func (pc *ProcCollector) CollectProcStat(metas []define.ProcStat) ([]common.MapStr, []common.MapStr, PortConfigStore) {
	var (
		exists  []common.MapStr
		visited = make(map[string]struct{})
		pcs     = PortConfigStore{
			Pid2Conf: map[int32][]string{}, // 进程对应的采集配置: 1 -> N; key: pid; 		value: [config id]
			Conf2Pid: map[string][]int32{}, // 采集配置对应的进程: 1 -> N; key: config id;	value: [pid]
		}
	)

	for _, meta := range metas {
		metaMapStr := pc.AsOneCmdbConfMapStr(meta)
		names := pc.cmdbConf.MatchNames(metaMapStr)

		for _, matched := range pc.cmdbConf.MatchRegex(names, metaMapStr["cmdline"].(string)) {
			perf := pc.GetOnePerfStat(meta.Pid)
			cloned := pc.AsOneCmdbConfMapStr(pc.MergeMetaDataPerfStat(meta, perf))
			cloned["name"] = matched.Name
			cloned["exists"] = true
			cloned["paramregex"] = matched.ParamRegex
			cloned["displayname"] = matched.DisplayName
			exists = append(exists, cloned)

			pcs.Pid2Conf[meta.Pid] = append(pcs.Pid2Conf[meta.Pid], matched.ID())
			visited[matched.ID()] = struct{}{}
		}
	}

	conf2pids := make(map[string][]int32)
	for k, confs := range pcs.Pid2Conf {
		for _, conf := range confs {
			conf2pids[conf] = append(conf2pids[conf], k)
		}
	}
	pcs.Conf2Pid = conf2pids

	var notExists []common.MapStr
	for _, item := range pc.cmdbConf.MatchNotExists(visited) {
		notExists = append(notExists, common.MapStr{
			"exists":      false,
			"name":        item.Name,
			"paramregex":  item.ParamRegex,
			"displayname": item.DisplayName,
		})
	}
	logger.Debugf("pc.cmdbConf: %+v", pc.cmdbConf)
	logger.Debugf("visited: %+v", visited)
	logger.Debugf("not exists: %+v", notExists)
	logger.Debugf("pcs.Conf2Pid: %+v", pcs.Conf2Pid)

	if pc.cmdbConf.ConvergePID {
		exists = pc.aggregateStats(exists)
	}

	return exists, notExists, pcs
}

func (pc *ProcCollector) GetAllMetaData() ([]define.ProcStat, error) {
	pc.snapshotMut.Lock()
	defer pc.snapshotMut.Unlock()

	snapshot, err := pc.mgr.AllMetaData()
	pc.snapshot = snapshot
	pc.updatedTs = time.Now().Unix()
	pc.updatedErr = err

	return snapshot, err
}

func (pc *ProcCollector) Snapshot() ([]define.ProcStat, int64, error) {
	pc.snapshotMut.Lock()
	defer pc.snapshotMut.Unlock()

	return pc.snapshot, pc.updatedTs, pc.updatedErr
}

func (pc *ProcCollector) GetOnePerfStat(pid int32) define.ProcStat {
	return pc.mgr.GetOnePerfStat(pid)
}

func (pc *ProcCollector) GetOneMetaData(pid int32) (define.ProcStat, error) {
	return pc.mgr.GetOneMetaData(pid)
}

func (pc *ProcCollector) MergeMetaDataPerfStat(meta, perf define.ProcStat) define.ProcStat {
	return pc.mgr.MergeMetaDataPerfStat(meta, perf)
}

var ProcCustomPerfCollector = NewProcCollector()
