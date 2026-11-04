// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package configs

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestActiveProfileConfig(t *testing.T) *ActiveProfileTaskConfig {
	t.Helper()
	conf := NewActiveProfileTaskConfig()
	// 运行目录正常由主配置的 path.data 推导；单测没有主配置，预先注入临时目录。
	conf.OutputDir = t.TempDir()
	conf.BKTenantID = "system"
	conf.BizID = 11
	conf.TaskID = 1001
	conf.BKHostID = 456
	conf.Name = "native-cpu-demo"
	conf.Creator = "admin"
	conf.Schedule = ActiveProfileScheduleConfig{
		Type:      ActiveProfileScheduleOnceAt,
		ExecuteAt: time.Now().Add(time.Hour).UnixMilli(),
		Timezone:  "Asia/Shanghai",
	}
	conf.ProcessSelector = ActiveProfileProcessSelector{
		Type: ActiveProfileSelectorPID,
		PIDs: "2341;2342",
	}
	conf.ResourceGuardConfig = ActiveProfileResourceGuardConfig{
		AutoStopOnResourcePressure: true,
		MaxDiskUsage:               1024,
		MinMemLevel:                100,
	}
	conf.EventDataID = 110001
	conf.ProfileDataID = 110002
	conf.ProfileAppName = "builtin_profile_app"
	conf.ProfileServiceName = "builtin_profile_app"
	return conf
}

func TestActiveProfileTaskConfigClean(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	if err := conf.Clean(); err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if conf.CollectionMethod != ActiveProfileMethodPerf ||
		conf.CollectionType != ActiveProfileTypeNativeCPU ||
		conf.CollectorConfig.StackUnwind != ActiveProfileStackUnwindFP ||
		conf.InlineSymbolization != ActiveProfileInlineAuto {
		t.Fatalf("unexpected defaults: %+v", conf)
	}
}

func TestActiveProfileTaskConfigRejectsUnsupportedCollection(t *testing.T) {
	for _, test := range []struct {
		name           string
		method         string
		collectionType string
	}{
		{name: "java is not implemented yet", method: "java", collectionType: "java_cpu"},
		{name: "python is out of scope", method: "python", collectionType: "python_cpu"},
		{name: "mismatched pair", method: ActiveProfileMethodPerf, collectionType: "go_heap"},
		{name: "golang does not collect native cpu", method: ActiveProfileMethodGolang, collectionType: ActiveProfileTypeNativeCPU},
		{name: "golang allocation is out of scope", method: ActiveProfileMethodGolang, collectionType: "go_allocation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestActiveProfileConfig(t)
			conf.CollectionMethod = test.method
			conf.CollectionType = test.collectionType
			if err := conf.Clean(); err == nil {
				t.Fatal("expected unsupported collection error")
			}
		})
	}
}

// newTestActiveProfileGoConfig 构造一个 Golang 采集配置：目标由 pprof endpoint 决定，
// 协议要求省略 process_selector。
func newTestActiveProfileGoConfig(t *testing.T, collectionType string) *ActiveProfileTaskConfig {
	t.Helper()
	conf := newTestActiveProfileConfig(t)
	conf.CollectionMethod = ActiveProfileMethodGolang
	conf.CollectionType = collectionType
	conf.ProcessSelector = ActiveProfileProcessSelector{}
	conf.CollectorConfig.IPAddress = "127.0.0.1"
	conf.CollectorConfig.Port = 6060
	return conf
}

// perf 专有参数由构造函数按 perf 预置，Golang 采集必须清零，避免它们进入 Event
// 配置快照与上传参数。
func TestActiveProfileGoCPUClearsPerfOnlyCollectorParams(t *testing.T) {
	conf := newTestActiveProfileGoConfig(t, ActiveProfileTypeGoCPU)
	if err := conf.Clean(); err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if conf.CollectorConfig.FrequencyHz != 0 || conf.CollectorConfig.StackUnwind != "" {
		t.Errorf("perf-only params survived: %+v", conf.CollectorConfig)
	}
	if conf.CollectorConfig.DurationSec != ActiveProfileDefaultDurationSec {
		t.Errorf("duration_sec = %d, want the default %d",
			conf.CollectorConfig.DurationSec, ActiveProfileDefaultDurationSec)
	}
	if !conf.IsGolang() {
		t.Error("IsGolang() = false")
	}
}

// go_heap 是瞬时快照，协议里没有 duration_sec；执行预算退化为后处理余量。
func TestActiveProfileGoHeapHasNoSamplingWindow(t *testing.T) {
	conf := newTestActiveProfileGoConfig(t, ActiveProfileTypeGoHeap)
	conf.CollectorConfig.DurationSec = 30
	if err := conf.Clean(); err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if conf.CollectorConfig.DurationSec != 0 {
		t.Errorf("duration_sec = %d, want 0 for go_heap", conf.CollectorConfig.DurationSec)
	}
	if got := conf.ExecutionTimeout(); got != ActiveProfilePostProcessAllowance {
		t.Errorf("ExecutionTimeout() = %s, want %s", got, ActiveProfilePostProcessAllowance)
	}
}

// resource_guard_config 与采集方式无关：整段可以不下发，下发后两种采集方式的解释
// 完全一致，不按 collection_method 分叉。
func TestActiveProfileResourceGuardIsCollectionMethodAgnostic(t *testing.T) {
	t.Run("omitted entirely leaves both protections off", func(t *testing.T) {
		conf := newTestActiveProfileGoConfig(t, ActiveProfileTypeGoCPU)
		conf.ResourceGuardConfig = ActiveProfileResourceGuardConfig{}
		if err := conf.Clean(); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}
		if conf.DiskQuotaEnabled() || conf.MemoryGuardEnabled() {
			t.Error("an omitted resource_guard_config must leave both protections off")
		}
	})

	guard := ActiveProfileResourceGuardConfig{
		AutoStopOnResourcePressure: true,
		MaxDiskUsage:               1024,
		MinMemLevel:                100,
	}
	for _, test := range []struct {
		name string
		conf func(*testing.T) *ActiveProfileTaskConfig
	}{
		{
			name: "perf",
			conf: newTestActiveProfileConfig,
		},
		{
			name: "golang",
			conf: func(t *testing.T) *ActiveProfileTaskConfig {
				return newTestActiveProfileGoConfig(t, ActiveProfileTypeGoCPU)
			},
		},
	} {
		t.Run(test.name+" keeps both thresholds as sent", func(t *testing.T) {
			conf := test.conf(t)
			conf.ResourceGuardConfig = guard
			if err := conf.Clean(); err != nil {
				t.Fatalf("Clean() error = %v", err)
			}
			if conf.ResourceGuardConfig != guard {
				t.Errorf("resource_guard_config = %+v, want it preserved as %+v", conf.ResourceGuardConfig, guard)
			}
			if !conf.DiskQuotaEnabled() || !conf.MemoryGuardEnabled() {
				t.Error("both protections should be active")
			}
		})
	}
}

func TestActiveProfileGoCollectorConfigValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*ActiveProfileTaskConfig)
		wantErr string
	}{
		{
			name:    "ip_address is required",
			mutate:  func(c *ActiveProfileTaskConfig) { c.CollectorConfig.IPAddress = "  " },
			wantErr: "ip_address",
		},
		{
			name:    "port is required",
			mutate:  func(c *ActiveProfileTaskConfig) { c.CollectorConfig.Port = 0 },
			wantErr: "port",
		},
		{
			name:    "port must be a valid tcp port",
			mutate:  func(c *ActiveProfileTaskConfig) { c.CollectorConfig.Port = 70000 },
			wantErr: "port",
		},
		{
			name:    "duration_sec is bounded",
			mutate:  func(c *ActiveProfileTaskConfig) { c.CollectorConfig.DurationSec = ActiveProfileMaxDurationSec + 1 },
			wantErr: "duration_sec",
		},
		{
			name: "process_selector must be omitted",
			mutate: func(c *ActiveProfileTaskConfig) {
				c.ProcessSelector = ActiveProfileProcessSelector{
					Type: ActiveProfileSelectorPID,
					PIDs: "2341",
				}
			},
			wantErr: "process_selector",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestActiveProfileGoConfig(t, ActiveProfileTypeGoCPU)
			test.mutate(conf)
			err := conf.Clean()
			if err == nil {
				t.Fatalf("Clean() error = nil, want an error mentioning %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("Clean() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

func TestActiveProfileTaskConfigRequiresIdentity(t *testing.T) {
	t.Run("tenant", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.BKTenantID = ""
		if err := conf.Clean(); err == nil {
			t.Fatal("expected bk_tenant_id validation error")
		}
	})
	t.Run("host", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.BKHostID = 0
		if err := conf.Clean(); err == nil {
			t.Fatal("expected bk_host_id validation error")
		}
	})
	t.Run("data ids", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.ProfileDataID = 0
		if err := conf.Clean(); err == nil {
			t.Fatal("expected profile_data_id validation error")
		}
	})
}

func TestActiveProfileCollectorConfigRequiresStackUnwind(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.StackUnwind = ""
		if err := conf.Clean(); err == nil {
			t.Fatal("expected stack_unwind validation error")
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.StackUnwind = "lbr"
		if err := conf.Clean(); err == nil {
			t.Fatal("expected stack_unwind validation error")
		}
	})
	t.Run("dwarf", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.StackUnwind = ActiveProfileStackUnwindDWARF
		if err := conf.Clean(); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}
	})
}

func TestActiveProfileProcessSelectorValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector ActiveProfileProcessSelector
		wantErr  bool
	}{
		{
			name:     "command requires match pattern",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorCommand, EnableDynamicCollection: true},
			wantErr:  true,
		},
		{
			name: "command rejects invalid exclude regexp",
			selector: ActiveProfileProcessSelector{
				Type:                    ActiveProfileSelectorCommand,
				MatchPattern:            "/data/order-api",
				ExcludePattern:          "([",
				EnableDynamicCollection: true,
			},
			wantErr: true,
		},
		{
			name: "command accepted",
			selector: ActiveProfileProcessSelector{
				Type:                    ActiveProfileSelectorCommand,
				MatchPattern:            "/data/order-api",
				ExcludePattern:          `/data/order-api[\d].+`,
				EnableDynamicCollection: true,
			},
		},
		{
			name: "static command requires host pids",
			selector: ActiveProfileProcessSelector{
				Type:         ActiveProfileSelectorCommand,
				MatchPattern: "/data/order-api",
			},
			wantErr: true,
		},
		{
			name: "static command accepted with pids",
			selector: ActiveProfileProcessSelector{
				Type:         ActiveProfileSelectorCommand,
				MatchPattern: "/data/order-api",
				HostPIDs:     []ActiveProfileHostPID{{BKHostID: 456, PID: 2341}},
			},
		},
		{
			name: "static command rejects non positive host pid",
			selector: ActiveProfileProcessSelector{
				Type:         ActiveProfileSelectorCommand,
				MatchPattern: "/data/order-api",
				HostPIDs:     []ActiveProfileHostPID{{BKHostID: 456, PID: 0}},
			},
			wantErr: true,
		},
		{
			name: "static command rejects too many host pids",
			selector: ActiveProfileProcessSelector{
				Type:         ActiveProfileSelectorCommand,
				MatchPattern: "/data/order-api",
				HostPIDs:     make([]ActiveProfileHostPID, ActiveProfileMaxSelectorHostPIDs+1),
			},
			wantErr: true,
		},
		{
			name: "pid file accepts multiple paths",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: " /var/run/a.pid; /var/run/b.pid;",
			},
		},
		{
			name: "pid file rejects blank paths",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: " ; ",
			},
			wantErr: true,
		},
		{
			name: "pid file rejects too many paths",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: strings.Repeat("/a;", ActiveProfileMaxSelectorPIDPaths+1),
			},
			wantErr: true,
		},
		{
			name: "pid file accepts paths at the total length limit",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: joinPaths(ActiveProfileMaxSelectorTextLength-1, 4),
			},
		},
		{
			name: "pid file rejects paths over the total length limit",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: joinPaths(ActiveProfileMaxSelectorTextLength, 4),
			},
			wantErr: true,
		},
		{
			name:     "pid file requires path",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPIDFile},
			wantErr:  true,
		},
		{
			name:     "pid requires values",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID},
			wantErr:  true,
		},
		{
			name:     "pid rejects non positive",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID, PIDs: "2341;0"},
			wantErr:  true,
		},
		{
			name:     "pid rejects non integer",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID, PIDs: "2341;abc"},
			wantErr:  true,
		},
		{
			name:     "pid accepts blanks and trailing separator",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID, PIDs: " 2341; 2342;"},
		},
		{
			name: "command rejects oversized match pattern",
			selector: ActiveProfileProcessSelector{
				Type:                    ActiveProfileSelectorCommand,
				MatchPattern:            strings.Repeat("a", ActiveProfileMaxSelectorTextLength+1),
				EnableDynamicCollection: true,
			},
			wantErr: true,
		},
		{
			name: "command rejects oversized exclude pattern",
			selector: ActiveProfileProcessSelector{
				Type:                    ActiveProfileSelectorCommand,
				MatchPattern:            "/data/order-api",
				ExcludePattern:          strings.Repeat("a", ActiveProfileMaxSelectorTextLength+1),
				EnableDynamicCollection: true,
			},
			wantErr: true,
		},
		{
			name: "command counts pattern length in characters",
			selector: ActiveProfileProcessSelector{
				Type:                    ActiveProfileSelectorCommand,
				MatchPattern:            strings.Repeat("数", ActiveProfileMaxSelectorTextLength),
				EnableDynamicCollection: true,
			},
		},
		{
			name: "pid file rejects oversized path",
			selector: ActiveProfileProcessSelector{
				Type:    ActiveProfileSelectorPIDFile,
				PIDPath: "/" + strings.Repeat("a", ActiveProfileMaxSelectorTextLength),
			},
			wantErr: true,
		},
		{
			name:     "pid accepts values at the limit",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID, PIDs: positivePIDs(ActiveProfileMaxSelectorPIDs)},
		},
		{
			name:     "pid rejects too many values",
			selector: ActiveProfileProcessSelector{Type: ActiveProfileSelectorPID, PIDs: positivePIDs(ActiveProfileMaxSelectorPIDs + 1)},
			wantErr:  true,
		},
		{
			name:     "unsupported type",
			selector: ActiveProfileProcessSelector{Type: "port"},
			wantErr:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestActiveProfileConfig(t)
			conf.ProcessSelector = test.selector
			err := conf.Clean()
			if test.wantErr && err == nil {
				t.Fatal("expected process selector validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Clean() error = %v", err)
			}
		})
	}
}

func joinPaths(length, count int) string {
	paths := make([]string, count)
	for i := range paths {
		paths[i] = "/" + strings.Repeat("a", length-1)
	}
	return strings.Join(paths, ActiveProfileSelectorSeparator)
}

func positivePIDs(count int) string {
	pids := make([]string, count)
	for i := range pids {
		pids[i] = strconv.Itoa(i + 1)
	}
	return strings.Join(pids, ActiveProfileSelectorSeparator)
}

// 关闭动态采集的 command 下发全部主机勾选的进程，Clean 后只保留本机的 PID。
func TestActiveProfileStaticCommandBindsOwnHostPIDs(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.ProcessSelector = ActiveProfileProcessSelector{
		Type:         ActiveProfileSelectorCommand,
		MatchPattern: "/data/order-api",
		HostPIDs: []ActiveProfileHostPID{
			{BKHostID: conf.BKHostID, PID: 2341},
			{BKHostID: conf.BKHostID + 1, PID: 812},
			{BKHostID: conf.BKHostID, PID: 2342},
		},
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if got := conf.ProcessSelector.TargetPIDs(); len(got) != 2 || got[0] != 2341 || got[1] != 2342 {
		t.Fatalf("target pids = %v, want only the pids selected on this host", got)
	}

	// 没有勾选本机进程的主机同样接受配置，执行时按未匹配到进程收敛
	conf.ProcessSelector.HostPIDs = []ActiveProfileHostPID{{BKHostID: conf.BKHostID + 1, PID: 812}}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if got := conf.ProcessSelector.TargetPIDs(); len(got) != 0 {
		t.Fatalf("target pids = %v, want none for a host without selection", got)
	}
}

func TestActiveProfileCommandSelectorCompilesExcludePattern(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.ProcessSelector = ActiveProfileProcessSelector{
		Type:                    ActiveProfileSelectorCommand,
		MatchPattern:            "/data/order-api",
		ExcludePattern:          `/data/order-api[\d].+`,
		EnableDynamicCollection: true,
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	exclude := conf.ProcessSelector.ExcludeRegexp()
	if exclude == nil {
		t.Fatal("exclude pattern should be compiled during Clean")
	}
	if !exclude.MatchString("/data/order-api2/bin") {
		t.Fatal("compiled exclude pattern does not match")
	}
}

func TestActiveProfileResourceGuardIndependentThresholds(t *testing.T) {
	for _, test := range []struct {
		name       string
		enabled    bool
		maxDisk    int64
		minMem     int64
		wantDisk   bool
		wantMemory bool
	}{
		{name: "switch off disables both", maxDisk: 1024, minMem: 100},
		{name: "only disk", enabled: true, maxDisk: 1024, wantDisk: true},
		{name: "only memory", enabled: true, minMem: 100, wantMemory: true},
		{name: "both", enabled: true, maxDisk: 1024, minMem: 100, wantDisk: true, wantMemory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestActiveProfileConfig(t)
			conf.ResourceGuardConfig = ActiveProfileResourceGuardConfig{
				AutoStopOnResourcePressure: test.enabled,
				MaxDiskUsage:               test.maxDisk,
				MinMemLevel:                test.minMem,
			}
			if err := conf.Clean(); err != nil {
				t.Fatal(err)
			}
			if got := conf.DiskQuotaEnabled(); got != test.wantDisk {
				t.Fatalf("DiskQuotaEnabled() = %t, want %t", got, test.wantDisk)
			}
			if got := conf.MemoryGuardEnabled(); got != test.wantMemory {
				t.Fatalf("MemoryGuardEnabled() = %t, want %t", got, test.wantMemory)
			}
		})
	}
}

func TestActiveProfileResourceGuardRejectsNegativeThresholds(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.ResourceGuardConfig.MaxDiskUsage = -1
	if err := conf.Clean(); err == nil {
		t.Fatal("expected negative max_disk_usage error")
	}
}

func TestActiveProfileTaskConfigRejectsUnsafeSamplingLimits(t *testing.T) {
	t.Run("maximum values are accepted", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.DurationSec = ActiveProfileMaxDurationSec
		conf.CollectorConfig.FrequencyHz = ActiveProfileMaxFrequencyHz
		if err := conf.Clean(); err != nil {
			t.Fatalf("Clean() maximum values error = %v", err)
		}
	})

	t.Run("duration", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.DurationSec = ActiveProfileMaxDurationSec + 1
		if err := conf.Clean(); err == nil {
			t.Fatal("expected duration limit error")
		}
	})

	t.Run("frequency", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.CollectorConfig.FrequencyHz = ActiveProfileMaxFrequencyHz + 1
		if err := conf.Clean(); err == nil {
			t.Fatal("expected frequency limit error")
		}
	})
}

func TestActiveProfileTaskConfigDerivesExecutionTimeout(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.CollectorConfig.DurationSec = ActiveProfileMaxDurationSec
	conf.Timeout = time.Second
	conf.AvailableDuration = time.Second
	conf.Period = time.Second
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	want := time.Duration(ActiveProfileMaxDurationSec)*time.Second +
		ActiveProfilePostProcessAllowance
	if conf.Timeout != want || conf.AvailableDuration != want || conf.Period != want {
		t.Fatalf(
			"derived durations timeout=%s available=%s period=%s, want %s",
			conf.Timeout,
			conf.AvailableDuration,
			conf.Period,
			want,
		)
	}
}

func TestActiveProfileMetaConfigDoesNotClampDerivedTimeout(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.CollectorConfig.DurationSec = ActiveProfileMaxDurationSec
	meta := &ActiveProfileMetaConfig{
		BaseTaskMetaParam: BaseTaskMetaParam{
			MaxTimeout: time.Second,
			MinPeriod:  time.Second,
		},
		Tasks: []*ActiveProfileTaskConfig{conf},
	}
	if err := meta.Clean(); err != nil {
		t.Fatal(err)
	}
	want := time.Duration(ActiveProfileMaxDurationSec)*time.Second +
		ActiveProfilePostProcessAllowance
	if conf.Timeout != want {
		t.Fatalf("task timeout = %s, want %s", conf.Timeout, want)
	}
	if meta.MaxTimeout != want {
		t.Fatalf("meta max timeout = %s, want %s", meta.MaxTimeout, want)
	}
}

func TestActiveProfileScheduleValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		schedule ActiveProfileScheduleConfig
		wantErr  bool
	}{
		{
			name:     "once_at requires execute_at",
			schedule: ActiveProfileScheduleConfig{Type: ActiveProfileScheduleOnceAt, Timezone: "UTC"},
			wantErr:  true,
		},
		{
			name: "once_at rejects cron expression",
			schedule: ActiveProfileScheduleConfig{
				Type:           ActiveProfileScheduleOnceAt,
				ExecuteAt:      time.Now().UnixMilli(),
				CronExpression: "0 * * * *",
				Timezone:       "UTC",
			},
			wantErr: true,
		},
		{
			name:     "cron requires expression",
			schedule: ActiveProfileScheduleConfig{Type: ActiveProfileScheduleCron, Timezone: "UTC"},
			wantErr:  true,
		},
		{
			name: "cron rejects execute_at",
			schedule: ActiveProfileScheduleConfig{
				Type:           ActiveProfileScheduleCron,
				CronExpression: "0 * * * *",
				ExecuteAt:      time.Now().UnixMilli(),
				Timezone:       "UTC",
			},
			wantErr: true,
		},
		{
			name: "cron rejects end before start",
			schedule: ActiveProfileScheduleConfig{
				Type:             ActiveProfileScheduleCron,
				CronExpression:   "0 * * * *",
				Timezone:         "UTC",
				EffectiveStartAt: 2000,
				EffectiveEndAt:   1000,
			},
			wantErr: true,
		},
		{
			name: "invalid timezone",
			schedule: ActiveProfileScheduleConfig{
				Type:      ActiveProfileScheduleOnceAt,
				ExecuteAt: time.Now().UnixMilli(),
				Timezone:  "not/a-timezone",
			},
			wantErr: true,
		},
		{
			name:     "unsupported type",
			schedule: ActiveProfileScheduleConfig{Type: "once", Timezone: "UTC"},
			wantErr:  true,
		},
		{
			name: "immediate accepted",
			schedule: ActiveProfileScheduleConfig{
				Type:      ActiveProfileScheduleImmediate,
				ExecuteAt: time.Now().UnixMilli(),
				Timezone:  "UTC",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := newTestActiveProfileConfig(t)
			conf.Schedule = test.schedule
			err := conf.Clean()
			if test.wantErr && err == nil {
				t.Fatal("expected schedule validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Clean() error = %v", err)
			}
		})
	}
}

// 执行身份必须在入口就校验完整，且与取消入口 cliCancelRequest.executionKey 的约束
// 一致——两侧对同一次执行推导出的执行键不能分叉。
func TestActiveProfileExecutionIdentityValidation(t *testing.T) {
	immediate := ActiveProfileScheduleConfig{
		Type:      ActiveProfileScheduleImmediate,
		ExecuteAt: time.Now().UnixMilli(),
		Timezone:  "UTC",
	}

	t.Run("bk_biz_id zero is rejected", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.BizID = 0
		err := conf.Clean()
		if err == nil || !strings.Contains(err.Error(), "bk_biz_id") {
			t.Fatalf("Clean() error = %v, want a bk_biz_id validation error", err)
		}
	})

	t.Run("bk_biz_id negative is accepted", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.BizID = -2
		if err := conf.Clean(); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}
	})

	// 立即执行的 task_id 是 SaaS 建的执行行 ID
	t.Run("immediate requires task_id", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.Schedule = immediate
		conf.TaskID = 0
		err := conf.Clean()
		if err == nil || !strings.Contains(err.Error(), "task_id") {
			t.Fatalf("Clean() error = %v, want a missing task_id error", err)
		}
	})

	t.Run("immediate accepts task_id", func(t *testing.T) {
		conf := newTestActiveProfileConfig(t)
		conf.Schedule = immediate
		conf.TaskID = 30521
		if err := conf.Clean(); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}
	})

	for _, scheduleType := range []string{ActiveProfileScheduleOnceAt, ActiveProfileScheduleCron} {
		t.Run(scheduleType+" requires task_id", func(t *testing.T) {
			conf := newTestActiveProfileConfig(t)
			conf.TaskID = 0
			switch scheduleType {
			case ActiveProfileScheduleOnceAt:
				conf.Schedule = ActiveProfileScheduleConfig{
					Type:      ActiveProfileScheduleOnceAt,
					ExecuteAt: time.Now().Add(time.Hour).UnixMilli(),
					Timezone:  "UTC",
				}
			case ActiveProfileScheduleCron:
				conf.Schedule = ActiveProfileScheduleConfig{
					Type:           ActiveProfileScheduleCron,
					CronExpression: "0 * * * *",
					Timezone:       "UTC",
				}
			}
			err := conf.Clean()
			if err == nil || !strings.Contains(err.Error(), "task_id") {
				t.Fatalf("Clean() error = %v, want a missing task_id error", err)
			}
		})
	}
}

func TestActiveProfileImmediateIsNotPlanned(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.Schedule = ActiveProfileScheduleConfig{
		Type:      ActiveProfileScheduleImmediate,
		ExecuteAt: time.Now().UnixMilli(),
		Timezone:  "UTC",
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if conf.IsPlanned() {
		t.Fatal("immediate executions must not participate in the planned task lock domain")
	}
	if _, ok := conf.InitialRunTime(time.Now()); ok {
		t.Fatal("immediate executions are not scheduled by the daemon")
	}
}

func TestActiveProfileOnceAtExpiry(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	executeAt := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	conf.Schedule = ActiveProfileScheduleConfig{
		Type:      ActiveProfileScheduleOnceAt,
		ExecuteAt: executeAt.UnixMilli(),
		Timezone:  "UTC",
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	past := executeAt.Add(time.Hour)
	next, ok := conf.InitialRunTime(past)
	if !ok || next.UnixMilli() != conf.Schedule.ExecuteAt {
		t.Fatalf("past once_at should retain its planned timestamp, got %v ok=%v", next, ok)
	}
	if !conf.ScheduleExpired(past) {
		t.Fatal("past once_at must be marked expired")
	}
	if conf.ScheduleExpired(executeAt.Add(-time.Hour)) {
		t.Fatal("future once_at must not be marked expired")
	}
	if conf.ScheduledAt(time.Now()).UnixMilli() != conf.Schedule.ExecuteAt {
		t.Fatal("once_at scheduled_at must equal its execute_at")
	}
}

func TestActiveProfileTaskConfigCron(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	conf.Schedule = ActiveProfileScheduleConfig{
		Type:           ActiveProfileScheduleCron,
		CronExpression: "  0   * * * *  ",
		Timezone:       "Asia/Shanghai",
	}
	if err := conf.Clean(); err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if conf.Schedule.CronExpression != "0 * * * *" {
		t.Fatalf("cron expression was not normalized: %q", conf.Schedule.CronExpression)
	}
	now := time.Date(2026, 7, 23, 10, 15, 0, 0, time.UTC)
	next, ok := conf.InitialRunTime(now)
	if !ok || !next.Equal(time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("next run = %v, ok=%v", next, ok)
	}
	after, ok := conf.NextRunTime(next)
	if !ok || !after.Equal(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("following run = %v, ok=%v", after, ok)
	}
}

func TestActiveProfileCronHonoursEffectiveWindow(t *testing.T) {
	start := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)
	conf := newTestActiveProfileConfig(t)
	conf.Schedule = ActiveProfileScheduleConfig{
		Type:             ActiveProfileScheduleCron,
		CronExpression:   "0 * * * *",
		Timezone:         "UTC",
		EffectiveStartAt: start.UnixMilli(),
		EffectiveEndAt:   end.UnixMilli(),
	}
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	// 生效起点之前加载时，第一次触发不早于起点。
	next, ok := conf.InitialRunTime(time.Date(2026, 7, 23, 9, 30, 0, 0, time.UTC))
	if !ok || !next.Equal(time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("first run inside the window = %v, ok=%v", next, ok)
	}
	// 越过结束时间即表示该 Cron 结束，不再产生后续 occurrence。
	if _, ok := conf.NextRunTime(end); ok {
		t.Fatal("cron must stop after its effective end")
	}
}

func TestActiveProfileScheduleIdentIgnoresDeliveryChanges(t *testing.T) {
	conf := newTestActiveProfileConfig(t)
	if err := conf.Clean(); err != nil {
		t.Fatal(err)
	}
	if err := conf.InitIdent(); err != nil {
		t.Fatal(err)
	}
	originalConfigIdent := conf.GetIdent()
	originalScheduleIdent := conf.GetScheduleIdent()
	if want := "bk_tenant_id=system"; !strings.Contains(originalScheduleIdent, want) {
		t.Fatalf("schedule ident %q does not contain %q", originalScheduleIdent, want)
	}

	conf.ProfileServiceName = "rotated-service-name"
	if err := conf.InitIdent(); err != nil {
		t.Fatal(err)
	}
	if conf.GetIdent() == originalConfigIdent {
		t.Fatal("full config ident should change when the report target changes")
	}
	if conf.GetScheduleIdent() != originalScheduleIdent {
		t.Fatal("schedule ident must not change when the report target changes")
	}

	conf.CollectorConfig.DurationSec = 60
	if conf.GetScheduleIdent() != originalScheduleIdent {
		t.Fatal("schedule ident must not change when sampling parameters change")
	}

	conf.Schedule.ExecuteAt++
	if conf.GetScheduleIdent() == originalScheduleIdent {
		t.Fatal("schedule ident should change when execute_at changes")
	}

	conf.Schedule.ExecuteAt--
	conf.BKTenantID = "tenant-2"
	if conf.GetScheduleIdent() == originalScheduleIdent {
		t.Fatal("schedule ident should change across tenants")
	}
}
