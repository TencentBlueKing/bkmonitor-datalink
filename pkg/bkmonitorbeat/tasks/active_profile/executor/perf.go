// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ErrEmptyProfile 表示采集正常结束但没有产出样本，例如目标进程在采集窗口内没有
// 被调度到。没有可查看的结果，上层按失败收敛并在 message 中说明。
var ErrEmptyProfile = errors.New("empty profile")

// 以下标记执行失败的类别，上层据此生成 finished 的 error_type。标记经 MarkError
// 附在原错误上，不改变错误文本。
var (
	// ErrPerfUnavailable 表示本机 perf 不可用或权限不足。
	ErrPerfUnavailable = errors.New("perf unavailable")
	// ErrNoTargetProcess 表示采集开始前目标进程已全部退出。
	ErrNoTargetProcess = errors.New("no target process")
	// ErrGoEndpoint 表示无法从 Go pprof endpoint 取到可用的产物。
	ErrGoEndpoint = errors.New("go pprof endpoint failed")
	// ErrInvalidConfig 表示配置不被当前采集器支持，多为 SaaS 与采集器版本不一致。
	ErrInvalidConfig = errors.New("invalid config")
	// ErrMemoryLimit 与 ErrDiskLimit 由资源保护触发，ResourceGuardError 按触发类别匹配。
	ErrMemoryLimit = errors.New("memory limit")
	ErrDiskLimit   = errors.New("disk limit")
)

type markedError struct {
	err  error
	mark error
}

func (e *markedError) Error() string { return e.err.Error() }

func (e *markedError) Unwrap() []error { return []error{e.err, e.mark} }

// MarkError 给错误附上类别标记，errors.Is 既能匹配原错误链也能匹配标记。
func MarkError(mark, err error) error {
	return &markedError{err: err, mark: mark}
}

// Request 是一次主机级采集执行的输入。目标进程由上层的 process_selector 解析，
// 运行目录由上层按单次执行创建并负责清理。
type Request struct {
	ScheduledAt time.Time
	PIDs        []int32
	RunDir      string
	// IP 是主机执行时 IP，与 Event target、运营指标同一来源。写入 pprof 的 ip
	// 标签供展示和按执行时 IP 检索；空值不写该标签，也不回退成 pprof endpoint
	// 上用户填写的 127.0.0.1。
	IP string

	// GoEndpointProcess 只用于 Golang 采集：pprof 响应不携带进程信息，样本的
	// pid/comm 标签只能来自按 endpoint 端口反查的唯一监听进程。反查不到时为 nil，
	// 产物只带执行定位标签。
	GoEndpointProcess *GoEndpointProcess
}

// GoEndpointProcess 是 pprof endpoint 端口反查到的监听进程。
type GoEndpointProcess struct {
	PID  int32
	Comm string
}

// RunCPU performs one native CPU profiling execution. It does not report an
// Event, send a metric, or know whether its caller is daemon or CLI.
func RunCPU(ctx context.Context, conf *configs.ActiveProfileTaskConfig, req Request) (*Result, error) {
	perfConf := newPerfConfig(conf, req)
	if err := perfConf.validate(); err != nil {
		return nil, MarkError(ErrInvalidConfig, err)
	}

	timeout := conf.ExecutionTimeout()
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !conf.ResourceGuardConfig.AutoStopOnResourcePressure {
		cmdCtx = withOutsideResourceLimit(cmdCtx)
	}

	logger.Infof("[active_profile] exec perf=%s pids=%v system_wide=%t duration=%ds frequency=%dhz stack_unwind=%s execution_timeout=%s",
		perfConf.PerfPath, perfConf.PIDs, perfConf.SystemWide, perfConf.DurationSec, perfConf.FrequencyHz,
		perfConf.CallGraph, timeout)
	value, err := executePerf(cmdCtx, perfConf)
	result := &value
	// 产物由 profileBuilder 构造，PeriodType 固定是 cpu，不需要回读产物。
	result.PeriodType = periodTypeCPU
	if err != nil {
		return result, err
	}
	if !result.Success {
		return result, fmt.Errorf("perf unsuccessful: %s", result.Message)
	}
	if result.ProfilePath == "" {
		return result, fmt.Errorf("perf returned empty profile path")
	}
	stat, err := os.Stat(result.ProfilePath)
	if err != nil {
		return result, fmt.Errorf("stat perf profile %s: %w", result.ProfilePath, err)
	}
	if stat.Size() == 0 {
		return result, fmt.Errorf("perf produced empty profile %s: %w", result.ProfilePath, ErrEmptyProfile)
	}
	return result, nil
}

func newPerfConfig(conf *configs.ActiveProfileTaskConfig, req Request) perfConfig {
	return perfConfig{
		DurationSec:         conf.CollectorConfig.DurationSec,
		FrequencyHz:         conf.CollectorConfig.FrequencyHz,
		PIDs:                req.PIDs,
		SystemWide:          conf.ProcessSelector.Type == configs.ActiveProfileSelectorSystem,
		CallGraph:           conf.CollectorConfig.StackUnwind,
		InlineSymbolization: conf.InlineSymbolization,
		Event:               eventCPUClock,
		PerfPath:            conf.PerfPath,
		OutputDir:           req.RunDir,
		KeepArtifacts:       conf.KeepArtifacts,
		Labels: SampleLabels{
			BizID:       conf.GetBizID(),
			TaskID:      conf.GetTaskID(),
			ScheduledAt: req.ScheduledAt.UnixMilli(),
			BKHostID:    conf.BKHostID,
			IP:          req.IP,
		},
		ResourceGuard:       defaultResourceGuard,
		ResourceGuardPolicy: resourceGuardPolicyFromConfig(conf, req.RunDir),
		ProfileBuildLimits:  defaultProfileBuildLimits(),
	}
}
