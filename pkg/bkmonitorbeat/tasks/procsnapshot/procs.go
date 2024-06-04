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
	"sync"
	"time"

	shiroups "github.com/shirou/gopsutil/v3/process"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcMeta struct {
	Pid      int32  `json:"pid"`
	PPid     int32  `json:"ppid"`
	Cwd      string `json:"cwd"`
	Cmd      string `json:"cmd"`
	Created  int64  `json:"created"`
	Uid      int32  `json:"uid"`
	Tid      int32  `json:"tid"`
	Exe      string `json:"exe"`
	Name     string `json:"name"`
	Username string `json:"username"`
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
	meta.Tid, _ = proc.Tgid()

	uids, _ := proc.Uids()
	if len(uids) > 0 {
		meta.Uid = uids[0]
	}
	return meta, nil
}

func AllProcsMeta() ([]ProcMeta, error) {
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

var (
	metaCache   []ProcMeta
	metaMut     sync.RWMutex
	metaUpdated time.Time
)

func copyProcsMeta(meta []ProcMeta) []ProcMeta {
	dst := make([]ProcMeta, 0, len(meta))
	for i := 0; i < len(meta); i++ {
		d := meta[i]
		dst = append(dst, d)
	}
	return dst
}

func AllProcsMetaWithCache(d time.Duration) ([]ProcMeta, error) {
	metaMut.Lock()
	defer metaMut.Unlock()

	fn := func() ([]ProcMeta, error) {
		meta, err := AllProcsMeta()
		if err != nil {
			return nil, err
		}

		metaUpdated = time.Now()
		metaCache = meta
		return copyProcsMeta(meta), nil
	}

	// 缓存从未更新
	if metaUpdated.IsZero() {
		return fn()
	}

	// 缓存已经更新过
	// 如果在 duration 周期内 则使用缓存
	if float64(time.Now().Unix()-metaUpdated.Unix()) < d.Seconds() {
		return copyProcsMeta(metaCache), nil
	}

	// 大于缓存周期了
	return fn()
}
