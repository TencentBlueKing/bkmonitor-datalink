// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	pprofprofile "github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile/executor"
)

// allowedEventDimensions 是数据建模允许出现在 Event dimension 中的全部字段。
// 执行参数、错误文本与内部诊断都必须留在 event.content 或本机日志里。
var allowedEventDimensions = map[string]bool{
	"bk_tenant_id":      true,
	"bk_biz_id":         true,
	"task_id":           true,
	"task_name":         true,
	"creator":           true,
	"bk_host_id":        true,
	"schedule_type":     true,
	"scheduled_at":      true,
	"collection_method": true,
	"collection_type":   true,
	"status":            true,
	"process_names":     true,
	"process_pids":      true,
}

type capturedEvent struct {
	DataID    int32
	EventName string `json:"event_name"`
	Target    string `json:"target"`
	Timestamp int64  `json:"timestamp"`
	Event     struct {
		Content string `json:"content"`
	} `json:"event"`
	Dimension map[string]any `json:"dimension"`
}

type capturedProfile struct {
	DataID      int32
	Profile     []byte `json:"data"`
	App         string `json:"app"`
	BizID       int32  `json:"biz_id"`
	PeriodType  string `json:"type"`
	ServiceName string `json:"service_name"`
	keys        []string
}

// capturedReports 是从事件通道里取回的全部上报，按载荷形态分开。终态运营指标与
// Event 共用同一个通道，因此这里必须区分，不能把指标当成 Event。
type capturedReports struct {
	Events   []capturedEvent
	Profiles []capturedProfile
	Metrics  int
}

// newReportChannel 返回常驻 Beat 的事件通道。缓冲留足，避免 finish 在测试里阻塞。
func newReportChannel() chan define.Event {
	return make(chan define.Event, 16)
}

// captureReports 排空事件通道并还原上报载荷。走的是真实路径：Event 与产物都先被
// 编码成 dataid + data 的形态，再由 gse output 发送。
func captureReports(t *testing.T, sink chan define.Event) capturedReports {
	t.Helper()
	var reports capturedReports
	for {
		select {
		case event := <-sink:
			appendReport(t, &reports, event)
		default:
			return reports
		}
	}
}

func appendReport(t *testing.T, reports *capturedReports, event define.Event) {
	t.Helper()
	payload := event.AsMapStr()
	dataID, ok := payload["dataid"].(int32)
	if !ok {
		t.Fatalf("report payload carries no int32 dataid: %T", payload["dataid"])
	}
	raw, err := json.Marshal(payload["data"])
	if err != nil {
		t.Fatalf("marshal report data: %v", err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("report data must be an array: %v", err)
	}
	for _, item := range items {
		switch {
		case item["event_name"] != nil:
			var captured capturedEvent
			decodeReportItem(t, item, &captured)
			captured.DataID = dataID
			reports.Events = append(reports.Events, captured)
		case item["metrics"] != nil:
			reports.Metrics++
		default:
			var captured capturedProfile
			decodeReportItem(t, item, &captured)
			captured.DataID = dataID
			for key := range item {
				captured.keys = append(captured.keys, key)
			}
			reports.Profiles = append(reports.Profiles, captured)
		}
	}
}

func decodeReportItem(t *testing.T, item map[string]json.RawMessage, target any) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal report item: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode report item: %v", err)
	}
}

// assertDimensionKeys 同时校验字段白名单和值类型。自定义事件 schema 只接受标量
// 字符串维度，上报端发数组会被接收端以 400 直接拒绝。
func assertDimensionKeys(t *testing.T, event capturedEvent) {
	t.Helper()
	for key, value := range event.Dimension {
		if !allowedEventDimensions[key] {
			t.Errorf("%s dimension contains disallowed field %q", event.EventName, key)
		}
		if _, ok := value.(string); !ok {
			t.Errorf("%s dimension %q must be a string, got %T", event.EventName, key, value)
		}
	}
}

// 没有上报链路时必须报错而不是静默丢弃：常驻 Beat 的通道为 nil 说明这条入口没有
// 接通 Beat 的事件链路，终态会因此丢失。
func TestReportWithoutLinkFails(t *testing.T) {
	conf := newTestTaskConfig(t)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt,
		ExecutionOutcome{Status: StatusSucceed})

	if err := reportFinishedEvent(context.Background(), nil, event); err == nil {
		t.Fatal("reportFinishedEvent() error = nil, want a missing-link error")
	}
	err := reportFinishedEvent(context.Background(), beatEventSink{}, event)
	if err == nil || !strings.Contains(err.Error(), "beat event link") {
		t.Fatalf("reportFinishedEvent() error = %v, want a beat event link error", err)
	}
}

func TestFinishedEventUsesNarrowDimensionsAndStructuredContent(t *testing.T) {
	sink := newReportChannel()

	conf := newTestTaskConfig(t)
	startedAt := time.UnixMilli(1786521405518)
	event := newFinishedEvent(conf, "10.0.0.8", time.UnixMilli(conf.Schedule.ExecuteAt), startedAt,
		startedAt.Add(10*time.Second), ExecutionOutcome{
			Status: StatusSucceed,
			Processes: []ProcessResult{
				{PID: 2341, ProcessName: "order-api"},
				{PID: 2342, ProcessName: "order-api"},
			},
		})
	if err := reportFinishedEvent(context.Background(), beatEventSink{events: sink}, event); err != nil {
		t.Fatalf("reportFinishedEvent() error = %v", err)
	}
	reports := captureReports(t, sink)
	if len(reports.Events) != 1 {
		t.Fatalf("event count = %d", len(reports.Events))
	}
	captured := reports.Events[0]
	// 路由只靠平台注入的 event_data_id。
	if captured.DataID != conf.EventDataID {
		t.Errorf("event dataid = %d, want %d", captured.DataID, conf.EventDataID)
	}
	if captured.EventName != finishedEventName {
		t.Fatalf("event_name = %q", captured.EventName)
	}
	assertDimensionKeys(t, captured)
	if captured.Target != "10.0.0.8" {
		t.Errorf("target = %q, want the execution-time IP", captured.Target)
	}
	if captured.Timestamp != startedAt.Add(10*time.Second).UnixMilli() {
		t.Errorf("timestamp = %d, want the actual finish time", captured.Timestamp)
	}
	for key, want := range map[string]string{
		"bk_tenant_id":      "system",
		"bk_biz_id":         "11",
		"task_id":           "1001",
		"task_name":         "native-cpu-demo",
		"creator":           "admin",
		"bk_host_id":        "456",
		"schedule_type":     "cron",
		"collection_method": "perf",
		"collection_type":   "native_cpu",
		"status":            StatusSucceed,
	} {
		if got, _ := captured.Dimension[key].(string); got != want {
			t.Errorf("dimension %s = %q, want %q", key, got, want)
		}
	}

	// dimension 只能是标量字符串，多个进程以分隔符拼接，进程名与 PID 按位置对应。
	for key, want := range map[string]string{
		"process_names": "order-api,order-api",
		"process_pids":  "2341,2342",
	} {
		value, ok := captured.Dimension[key].(string)
		if !ok {
			t.Fatalf("%s must be reported as a string, got %T", key, captured.Dimension[key])
		}
		if value != want {
			t.Errorf("%s = %q, want %q", key, value, want)
		}
	}

	var content finishedContent
	if err := json.Unmarshal([]byte(captured.Event.Content), &content); err != nil {
		t.Fatalf("event.content is not valid JSON: %v", err)
	}
	if content.SchemaVersion != eventContentSchemaVersion {
		t.Errorf("schema_version = %d", content.SchemaVersion)
	}
	if content.StartedAt != startedAt.UnixMilli() || content.FinishedAt != startedAt.Add(10*time.Second).UnixMilli() {
		t.Errorf("content times started=%d finished=%d", content.StartedAt, content.FinishedAt)
	}
	if content.ExecutionDurationMS != 10000 {
		t.Errorf("execution_duration_ms = %d", content.ExecutionDurationMS)
	}
	if len(content.Processes) != 2 || content.Processes[0].PID != 2341 {
		t.Errorf("processes = %+v", content.Processes)
	}
	if content.ErrorMessage != "" {
		t.Errorf("successful error_message = %q", content.ErrorMessage)
	}
	if content.Config.Schedule.Type == "" {
		t.Fatal("finished content must carry the execution config snapshot")
	}
	config := content.Config
	if config.Schedule.Type != configs.ActiveProfileScheduleCron ||
		config.Schedule.CronExpression != "0 */2 * * *" {
		t.Errorf("config.schedule = %+v", config.Schedule)
	}
	if config.Schedule.ExecuteAt != 0 {
		t.Error("cron config must not carry execute_at")
	}
	if len(config.TargetConfig.Nodes) != 1 || config.TargetConfig.Nodes[0].BKHostID != 456 {
		t.Errorf("config.target_config = %+v", config.TargetConfig)
	}
	if config.CollectorConfig.StackUnwind != configs.ActiveProfileStackUnwindFP {
		t.Errorf("config.collector_config = %+v", config.CollectorConfig)
	}
	if config.ResourceGuardConfig.MaxDiskUsage != 1024 || config.ResourceGuardConfig.MinMemLevel != 100 {
		t.Errorf("config.resource_guard_config = %+v", config.ResourceGuardConfig)
	}
	// 每台主机写入同一份完整选择器，pid 模式原样保留分号分隔的 pids。
	if config.ProcessSelector == nil || config.ProcessSelector.Config.PIDs != "2341;2342" {
		t.Fatalf("config.process_selector = %+v", config.ProcessSelector)
	}
}

// 选择器快照与页面提交的 {type, config} 结构一致：command 固定带排除规则与动态采集开关，
// 关闭动态采集时保留全部主机勾选的 host_pids；pid_file 与 pid 原样保留分号分隔的字符串。
func TestSelectorSnapshotMatchesPageStructure(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector configs.ActiveProfileProcessSelector
		want     string
	}{
		{
			name: "dynamic command",
			selector: configs.ActiveProfileProcessSelector{
				Type: configs.ActiveProfileSelectorCommand, MatchPattern: "/data/order-api", EnableDynamicCollection: true,
			},
			want: `{"type":"command","config":{"match_pattern":"/data/order-api","exclude_pattern":"","enable_dynamic_collection":true}}`,
		},
		{
			name: "static command",
			selector: configs.ActiveProfileProcessSelector{
				Type: configs.ActiveProfileSelectorCommand, MatchPattern: "/data/order-api",
				HostPIDs: []configs.ActiveProfileHostPID{{BKHostID: 456, PID: 2341}, {BKHostID: 457, PID: 812}},
			},
			want: `{"type":"command","config":{"match_pattern":"/data/order-api","exclude_pattern":"","enable_dynamic_collection":false,` +
				`"host_pids":[{"bk_host_id":456,"pid":2341},{"bk_host_id":457,"pid":812}]}}`,
		},
		{
			name: "pid file",
			selector: configs.ActiveProfileProcessSelector{
				Type: configs.ActiveProfileSelectorPIDFile, PIDPath: "/var/run/a.pid;/var/run/b.pid",
			},
			want: `{"type":"pid_file","config":{"pid_path":"/var/run/a.pid;/var/run/b.pid"}}`,
		},
		{
			name:     "pid",
			selector: configs.ActiveProfileProcessSelector{Type: configs.ActiveProfileSelectorPID, PIDs: "2341;2342"},
			want:     `{"type":"pid","config":{"pids":"2341;2342"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestTaskConfig(t)
			conf.ProcessSelector = test.selector
			raw, err := json.Marshal(newSelectorContent(conf))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != test.want {
				t.Fatalf("selector snapshot = %s, want %s", raw, test.want)
			}
		})
	}
}

// Event 的 config 按 collection_method + collection_type 写入完整 Schema，不使用
// 字段并集：Golang 只带 endpoint 参数，perf 专有参数和 process_selector 都不出现，
// go_heap 连 duration_sec 也没有。
func TestFinishedEventConfigMatchesCollectionMethodSchema(t *testing.T) {
	for _, test := range []struct {
		collectionType string
		wantKeys       []string
		absentKeys     []string
	}{
		{
			collectionType: configs.ActiveProfileTypeGoCPU,
			wantKeys:       []string{"ip_address", "port", "duration_sec"},
			absentKeys:     []string{"frequency_hz", "stack_unwind"},
		},
		{
			collectionType: configs.ActiveProfileTypeGoHeap,
			wantKeys:       []string{"ip_address", "port"},
			absentKeys:     []string{"duration_sec", "frequency_hz", "stack_unwind"},
		},
	} {
		t.Run(test.collectionType, func(t *testing.T) {
			conf := newTestGoTaskConfig(t, test.collectionType)
			startedAt := time.Unix(1786521405, 0)
			event := newFinishedEvent(conf, "127.0.0.1", startedAt, startedAt, startedAt.Add(time.Second),
				ExecutionOutcome{Status: StatusSucceed})

			var content struct {
				Config struct {
					CollectorConfig map[string]any `json:"collector_config"`
					ProcessSelector any            `json:"process_selector"`
				} `json:"config"`
			}
			if err := json.Unmarshal([]byte(eventContentOf(t, event)), &content); err != nil {
				t.Fatalf("event.content is not valid JSON: %v", err)
			}

			collector := content.Config.CollectorConfig
			for _, key := range test.wantKeys {
				if _, exists := collector[key]; !exists {
					t.Errorf("collector_config is missing %q: %+v", key, collector)
				}
			}
			for _, key := range test.absentKeys {
				if _, exists := collector[key]; exists {
					t.Errorf("collector_config must not carry %q: %+v", key, collector)
				}
			}
			if content.Config.ProcessSelector != nil {
				t.Errorf("golang config must omit process_selector, got %v", content.Config.ProcessSelector)
			}
		})
	}
}

// TestProcessDimensionsAlignNamesWithPIDs 覆盖进程维度的两个约束：进程名与 PID 按位置
// 一一对应；进程名里的分隔符必须归一化，否则 split 后两边的位置会错开。
func TestProcessDimensionsAlignNamesWithPIDs(t *testing.T) {
	t.Run("names follow pids in ascending pid order", func(t *testing.T) {
		names, pids := processDimensions([]ProcessResult{
			{PID: 2677756, ProcessName: "workload_g"},
			{PID: 326635, ProcessName: "workload_a"},
			{PID: 326636, ProcessName: "workload_a"},
			{PID: 326635, ProcessName: "workload_a"},
		})
		if names != "workload_a,workload_a,workload_g" || pids != "326635,326636,2677756" {
			t.Fatalf("process dimensions = %q / %q", names, pids)
		}
	})

	t.Run("separator inside a process name is normalized", func(t *testing.T) {
		names, pids := processDimensions([]ProcessResult{
			{PID: 7, ProcessName: "odd,name"},
			{PID: 8, ProcessName: "java"},
		})
		if names != "odd_name,java" || pids != "7,8" {
			t.Fatalf("process dimensions = %q / %q", names, pids)
		}
		if len(strings.Split(names, processKeySeparator)) != len(strings.Split(pids, processKeySeparator)) {
			t.Fatal("names and pids must split into the same number of items")
		}
	})

	t.Run("no processes yields empty dimensions", func(t *testing.T) {
		if names, pids := processDimensions(nil); names != "" || pids != "" {
			t.Fatalf("process dimensions = %q / %q, want empty", names, pids)
		}
	})
}

// event.content.processes 必须保留真实进程名，归一化只作用于筛选键。
func TestFinishedContentKeepsTrueProcessName(t *testing.T) {
	conf := newTestTaskConfig(t)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusSucceed, Processes: []ProcessResult{{PID: 7, ProcessName: "odd,name"}}})

	var content finishedContent
	if err := json.Unmarshal([]byte(eventContentOf(t, event)), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Processes) != 1 || content.Processes[0].ProcessName != "odd,name" {
		t.Fatalf("content.processes must keep the real name: %+v", content.Processes)
	}
}

func TestStartedEventReportsRunningWithoutTerminalFields(t *testing.T) {
	sink := newReportChannel()

	conf := newTestTaskConfig(t)
	startedAt := time.UnixMilli(1786521405518)
	event := newStartedEvent(conf, "10.0.0.8", time.UnixMilli(conf.Schedule.ExecuteAt), startedAt)
	if err := reportStartedEvent(context.Background(), beatEventSink{events: sink}, event); err != nil {
		t.Fatalf("reportStartedEvent() error = %v", err)
	}
	reports := captureReports(t, sink)
	if len(reports.Events) != 1 {
		t.Fatalf("event count = %d", len(reports.Events))
	}
	captured := reports.Events[0]
	if captured.EventName != startedEventName {
		t.Fatalf("event_name = %q", captured.EventName)
	}
	assertDimensionKeys(t, captured)
	if got, _ := captured.Dimension["status"].(string); got != StatusRunning {
		t.Errorf("started status = %q", got)
	}
	for _, key := range []string{"process_names", "process_pids"} {
		if _, exists := captured.Dimension[key]; exists {
			t.Errorf("started Event must not carry %s", key)
		}
	}
	if captured.Timestamp != startedAt.UnixMilli() {
		t.Errorf("timestamp = %d", captured.Timestamp)
	}
	var content startedContent
	if err := json.Unmarshal([]byte(captured.Event.Content), &content); err != nil {
		t.Fatalf("event.content is not valid JSON: %v", err)
	}
	if content.SchemaVersion != eventContentSchemaVersion || content.StartedAt != startedAt.UnixMilli() {
		t.Errorf("started content = %+v", content)
	}
	if content.Config.CollectorConfig.DurationSec != 30 {
		t.Errorf("started content must carry the execution config: %+v", content.Config)
	}
}

// 立即执行的 task_id 是 SaaS 建的执行行 ID，预计算按它定位执行行
func TestImmediateEventsCarryTaskID(t *testing.T) {
	conf := newTestTaskConfig(t)
	conf.TaskID = 30521
	conf.Schedule = configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleImmediate,
		ExecuteAt: time.Now().UnixMilli(),
		Timezone:  "Asia/Shanghai",
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	finished := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt,
		ExecutionOutcome{Status: StatusSucceed})
	dimension := finished.data()["dimension"].(common.MapStr)
	if got, _ := dimension["task_id"].(string); got != "30521" {
		t.Fatalf("immediate Event task_id = %q, want 30521", got)
	}
	var content finishedContent
	raw := eventContentOf(t, finished)
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	if content.Config.Schedule.Type != configs.ActiveProfileScheduleImmediate ||
		content.Config.Schedule.ExecuteAt != conf.Schedule.ExecuteAt {
		t.Fatalf("immediate schedule snapshot = %+v", content.Config.Schedule)
	}
}

func TestFinishedEventContentStaysParsableWhenOversized(t *testing.T) {
	conf := newTestTaskConfig(t)
	processes := make([]ProcessResult, 0, 4096)
	for i := 0; i < 4096; i++ {
		processes = append(processes, ProcessResult{
			PID:         int32(i + 1),
			ProcessName: strings.Repeat("p", 64),
		})
	}
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusPartialSucceed, Processes: processes, ErrorMessage: "one process failed"})
	raw := eventContentOf(t, event)
	if len(raw) > maxEventContentBytes {
		t.Fatalf("event.content = %d bytes, want <= %d", len(raw), maxEventContentBytes)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatalf("degraded event.content must still be valid JSON: %v", err)
	}
	if content.SchemaVersion != eventContentSchemaVersion || content.FinishedAt == 0 {
		t.Fatalf("degraded content lost required fields: %+v", content)
	}
	if !strings.Contains(content.ErrorMessage, "one process failed") {
		t.Errorf("degraded content must keep the original error: %q", content.ErrorMessage)
	}
	if !strings.Contains(content.ErrorMessage, "size limit") {
		t.Errorf("degraded content must explain the degradation: %q", content.ErrorMessage)
	}
	if content.Config.Schedule.Type == "" {
		t.Error("degraded content must keep the config snapshot")
	}
	if !strings.Contains(raw, `"processes":[]`) {
		t.Errorf("degraded content must keep processes as an empty array: %s", raw)
	}
}

func testTargetHosts(count int) []configs.ActiveProfileTargetHost {
	hosts := make([]configs.ActiveProfileTargetHost, 0, count)
	for i := 0; i < count; i++ {
		hosts = append(hosts, configs.ActiveProfileTargetHost{BKHostID: int64(1000000 + i), IP: "10.100.200.123"})
	}
	return hosts
}

func TestEventsCarryTargetHosts(t *testing.T) {
	conf := newTestTaskConfig(t)
	conf.TargetHosts = testTargetHosts(2)
	startedAt := time.Now()

	var started startedContent
	startedRaw := newStartedEvent(conf, "10.0.0.8", startedAt, startedAt).data()["event"].(common.MapStr)["content"]
	if err := json.Unmarshal([]byte(startedRaw.(string)), &started); err != nil {
		t.Fatal(err)
	}
	var finished finishedContent
	finishedEvent := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusSucceed})
	if err := json.Unmarshal([]byte(eventContentOf(t, finishedEvent)), &finished); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]configs.ActiveProfileTargetHost{
		"started": started.TargetHosts, "finished": finished.TargetHosts,
	} {
		if len(got) != 2 || got[0] != conf.TargetHosts[0] || got[1] != conf.TargetHosts[1] {
			t.Errorf("%s target_hosts = %+v, want %+v", name, got, conf.TargetHosts)
		}
	}
}

// 立即执行的 CLI 不带快照，content 里不出现 target_hosts 键。
func TestEventsOmitTargetHostsWhenNotConfigured(t *testing.T) {
	conf := newTestTaskConfig(t)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt, ExecutionOutcome{Status: StatusSucceed})
	if raw := eventContentOf(t, event); strings.Contains(raw, "target_hosts") {
		t.Errorf("content without target hosts = %s, want no target_hosts key", raw)
	}
}

// target_hosts 随目标主机数增长，不计入 content 上限：主机很多时进程列表不因此被丢弃。
func TestFinishedEventTargetHostsDoNotCountTowardSizeLimit(t *testing.T) {
	conf := newTestTaskConfig(t)
	conf.TargetHosts = testTargetHosts(2000)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusSucceed, Processes: []ProcessResult{{PID: 2341, ProcessName: "order-api"}}})

	raw := eventContentOf(t, event)
	if len(raw) <= maxEventContentBytes {
		t.Fatalf("event.content = %d bytes, want the full snapshot beyond %d", len(raw), maxEventContentBytes)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.TargetHosts) != 2000 {
		t.Errorf("target_hosts = %d, want all 2000 hosts", len(content.TargetHosts))
	}
	if len(content.Processes) != 1 || strings.Contains(content.ErrorMessage, "size limit") {
		t.Errorf("processes must not be degraded by target_hosts: processes=%+v message=%q",
			content.Processes, content.ErrorMessage)
	}
}

// 进程列表自身超限时照常降级，target_hosts 仍原样保留。
func TestFinishedEventKeepsTargetHostsWhenProcessesDegrade(t *testing.T) {
	conf := newTestTaskConfig(t)
	conf.TargetHosts = testTargetHosts(3)
	processes := make([]ProcessResult, 0, 4096)
	for i := 0; i < 4096; i++ {
		processes = append(processes, ProcessResult{PID: int32(i + 1), ProcessName: strings.Repeat("p", 64)})
	}
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusPartialSucceed, Processes: processes})

	var content finishedContent
	if err := json.Unmarshal([]byte(eventContentOf(t, event)), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Processes) != 0 || !strings.Contains(content.ErrorMessage, "size limit") {
		t.Errorf("oversized processes must still degrade: processes=%d message=%q",
			len(content.Processes), content.ErrorMessage)
	}
	if len(content.TargetHosts) != 3 {
		t.Errorf("target_hosts = %+v, want the full snapshot kept", content.TargetHosts)
	}
}

func TestFinishedEventContentAlwaysCarriesProcesses(t *testing.T) {
	conf := newTestTaskConfig(t)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusFailed, ErrorMessage: "connection refused"})
	if raw := eventContentOf(t, event); !strings.Contains(raw, `"processes":[]`) {
		t.Errorf("finished content without processes = %s, want an empty processes array", raw)
	}
}

func TestExpiredScheduleReportsFailedFinishedWithoutStarted(t *testing.T) {
	sink := newReportChannel()

	conf := newTestTaskConfig(t)
	scheduledAt := time.Now().Add(-time.Minute)
	conf.Schedule = configs.ActiveProfileScheduleConfig{
		Type:      configs.ActiveProfileScheduleOnceAt,
		ExecuteAt: scheduledAt.UnixMilli(),
		Timezone:  "UTC",
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	gather := newTestGather(conf)
	ctx := define.WithScheduleExpired(define.WithScheduledAt(context.Background(), scheduledAt))
	gather.Run(ctx, sink)

	reports := captureReports(t, sink)
	if len(reports.Events) != 1 {
		t.Fatalf("expired schedule sent %d events, want only the terminal finished", len(reports.Events))
	}
	if len(reports.Profiles) != 0 {
		t.Fatalf("expired schedule must not report a profile: %+v", reports.Profiles)
	}
	captured := reports.Events[0]
	if captured.EventName != finishedEventName {
		t.Fatalf("expired schedule must not report started: %q", captured.EventName)
	}
	assertDimensionKeys(t, captured)
	if got, _ := captured.Dimension["status"].(string); got != StatusFailed {
		t.Errorf("expired status = %q", got)
	}
	var content finishedContent
	if err := json.Unmarshal([]byte(captured.Event.Content), &content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.ErrorMessage, "already passed") {
		t.Errorf("expired error_message = %q", content.ErrorMessage)
	}
	if len(content.Processes) != 0 {
		t.Errorf("expired occurrence must not list processes: %+v", content.Processes)
	}
}

// pprof 载荷的五个字段与 Profiling 结果表的清洗规则一一对应：产物本身、产物归属
// （app/biz_id/service_name）和 type。少一个键，整条记录都清洗不出来。
func TestProfileReportCarriesProfileAndTarget(t *testing.T) {
	sink := newReportChannel()
	conf := newTestTaskConfig(t)
	// perf 的产物在运行目录里，上报时从文件读出完整字节。
	result := &executor.Result{ProfilePath: writeTestProfile(t), PeriodType: "cpu"}

	if err := reportProfile(context.Background(), beatEventSink{events: sink}, conf, result); err != nil {
		t.Fatalf("reportProfile() error = %v", err)
	}
	reports := captureReports(t, sink)
	if len(reports.Profiles) != 1 {
		t.Fatalf("profile count = %d", len(reports.Profiles))
	}
	captured := reports.Profiles[0]
	if captured.DataID != conf.ProfileDataID {
		t.Errorf("profile dataid = %d, want %d", captured.DataID, conf.ProfileDataID)
	}
	if captured.App != conf.ProfileAppName || captured.ServiceName != conf.ProfileServiceName {
		t.Errorf("profile target app=%q service_name=%q", captured.App, captured.ServiceName)
	}
	if captured.BizID != conf.GetBizID() {
		t.Errorf("profile biz_id = %d, want %d", captured.BizID, conf.GetBizID())
	}
	if captured.PeriodType != "cpu" {
		t.Errorf("profile type = %q, want the pprof period type", captured.PeriodType)
	}
	if want := []string{"app", "biz_id", "data", "type", "service_name"}; !sameKeys(captured.keys, want) {
		t.Errorf("profile payload keys = %v, want exactly %v", captured.keys, want)
	}
	// 载荷里的产物必须是可解析的未压缩 pprof，样本标签不受传输层影响。
	parsed, err := pprofprofile.ParseData(captured.Profile)
	if err != nil {
		t.Fatalf("profile payload is not a parsable pprof: %v", err)
	}
	if got := parsed.Sample[0].Label["pid"]; len(got) != 1 || got[0] != "2341" {
		t.Errorf("sample pid label = %v", got)
	}
	if _, exists := parsed.Sample[0].Label["profile_id"]; exists {
		t.Error("pprof samples must not carry profile_id")
	}
}

// Golang 采集的产物不落盘，上报要能直接用内存里的那份。
func TestProfileReportReadsInMemoryProfile(t *testing.T) {
	profileData, err := os.ReadFile(writeTestProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	sink := newReportChannel()
	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoHeap)
	result := &executor.Result{ProfileData: profileData, PeriodType: "space"}

	if err := reportProfile(context.Background(), beatEventSink{events: sink}, conf, result); err != nil {
		t.Fatalf("reportProfile() error = %v", err)
	}
	reports := captureReports(t, sink)
	if len(reports.Profiles) != 1 {
		t.Fatalf("profile count = %d", len(reports.Profiles))
	}
	if got := reports.Profiles[0].Profile; len(got) != len(profileData) {
		t.Errorf("profile payload = %d bytes, want %d", len(got), len(profileData))
	}
	// heap 的 type 不是 cpu：清洗规则原样入库，SaaS 靠它区分产物种类。
	if got := reports.Profiles[0].PeriodType; got != "space" {
		t.Errorf("heap profile type = %q, want space", got)
	}
}

// 缺 type 的产物不能发出去：清洗规则读不到该键，数据会停在 Kafka 里不落库。
func TestProfileReportFailsWithoutPeriodType(t *testing.T) {
	sink := newReportChannel()
	conf := newTestTaskConfig(t)

	err := reportProfile(context.Background(), beatEventSink{events: sink}, conf,
		&executor.Result{ProfilePath: writeTestProfile(t)})
	if err == nil || !strings.Contains(err.Error(), "period type") {
		t.Fatalf("reportProfile() error = %v, want a missing-period-type error", err)
	}
	if reports := captureReports(t, sink); len(reports.Profiles) != 0 {
		t.Fatalf("a profile without type must not be sent: %+v", reports.Profiles)
	}
}

// 没有产物时必须报错：静默发一条空载荷会让 SaaS 以为结果已落库。
func TestProfileReportFailsWithoutProfile(t *testing.T) {
	sink := newReportChannel()
	conf := newTestTaskConfig(t)

	err := reportProfile(context.Background(), beatEventSink{events: sink}, conf, &executor.Result{})
	if err == nil {
		t.Fatal("reportProfile() error = nil, want a missing-profile error")
	}
	if reports := captureReports(t, sink); len(reports.Profiles) != 0 {
		t.Fatalf("failed profile report must send nothing: %+v", reports.Profiles)
	}
}

func sameKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, key := range got {
		seen[key] = true
	}
	for _, key := range want {
		if !seen[key] {
			return false
		}
	}
	return true
}

// eventContentOf 取出 finished Event 中的 event.content JSON 文本。
func eventContentOf(t *testing.T, event *FinishedEvent) string {
	t.Helper()
	content, ok := event.data()["event"].(common.MapStr)["content"].(string)
	if !ok {
		t.Fatal("event.content must be a JSON string")
	}
	return content
}

func writeTestProfile(t *testing.T) string {
	t.Helper()
	profilePath := filepath.Join(t.TempDir(), "profile.pprof")
	file, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	profile := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		Sample: []*pprofprofile.Sample{{
			Value: []int64{1, int64(time.Second)},
			Label: map[string][]string{"pid": {"2341"}, "comm": {"order-api"}},
		}},
		PeriodType:    &pprofprofile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:        int64(time.Second / 99),
		TimeNanos:     time.Now().UnixNano(),
		DurationNanos: int64(10 * time.Second),
	}
	if err := profile.Write(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return profilePath
}

// 成功代表 Profiling 结果可用：没有匹配到进程与采集结果为空都查不到结果，按失败收敛，原因留在 message。
func TestStatusForTreatsNoMatchAndEmptyProfileAsFailed(t *testing.T) {
	for name, err := range map[string]error{
		"no process matched":   errNoProcessMatched,
		"pid files unreadable": fmt.Errorf("%w: %w", errNoProcessMatched, errors.New("read pid file /a.pid")),
		"empty profile":        fmt.Errorf("perf script contains no samples: %w", executor.ErrEmptyProfile),
		"other errors":         errors.New("perf unsuccessful"),
	} {
		if got := statusFor(context.Background(), err); got != StatusFailed {
			t.Errorf("%s: status = %s, want %s", name, got, StatusFailed)
		}
	}
}

// error_type 按错误链上的类别标记归类，标记不改变 message；超时与取消不归类。
func TestFailedOutcomeClassifiesErrorType(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"no process matched": {errNoProcessMatched, ErrorTypeProcessNotFound},
		"targets exited": {
			executor.MarkError(executor.ErrNoTargetProcess, errors.New("no target process is available")),
			ErrorTypeProcessNotFound,
		},
		"empty profile": {
			fmt.Errorf("perf script contains no samples: %w", executor.ErrEmptyProfile),
			ErrorTypeEmptyProfile,
		},
		"empty go endpoint": {
			executor.MarkError(executor.ErrGoEndpoint, fmt.Errorf("returned no data: %w", executor.ErrEmptyProfile)),
			ErrorTypeEmptyProfile,
		},
		"perf unavailable": {
			executor.MarkError(executor.ErrPerfUnavailable, errors.New("locate perf")),
			ErrorTypePerfUnavailable,
		},
		"go endpoint": {
			executor.MarkError(executor.ErrGoEndpoint, errors.New("connection refused")),
			ErrorTypeGoEndpointFailed,
		},
		"memory limit": {executor.MarkError(executor.ErrMemoryLimit, errors.New("memory")), ErrorTypeMemoryLimit},
		"disk limit":   {executor.MarkError(executor.ErrDiskLimit, errors.New("disk")), ErrorTypeDiskLimit},
		"report":       {sendReport(context.Background(), nil, nil, "profile"), ErrorTypeReportFailed},
		"invalid config": {
			executor.MarkError(executor.ErrInvalidConfig, errors.New("unsupported")),
			ErrorTypeConfigInvalid,
		},
		"other": {errors.New("perf unsuccessful"), ErrorTypeCollectFailed},
	} {
		outcome := failedOutcome(context.Background(), tc.err, nil)
		if outcome.Status != StatusFailed || outcome.ErrorType != tc.want || outcome.ErrorMessage != tc.err.Error() {
			t.Errorf("%s: outcome = %+v, want error_type %s", name, outcome, tc.want)
		}
	}

	if got := executor.MarkError(executor.ErrPerfUnavailable, errors.New("locate perf")).Error(); got != "locate perf" {
		t.Errorf("marked error text = %q, want the original text", got)
	}
	timeout := executor.MarkError(executor.ErrGoEndpoint, fmt.Errorf("fetch: %w", context.DeadlineExceeded))
	if outcome := failedOutcome(context.Background(), timeout, nil); outcome.Status != StatusTimeout || outcome.ErrorType != "" {
		t.Errorf("timeout outcome = %+v, want no error_type", outcome)
	}
}

func TestFinishedEventContentCarriesErrorType(t *testing.T) {
	conf := newTestTaskConfig(t)
	startedAt := time.Now()
	event := newFinishedEvent(conf, "10.0.0.8", startedAt, startedAt, startedAt.Add(time.Second),
		ExecutionOutcome{Status: StatusFailed, ErrorType: ErrorTypeProcessNotFound, ErrorMessage: "no process"})
	var content finishedContent
	if err := json.Unmarshal([]byte(eventContentOf(t, event)), &content); err != nil {
		t.Fatal(err)
	}
	if content.ErrorType != ErrorTypeProcessNotFound || content.ErrorMessage != "no process" {
		t.Fatalf("content error = (%q, %q)", content.ErrorType, content.ErrorMessage)
	}
}
