// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processsnapshot

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type procStateSnapshot struct {
	updatedAt time.Time
	states    map[int32]string
}

var sharedProcStateSnapshot struct {
	mut      sync.RWMutex
	snapshot procStateSnapshot
}

func UpdateSharedProcStateSnapshot(stats []define.ProcStat) {
	states := make(map[int32]string, len(stats))
	for _, stat := range stats {
		if stat.Status == "" {
			continue
		}
		states[stat.Pid] = stat.Status
	}

	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot = procStateSnapshot{
		updatedAt: time.Now(),
		states:    states,
	}
	sharedProcStateSnapshot.mut.Unlock()
}

func SharedProcStates(maxAge time.Duration) (map[int32]string, bool) {
	sharedProcStateSnapshot.mut.RLock()
	defer sharedProcStateSnapshot.mut.RUnlock()

	snapshot := sharedProcStateSnapshot.snapshot
	if snapshot.updatedAt.IsZero() || len(snapshot.states) == 0 {
		return nil, false
	}
	if maxAge > 0 && time.Since(snapshot.updatedAt) > maxAge {
		return nil, false
	}

	states := make(map[int32]string, len(snapshot.states))
	for pid, state := range snapshot.states {
		states[pid] = state
	}

	return states, true
}
