// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package active_profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// newGatherTestConfig 使用当前测试进程作为采集目标，保证 PID 在 preflight 时存在。
func newGatherTestConfig(t *testing.T, taskID int32) *configs.ActiveProfileTaskConfig {
	t.Helper()
	conf := newTestTaskConfig(t)
	conf.TaskID = taskID
	conf.Schedule = configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(time.Hour).UnixMilli(),
		Timezone:  "UTC",
	}
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type: configs.ActiveProfileSelectorPID,
		PIDs: strconv.Itoa(os.Getpid()),
	}
	conf.CollectorConfig.DurationSec = 1
	conf.InlineSymbolization = configs.ActiveProfileInlineOff
	conf.PerfPath = writeGatherFakePerf(t)
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	return conf
}

func TestGatherReportsStartedThenFinished(t *testing.T) {
	sink := newReportChannel()
	conf := newGatherTestConfig(t, 1101)
	newTestGather(conf).Run(context.Background(), sink)

	reports := captureReports(t, sink)
	events := reports.Events
	if len(events) != 2 {
		t.Fatalf("event count = %d, want started and finished", len(events))
	}
	// 产物与 Event 共用同一条 Beat 事件链路，按各自的 DataID 路由。
	if len(reports.Profiles) != 1 || reports.Profiles[0].DataID != conf.ProfileDataID {
		t.Fatalf("profile reports = %+v", reports.Profiles)
	}
	if events[0].EventName != startedEventName || events[1].EventName != finishedEventName {
		t.Fatalf("event order = %s, %s", events[0].EventName, events[1].EventName)
	}
	for _, event := range events {
		assertDimensionKeys(t, event)
		for _, removed := range []string{"run_id", "profile_id", "run_stage", "run_code", "symbolization_status"} {
			if _, exists := event.Dimension[removed]; exists {
				t.Errorf("%s must not carry removed field %q", event.EventName, removed)
			}
		}
	}
	finished := events[1]
	if got, _ := finished.Dimension["status"].(string); got != StatusSucceed {
		t.Fatalf("finished status = %q", got)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(finished.Event.Content), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Processes) != 1 || content.Processes[0].PID != int32(os.Getpid()) {
		t.Fatalf("processes = %+v", content.Processes)
	}
}

func TestGatherFailsWhenProcessSelectorMatchesNothing(t *testing.T) {
	sink := newReportChannel()
	conf := newGatherTestConfig(t, 1102)
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type:                    configs.ActiveProfileSelectorCommand,
		MatchPattern:            "a-command-line-that-does-not-exist-on-this-host",
		EnableDynamicCollection: true,
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	newTestGather(conf).Run(context.Background(), sink)

	events := captureReports(t, sink).Events
	if len(events) != 2 {
		t.Fatalf("event count = %d", len(events))
	}
	finished := events[1]
	if got, _ := finished.Dimension["status"].(string); got != StatusFailed {
		t.Fatalf("status = %q, want %s", got, StatusFailed)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(finished.Event.Content), &content); err != nil {
		t.Fatal(err)
	}
	if content.ErrorMessage == "" {
		t.Fatal("no matched process must explain itself in message")
	}
	if content.ErrorType != ErrorTypeProcessNotFound {
		t.Fatalf("error_type = %q, want %s", content.ErrorType, ErrorTypeProcessNotFound)
	}
}

// TestGatherLockBlocksSameTaskAndAllowsOthers 覆盖同 task 并发保护：同一个计划
// task 的第二轮直接以忙碌原因结束，不同 task 不互斥。
func TestGatherLockBlocksSameTaskAndAllowsOthers(t *testing.T) {
	outputDir := t.TempDir()
	conf := newGatherTestConfig(t, 1103)
	conf.OutputDir = outputDir
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}

	held, acquired, err := acquireTaskRunLock(conf)
	if err != nil || !acquired {
		t.Fatalf("first lock acquire = %v, err = %v", acquired, err)
	}
	defer held.Release()

	sink := newReportChannel()

	// 同 task 第二轮：抢锁失败，只上报一条 failed finished，不启动采集器。
	newTestGather(conf).Run(context.Background(), sink)
	events := captureReports(t, sink).Events
	if len(events) != 1 || events[0].EventName != finishedEventName {
		t.Fatalf("busy run events = %+v", events)
	}
	if got, _ := events[0].Dimension["status"].(string); got != StatusFailed {
		t.Fatalf("busy status = %q", got)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(events[0].Event.Content), &content); err != nil {
		t.Fatal(err)
	}
	if content.ErrorMessage != errTaskAlreadyRunning {
		t.Fatalf("busy error_message = %q", content.ErrorMessage)
	}

	// 不同 task 共享同一台主机时不互斥。
	other := newGatherTestConfig(t, 1104)
	other.OutputDir = outputDir
	if err := other.Clean(); err != nil {
		t.Fatal(err)
	}
	otherLock, acquired, err := acquireTaskRunLock(other)
	if err != nil || !acquired {
		t.Fatalf("different task lock acquire = %v, err = %v", acquired, err)
	}
	otherLock.Release()
}

func TestImmediateExecutionsDoNotUseTheTaskLock(t *testing.T) {
	conf := newGatherTestConfig(t, 30521)
	conf.Schedule = configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleImmediate,
		ExecuteAt: time.Now().UnixMilli(),
		Timezone:  "UTC",
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	first, acquired, err := acquireTaskRunLock(conf)
	if err != nil || !acquired || first != nil {
		t.Fatalf("immediate lock = %v, acquired = %v, err = %v", first, acquired, err)
	}
	second, acquired, err := acquireTaskRunLock(conf)
	if err != nil || !acquired || second != nil {
		t.Fatal("immediate executions must never block each other")
	}
}

func TestGatherKeepsArtifactsWhenConfigured(t *testing.T) {
	conf := newGatherTestConfig(t, 1105)
	conf.KeepArtifacts = true
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	newTestGather(conf).Run(context.Background(), newReportChannel())

	runDirs, err := os.ReadDir(filepath.Join(conf.OutputDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runDirs) != 1 || !runDirs[0].IsDir() {
		t.Fatalf("expected one kept run directory, got %v", runDirs)
	}
	for _, name := range []string{"perf.data", "profile.pprof"} {
		info, statErr := os.Stat(filepath.Join(conf.OutputDir, "runs", runDirs[0].Name(), name))
		if statErr != nil {
			t.Fatalf("kept artifact %s missing: %v", name, statErr)
		}
		if info.Size() <= 0 {
			t.Fatalf("kept artifact %s is empty", name)
		}
	}
}

func TestGatherRemovesRunDirectoryByDefault(t *testing.T) {
	conf := newGatherTestConfig(t, 1106)
	newTestGather(conf).Run(context.Background(), newReportChannel())

	runDirs, err := os.ReadDir(filepath.Join(conf.OutputDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runDirs) != 0 {
		t.Fatalf("run directory was not cleaned up: %v", runDirs)
	}
}

func TestSweepStaleRunDirsRemovesOnlyExpiredDirectories(t *testing.T) {
	outputDir := t.TempDir()
	runs := filepath.Join(outputDir, "runs")
	stale := filepath.Join(runs, "stale")
	fresh := filepath.Join(runs, "fresh")
	for _, dir := range []string{stale, fresh} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleRunDirs(outputDir, time.Hour)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale run directory should be removed, stat error = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh run directory must be kept: %v", err)
	}
}

func writeGatherFakePerf(t *testing.T) string {
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
      if [ "$1" = "-o" ]; then output="$2"; break; fi
      shift
    done
    printf 'fake perf data' >"$output"
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
