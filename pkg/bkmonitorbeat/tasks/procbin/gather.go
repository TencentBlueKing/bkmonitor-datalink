// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procbin

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/procsnapshot"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	running atomic.Bool
	config  *configs.ProcBinConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcBinConfig)

	gather.Init()
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	if g.running.Load() {
		logger.Info("ProcBin task has running, will skip")
		return
	}

	g.running.Store(true)
	defer g.running.Store(false)

	now := time.Now()
	procs, err := procsnapshot.AllProcsMetaWithCache(g.config.Period)
	if err != nil {
		logger.Errorf("faile to get all procs meta: %v", err)
		return
	}

	var procbins []ProcBin
	pcs := make(map[pidCreated]struct{})
	for i := 0; i < len(procs); i++ {
		proc := procs[i]
		if proc.Cmd == "" {
			continue
		}

		pc := pidCreated{pid: proc.Pid, created: proc.Created}
		si := readStatInfo(pc, proc.Exe, g.config.MaxBytes)
		pcs[pc] = struct{}{}

		procbins = append(procbins, ProcBin{
			Pid:        proc.Pid,
			Uid:        si.Uid,
			MD5:        si.MD5,
			Path:       si.Path,
			Size:       si.Size,
			IsLargeBin: si.IsLargeBin,
			IsDeleted:  si.IsDeleted,
			Modify:     si.Modify.Unix(),
			Change:     si.Change.Unix(),
			Access:     si.Access.Unix(),
		})
	}
	e <- &Event{dataid: g.config.DataID, data: procbins, utcTime: now}
	cleanupCached(pcs)
}

type StatInfo struct {
	Path       string
	Size       int64
	Uid        uint32
	MD5        string
	Modify     time.Time
	Access     time.Time
	Change     time.Time
	IsLargeBin bool
	IsDeleted  bool
}

func readStatInfo(pc pidCreated, path string, maxSize int64) *StatInfo {
	info, err := os.Stat(path)
	if err != nil {
		return &StatInfo{
			Path:      path,
			IsDeleted: true,
		}
	}

	var si StatInfo
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		si.Path = path
		si.Size = info.Size()
		si.Modify = info.ModTime()
	} else {
		si.Path = path
		si.Size = sys.Size
		si.Uid = sys.Uid
		si.Modify = time.Unix(0, sys.Mtim.Nano())
		si.Access = time.Unix(0, sys.Atim.Nano())
		si.Change = time.Unix(0, sys.Ctim.Nano())
	}

	if si.Size > maxSize {
		si.IsLargeBin = true
		return &si
	}

	si.MD5 = hashWithCached(pc, path)
	return &si
}
