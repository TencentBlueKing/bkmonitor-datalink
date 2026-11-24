// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 进程级 CPU 时钟要能读到真实消耗：空转一段时间后读数必须增长。
func TestProcessCPUTimeTracksConsumedCPU(t *testing.T) {
	pid := int32(os.Getpid())
	before, err := processCPUTime(pid)
	if err != nil {
		t.Fatalf("processCPUTime() error = %v", err)
	}

	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
	}

	after, err := processCPUTime(pid)
	if err != nil {
		t.Fatalf("processCPUTime() error = %v", err)
	}
	if after-before < 20*time.Millisecond {
		t.Fatalf("cpu time grew by %s after spinning 50ms (before=%s after=%s)", after-before, before, after)
	}
}

// 实际采集读的是别的进程，内核对"读自己"和"读其他进程"走不同分支，必须单独覆盖：
// 子进程空转期间，读到的 CPU 时间要跟着增长。
func TestProcessCPUTimeReadsOtherProcess(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "while :; do :; done")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	pid := int32(child.Process.Pid)

	before, err := processCPUTime(pid)
	if err != nil {
		t.Fatalf("processCPUTime(child) error = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	after, err := processCPUTime(pid)
	if err != nil {
		t.Fatalf("processCPUTime(child) error = %v", err)
	}
	if after-before < 50*time.Millisecond {
		t.Fatalf("child cpu time grew by %s while spinning 200ms (before=%s after=%s)", after-before, before, after)
	}
}

func TestReadTargetCPUTimesSkipsUnreadableProcesses(t *testing.T) {
	pid := int32(os.Getpid())
	// 已退出并被回收的子进程，PID 不再对应任何进程。
	child := exec.Command("/bin/sh", "-c", "exit 0")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	exited := int32(child.Process.Pid)

	times := readTargetCPUTimes([]int32{pid, exited, 0})
	if _, ok := times[pid]; !ok {
		t.Fatalf("own process missing from %v", times)
	}
	if _, ok := times[exited]; ok {
		t.Fatalf("exited process must be skipped: %v", times)
	}
	// pid 0 在时钟编码里表示调用者自己，不能被当成目标读出来。
	if _, ok := times[0]; ok {
		t.Fatalf("pid 0 must be rejected: %v", times)
	}
}

// perf 正常结束但没有样本时，失败信息要带上目标的 CPU 占用和调参方向，并保持
// empty_profile 归类。
func TestExecutePerfExplainsEmptyProfile(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeNoSampleFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if !errors.Is(err, ErrEmptyProfile) {
		t.Fatalf("executePerf() error = %v, want ErrEmptyProfile", err)
	}
	for _, want := range []string{
		"target processes used ",
		"of CPU during the 15s sampling window",
		"at 99hz a sample needs 10.1ms of CPU",
		"raise frequency_hz or duration_sec",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if result.Success || result.Message != err.Error() {
		t.Fatalf("result should carry the same failure message: %+v", result)
	}
}

// writeNoSampleFakePerf 模拟采样窗口内目标没有被采到：record 成功，script 没有输出。
func writeNoSampleFakePerf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := `#!/bin/sh
set -eu
case "$1" in
  version)
    echo "perf version fake"
    ;;
  record)
    shift
    output=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-o" ]; then output="$2"; break; fi
      shift
    done
    printf 'fake perf data' >"$output"
    ;;
  script)
    ;;
  *)
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
