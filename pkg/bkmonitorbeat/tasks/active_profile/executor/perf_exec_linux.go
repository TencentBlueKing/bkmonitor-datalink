// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	maxCommandStderr      = 64 * 1024
	processInterruptGrace = 2 * time.Second
)

// preflightCommandTimeout 是 preflight 中单条 perf 命令的超时。Beat 在 resource_limit
// cgroup 内且多个采集同时启动时，perf 启动会被 CPU 配额拖慢。
var preflightCommandTimeout = 10 * time.Second

// executePerf 在调用方提供的运行目录内完成一次采集。目录的创建与清理都由调用方
// 负责，因此这里不再自行创建临时目录，也不会在失败时删除整个目录。
func executePerf(ctx context.Context, conf perfConfig) (result perfResult, returnErr error) {
	if err := conf.validate(); err != nil {
		result.Message = err.Error()
		return result, err
	}

	if err := os.MkdirAll(conf.OutputDir, 0o750); err != nil {
		result.Message = err.Error()
		return result, err
	}
	if conf.ResourceGuardPolicy.enabled() {
		if conf.ResourceGuard == nil {
			conf.ResourceGuard = defaultResourceGuard
		}
		guardedCtx, cancel := context.WithCancelCause(ctx)
		unregister, err := conf.ResourceGuard.Register(
			conf.OutputDir,
			time.Now(),
			conf.ResourceGuardPolicy,
			cancel,
		)
		if err != nil {
			cancel(nil)
			result.Message = err.Error()
			return result, err
		}
		ctx = guardedCtx
		defer func() {
			unregister()
			guardErr := resourceGuardError(context.Cause(guardedCtx))
			cancel(nil)
			if guardErr == nil {
				return
			}
			result.Success = false
			result.Message = guardErr.Error()
			returnErr = guardErr
		}()
	}

	preflight, err := runPreflight(ctx, conf)
	result.Preflight = preflight
	if err != nil {
		result.Message = err.Error()
		return result, err
	}

	perfDataPath := filepath.Join(conf.OutputDir, "perf.data")
	profilePath := filepath.Join(conf.OutputDir, "profile.pprof")
	result.PerfDataPath = perfDataPath
	result.ProfilePath = profilePath
	result.CollectedPIDs = preflight.AlivePIDs

	recordArgs := buildRecordArgs(conf, preflight.AlivePIDs, perfDataPath)
	recordStderr, err := runCommand(ctx, preflight.PerfPath, recordArgs, io.Discard)
	result.RecordStderr = recordStderr
	if err != nil {
		result.Message = errWithOutput(err, recordStderr).Error()
		return result, err
	}
	if info, statErr := os.Stat(perfDataPath); statErr == nil {
		result.PerfDataBytes = info.Size()
	}

	// 使用 preflight 探测出的字段集，并按同一来源决定第一列语义。
	conf.OffsetMode = preflight.OffsetMode
	scriptArgs := []string{
		"script",
		"-i", perfDataPath,
		"-F", preflight.ScriptFields,
	}
	stats, scriptStderr, scriptErr := runScriptCommand(
		ctx,
		preflight.PerfPath,
		scriptArgs,
		func(reader io.Reader) (profileStats, error) {
			return convertPerfScriptReaderSafelyContext(ctx, reader, profilePath, conf)
		},
	)
	result.ScriptStderr = scriptStderr
	if scriptErr != nil {
		result.Message = errWithOutput(scriptErr, scriptStderr).Error()
		return result, scriptErr
	}
	result.SampleCount = stats.Samples
	result.TotalFrames = stats.TotalFrames
	result.UnknownFrames = stats.UnknownFrames
	result.UnknownDSOTop = stats.UnknownDSOTop
	result.SymbolizationStatus = stats.SymbolizationStatus
	result.SymbolizerBackend = stats.SymbolizerBackend
	result.SymbolizationCandidates = stats.SymbolizationCandidates
	result.SymbolizationAttempts = stats.SymbolizationAttempts
	result.SymbolizedLocations = stats.SymbolizedLocations
	result.InlineLocations = stats.InlineLocations
	result.SymbolizationFailures = stats.SymbolizationFailures
	result.SymbolizationSkipped = stats.SymbolizationSkipped
	result.SymbolizationUnresolved = stats.SymbolizationUnresolved
	result.SymbolizationError = stats.SymbolizationError
	if info, statErr := os.Stat(profilePath); statErr == nil {
		result.ProfileBytes = info.Size()
	}

	if !conf.KeepArtifacts {
		if removeErr := os.Remove(perfDataPath); removeErr == nil {
			result.PerfDataPath = ""
		} else {
			result.Message = fmt.Sprintf("profile complete but remove perf.data failed: %v", removeErr)
		}
	}

	result.Success = true
	return result, nil
}

// runPreflight 检查 perf 可用性与权限，并把目标进程收敛为当前仍然可采集的集合。
// 部分进程在解析之后退出属于预期结果，只有全部目标都不可用时才失败。
func runPreflight(ctx context.Context, conf perfConfig) (preflightResult, error) {
	result := preflightResult{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		EffectiveUID: os.Geteuid(),
		TargetPIDs:   conf.PIDs,
	}

	resolved, err := exec.LookPath(conf.PerfPath)
	if err != nil {
		return result, MarkError(ErrPerfUnavailable, fmt.Errorf("locate perf %q: %w", conf.PerfPath, err))
	}
	result.PerfPath = resolved

	versionCtx, cancel := context.WithTimeout(ctx, preflightCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(versionCtx, resolved, "version").CombinedOutput()
	if err != nil {
		return result, MarkError(
			ErrPerfUnavailable,
			fmt.Errorf("run perf version: %w: %s",
				preflightCommandError(ctx, versionCtx, err), strings.TrimSpace(string(out))),
		)
	}
	result.PerfVersion = strings.TrimSpace(string(out))

	if raw, readErr := os.ReadFile("/proc/sys/kernel/perf_event_paranoid"); readErr == nil {
		if value, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil {
			result.PerfEventParanoid = &value
		}
	}

	if release, unameErr := kernelRelease(); unameErr == nil {
		result.KernelRelease = release
	}
	readProcessSecurityStatus(&result)

	if !conf.SystemWide {
		alive := make([]int32, 0, len(conf.PIDs))
		var lastErr error
		for _, pid := range conf.PIDs {
			if err := checkTargetProcess(pid); err != nil {
				lastErr = err
				continue
			}
			alive = append(alive, pid)
		}
		if len(alive) == 0 {
			return result, MarkError(ErrNoTargetProcess, fmt.Errorf("no target process is available: %w", lastErr))
		}
		result.AlivePIDs = alive
		result.ReadableTargetPIDs = len(alive)
	}

	probeFile, err := os.CreateTemp(conf.OutputDir, ".perf-preflight-*.data")
	if err != nil {
		return result, fmt.Errorf("create preflight output in %s: %w", conf.OutputDir, err)
	}
	probePath := probeFile.Name()
	if err := probeFile.Close(); err != nil {
		_ = os.Remove(probePath)
		return result, fmt.Errorf("close preflight output: %w", err)
	}
	result.OutputWritable = true
	defer os.Remove(probePath)

	probeCtx, cancel := context.WithTimeout(ctx, preflightCommandTimeout)
	defer cancel()
	probeStderr, err := runCommand(
		probeCtx,
		resolved,
		buildProbeArgs(conf, result.AlivePIDs, probePath),
		io.Discard,
	)
	if err != nil {
		return result, MarkError(
			ErrPerfUnavailable,
			fmt.Errorf("perf event probe failed: %w: %s",
				preflightCommandError(ctx, probeCtx, err), strings.TrimSpace(probeStderr)),
		)
	}
	result.EventProbe = true

	// 在采样之前确定 perf script 的字段集。dsoff 自 Linux 6.4 才引入，低版本 perf 会
	// 在解析选项阶段直接拒绝；不先探测的话，失败要等到整个采样窗口跑完之后才暴露。
	candidate, err := probeScriptFields(ctx, resolved, probePath)
	if err != nil {
		return result, err
	}
	result.ScriptFields = candidate.Fields
	result.OffsetMode = candidate.Mode
	return result, nil
}

// probeScriptFields 依次尝试候选字段集，返回第一个被本机 perf 接受的。perf 在解析
// 选项阶段就会拒绝无效字段，因此 probe 数据里有没有样本都不影响判定。
func probeScriptFields(ctx context.Context, perfPath, probePath string) (scriptFieldCandidate, error) {
	var lastErr error
	for _, candidate := range scriptFieldCandidates {
		probeCtx, cancel := context.WithTimeout(ctx, preflightCommandTimeout)
		stderr, err := runCommand(
			probeCtx,
			perfPath,
			[]string{"script", "-i", probePath, "-F", candidate.Fields},
			io.Discard,
		)
		err = preflightCommandError(ctx, probeCtx, err)
		cancel()
		if err == nil {
			return candidate, nil
		}
		lastErr = fmt.Errorf("perf script rejected fields %q: %w: %s",
			candidate.Fields, err, strings.TrimSpace(stderr))
	}
	return scriptFieldCandidate{}, MarkError(
		ErrPerfUnavailable,
		fmt.Errorf("no supported perf script field set: %w", lastErr),
	)
}

// preflightCommandError 区分 preflight 命令自身超时与整次执行被取消或超时。前者只说明
// perf 在本机启动过慢，按 perf 不可用失败，不能带出 DeadlineExceeded 被报成整次执行 timeout。
func preflightCommandError(parent, command context.Context, err error) error {
	if err != nil && parent.Err() == nil && errors.Is(command.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s", preflightCommandTimeout)
	}
	return err
}

func checkTargetProcess(pid int32) error {
	procDir := filepath.Join("/proc", strconv.FormatInt(int64(pid), 10))
	if _, err := os.Stat(procDir); err != nil {
		return fmt.Errorf("target pid %d is unavailable: %w", pid, err)
	}
	maps, err := os.Open(filepath.Join(procDir, "maps"))
	if err != nil {
		return fmt.Errorf("read target pid %d maps: %w", pid, err)
	}
	_ = maps.Close()
	return nil
}

// 两条 record 命令都带 -N：perf 默认会把命中的 DSO 存进运行用户的 build-id 缓存
// （root 即 ~/.debug），那份数据不在运行目录里，既不受磁盘配额约束也不会被清理。
func buildProbeArgs(conf perfConfig, pids []int32, outputPath string) []string {
	args := []string{
		"record",
		"-N",
		"-e", conf.Event,
		"-g",
		"--call-graph", conf.CallGraph,
		"-o", outputPath,
	}
	args = appendTargetArgs(args, conf, pids)
	return append(args, "--", "sleep", "0.05")
}

func buildRecordArgs(conf perfConfig, pids []int32, outputPath string) []string {
	args := []string{
		"record",
		"-N",
		"-e", conf.Event,
		"-g",
		"--call-graph", conf.CallGraph,
		"-F", strconv.Itoa(conf.FrequencyHz),
		"-o", outputPath,
	}
	args = appendTargetArgs(args, conf, pids)
	return append(args, "--", "sleep", strconv.Itoa(conf.DurationSec))
}

// appendTargetArgs 用一次 perf record 覆盖全部目标进程，产出单个合并产物。
func appendTargetArgs(args []string, conf perfConfig, pids []int32) []string {
	if conf.SystemWide {
		return append(args, "-a")
	}
	return append(args, "-p", joinPIDs(pids))
}

func joinPIDs(pids []int32) string {
	values := make([]string, 0, len(pids))
	for _, pid := range pids {
		values = append(values, strconv.FormatInt(int64(pid), 10))
	}
	return strings.Join(values, ",")
}

func kernelRelease() (string, error) {
	var info unix.Utsname
	if err := unix.Uname(&info); err != nil {
		return "", err
	}
	value := make([]byte, 0, len(info.Release))
	for _, char := range info.Release {
		if char == 0 {
			break
		}
		value = append(value, byte(char))
	}
	return string(value), nil
}

func readProcessSecurityStatus(result *preflightResult) {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "CapEff":
			result.CapabilityEffective = value
			capabilities, parseErr := strconv.ParseUint(value, 16, 64)
			if parseErr == nil {
				result.CapSysPtrace = hasCapability(capabilities, 19)
				result.CapSysAdmin = hasCapability(capabilities, 21)
				result.CapPerfmon = hasCapability(capabilities, 38)
			}
		case "Seccomp":
			result.SeccompMode, _ = strconv.Atoi(value)
		}
	}
}

func hasCapability(capabilities uint64, capability uint) bool {
	return capabilities&(uint64(1)<<capability) != 0
}

func runCommand(ctx context.Context, path string, args []string, stdout io.Writer) (string, error) {
	stderr := &limitedBuffer{limit: maxCommandStderr}
	cmd := exec.Command(path, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return stderr.String(), err
	}
	leaveResourceLimit(ctx, cmd.Process.Pid)
	preferChildOOMKill(ctx, cmd.Process.Pid)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return stderr.String(), err
	case <-ctx.Done():
		stopErr := stopProcessGroup(cmd.Process.Pid, done, processInterruptGrace)
		ctxErr := contextCause(ctx)
		if stopErr != nil {
			return stderr.String(), fmt.Errorf("%w; stop process group: %v", ctxErr, stopErr)
		}
		return stderr.String(), ctxErr
	}
}

func runScriptCommand(
	ctx context.Context,
	path string,
	args []string,
	consume func(io.Reader) (profileStats, error),
) (profileStats, string, error) {
	stderr := &limitedBuffer{limit: maxCommandStderr}
	cmd := exec.Command(path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return profileStats{}, stderr.String(), err
	}
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return profileStats{}, stderr.String(), err
	}
	leaveResourceLimit(ctx, cmd.Process.Pid)
	preferChildOOMKill(ctx, cmd.Process.Pid)

	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGINT)
			timer := time.NewTimer(processInterruptGrace)
			defer timer.Stop()
			select {
			case <-watchDone:
			case <-timer.C:
				_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-watchDone:
		}
	}()

	stats, parseErr := consume(stdout)
	if parseErr != nil && ctx.Err() == nil {
		_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	waitErr := cmd.Wait()
	close(watchDone)

	if ctx.Err() != nil {
		return stats, stderr.String(), contextCause(ctx)
	}
	if parseErr != nil {
		return stats, stderr.String(), parseErr
	}
	return stats, stderr.String(), waitErr
}

func stopProcessGroup(pid int, done <-chan error, grace time.Duration) error {
	interruptErr := signalProcessGroup(pid, syscall.SIGINT)
	if interruptErr != nil {
		_ = signalProcessGroup(pid, syscall.SIGKILL)
		<-done
		return interruptErr
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		if err := signalProcessGroup(pid, syscall.SIGKILL); err != nil {
			return err
		}
		<-done
		return nil
	}
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid process id %d", pid)
	}
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func errWithOutput(err error, output string) error {
	if strings.TrimSpace(output) == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLen, nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return originalLen, nil
	}
	_, _ = b.buf.Write(p)
	return originalLen, nil
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "\n...[truncated]"
	}
	return b.buf.String()
}
