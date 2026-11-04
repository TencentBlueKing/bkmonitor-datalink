// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-ucfg/yaml"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// runbookChildConfig 是 perf + pid 的 once_at 子配置。手工验证时会照抄这份内容，
// 因此它必须能被真实解析和校验。
const runbookChildConfig = `
type: active_profile
name: native-pid-demo
version: "1"

dataid: 0
max_timeout: 15m
min_period: 1m

tasks:
  - task_id: 1201
    bk_tenant_id: system
    bk_biz_id: 11
    bk_host_id: 456
    name: native-pid-demo

    collection_method: perf
    collection_type: native_cpu

    schedule:
      type: once_at
      execute_at: 4070880000000
      timezone: Asia/Shanghai

    process_selector:
      type: pid
      pids: "2341"

    collector_config:
      duration_sec: 15
      frequency_hz: 99
      stack_unwind: fp

    resource_guard_config:
      auto_stop_on_resource_pressure: true
      max_disk_usage: 1024
      min_mem_level: 100

    event_data_id: 527804
    profile_data_id: 524588

    profile_app_name: builtin_profile_app
    profile_service_name: builtin_profile_app

    perf_path: /usr/bin/perf
    keep_artifacts: false
    inline_symbolization: auto
`

func TestRunbookConfigIsValidChildConfig(t *testing.T) {
	task := unpackSingleChildConfig(t, []byte(runbookChildConfig))

	if task.CollectionMethod != configs.ActiveProfileMethodPerf ||
		task.CollectionType != configs.ActiveProfileTypeNativeCPU {
		t.Fatalf("collection = %s/%s", task.CollectionMethod, task.CollectionType)
	}
	if task.BKHostID != 456 || task.ProfileDataID != 524588 {
		t.Fatalf("bk_host_id = %d, profile_data_id = %d", task.BKHostID, task.ProfileDataID)
	}
	if task.Schedule.Type != configs.ActiveProfileScheduleOnceAt || task.Schedule.ExecuteAt != 4070880000000 {
		t.Fatalf("schedule = %+v", task.Schedule)
	}
	if task.ProcessSelector.Type != configs.ActiveProfileSelectorPID ||
		task.ProcessSelector.PIDs != "2341" {
		t.Fatalf("process_selector = %+v", task.ProcessSelector)
	}
	if task.CollectorConfig.StackUnwind != configs.ActiveProfileStackUnwindFP ||
		task.CollectorConfig.DurationSec != 15 {
		t.Fatalf("collector_config = %+v", task.CollectorConfig)
	}
	if !task.ResourceGuardConfig.AutoStopOnResourcePressure ||
		task.ResourceGuardConfig.MaxDiskUsage != 1024 ||
		task.ResourceGuardConfig.MinMemLevel != 100 {
		t.Fatalf("resource_guard_config = %+v", task.ResourceGuardConfig)
	}
	if !task.DiskQuotaEnabled() || !task.MemoryGuardEnabled() {
		t.Fatal("both resource protections should be active in the runbook config")
	}
}

// runbookGoCPUChildConfig 是 golang + go_cpu 的 cron 子配置。
const runbookGoCPUChildConfig = `
type: active_profile
name: go-cpu-demo
version: "1"

dataid: 0
max_timeout: 15m
min_period: 1m

tasks:
  - task_id: 1205
    bk_tenant_id: system
    bk_biz_id: 11
    bk_host_id: 1001
    name: go-cpu-demo

    collection_method: golang
    collection_type: go_cpu

    schedule:
      type: cron
      cron_expression: "*/1 * * * *"
      timezone: Asia/Shanghai

    collector_config:
      ip_address: 127.0.0.1
      port: 6060
      duration_sec: 50

    resource_guard_config:
      auto_stop_on_resource_pressure: true
      max_disk_usage: 1024
      min_mem_level: 100

    event_data_id: 527804
    profile_data_id: 524588
    profile_app_name: builtin_profile_app
    profile_service_name: builtin_profile_app
`

func TestRunbookGoCPUConfigIsValidChildConfig(t *testing.T) {
	task := unpackSingleChildConfig(t, []byte(runbookGoCPUChildConfig))

	if !task.IsGolang() || task.CollectionType != configs.ActiveProfileTypeGoCPU {
		t.Fatalf("collection = %s/%s", task.CollectionMethod, task.CollectionType)
	}
	if task.CollectorConfig.IPAddress != "127.0.0.1" ||
		task.CollectorConfig.Port != 6060 ||
		task.CollectorConfig.DurationSec != 50 {
		t.Errorf("collector_config = %+v", task.CollectorConfig)
	}
	if task.ProcessSelector.Type != "" {
		t.Errorf("golang config must omit process_selector, got %+v", task.ProcessSelector)
	}
	// 资源保护不按采集方式分叉：Go 沿用 perf 的阈值，两项都该生效。
	// max_disk_usage 对 Go 恒不触发，但仍然是"已启用"状态。
	if !task.DiskQuotaEnabled() || !task.MemoryGuardEnabled() {
		t.Errorf("resource_guard_config = %+v, want both protections active", task.ResourceGuardConfig)
	}
	// 这里校验的是整次执行预算：duration_sec + 300 秒后处理余量，不是 pprof HTTP
	// 请求本身的超时。
	if want := 50*time.Second + configs.ActiveProfilePostProcessAllowance; task.ExecutionTimeout() != want {
		t.Errorf("ExecutionTimeout() = %s, want %s", task.ExecutionTimeout(), want)
	}
}

// goHeapChildConfig 覆盖下发协议里 go_heap 的最小形态：没有 process_selector，
// collector_config 只有 endpoint，连 duration_sec 都不带。
const goHeapChildConfig = `
type: active_profile
name: go-heap-demo
version: "1"

dataid: 0
min_period: 1m

tasks:
  - task_id: 1301
    bk_tenant_id: system
    bk_biz_id: 11
    bk_host_id: 456
    name: go-heap-demo

    collection_method: golang
    collection_type: go_heap

    schedule:
      type: once_at
      execute_at: 4070880000000
      timezone: Asia/Shanghai

    collector_config:
      ip_address: 127.0.0.1
      port: 6060

    resource_guard_config:
      auto_stop_on_resource_pressure: false
      max_disk_usage: 0
      min_mem_level: 0

    event_data_id: 527804
    profile_data_id: 524588
    profile_app_name: builtin_profile_app
    profile_service_name: builtin_profile_app
`

func TestGoHeapChildConfigNeedsNoSamplingWindow(t *testing.T) {
	task := unpackSingleChildConfig(t, []byte(goHeapChildConfig))

	if !task.IsGolang() || task.CollectionType != configs.ActiveProfileTypeGoHeap {
		t.Fatalf("collection = %s/%s", task.CollectionMethod, task.CollectionType)
	}
	if task.CollectorConfig.IPAddress != "127.0.0.1" || task.CollectorConfig.Port != 6060 {
		t.Errorf("collector_config = %+v", task.CollectorConfig)
	}
	// 构造函数按 perf 预置的采样参数必须被清掉，heap 也不该被补上默认时长。
	if task.CollectorConfig.DurationSec != 0 ||
		task.CollectorConfig.FrequencyHz != 0 ||
		task.CollectorConfig.StackUnwind != "" {
		t.Errorf("perf-only params survived: %+v", task.CollectorConfig)
	}
	if task.DiskQuotaEnabled() || task.MemoryGuardEnabled() {
		t.Error("resource protection must stay off when the platform sends zero thresholds")
	}
}

func unpackChildConfig(t *testing.T, raw []byte) []*configs.ActiveProfileTaskConfig {
	t.Helper()
	cfg, err := yaml.NewConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	meta := new(configs.ActiveProfileMetaConfig)
	child := configs.ChildTaskMetaConfig{TaskMetaConfig: meta}
	if err := cfg.Unpack(&child); err != nil {
		t.Fatalf("unpack child config: %v", err)
	}
	if child.Type != configs.ConfigTypeActiveProfile {
		t.Fatalf("type = %q, want %q", child.Type, configs.ConfigTypeActiveProfile)
	}
	// 运行目录不是子配置字段，正常由主配置的 path.data 推导；单测没有主配置，
	// 在 Clean 之前预先注入临时目录。
	for _, task := range meta.Tasks {
		task.OutputDir = t.TempDir()
	}
	if err := child.Clean(); err != nil {
		t.Fatalf("clean child config: %v", err)
	}
	unpacked := make([]*configs.ActiveProfileTaskConfig, 0, len(child.GetTaskConfigList()))
	for _, task := range child.GetTaskConfigList() {
		typed, ok := task.(*configs.ActiveProfileTaskConfig)
		if !ok {
			t.Fatalf("task type = %T", task)
		}
		unpacked = append(unpacked, typed)
	}
	return unpacked
}

// unpackSingleChildConfig 用于只声明一个任务的配置片段。
func unpackSingleChildConfig(t *testing.T, raw []byte) *configs.ActiveProfileTaskConfig {
	t.Helper()
	tasks := unpackChildConfig(t, raw)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	return tasks[0]
}

// NodeMan 模板把 host_pids 渲染成 JSON 流式列表，Beat 按 YAML 解析后只取本机勾选的 PID。
func TestStaticCommandChildConfigBindsOwnHostPIDs(t *testing.T) {
	raw := strings.Replace(runbookChildConfig, "      type: pid\n      pids: \"2341\"\n", `      type: "command"
      match_pattern: "/data/order-api"
      enable_dynamic_collection: false
      host_pids: [{"bk_host_id": 456, "pid": 2341}, {"bk_host_id": 457, "pid": 812}]
`, 1)
	if raw == runbookChildConfig {
		t.Fatal("process_selector block was not replaced")
	}
	task := unpackSingleChildConfig(t, []byte(raw))

	if got := task.ProcessSelector.TargetPIDs(); len(got) != 1 || got[0] != 2341 {
		t.Fatalf("target pids = %v, want only the pid selected on host 456", got)
	}
}

// NodeMan 模板把公共上下文里的 target_hosts 渲染成 JSON 流式列表，每台主机拿到同一份完整快照。
func TestChildConfigCarriesTargetHosts(t *testing.T) {
	raw := runbookChildConfig + `    target_hosts: [{"bk_host_id": 456, "ip": "10.0.0.8"}, {"bk_host_id": 457, "ip": ""}]
`
	task := unpackSingleChildConfig(t, []byte(raw))

	want := []configs.ActiveProfileTargetHost{{BKHostID: 456, IP: "10.0.0.8"}, {BKHostID: 457, IP: ""}}
	if len(task.TargetHosts) != len(want) || task.TargetHosts[0] != want[0] || task.TargetHosts[1] != want[1] {
		t.Fatalf("target_hosts = %+v, want %+v", task.TargetHosts, want)
	}
}
