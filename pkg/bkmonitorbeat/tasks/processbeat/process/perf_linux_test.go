//go:build linux

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
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestProcPerfMgrUsesHostProcRoot(t *testing.T) {
	procRoot := filepath.Join(t.TempDir(), "hostproc")
	writeProcBootTime(t, procRoot, 1700000000)
	writeLinuxProc(t, procRoot, 424242, "host-worker", "S", 1, 12345, "host-worker\x00--config\x00x", 1000)
	t.Setenv("HOST_PROC", procRoot)

	stat, err := (&procPerfMgr{}).GetOneMetaData(424242)
	if err != nil {
		t.Fatalf("GetOneMetaData error: %v", err)
	}
	if stat.Name != "host-worker" || stat.Pid != 424242 || stat.PPid != 1 {
		t.Fatalf("unexpected stat from HOST_PROC: %+v", stat)
	}
	if stat.Cmd != "host-worker --config x" {
		t.Fatalf("cmd = %q, want host-worker --config x", stat.Cmd)
	}
}

func TestProcPerfMgrMetaDataDoesNotTouchPerfCaches(t *testing.T) {
	procRoot := filepath.Join(t.TempDir(), "hostproc")
	writeProcBootTime(t, procRoot, 1700000000)
	writeLinuxProc(t, procRoot, 424243, "host-worker", "S", 1, 12345, "host-worker\x00", 1000)
	t.Setenv("HOST_PROC", procRoot)

	cpuStat := define.CPUStat{Ts: time.Unix(100, 0), Total: 1234}
	ioStat := define.IOStat{Ts: time.Unix(100, 0), ReadBytes: 10, WriteBytes: 20}
	mgr := &procPerfMgr{
		cpuCache: map[int32]define.CPUStat{424243: cpuStat},
		ioCache:  map[int32]define.IOStat{424243: ioStat},
	}

	if _, err := mgr.GetOneMetaData(424243); err != nil {
		t.Fatalf("GetOneMetaData error: %v", err)
	}
	if got := mgr.cpuCache[424243]; got != cpuStat {
		t.Fatalf("cpu cache changed after metadata read: got %+v, want %+v", got, cpuStat)
	}
	if got := mgr.ioCache[424243]; got != ioStat {
		t.Fatalf("io cache changed after metadata read: got %+v, want %+v", got, ioStat)
	}
}

func writeProcBootTime(t *testing.T, procRoot string, boot uint64) {
	t.Helper()

	if err := os.MkdirAll(procRoot, 0o755); err != nil {
		t.Fatalf("mkdir proc root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "stat"), []byte("btime "+strconv.FormatUint(boot, 10)+"\n"), 0o644); err != nil {
		t.Fatalf("write proc stat: %v", err)
	}
}
