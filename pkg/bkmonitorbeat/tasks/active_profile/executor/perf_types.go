// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

const (
	callGraphFP    = configs.ActiveProfileStackUnwindFP
	callGraphDWARF = configs.ActiveProfileStackUnwindDWARF

	inlineSymbolizationAuto  = configs.ActiveProfileInlineAuto
	inlineSymbolizationOff   = configs.ActiveProfileInlineOff
	symbolizationNotStarted  = "not_started"
	symbolizationDisabled    = "disabled"
	symbolizationUnavailable = "unavailable"
	symbolizationComplete    = "complete"
	symbolizationDegraded    = "degraded"

	// eventCPUClock 是一期 native_cpu 固定使用的 perf 事件，不作为下发协议字段。
	eventCPUClock = "cpu-clock"

	// periodTypeCPU 是 perf 产物 pprof 的 PeriodType.Type。上报载荷的 type 取该值，
	// Profiling 结果表的清洗规则按它逐条入库。
	periodTypeCPU = "cpu"
)

// scriptOffsetMode 决定如何解释 perf script 每一帧的第一列。引入 dsoff 的那组 patch
// 同时改变了 callchain 第一列的语义，因此两者必须成对选择。
type scriptOffsetMode int

const (
	// offsetModeDSOFF 对应 perf >= 6.4：第一列是 raw ip，文件偏移由独立的 dsoff
	// 字段以 (dso+0x...) 形式给出。
	offsetModeDSOFF scriptOffsetMode = iota
	// offsetModeLegacyIP 对应 perf < 6.4：callchain 第一列对非内核 DSO 本身就是
	// 文件偏移，没有 dsoff 字段。内核帧打印的仍然是 raw ip。
	offsetModeLegacyIP
)

func (m scriptOffsetMode) String() string {
	if m == offsetModeLegacyIP {
		return "legacy_ip_offset"
	}
	return "dsoff"
}

// scriptFieldCandidate 是一组 perf script 字段及其第一列语义。
type scriptFieldCandidate struct {
	Fields string
	Mode   scriptOffsetMode
}

// scriptFieldCandidates 按顺序探测，使用第一个被本机 perf 接受的字段集。perf 在解析
// 选项阶段就会拒绝无效字段，因此探测与采样数据无关，只看退出码。
// 两级覆盖到 perf 3.x/4.x：dsoff 自 Linux 6.4 引入，低版本用第二级，第一列即偏移，
// 符号化质量与高版本一致。
var scriptFieldCandidates = []scriptFieldCandidate{
	{Fields: "comm,pid,time,period,event,ip,sym,dso,dsoff", Mode: offsetModeDSOFF},
	{Fields: "comm,pid,time,period,event,ip,sym,dso", Mode: offsetModeLegacyIP},
}

const (
	SymbolizationNotStarted  = symbolizationNotStarted
	SymbolizationUnavailable = symbolizationUnavailable
	SymbolizationDegraded    = symbolizationDegraded
)

// SampleLabels 是写入 pprof 每个样本的执行定位标签。查询侧用业务、执行时间、
// 主机和任务定位一次主机执行，多进程产物再由 pid/comm 区分。ip 是主机执行时
// IP，与 Event target 相同，供展示和历史检索，不参与唯一定位；采集方式和采集
// 类型仍只留在 Event 里，不重复写入样本。
type SampleLabels struct {
	BizID int32
	// TaskID 三类执行都有：计划任务为计划 ID，立即执行为 SaaS 建的执行行 ID。
	TaskID      int32
	ScheduledAt int64
	BKHostID    int64
	IP          string
}

type perfConfig struct {
	DurationSec int
	FrequencyHz int
	// PIDs 是本次采集的全部目标进程。一次 perf record 覆盖全部 PID，产出单个
	// 合并 pprof。SystemWide 只供本地调试使用，不属于下发协议。
	PIDs                []int32
	SystemWide          bool
	CallGraph           string
	InlineSymbolization string
	Event               string
	PerfPath            string
	// OutputDir 是调用方按单次执行创建的运行目录，采样文件、转换临时文件和
	// 清理范围都限定在该目录内。
	OutputDir     string
	KeepArtifacts bool
	Labels        SampleLabels
	// OffsetMode 由 preflight 探测本机 perf 接受的字段集后决定，不是配置项。
	OffsetMode          scriptOffsetMode
	ResourceGuard       *ResourceGuard
	ResourceGuardPolicy ResourceGuardPolicy
	ProfileBuildLimits  profileBuildLimits
}

func (c perfConfig) validate() error {
	if c.DurationSec < 1 || c.DurationSec > configs.ActiveProfileMaxDurationSec {
		return fmt.Errorf("duration must be between 1 and %d seconds", configs.ActiveProfileMaxDurationSec)
	}
	if c.FrequencyHz < 1 || c.FrequencyHz > configs.ActiveProfileMaxFrequencyHz {
		return fmt.Errorf("frequency must be between 1 and %d hz", configs.ActiveProfileMaxFrequencyHz)
	}
	if c.SystemWide {
		if len(c.PIDs) > 0 {
			return fmt.Errorf("system wide profiling does not accept target pids")
		}
	} else {
		if len(c.PIDs) == 0 {
			return fmt.Errorf("at least one target pid is required")
		}
		for _, pid := range c.PIDs {
			if pid <= 0 {
				return fmt.Errorf("target pids must all be positive")
			}
		}
	}
	if c.CallGraph != callGraphFP && c.CallGraph != callGraphDWARF {
		return fmt.Errorf("unsupported call graph %q", c.CallGraph)
	}
	if c.InlineSymbolization != inlineSymbolizationAuto && c.InlineSymbolization != inlineSymbolizationOff {
		return fmt.Errorf("unsupported inline symbolization %q", c.InlineSymbolization)
	}
	if c.Event != eventCPUClock {
		return fmt.Errorf("unsupported event %q", c.Event)
	}
	if strings.TrimSpace(c.PerfPath) == "" {
		return fmt.Errorf("perf path is required")
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return fmt.Errorf("output directory is required")
	}
	return nil
}

type preflightResult struct {
	PerfPath            string
	PerfVersion         string
	OS                  string
	Arch                string
	KernelRelease       string
	EffectiveUID        int
	PerfEventParanoid   *int
	CapabilityEffective string
	CapPerfmon          bool
	CapSysAdmin         bool
	CapSysPtrace        bool
	SeccompMode         int
	TargetPIDs          []int32
	// AlivePIDs 是 preflight 收敛后仍然可采集的目标进程。解析之后退出的进程被
	// 移除，因此可能少于 TargetPIDs。
	AlivePIDs          []int32
	ReadableTargetPIDs int
	OutputWritable     bool
	EventProbe         bool
	// ScriptFields 与 OffsetMode 是探测出的 perf script 字段集及第一列语义。
	ScriptFields string
	OffsetMode   scriptOffsetMode
}

type perfResult struct {
	Success   bool
	Message   string
	Preflight preflightResult
	// CollectedPIDs 是实际交给 perf record 的目标进程，用于生成 finished Event
	// 的 processes 列表。
	CollectedPIDs []int32
	PerfDataPath  string
	PerfDataBytes int64
	// ProfilePath 是落在运行目录里的产物。perf 的产物由 perf 自己写盘，因此始终有
	// 值；Golang 采集只在 keep_artifacts 打开时为了事后检查才写一份，否则为空。
	ProfilePath string
	// ProfileData 是全程留在内存里的产物，未压缩 protobuf。Golang 采集从 endpoint
	// 取回的就是最终 pprof，中间没有放大环节，落盘不降低内存峰值、只多一次写读；
	// perf 的中间产物体积大且本就由 perf 写盘，这里为 nil。
	ProfileData  []byte
	ProfileBytes int64
	SampleCount  int64
	// PeriodType 是产物 pprof 的 PeriodType.Type（perf 与 go_cpu 为 cpu，go_heap 为
	// space）。它填上报载荷的 type 字段：Profiling 结果表的清洗规则逐条取这个键，
	// 缺失会让整条记录清洗不出来。
	PeriodType              string
	TotalFrames             int64
	UnknownFrames           int64
	UnknownDSOTop           []dsoCount
	SymbolizationStatus     string
	SymbolizerBackend       string
	SymbolizationCandidates int64
	SymbolizationAttempts   int64
	SymbolizedLocations     int64
	InlineLocations         int64
	SymbolizationFailures   int64
	SymbolizationSkipped    int64
	SymbolizationUnresolved int64
	SymbolizationError      string
	RecordStderr            string
	ScriptStderr            string
}

// Result is the host-level execution result shared by the scheduled Gather and
// the lightweight one-shot CLI. Scheduling and delivery are intentionally not
// part of this type. 执行阶段与结果码不进入 Event 协议，只用于本机日志。
type Result = perfResult

// ProfileReader 打开待发送的产物，屏蔽两种采集方式载体不同这件事：perf 的产物在运行
// 目录里，Golang 采集的产物在内存里。调用方负责 Close。
func (r *perfResult) ProfileReader() (io.ReadCloser, error) {
	if r.ProfileData != nil {
		return io.NopCloser(bytes.NewReader(r.ProfileData)), nil
	}
	if r.ProfilePath == "" {
		return nil, fmt.Errorf("execution produced no profile")
	}
	file, err := os.Open(r.ProfilePath)
	if err != nil {
		return nil, fmt.Errorf("open profile %s: %w", r.ProfilePath, err)
	}
	return file, nil
}

type dsoCount struct {
	DSO   string
	Count int64
}

// UnknownDSOSummary 把没有函数名的帧按 DSO 归类成一行日志。条目已由转换器按次数
// 降序截断为 Top 20，这里不再裁剪。计数是帧出现次数，同一地址在多个样本里重复出现
// 会重复计入。
func (r *perfResult) UnknownDSOSummary() string {
	if len(r.UnknownDSOTop) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(r.UnknownDSOTop))
	for _, item := range r.UnknownDSOTop {
		pairs = append(pairs, fmt.Sprintf("%s=%d", item.DSO, item.Count))
	}
	return "[" + strings.Join(pairs, " ") + "]"
}
