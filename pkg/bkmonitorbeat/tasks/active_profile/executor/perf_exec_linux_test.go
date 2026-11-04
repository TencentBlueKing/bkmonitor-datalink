// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunPerfCPUExecutesPerfDirectly(t *testing.T) {
	conf := newTestTaskConfig(t, int32(os.Getpid()))
	conf.PerfPath = writeFakePerf(t)
	runDir := filepath.Join(conf.OutputDir, "run")

	result, err := RunCPU(context.Background(), conf, Request{
		ScheduledAt: time.UnixMilli(conf.Schedule.ExecuteAt),
		PIDs:        []int32{int32(os.Getpid())},
		RunDir:      runDir,
	})
	if err != nil {
		t.Fatalf("RunCPU() error = %v result=%+v", err, result)
	}
	path := result.ProfilePath
	if path == "" || result.SampleCount != 1 || !result.Success {
		t.Fatalf("unexpected path=%q result=%+v", path, result)
	}
	// perf 的产物由 perf 自己写盘，必须落在调用方给的运行目录里，不进内存。
	if filepath.Dir(path) != runDir {
		t.Fatalf("profile %q was not written inside the caller run directory %q", path, runDir)
	}
	if result.ProfileData != nil {
		t.Error("perf profiles must stay on disk, not be buffered in memory")
	}
	if len(result.CollectedPIDs) != 1 || result.CollectedPIDs[0] != int32(os.Getpid()) {
		t.Fatalf("collected pids = %v", result.CollectedPIDs)
	}
}

func TestExecutePerfStreamsScriptAndCleansPerfData(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err != nil {
		t.Fatalf("executePerf() error = %v result=%+v", err, result)
	}
	if !result.Success || result.SampleCount != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.PerfDataBytes <= 0 || result.ProfileBytes <= 0 {
		t.Fatalf("artifact sizes should be recorded: %+v", result)
	}
	if result.PerfDataPath != "" {
		t.Fatalf("perf.data should be removed by default: %+v", result)
	}
	if _, err := os.Stat(result.ProfilePath); err != nil {
		t.Fatalf("profile missing: %v", err)
	}
	// 运行目录由调用方负责清理，采集器只保留最终产物。
	entries, err := os.ReadDir(conf.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "profile.pprof" {
		t.Fatalf("unexpected run directory entries: %v", entries)
	}
}

func TestExecutePerfKeepsArtifactsForDebugging(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeFakePerf(t)
	conf.OutputDir = t.TempDir()
	conf.KeepArtifacts = true

	result, err := executePerf(context.Background(), conf)
	if err != nil {
		t.Fatalf("executePerf() error = %v result=%+v", err, result)
	}
	for path, wantSize := range map[string]int64{
		result.PerfDataPath: result.PerfDataBytes,
		result.ProfilePath:  result.ProfileBytes,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("kept artifact %q missing: %v", path, statErr)
		}
		if info.Size() != wantSize || wantSize <= 0 {
			t.Fatalf("artifact %q size=%d, recorded=%d", path, info.Size(), wantSize)
		}
	}
}

// TestPerfCommandsCoverAllTargetPIDs 覆盖多进程采集：一次 perf record 用逗号分隔
// 的 PID 列表覆盖全部目标，产出单个合并产物。
func TestPerfCommandsCoverAllTargetPIDs(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{2341, 2342, 812}

	for _, args := range [][]string{
		buildRecordArgs(conf, conf.PIDs, "/tmp/perf.data"),
		buildProbeArgs(conf, conf.PIDs, "/tmp/preflight.data"),
	} {
		assertArgumentPair(t, args, "-p", "2341,2342,812")
		if contains(args, "-a") {
			t.Fatalf("pid scoped profiling must not request system wide: %v", args)
		}
	}
}

func TestPerfCommandsUseSystemWideOnlyForLocalDebugging(t *testing.T) {
	conf := validPerfConfig()
	conf.SystemWide = true
	conf.PIDs = nil
	args := buildRecordArgs(conf, nil, "/tmp/perf.data")
	if !contains(args, "-a") {
		t.Fatalf("system wide profiling must pass -a: %v", args)
	}
	if contains(args, "-p") {
		t.Fatalf("system wide profiling must not pass -p: %v", args)
	}
}

// build-id 缓存在运行目录之外，不受磁盘配额约束也不会被清理，两条 record 都不能写它。
func TestPerfRecordSkipsBuildIDCache(t *testing.T) {
	conf := validPerfConfig()
	for _, args := range [][]string{
		buildRecordArgs(conf, conf.PIDs, "/tmp/perf.data"),
		buildProbeArgs(conf, conf.PIDs, "/tmp/preflight.data"),
	} {
		if !contains(args, "-N") {
			t.Fatalf("perf record must not update the build-id cache: %v", args)
		}
	}
}

func TestPerfCommandsUseConfiguredStackUnwind(t *testing.T) {
	conf := validPerfConfig()
	conf.CallGraph = callGraphDWARF

	assertArgumentPair(t, buildProbeArgs(conf, conf.PIDs, "/tmp/preflight.data"), "--call-graph", "dwarf")
	assertArgumentPair(t, buildRecordArgs(conf, conf.PIDs, "/tmp/perf.data"), "--call-graph", "dwarf")
}

// TestPreflightDropsUnavailablePIDs 覆盖"解析之后进程退出"的预期结果：只要还有一个
// 目标可用就继续采集，收敛后的目标进入 CollectedPIDs。
func TestPreflightDropsUnavailablePIDs(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid()), 1 << 30}
	conf.PerfPath = writeFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err != nil {
		t.Fatalf("executePerf() error = %v result=%+v", err, result)
	}
	if len(result.CollectedPIDs) != 1 || result.CollectedPIDs[0] != int32(os.Getpid()) {
		t.Fatalf("collected pids = %v", result.CollectedPIDs)
	}
}

func TestPreflightFailsWhenNoTargetIsAvailable(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{1 << 30}
	conf.PerfPath = writeFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err == nil {
		t.Fatalf("expected preflight failure, result = %+v", result)
	}
	if !strings.Contains(result.Message, "no target process is available") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}

func TestExecutePerfResourceGuardRejectsBeforePerf(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "perf-started")
	perfPath := filepath.Join(t.TempDir(), "perf")
	if err := os.WriteFile(perfPath, []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\n", marker)), 0o700); err != nil {
		t.Fatal(err)
	}
	probe := &fakeResourceProbe{memoryAvailable: 512}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = perfPath
	conf.OutputDir = t.TempDir()
	conf.ResourceGuard = guard
	conf.ResourceGuardPolicy = ResourceGuardPolicy{
		RunDir:             conf.OutputDir,
		MinFreeMemoryBytes: 512,
		MaxDiskUsageBytes:  1024,
	}

	result, err := executePerf(context.Background(), conf)
	var guardErr *ResourceGuardError
	if !errors.As(err, &guardErr) || guardErr.Resource != resourceMemory {
		t.Fatalf("executePerf() error=%v result=%+v", err, result)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("perf should not start after admission rejection: %v", statErr)
	}
}

func TestExecutePerfResourceGuardStopsRunningPerf(t *testing.T) {
	started := filepath.Join(t.TempDir(), "record-started")
	perfPath := writeBlockingFakePerf(t, started)
	probe := &fakeResourceProbe{memoryAvailable: 4096}
	guard := newResourceGuard(probe, 10*time.Millisecond)
	defer guard.Close()
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = perfPath
	conf.OutputDir = t.TempDir()
	conf.ResourceGuard = guard
	conf.ResourceGuardPolicy = ResourceGuardPolicy{
		RunDir:             conf.OutputDir,
		MinFreeMemoryBytes: 1024,
	}

	type response struct {
		result perfResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := executePerf(context.Background(), conf)
		done <- response{result: result, err: err}
	}()
	waitForFile(t, started)
	probe.set(512, 0)
	select {
	case response := <-done:
		var guardErr *ResourceGuardError
		if !errors.As(response.err, &guardErr) || guardErr.Resource != resourceMemory {
			t.Fatalf("executePerf() error=%v result=%+v", response.err, response.result)
		}
		if response.result.Success {
			t.Fatalf("guarded run must not report success: %+v", response.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resource guard did not stop perf")
	}
}

func TestExecutePerfStopsPerfScriptOnConversionLimit(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeBlockingScriptFakePerf(t)
	conf.OutputDir = t.TempDir()
	conf.ProfileBuildLimits = profileBuildLimits{MaxUniqueSamples: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := executePerf(ctx, conf)
	if err == nil || !isProfileConversionLimitError(err) {
		t.Fatalf("executePerf() error=%v result=%+v", err, result)
	}
	if _, statErr := os.Stat(result.ProfilePath); !os.IsNotExist(statErr) {
		t.Fatalf("conversion limit left a partial pprof: %v", statErr)
	}
}

// TestPreflightSelectsDSOFFWhenSupported 覆盖 perf >= 6.4：首选字段集被接受。
func TestPreflightSelectsDSOFFWhenSupported(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err != nil {
		t.Fatalf("executePerf() error = %v result=%+v", err, result)
	}
	if !strings.Contains(result.Preflight.ScriptFields, "dsoff") ||
		result.Preflight.OffsetMode != offsetModeDSOFF {
		t.Fatalf("unexpected field probe result: %+v", result.Preflight)
	}
}

// TestPreflightFallsBackWhenDSOFFRejected 覆盖 perf < 6.4：首选字段集被拒后降级到
// legacy 字段集，并且照样完成采集与符号化——低版本不是降级方案，偏移信息一直都在。
func TestPreflightFallsBackWhenDSOFFRejected(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeLegacyFakePerf(t)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err != nil {
		t.Fatalf("executePerf() error = %v result=%+v", err, result)
	}
	if strings.Contains(result.Preflight.ScriptFields, "dsoff") {
		t.Fatalf("legacy perf must not keep the dsoff field: %q", result.Preflight.ScriptFields)
	}
	if result.Preflight.OffsetMode != offsetModeLegacyIP {
		t.Fatalf("legacy perf must read the first column as an offset: %+v", result.Preflight)
	}
	if !result.Success || result.SampleCount != 1 {
		t.Fatalf("legacy perf should still produce a profile: %+v", result)
	}
}

// TestPreflightFailsWhenNoFieldSetIsAccepted 覆盖两级都被拒的情况：必须在采样之前
// 就失败，而不是跑完整个采样窗口再报错。
func TestPreflightFailsWhenNoFieldSetIsAccepted(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "record-invoked")
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeScriptRejectingFakePerf(t, marker)
	conf.OutputDir = t.TempDir()

	result, err := executePerf(context.Background(), conf)
	if err == nil {
		t.Fatalf("expected a preflight failure, result = %+v", result)
	}
	if !strings.Contains(result.Message, "no supported perf script field set") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
	// preflight 的 record 探针会跑，但真正的采样不应该开始。
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Count(string(data), "sample\n") != 0 {
		t.Fatalf("the sampling window must not start: %q", data)
	}
}

// TestPreflightCommandTimeoutIsPerfUnavailable 覆盖 CPU 配额下 perf 探测过慢：preflight
// 命令超时按 perf 不可用失败，不能带出 DeadlineExceeded 被归为整次执行 timeout。
func TestPreflightCommandTimeoutIsPerfUnavailable(t *testing.T) {
	original := preflightCommandTimeout
	preflightCommandTimeout = 200 * time.Millisecond
	t.Cleanup(func() { preflightCommandTimeout = original })

	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeSlowProbeFakePerf(t)
	conf.OutputDir = t.TempDir()

	_, err := executePerf(context.Background(), conf)
	if !errors.Is(err, ErrPerfUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executePerf() error = %v, want perf unavailable without deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "perf event probe failed: timed out after 200ms") {
		t.Fatalf("error does not name the timed out probe: %v", err)
	}
}

// TestPreflightKeepsExecutionDeadline 保证整次执行预算耗尽时仍然报 timeout。
func TestPreflightKeepsExecutionDeadline(t *testing.T) {
	conf := validPerfConfig()
	conf.PIDs = []int32{int32(os.Getpid())}
	conf.PerfPath = writeSlowProbeFakePerf(t)
	conf.OutputDir = t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := executePerf(ctx, conf)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executePerf() error = %v, want the execution deadline", err)
	}
}

func contains(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

// writeLegacyFakePerf 模拟 perf < 6.4：script 拒绝含 dsoff 的字段集，输出里也没有
// dsoff，非内核帧的第一列本身就是文件偏移。
func writeLegacyFakePerf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  version)
    echo "perf version 4.19.90"
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
    for arg in "$@"; do
      case "$arg" in
        *dsoff*)
          echo "Invalid field requested: dsoff" >&2
          exit 129
          ;;
      esac
    done
    cat <<'EOF'
perf-busy %d 1.000: 10101010 cpu-clock:
                  40040f busy3+0xf (/tmp/perf-busy)

EOF
    ;;
  *)
    exit 2
    ;;
esac
`, os.Getpid())
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeScriptRejectingFakePerf 模拟所有候选字段集都不被接受的 perf，并记录 record
// 的调用方式，用于断言真正的采样窗口没有开始。
func writeScriptRejectingFakePerf(t *testing.T, marker string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  version)
    echo "perf version 3.10.0"
    ;;
  record)
    shift
    output=""
    is_probe=0
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-o" ]; then output="$2"; fi
      if [ "$1" = "0.05" ]; then is_probe=1; fi
      shift
    done
    if [ "$is_probe" -eq 1 ]; then
      echo "probe" >>%q
    else
      echo "sample" >>%q
    fi
    printf 'fake perf data' >"$output"
    ;;
  script)
    echo "Invalid field requested" >&2
    exit 129
    ;;
  *)
    exit 2
    ;;
esac
`, marker, marker)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBlockingFakePerf(t *testing.T, started string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  version)
    echo "perf version fake"
    ;;
  record)
    shift
    output=""
    is_probe=0
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-o" ]; then output="$2"; fi
      if [ "$1" = "0.05" ]; then is_probe=1; fi
      shift
    done
    if [ "$is_probe" -eq 1 ]; then
      printf 'probe' >"$output"
      exit 0
    fi
    touch %q
    trap 'exit 130' INT
    while :; do sleep 1; done
    ;;
  script)
    exit 0
    ;;
esac
`, started)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBlockingScriptFakePerf(t *testing.T) string {
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
    case "$*" in
      *.perf-preflight-*) exit 0 ;;
    esac
    cat <<'EOF'
cpu 1 1.000: 10 cpu-clock:
  1 first (/tmp/a)

cpu 2 1.100: 10 cpu-clock:
  1 first (/tmp/a)

EOF
    while :; do sleep 1; done
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSlowProbeFakePerf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := `#!/bin/sh
case "$1" in
  version)
    echo "perf version fake"
    ;;
  record)
    exec sleep 30
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertArgumentPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("arguments %q do not contain %q %q", args, key, value)
}

func writeFakePerf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  version)
    echo "perf version fake"
    ;;
  record)
    shift
    output=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-o" ]; then
        output="$2"
        break
      fi
      shift
    done
    printf 'fake perf data' >"$output"
    echo "[ perf record: Captured and wrote data (1 samples) ]" >&2
    ;;
  script)
    cat <<'EOF'
perf-busy %d 1.000: 10101010 cpu-clock:
                  40040f busy3+0xf (/tmp/perf-busy+0x40f)

EOF
    ;;
  *)
    exit 2
    ;;
esac
`, os.Getpid())
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
