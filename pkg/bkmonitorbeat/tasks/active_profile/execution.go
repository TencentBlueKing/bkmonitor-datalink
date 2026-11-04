// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/active_profile/executor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ErrExecutionCancelled 是用户取消整次立即执行时的 context 取消原因。取消后由原
// 采集进程尽力上报 cancelled finished Event 与终态指标。
var ErrExecutionCancelled = errors.New("execution cancelled by user")

// staleRunDirBuffer 是判定运行目录为残留的额外宽限。超过执行预算加该宽限仍存在的
// 目录一定来自异常退出的上一个进程。
const staleRunDirBuffer = time.Hour

// ExecuteOptions 描述两个执行入口的差异。计划任务由常驻 Beat 上报 started 与
// finished；立即执行的开始由 SaaS 直接写入执行表，轻量 CLI 只上报 finished。
// 取消对两种模式一视同仁，都通过运行目录里的取消标记触发。
type ExecuteOptions struct {
	ReportStarted bool
	FallbackIP    string
	// Sink 投递 Event 与 pprof 产物。常驻 Beat 用 Beat 的事件通道，轻量 CLI 用自建
	// GSE 客户端，两者都按平台注入的 DataID 路由。
	Sink           reportSink
	MetricSink     chan<- define.Event
	GatherUpDataID int32
}

// Execute 执行一次主机级采集并收敛终态。无论成功、失败、冲突、熔断、超时还是被
// 取消，都会尝试发送 finished Event 与终态运营指标。
func Execute(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	scheduledAt time.Time,
	opts ExecuteOptions,
) {
	ip := resolveExecutionIP(opts.FallbackIP)
	startedAt := time.Now()

	// 同 task 并发保护只对计划任务生效，立即执行不参与该锁域。
	lock, acquired, err := acquireTaskRunLock(conf)
	if err != nil {
		finish(ctx, conf, ip, scheduledAt, startedAt, opts, ExecutionOutcome{
			Status:       StatusFailed,
			ErrorType:    ErrorTypeCollectFailed,
			ErrorMessage: fmt.Sprintf("acquire task run lock failed: %v", err),
		})
		return
	}
	if !acquired {
		// 抢锁失败不等待、不排队、不启动采集器。
		logger.Warnf("[active_profile] task_id=%d skipped: %s", conf.GetTaskID(), errTaskAlreadyRunning)
		finish(ctx, conf, ip, scheduledAt, startedAt, opts, ExecutionOutcome{
			Status:       StatusFailed,
			ErrorType:    ErrorTypeTaskBusy,
			ErrorMessage: errTaskAlreadyRunning,
		})
		return
	}
	defer lock.Release()

	if opts.ReportStarted {
		started := newStartedEvent(conf, ip, scheduledAt, startedAt)
		if reportErr := reportStartedEvent(ctx, opts.Sink, started); reportErr != nil {
			// started 丢失不影响执行：SaaS 允许 finished 在 started 缺失时独立生效。
			logger.Errorf("[active_profile] report started event failed: %v", reportErr)
		}
	}

	sweepStaleRunDirs(conf.OutputDir, conf.ExecutionTimeout()+staleRunDirBuffer)
	run, err := openRunContext(conf, scheduledAt)
	if err != nil {
		finish(ctx, conf, ip, scheduledAt, startedAt, opts, ExecutionOutcome{
			Status:       StatusFailed,
			ErrorType:    ErrorTypeCollectFailed,
			ErrorMessage: err.Error(),
		})
		return
	}
	defer run.Close(conf.KeepArtifacts)

	// 取消只作用于本次执行的 context：采集层据此回收自己拉起的 perf 子进程组，
	// 同进程的其他任务完全不受影响。finish 使用未取消的外层 ctx，因此取消之后
	// 终态 Event 仍有机会送达。
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	stopWatching := watchCancelMarker(runCtx, run.Dir, cancelRun)
	defer stopWatching()

	outcome := collect(runCtx, conf, run, scheduledAt, ip, opts.Sink)
	finish(ctx, conf, ip, scheduledAt, startedAt, opts, outcome)
}

// collect 完成进程解析、采集与产物上报，并把结果收敛为主机终态。
func collect(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	run *runContext,
	scheduledAt time.Time,
	ip string,
	sink reportSink,
) ExecutionOutcome {
	// Go 采集没有 process_selector：目标由 pprof endpoint 决定，进程只是采集后
	// 尽力反查出来的标签来源，反查不到不影响本次执行。
	var processes []ProcessResult
	var err error
	if conf.IsGolang() {
		processes = resolveGoEndpointProcess(ctx, conf.CollectorConfig.Port)
	} else {
		processes, err = resolveProcesses(&conf.ProcessSelector)
		if err != nil {
			return failedOutcome(ctx, err, nil)
		}
	}

	req := executor.Request{
		ScheduledAt: scheduledAt,
		PIDs:        processPIDs(processes),
		RunDir:      run.Dir,
		IP:          ip,
	}
	var result *executor.Result
	if conf.IsGolang() {
		if len(processes) == 1 {
			req.GoEndpointProcess = &executor.GoEndpointProcess{
				PID:  processes[0].PID,
				Comm: processes[0].ProcessName,
			}
		}
		result, err = executor.RunGoPprof(ctx, conf, req)
		logGoExecutorResult(run.Key, result)
	} else {
		result, err = executor.RunCPU(ctx, conf, req)
		logExecutorResult(run.Key, result)
	}
	if err != nil {
		return failedOutcome(ctx, err, processes)
	}

	collected := collectedProcesses(processes, result.CollectedPIDs)
	if reportErr := reportProfile(ctx, sink, conf, result); reportErr != nil {
		return failedOutcome(ctx, reportErr, collected)
	}

	// 单次 perf record 覆盖全部目标进程，无法逐进程判定成败；能观察到的失败是
	// 解析之后、采集之前退出的进程。至少一个进程被采集就保留合并产物。
	if len(collected) < len(processes) {
		return ExecutionOutcome{
			Status:    StatusPartialSucceed,
			Processes: collected,
			ErrorType: ErrorTypeProcessesExited,
			ErrorMessage: fmt.Sprintf(
				"%d of %d matched processes were no longer available when profiling started",
				len(processes)-len(collected),
				len(processes),
			),
		}
	}
	return ExecutionOutcome{Status: StatusSucceed, Processes: collected}
}

// finish 发送 finished Event 与终态运营指标。取消场景下调度 context 已被取消，
// 因此这里使用独立的发送 context，保证终态仍有机会送达。
func finish(
	ctx context.Context,
	conf *configs.ActiveProfileTaskConfig,
	ip string,
	scheduledAt time.Time,
	startedAt time.Time,
	opts ExecuteOptions,
	outcome ExecutionOutcome,
) {
	event := newFinishedEvent(conf, ip, scheduledAt, startedAt, time.Now(), outcome)
	// 取消与熔断都会取消执行 context，终态仍然要有机会送达，因此这里剥掉取消，
	// 只保留 sendReport 自己的投递超时。
	if err := reportFinishedEvent(context.WithoutCancel(ctx), opts.Sink, event); err != nil {
		logger.Errorf("[active_profile] report finished event failed: %v", err)
	}
	reportExecutionMetric(opts.MetricSink, opts.GatherUpDataID, event)
	if outcome.Status == StatusSucceed {
		logger.Infof(
			"[active_profile] task_id=%d scheduled_at=%d status=%s processes=%d",
			conf.GetTaskID(), scheduledAt.UnixMilli(), outcome.Status, len(outcome.Processes),
		)
		return
	}
	logger.Errorf(
		"[active_profile] task_id=%d scheduled_at=%d status=%s error=%s",
		conf.GetTaskID(), scheduledAt.UnixMilli(), outcome.Status, outcome.ErrorMessage,
	)
}

// statusFor 把执行错误映射到 finished 允许上报的终态。running 与 unknown 由 SaaS
// 派生，采集端不写回。成功代表 Profiling 结果可用，没有匹配到进程与采集结果为空
// 都查不到结果，按失败收敛，原因保留在 message 中。
func statusFor(ctx context.Context, err error) string {
	if errors.Is(context.Cause(ctx), ErrExecutionCancelled) || errors.Is(err, ErrExecutionCancelled) {
		return StatusCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	return StatusFailed
}

// failedOutcome 把执行错误收敛为终态；超时与取消由状态本身说明，不归类。
func failedOutcome(ctx context.Context, err error, processes []ProcessResult) ExecutionOutcome {
	outcome := ExecutionOutcome{Status: statusFor(ctx, err), Processes: processes, ErrorMessage: err.Error()}
	if outcome.Status == StatusFailed {
		outcome.ErrorType = errorTypeFor(err)
	}
	return outcome
}

// errorTypeFor 按错误链上的类别标记归类失败原因，没有标记的归为采集执行失败。
// 没有样本的检查要在 Go endpoint 之前：endpoint 返回空数据时两个标记都在。
func errorTypeFor(err error) string {
	switch {
	case errors.Is(err, errNoProcessMatched), errors.Is(err, executor.ErrNoTargetProcess):
		return ErrorTypeProcessNotFound
	case errors.Is(err, executor.ErrEmptyProfile):
		return ErrorTypeEmptyProfile
	case errors.Is(err, executor.ErrPerfUnavailable):
		return ErrorTypePerfUnavailable
	case errors.Is(err, executor.ErrGoEndpoint):
		return ErrorTypeGoEndpointFailed
	case errors.Is(err, executor.ErrMemoryLimit):
		return ErrorTypeMemoryLimit
	case errors.Is(err, executor.ErrDiskLimit):
		return ErrorTypeDiskLimit
	case errors.Is(err, errReportFailed):
		return ErrorTypeReportFailed
	case errors.Is(err, executor.ErrInvalidConfig):
		return ErrorTypeConfigInvalid
	default:
		return ErrorTypeCollectFailed
	}
}

// collectedProcesses 保留实际交给 perf 的进程，顺序与解析结果一致。
func collectedProcesses(processes []ProcessResult, collectedPIDs []int32) []ProcessResult {
	if len(collectedPIDs) == 0 {
		return processes
	}
	kept := make(map[int32]struct{}, len(collectedPIDs))
	for _, pid := range collectedPIDs {
		kept[pid] = struct{}{}
	}
	collected := make([]ProcessResult, 0, len(collectedPIDs))
	for _, process := range processes {
		if _, ok := kept[process.PID]; ok {
			collected = append(collected, process)
		}
	}
	return collected
}

// resolveExecutionIP 返回主机执行时 IP，写入 Event target、运营指标和 pprof
// ip 标签。优先取 Watcher 上报的主机内网 IP，不是 Golang 配置里的 pprof 地址。
func resolveExecutionIP(fallback string) string {
	if define.GlobalWatcher != nil {
		if ip := define.GlobalWatcher.GetHostInnerIp(); ip != "" {
			return ip
		}
	}
	if fallback != "" {
		return fallback
	}
	if ips := tasks.DefaultIPs(configs.IPv4); len(ips) > 0 {
		return ips[0]
	}
	return ""
}

// logGoExecutorResult 记录 Go 产物统计。Go 的 pprof 由目标进程自带符号，没有
// preflight、字段探测与符号化过程，因此不复用 perf 那组诊断字段。
func logGoExecutorResult(runKey string, result *executor.Result) {
	if result == nil {
		return
	}
	logger.Infof(
		"[active_profile] run=%s samples=%d profile_bytes=%d",
		runKey, result.SampleCount, result.ProfileBytes,
	)
}

// logExecutorResult 把符号化与产物统计留在本机日志。这些内部诊断信息不进入 Event。
func logExecutorResult(runKey string, result *executor.Result) {
	if result == nil {
		return
	}
	logger.Infof(
		"[active_profile] run=%s perf_version=%q script_fields=%q offset_mode=%s",
		runKey,
		result.Preflight.PerfVersion,
		result.Preflight.ScriptFields,
		result.Preflight.OffsetMode,
	)
	logger.Infof(
		"[active_profile] run=%s samples=%d unknown=%d/%d profile_bytes=%d perf_data_bytes=%d symbolization=%s backend=%s symbolized=%d inline=%d failures=%d skipped=%d unresolved=%d message=%s",
		runKey,
		result.SampleCount,
		result.UnknownFrames,
		result.TotalFrames,
		result.ProfileBytes,
		result.PerfDataBytes,
		result.SymbolizationStatus,
		result.SymbolizerBackend,
		result.SymbolizedLocations,
		result.InlineLocations,
		result.SymbolizationFailures,
		result.SymbolizationSkipped,
		result.SymbolizationUnresolved,
		result.SymbolizationError,
	)
	// 没有函数名的帧按 DSO 归类打出来。unknown 计数本身只能说明"有多少帧没名字"，
	// 定位要靠这份归类：缺 symtab 的业务二进制、JIT 匿名映射和容器内路径在这里一眼
	// 能分开。没有 unknown 帧时为空，不打这行。
	if summary := result.UnknownDSOSummary(); summary != "" {
		logger.Infof("[active_profile] run=%s unknown_dso_top=%s", runKey, summary)
	}
}
