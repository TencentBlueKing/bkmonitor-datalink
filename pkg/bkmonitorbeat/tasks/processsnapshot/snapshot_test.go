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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestSharedProcStates(t *testing.T) {
	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot = procStateSnapshot{}
	sharedProcStateSnapshot.mut.Unlock()

	UpdateSharedProcStateSnapshot([]define.ProcStat{
		{Pid: 1, Status: "running"},
		{Pid: 2, Status: "zombie"},
		{Pid: 3, Status: "zombie"},
	})

	snapshot, ok := SharedProcStates(time.Minute)
	if !ok {
		t.Fatal("expected shared snapshot to be available")
	}
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 process states, got %d", len(snapshot))
	}
	if snapshot[2] != "zombie" {
		t.Fatalf("expected pid 2 state zombie, got %s", snapshot[2])
	}

	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot.updatedAt = time.Now().Add(-2 * time.Minute)
	sharedProcStateSnapshot.mut.Unlock()

	if _, ok := SharedProcStates(time.Minute); ok {
		t.Fatal("expected stale shared snapshot to be rejected")
	}
}
