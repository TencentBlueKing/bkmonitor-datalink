// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// 匹配预览的输出直接被 SaaS 解析：stdout 必须是单个 JSON 对象，字段固定为
// pid/process_name/command/started_at，匹配不到时是空数组而不是 null 或报错。
func TestMatchCLIPrintsMatchedProcesses(t *testing.T) {
	t.Run("pid selector describes the live process", func(t *testing.T) {
		request := fmt.Sprintf(`{"process_selector":{"type":"pid","pids":"%d"}}`, os.Getpid())
		var out bytes.Buffer
		if err := MatchCLI(strings.NewReader(request), &out); err != nil {
			t.Fatalf("MatchCLI() error = %v", err)
		}
		var response struct {
			Processes []map[string]any `json:"processes"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatalf("stdout is not a single JSON object: %v (%q)", err, out.String())
		}
		if len(response.Processes) != 1 {
			t.Fatalf("processes = %+v, want exactly the test process", response.Processes)
		}
		process := response.Processes[0]
		for _, key := range []string{"pid", "process_name", "command", "started_at"} {
			if _, exists := process[key]; !exists {
				t.Errorf("matched process is missing %q: %+v", key, process)
			}
		}
		if len(process) != 4 {
			t.Errorf("matched process carries %d fields, want exactly 4: %+v", len(process), process)
		}
		if pid, _ := process["pid"].(float64); int(pid) != os.Getpid() {
			t.Errorf("pid = %v, want %d", process["pid"], os.Getpid())
		}
		if command, _ := process["command"].(string); command == "" {
			t.Error("command must carry the full command line")
		}
		if startedAt, _ := process["started_at"].(float64); startedAt <= 0 {
			t.Errorf("started_at = %v, want the process start time in milliseconds", process["started_at"])
		}
	})

	// 匹配不到进程是合法结果，页面要显示"没有匹配到"，不是执行失败。
	t.Run("no match yields an empty array", func(t *testing.T) {
		request := `{"process_selector":{"type":"command","match_pattern":"a-command-line-that-does-not-exist-on-this-host"}}`
		var out bytes.Buffer
		if err := MatchCLI(strings.NewReader(request), &out); err != nil {
			t.Fatalf("MatchCLI() error = %v", err)
		}
		if got := strings.TrimSpace(out.String()); got != `{"processes":[]}` {
			t.Fatalf("stdout = %q, want an empty processes array", got)
		}
	})

	// 选择器校验与下发配置共用同一个入口，非法选择器在预览阶段就该被拒。
	t.Run("invalid selectors are rejected", func(t *testing.T) {
		for name, request := range map[string]string{
			"unsupported type":     `{"process_selector":{"type":"nope"}}`,
			"command without rule": `{"process_selector":{"type":"command"}}`,
			"bad exclude regexp":   `{"process_selector":{"type":"command","match_pattern":"java","exclude_pattern":"("}}`,
			"pid without pids":     `{"process_selector":{"type":"pid"}}`,
		} {
			t.Run(name, func(t *testing.T) {
				var out bytes.Buffer
				err := MatchCLI(strings.NewReader(request), &out)
				if err == nil {
					t.Fatalf("MatchCLI() error = nil, want a selector validation error, stdout=%q", out.String())
				}
				if out.Len() != 0 {
					t.Fatalf("a failed match must not print a result: %q", out.String())
				}
				// 失败原因单独输出为 JSON，error_type 与 finished 同一套取值
				var failureOut bytes.Buffer
				WriteMatchFailure(&failureOut, err)
				var failure cliMatchFailure
				if decodeErr := json.Unmarshal(failureOut.Bytes(), &failure); decodeErr != nil {
					t.Fatalf("match failure must be JSON: %q", failureOut.String())
				}
				if failure != (cliMatchFailure{ErrorType: ErrorTypeConfigInvalid, Message: err.Error()}) {
					t.Fatalf("match failure = %+v", failure)
				}
			})
		}
	})
}

// TestExecutionKeyIsSharedByCollectorAndCancel 是这套取消机制的核心不变量：采集端
// 由任务配置推导执行键，取消端由请求字段推导，两者必须完全一致，否则取消永远定位
// 不到运行目录。
func TestExecutionKeyIsSharedByCollectorAndCancel(t *testing.T) {
	scheduledAt := time.UnixMilli(1789032616000)

	t.Run("planned carries task_id", func(t *testing.T) {
		conf := newTestTaskConfig(t)
		conf.TaskID = 1204
		conf.BKHostID = 1002
		if err := conf.Clean(); err != nil {
			t.Fatal(err)
		}
		request := cliCancelRequest{
			BKTenantID:   conf.BKTenantID,
			BizID:        conf.GetBizID(),
			BKHostID:     conf.BKHostID,
			TaskID:       conf.GetTaskID(),
			ScheduleType: configs.ActiveProfileScheduleCron,
			ScheduledAt:  scheduledAt.UnixMilli(),
		}
		fromCancel, err := request.executionKey()
		if err != nil {
			t.Fatal(err)
		}
		if fromCollector := executionKey(conf, scheduledAt); fromCollector != fromCancel {
			t.Fatalf("execution key mismatch: collector=%q cancel=%q", fromCollector, fromCancel)
		}
	})

	// 立即执行的 task_id 是 SaaS 建的执行行 ID，同样进入执行键
	t.Run("immediate carries task_id", func(t *testing.T) {
		conf := newTestTaskConfig(t)
		conf.TaskID = 30521
		conf.BKHostID = 1002
		conf.Schedule = configs.ActiveProfileScheduleConfig{
			Type:      configs.ActiveProfileScheduleImmediate,
			ExecuteAt: scheduledAt.UnixMilli(),
			Timezone:  "UTC",
		}
		if err := conf.Clean(); err != nil {
			t.Fatal(err)
		}
		request := cliCancelRequest{
			BKTenantID:   conf.BKTenantID,
			BizID:        conf.GetBizID(),
			BKHostID:     conf.BKHostID,
			TaskID:       conf.GetTaskID(),
			ScheduleType: configs.ActiveProfileScheduleImmediate,
			ScheduledAt:  scheduledAt.UnixMilli(),
		}
		fromCancel, err := request.executionKey()
		if err != nil {
			t.Fatal(err)
		}
		if fromCollector := executionKey(conf, scheduledAt); fromCollector != fromCancel {
			t.Fatalf("execution key mismatch: collector=%q cancel=%q", fromCollector, fromCancel)
		}
	})
}

func TestCancelRequestValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		request cliCancelRequest
		wantErr bool
	}{
		{
			name: "immediate accepted with task_id",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002, TaskID: 30521,
				ScheduleType: configs.ActiveProfileScheduleImmediate, ScheduledAt: 1,
			},
		},
		{
			name: "cron accepted with task_id",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002, TaskID: 1204,
				ScheduleType: configs.ActiveProfileScheduleCron, ScheduledAt: 1,
			},
		},
		{
			name: "once_at accepted with task_id",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002, TaskID: 1204,
				ScheduleType: configs.ActiveProfileScheduleOnceAt, ScheduledAt: 1,
			},
		},
		{
			name: "planned without task_id rejected",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002,
				ScheduleType: configs.ActiveProfileScheduleCron, ScheduledAt: 1,
			},
			wantErr: true,
		},
		{
			name: "immediate without task_id rejected",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002,
				ScheduleType: configs.ActiveProfileScheduleImmediate, ScheduledAt: 1,
			},
			wantErr: true,
		},
		{
			name: "unsupported schedule type rejected",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, BKHostID: 1002,
				ScheduleType: "once", ScheduledAt: 1,
			},
			wantErr: true,
		},
		{
			name: "missing host rejected",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 11, TaskID: 30521,
				ScheduleType: configs.ActiveProfileScheduleImmediate, ScheduledAt: 1,
			},
			wantErr: true,
		},
		{
			name: "negative bk_biz_id accepted",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: -2, BKHostID: 1002, TaskID: 30521,
				ScheduleType: configs.ActiveProfileScheduleImmediate, ScheduledAt: 1,
			},
		},
		{
			name: "zero bk_biz_id rejected",
			request: cliCancelRequest{
				BKTenantID: "system", BizID: 0, BKHostID: 1002, TaskID: 30521,
				ScheduleType: configs.ActiveProfileScheduleImmediate, ScheduledAt: 1,
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.request.executionKey()
			if test.wantErr && err == nil {
				t.Fatal("expected a validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestCancelMarkerStopsRunningExecution 覆盖取消的完整语义：标记出现后本次执行被
// 取消，且取消原因能收敛成 cancelled 终态。
func TestCancelMarkerStopsRunningExecution(t *testing.T) {
	outputDir := t.TempDir()
	key := buildExecutionKey("system", 11, 1204, 1789032616000, 1002)
	runDir := runDirFor(outputDir, key)
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := watchCancelMarker(ctx, runDir, cancel)
	defer stop()

	select {
	case <-ctx.Done():
		t.Fatal("execution was cancelled before any cancel request")
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := requestCancel(outputDir, key); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancel marker did not stop the execution")
	}
	if got := statusFor(ctx, context.Cause(ctx)); got != StatusCancelled {
		t.Fatalf("status = %q, want %q", got, StatusCancelled)
	}
}

// 取消命令早于采集启动时，标记已经落盘，采集启动应当立刻结束。
func TestCancelMarkerWrittenBeforeRunStartsIsDetectedImmediately(t *testing.T) {
	outputDir := t.TempDir()
	key := buildExecutionKey("system", 11, 0, 1789032616000, 1002)
	if _, err := requestCancel(outputDir, key); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := watchCancelMarker(ctx, runDirFor(outputDir, key), cancel)
	defer stop()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("a marker present at startup must cancel the execution immediately")
	}
}

// 重复取消与目标已结束都必须是安全无操作。
func TestRequestCancelIsIdempotent(t *testing.T) {
	outputDir := t.TempDir()
	key := buildExecutionKey("system", 11, 1204, 1789032616000, 1002)
	first, err := requestCancel(outputDir, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := requestCancel(outputDir, key)
	if err != nil {
		t.Fatalf("repeating a cancel must be a safe no-op: %v", err)
	}
	if first != second {
		t.Fatalf("cancel marker path changed between calls: %q vs %q", first, second)
	}
	if !cancelRequested(runDirFor(outputDir, key)) {
		t.Fatal("cancel marker should be present")
	}
}

// 运行目录不可写时必须明确报错，不能让用户以为已经取消。
func TestRequestCancelFailsLoudlyWhenRunDirIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	outputDir := t.TempDir()
	key := buildExecutionKey("system", 11, 0, 1789032616000, 1002)
	runDir := runDirFor(outputDir, key)
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(runDir, 0o750) })

	if _, err := requestCancel(outputDir, key); err == nil {
		t.Fatal("writing the cancel marker into a read-only run directory must fail loudly")
	}
}

func TestRunCLIRejectsPlannedSchedule(t *testing.T) {
	payload := `{
      "bk_tenant_id": "system",
      "bk_biz_id": 11,
      "bk_host_id": 1002,
      "task_id": 1201,
      "name": "planned-should-not-run-here",
      "collection_method": "perf",
      "collection_type": "native_cpu",
      "schedule": {"type": "once_at", "execute_at": 1789032616000, "timezone": "UTC"},
      "process_selector": {"type": "pid", "pids": "1"},
      "collector_config": {"duration_sec": 15, "frequency_hz": 99, "stack_unwind": "fp"},
      "resource_guard_config": {"auto_stop_on_resource_pressure": false},
      "event_data_id": 527804,
      "profile_data_id": 524588,
      "profile_app_name": "builtin_profile_app",
      "profile_service_name": "builtin_profile_app"
    }`
	// 拒绝发生在解析配置之前，因此不需要主配置也能断言。
	err := RunCLI(strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "only executes immediate schedules") {
		t.Fatalf("planned schedules must be rejected before any config resolution, got %v", err)
	}
}

func TestCLIPayloadBounds(t *testing.T) {
	if _, err := readCLIPayload(strings.NewReader("")); err == nil {
		t.Fatal("empty payload must be rejected")
	}
	oversized := strings.Repeat("x", maxCLIPayloadBytes+1)
	if _, err := readCLIPayload(strings.NewReader(oversized)); err == nil {
		t.Fatal("oversized payload must be rejected")
	}
}

// 运行目录必须落在 <output_dir>/runs 之下，取消命令才能按同一规则推导。
func TestRunDirLivesUnderOutputRoot(t *testing.T) {
	outputDir := "/var/lib/bkmonitorbeat/active_profile"
	dir := runDirFor(outputDir, buildExecutionKey("system", 11, 1204, 1, 1002))
	if !strings.HasPrefix(dir, filepath.Join(outputDir, "runs")+string(os.PathSeparator)) {
		t.Fatalf("run dir %q must live under <output_dir>/runs", dir)
	}
}

// 关闭动态采集的 command 只采集勾选在本机的 PID，不再按命令行重新匹配；没有勾选本机
// 进程的主机按未匹配到进程收敛。
func TestStaticCommandSelectorResolvesOwnHostPIDs(t *testing.T) {
	conf := newTestTaskConfig(t)
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{
		Type:         configs.ActiveProfileSelectorCommand,
		MatchPattern: "a-command-line-that-does-not-exist-on-this-host",
		HostPIDs: []configs.ActiveProfileHostPID{
			{BKHostID: conf.BKHostID, PID: int32(os.Getpid())},
			{BKHostID: conf.BKHostID + 1, PID: 1},
		},
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	processes, err := resolveProcesses(&conf.ProcessSelector)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].PID != int32(os.Getpid()) {
		t.Fatalf("processes = %+v, want exactly the selected test process", processes)
	}

	conf.ProcessSelector.HostPIDs = []configs.ActiveProfileHostPID{{BKHostID: conf.BKHostID + 1, PID: 1}}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProcesses(&conf.ProcessSelector); !errors.Is(err, errNoProcessMatched) {
		t.Fatalf("error = %v, want no process matched", err)
	}
}

// 同一组 PID 文件下发到所有主机，读不到或内容非法的文件跳过，采集其余文件里的进程。
func TestPIDFileSelectorSkipsUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "alive.pid")
	invalid := filepath.Join(dir, "invalid.pid")
	if err := os.WriteFile(alive, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte("not-a-pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.pid")

	selector := configs.ActiveProfileProcessSelector{
		Type:    configs.ActiveProfileSelectorPIDFile,
		PIDPath: strings.Join([]string{missing, invalid, alive}, configs.ActiveProfileSelectorSeparator),
	}
	if err := selector.Clean(); err != nil {
		t.Fatal(err)
	}
	processes, err := resolveProcesses(&selector)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].PID != int32(os.Getpid()) {
		t.Fatalf("processes = %+v, want exactly the test process", processes)
	}

	// 一个 PID 都读不到按未匹配到进程收敛，并说明每个文件的原因
	selector.PIDPath = strings.Join([]string{missing, invalid}, configs.ActiveProfileSelectorSeparator)
	if err := selector.Clean(); err != nil {
		t.Fatal(err)
	}
	_, err = resolveProcesses(&selector)
	if !errors.Is(err, errNoProcessMatched) || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), invalid) {
		t.Fatalf("error = %v, want no process matched with reasons of every pid file", err)
	}
}

// pid_path 由平台用户填写而 Beat 以 root 读取：错误信息不能带出文件内容；超过上限、
// 不是普通文件以及超出 int32 的内容都按非法拒绝，而不是截断或回绕成别的 PID。
func TestReadPIDFileRejectsWithoutEchoingContent(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "shadow")
	if err := os.WriteFile(secret, []byte("root:$6$secret-hash:19000:0:99999:7:::\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(dir, "oversized.pid")
	if err := os.WriteFile(oversized, []byte(strings.Repeat(" ", maxPIDFileBytes)+"1"), 0o600); err != nil {
		t.Fatal(err)
	}
	overflow := filepath.Join(dir, "overflow.pid")
	if err := os.WriteFile(overflow, []byte("4294967297\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"content is not a pid":  secret,
		"larger than the limit": oversized,
		"out of int32 range":    overflow,
		"directory":             dir,
	} {
		t.Run(name, func(t *testing.T) {
			pid, err := readPIDFile(path)
			if err == nil {
				t.Fatalf("readPIDFile() = %d, want an error", pid)
			}
			if strings.Contains(err.Error(), "secret-hash") {
				t.Fatalf("error leaks file content: %v", err)
			}
		})
	}
}
