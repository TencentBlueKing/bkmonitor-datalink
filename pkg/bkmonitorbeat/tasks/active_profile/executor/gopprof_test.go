// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pprofprofile "github.com/google/pprof/profile"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

// newTestGoTaskConfig 构造一个指向给定 endpoint 的 Golang 采集配置。
func newTestGoTaskConfig(t *testing.T, collectionType, host string, port int) *configs.ActiveProfileTaskConfig {
	t.Helper()
	conf := newTestTaskConfig(t, 1)
	conf.CollectionMethod = configs.ActiveProfileMethodGolang
	conf.CollectionType = collectionType
	conf.ProcessSelector = configs.ActiveProfileProcessSelector{}
	conf.CollectorConfig.IPAddress = host
	conf.CollectorConfig.Port = port
	conf.CollectorConfig.DurationSec = 1
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	return conf
}

// newTestGoProfilePayload 生成一份带既有样本标签的 CPU pprof，用于验证注入行为
// 既补齐了执行定位标签，也没有丢弃目标进程自己打的标签。
func newTestGoProfilePayload(t *testing.T, periodNanos int64) []byte {
	t.Helper()
	function := &pprofprofile.Function{ID: 1, Name: "main.work", SystemName: "main.work", Filename: "main.go"}
	location := &pprofprofile.Location{ID: 1, Line: []pprofprofile.Line{{Function: function, Line: 20}}}
	profile := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		PeriodType: &pprofprofile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     periodNanos,
		Function:   []*pprofprofile.Function{function},
		Location:   []*pprofprofile.Location{location},
		Sample: []*pprofprofile.Sample{{
			Location: []*pprofprofile.Location{location},
			Value:    []int64{3, 3 * periodNanos},
			Label:    map[string][]string{"tenant": {"inner"}},
		}},
	}
	var buffer bytes.Buffer
	if err := profile.Write(&buffer); err != nil {
		t.Fatalf("write test profile: %v", err)
	}
	return buffer.Bytes()
}

// newTestGoPprofServer 起一个假 pprof endpoint，并返回其主机与端口。
func newTestGoPprofServer(t *testing.T, handler http.HandlerFunc) (string, int) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	return parsed.Hostname(), port
}

// parseResultProfile 解析采集结果里的产物。Golang 采集不落盘，产物只存在于
// Result.ProfileData 中。
func parseResultProfile(t *testing.T, result *Result) *pprofprofile.Profile {
	t.Helper()
	if len(result.ProfileData) == 0 {
		t.Fatal("result carries no in-memory profile")
	}
	parsed, err := pprofprofile.ParseData(result.ProfileData)
	if err != nil {
		t.Fatalf("parse result profile: %v", err)
	}
	return parsed
}

// Go 产物必须携带与 perf 路径完全一致的执行定位标签，并在反查到唯一监听进程时
// 补上 pid/comm。目标进程自带的标签不能被丢弃。
func TestRunGoPprofLabelsSamples(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	var requestedPath, requestedQuery string
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		requestedQuery = r.URL.RawQuery
		_, _ = w.Write(payload)
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	req := Request{
		ScheduledAt:       time.UnixMilli(1788310800000),
		RunDir:            t.TempDir(),
		IP:                "192.0.2.10",
		GoEndpointProcess: &GoEndpointProcess{PID: 4321, Comm: "workload"},
	}

	result, err := RunGoPprof(context.Background(), conf, req)
	if err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	if requestedPath != goPprofCPUPath {
		t.Errorf("requested path = %q, want %q", requestedPath, goPprofCPUPath)
	}
	if requestedQuery != "seconds=1" {
		t.Errorf("requested query = %q, want seconds=1", requestedQuery)
	}
	if !result.Success || result.SampleCount != 1 || result.ProfileBytes <= 0 {
		t.Fatalf("result = %+v", result)
	}
	// 上报载荷的 type 取产物自报的 PeriodType，不按 collection_type 推断。
	if result.PeriodType != "cpu" {
		t.Errorf("PeriodType = %q, want cpu", result.PeriodType)
	}
	if len(result.CollectedPIDs) != 1 || result.CollectedPIDs[0] != 4321 {
		t.Errorf("CollectedPIDs = %v, want [4321]", result.CollectedPIDs)
	}

	labels := parseResultProfile(t, result).Sample[0].Label
	expected := map[string]string{
		"bk_biz_id":    "11",
		"task_id":      "1001",
		"scheduled_at": "1788310800000",
		"bk_host_id":   "456",
		"ip":           "192.0.2.10",
		"pid":          "4321",
		"comm":         "workload",
		"tenant":       "inner",
	}
	for key, want := range expected {
		got := labels[key]
		if len(got) != 1 || got[0] != want {
			t.Errorf("label %q = %v, want [%s]", key, got, want)
		}
	}
	if len(labels) != len(expected) {
		t.Errorf("labels = %v, want exactly %d keys", labels, len(expected))
	}
	if labels["ip"][0] == conf.CollectorConfig.IPAddress {
		t.Errorf("ip label must be the host IP, not collector_config.ip_address %q", conf.CollectorConfig.IPAddress)
	}
}

// 正常路径不落盘：产物取回时已全部在内存里，写盘不降低内存峰值。keep_artifacts
// 打开时才留一份供事后检查，且内容与内存中的一致。
func TestRunGoPprofKeepsProfileInMemory(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})

	t.Run("default writes nothing to the run dir", func(t *testing.T) {
		conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
		runDir := t.TempDir()
		result, err := RunGoPprof(context.Background(), conf, Request{
			ScheduledAt: time.UnixMilli(1788310800000),
			RunDir:      runDir,
		})
		if err != nil {
			t.Fatalf("RunGoPprof() error = %v", err)
		}
		if result.ProfilePath != "" {
			t.Errorf("ProfilePath = %q, want empty when keep_artifacts is off", result.ProfilePath)
		}
		if int64(len(result.ProfileData)) != result.ProfileBytes || result.ProfileBytes == 0 {
			t.Errorf("ProfileData len = %d, ProfileBytes = %d", len(result.ProfileData), result.ProfileBytes)
		}
		entries, err := os.ReadDir(runDir)
		if err != nil {
			t.Fatalf("read run dir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("run dir must stay empty, got %d entries", len(entries))
		}
	})

	t.Run("keep_artifacts writes the same bytes", func(t *testing.T) {
		conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
		conf.KeepArtifacts = true
		result, err := RunGoPprof(context.Background(), conf, Request{
			ScheduledAt: time.UnixMilli(1788310800000),
			RunDir:      t.TempDir(),
		})
		if err != nil {
			t.Fatalf("RunGoPprof() error = %v", err)
		}
		if result.ProfilePath == "" {
			t.Fatal("ProfilePath must be set when keep_artifacts is on")
		}
		onDisk, err := os.ReadFile(result.ProfilePath)
		if err != nil {
			t.Fatalf("read kept profile: %v", err)
		}
		if !bytes.Equal(onDisk, result.ProfileData) {
			t.Error("kept artifact differs from the in-memory profile")
		}
	})
}

// ProfileReader 要屏蔽两种载体的差异：内存产物直接读，落盘产物从文件读。
func TestResultProfileReader(t *testing.T) {
	t.Run("in-memory profile", func(t *testing.T) {
		result := &Result{ProfileData: []byte("in-memory")}
		reader, err := result.ProfileReader()
		if err != nil {
			t.Fatalf("ProfileReader() error = %v", err)
		}
		defer reader.Close()
		raw, err := io.ReadAll(reader)
		if err != nil || string(raw) != "in-memory" {
			t.Fatalf("read = %q, err = %v", raw, err)
		}
	})

	t.Run("on-disk profile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "profile.pprof")
		if err := os.WriteFile(path, []byte("on-disk"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := &Result{ProfilePath: path}
		reader, err := result.ProfileReader()
		if err != nil {
			t.Fatalf("ProfileReader() error = %v", err)
		}
		defer reader.Close()
		raw, err := io.ReadAll(reader)
		if err != nil || string(raw) != "on-disk" {
			t.Fatalf("read = %q, err = %v", raw, err)
		}
	})

	t.Run("no profile at all", func(t *testing.T) {
		if _, err := (&Result{}).ProfileReader(); err == nil {
			t.Fatal("ProfileReader() error = nil, want a missing-profile error")
		}
	})
}

// 反查不到监听进程时不能写占位 pid/comm，只保留执行定位标签。
func TestRunGoPprofOmitsProcessLabelsWhenEndpointProcessUnknown(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	result, err := RunGoPprof(context.Background(), conf, Request{
		ScheduledAt: time.UnixMilli(1788310800000),
		RunDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	if len(result.CollectedPIDs) != 0 {
		t.Errorf("CollectedPIDs = %v, want empty", result.CollectedPIDs)
	}
	labels := parseResultProfile(t, result).Sample[0].Label
	for _, key := range []string{"pid", "comm"} {
		if _, exists := labels[key]; exists {
			t.Errorf("label %q must be absent when the endpoint process is unknown", key)
		}
	}
}

// 立即任务的 task_id 是 SaaS 建的执行行 ID，与计划任务一样写入标签。
func TestRunGoPprofImmediateCarriesTaskIDLabel(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	conf.TaskID = 30521
	result, err := RunGoPprof(context.Background(), conf, Request{
		ScheduledAt: time.UnixMilli(1788310800000),
		RunDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	labels := parseResultProfile(t, result).Sample[0].Label
	if got := labels["task_id"]; len(got) != 1 || got[0] != "30521" {
		t.Errorf("task_id label = %v, want [30521]", got)
	}
	if _, exists := labels["ip"]; exists {
		t.Error("empty host IP must not invent an ip label")
	}
}

// heap 不带 seconds 参数，采样率也不适用。
func TestRunGoPprofHeapUsesSnapshotEndpoint(t *testing.T) {
	heap := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "inuse_space", Unit: "bytes"}},
		PeriodType: &pprofprofile.ValueType{Type: "space", Unit: "bytes"},
		Period:     512 * 1024,
		Sample:     []*pprofprofile.Sample{{Value: []int64{2048}}},
	}
	var buffer bytes.Buffer
	if err := heap.Write(&buffer); err != nil {
		t.Fatalf("write heap profile: %v", err)
	}

	var requestedPath, requestedQuery string
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		requestedQuery = r.URL.RawQuery
		_, _ = w.Write(buffer.Bytes())
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoHeap, host, port)
	result, err := RunGoPprof(context.Background(), conf, Request{
		ScheduledAt: time.UnixMilli(1788310800000),
		RunDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	if requestedPath != goPprofHeapPath {
		t.Errorf("requested path = %q, want %q", requestedPath, goPprofHeapPath)
	}
	if requestedQuery != "" {
		t.Errorf("heap request must not carry query parameters, got %q", requestedQuery)
	}
	if !result.Success || result.SampleCount != 1 {
		t.Errorf("heap result = %+v", result)
	}
	if result.PeriodType != "space" {
		t.Errorf("PeriodType = %q, want space for heap", result.PeriodType)
	}
}

// endpoint 返回非 200 时，错误信息要带上响应体，否则无法区分"未开 pprof"与"被拒绝"。
func TestRunGoPprofReportsEndpointFailure(t *testing.T) {
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("profile duration exceeds server's WriteTimeout"))
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	_, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()})
	if err == nil {
		t.Fatal("RunGoPprof() error = nil, want an endpoint failure")
	}
	if !strings.Contains(err.Error(), "WriteTimeout") {
		t.Errorf("error = %v, want it to include the endpoint response body", err)
	}
}

// 空产物不能被当成成功上报，否则页面会显示一次没有数据的成功执行。
func TestRunGoPprofRejectsProfileWithoutSamples(t *testing.T) {
	empty := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "samples", Unit: "count"}},
		PeriodType: &pprofprofile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     int64(10 * time.Millisecond),
	}
	var buffer bytes.Buffer
	if err := empty.Write(&buffer); err != nil {
		t.Fatalf("write empty profile: %v", err)
	}
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buffer.Bytes())
	})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	_, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "without samples") {
		t.Fatalf("RunGoPprof() error = %v, want a profile-without-samples failure", err)
	}
}

// withStubbedResourceGuard 用受控探针替换进程级资源保护，测试结束后恢复。
func withStubbedResourceGuard(t *testing.T, probe resourceProbe) {
	t.Helper()
	original := defaultResourceGuard
	stub := newResourceGuard(probe, time.Hour)
	defaultResourceGuard = stub
	t.Cleanup(func() {
		defaultResourceGuard = original
		stub.Close()
	})
}

// 宿主内存低水位对 Golang 采集同样生效：min_mem_level 判的是宿主可用内存，与采集
// 方式无关。低于水位时在准入阶段就拒绝，连 endpoint 都不该去打。
func TestRunGoPprofRejectedByHostMemoryWatermark(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	var requested atomic.Bool
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requested.Store(true)
		_, _ = w.Write(payload)
	})
	// 探针报告宿主只剩 64 MB，低于下面配置的 100 MB 水位。
	withStubbedResourceGuard(t, &fakeResourceProbe{memoryAvailable: 64 << 20})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	conf.ResourceGuardConfig.AutoStopOnResourcePressure = true
	conf.ResourceGuardConfig.MinMemLevel = 100
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}

	_, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()})
	if err == nil {
		t.Fatal("RunGoPprof() error = nil, want a host memory watermark rejection")
	}
	guardErr := resourceGuardError(err)
	if guardErr == nil || guardErr.Resource != resourceMemory {
		t.Fatalf("error = %v, want a memory ResourceGuardError", err)
	}
	if requested.Load() {
		t.Error("admission must reject before the endpoint is requested")
	}
}

// 关闭开关时整套保护关闭，此时不该有任何探测，Golang 采集也走同一条判断。
func TestRunGoPprofSkipsGuardWhenDisabled(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})
	// 探针报告的可用内存远低于水位；开关关闭时不该被读到。
	probe := &fakeResourceProbe{memoryAvailable: 1}
	withStubbedResourceGuard(t, probe)

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoCPU, host, port)
	conf.ResourceGuardConfig = configs.ActiveProfileResourceGuardConfig{
		AutoStopOnResourcePressure: false,
		MinMemLevel:                100,
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}

	if _, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()}); err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	probe.mu.Lock()
	calls := probe.memoryCalls
	probe.mu.Unlock()
	if calls != 0 {
		t.Errorf("memory probe was called %d times with protection disabled", calls)
	}
}

// 开关打开时，解压后放不进剩余内存的 pprof 在解析前以 memory_limit 拒绝；压缩体积很小的
// 响应同样按解压后的体积判断。
func TestRunGoPprofRejectsProfileBeyondMemoryBudget(t *testing.T) {
	function := &pprofprofile.Function{ID: 1, Name: strings.Repeat("x", 2<<20)}
	location := &pprofprofile.Location{ID: 1, Line: []pprofprofile.Line{{Function: function}}}
	large := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "samples", Unit: "count"}},
		Function:   []*pprofprofile.Function{function},
		Location:   []*pprofprofile.Location{location},
		Sample:     []*pprofprofile.Sample{{Location: []*pprofprofile.Location{location}, Value: []int64{1}}},
	}
	var payload bytes.Buffer
	if err := large.Write(&payload); err != nil {
		t.Fatal(err)
	}
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload.Bytes())
	})
	// 低水位取保留余量 64 MB，剩余 36 MB 扣掉固定开销后只容得下约 0.8 MB 的未压缩 pprof。
	withStubbedResourceGuard(t, &fakeResourceProbe{memoryAvailable: 100 * bytesPerMB})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoHeap, host, port)
	conf.ResourceGuardConfig.AutoStopOnResourcePressure = true
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	_, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()})
	if !errors.Is(err, ErrMemoryLimit) || !strings.Contains(err.Error(), "too large to parse") {
		t.Fatalf("RunGoPprof() error = %v, want a memory_limit rejection before parsing", err)
	}
}

func TestRunGoPprofReleasesMemoryReservation(t *testing.T) {
	payload := newTestGoProfilePayload(t, int64(10*time.Millisecond))
	host, port := newTestGoPprofServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})
	withStubbedResourceGuard(t, &fakeResourceProbe{memoryAvailable: 1024 * bytesPerMB})

	conf := newTestGoTaskConfig(t, configs.ActiveProfileTypeGoHeap, host, port)
	conf.ResourceGuardConfig.AutoStopOnResourcePressure = true
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	result, err := RunGoPprof(context.Background(), conf, Request{RunDir: t.TempDir()})
	if err != nil {
		t.Fatalf("RunGoPprof() error = %v", err)
	}
	if parsed := parseResultProfile(t, result); len(parsed.Sample) != 1 {
		t.Fatalf("samples = %d, want 1", len(parsed.Sample))
	}
	if reserved := defaultResourceGuard.reservedMemory; reserved != 0 {
		t.Fatalf("reserved memory = %d after the run", reserved)
	}
}

func TestGoPprofEndpoint(t *testing.T) {
	cases := []struct {
		name           string
		collectionType string
		host           string
		durationSec    int
		wantURL        string
		wantTimeout    time.Duration
	}{
		{
			name:           "cpu carries the sampling window",
			collectionType: configs.ActiveProfileTypeGoCPU,
			host:           "127.0.0.1",
			durationSec:    30,
			wantURL:        "http://127.0.0.1:6060/debug/pprof/profile?seconds=30",
			wantTimeout:    30*time.Second + goPprofFetchSlack,
		},
		{
			name:           "heap is a snapshot with a fixed budget",
			collectionType: configs.ActiveProfileTypeGoHeap,
			host:           "127.0.0.1",
			wantURL:        "http://127.0.0.1:6060/debug/pprof/heap",
			wantTimeout:    goPprofHeapTimeout,
		},
		{
			// IPv6 必须带方括号，否则 host:port 无法解析。
			name:           "ipv6 host is bracketed",
			collectionType: configs.ActiveProfileTypeGoHeap,
			host:           "::1",
			wantURL:        "http://[::1]:6060/debug/pprof/heap",
			wantTimeout:    goPprofHeapTimeout,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			conf := configs.NewActiveProfileTaskConfig()
			conf.CollectionMethod = configs.ActiveProfileMethodGolang
			conf.CollectionType = testCase.collectionType
			conf.CollectorConfig.IPAddress = testCase.host
			conf.CollectorConfig.Port = 6060
			conf.CollectorConfig.DurationSec = testCase.durationSec

			endpoint, timeout, err := goPprofEndpoint(conf)
			if err != nil {
				t.Fatalf("goPprofEndpoint() error = %v", err)
			}
			if endpoint != testCase.wantURL {
				t.Errorf("endpoint = %q, want %q", endpoint, testCase.wantURL)
			}
			if timeout != testCase.wantTimeout {
				t.Errorf("timeout = %s, want %s", timeout, testCase.wantTimeout)
			}
		})
	}
}
