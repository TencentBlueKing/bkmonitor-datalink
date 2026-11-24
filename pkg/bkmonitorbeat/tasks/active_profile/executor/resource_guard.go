// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package executor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const defaultResourceGuardCheckInterval = time.Second

const (
	resourceMemory = "memory"
	resourceDisk   = "disk"

	guardKindPressure    = "pressure"
	guardKindQuota       = "quota"
	guardKindCheckFailed = "check_failed"
	bytesPerMB           = uint64(1024 * 1024)
)

// Beat 自身的内存保留余量取 max(64MB, 内存上限的 20%)，没有 cgroup 上限时取 64MB。余量要
// 容纳两次复查之间的增长和 pprof 序列化的瞬时分配，实测后者约为转换内存的 7%。
const (
	beatMemoryReserveFloor   = 64 * bytesPerMB
	beatMemoryReserveDivisor = 5
)

// 启动采集时在保留余量之上还要求的可用内存。perf 路径要同时容纳 perf record、perf script
// 与符号化工具的启动开销，Go 路径只有一次 HTTP 拉取和解析的固定开销。
const (
	perfStartHeadroomBytes = 128 * bytesPerMB
	goStartHeadroomBytes   = 32 * bytesPerMB
)

// ResourceGuardPolicy 是平台下发的资源保护策略。两项保护相互独立：内存是宿主
// 可用内存低水位，磁盘是本次采集临时文件的占用配额。任一项阈值为 0 时只关闭该项，
// 不会连带关掉另一项。
type ResourceGuardPolicy struct {
	RunDir             string
	MinFreeMemoryBytes uint64
	MaxDiskUsageBytes  uint64
	// ProtectBeat 对应"资源不足时停止"打开：不论 min_mem_level 配多少，内存低水位都不低于
	// Beat 自身的保留余量，保证采集被叫停而不是 Beat 被 OOM。
	ProtectBeat bool
	// StartHeadroomBytes 是启动时在内存低水位之上还要求的可用内存。
	StartHeadroomBytes uint64
}

func (p ResourceGuardPolicy) memoryEnabled() bool { return p.MinFreeMemoryBytes > 0 || p.ProtectBeat }

func (p ResourceGuardPolicy) diskEnabled() bool {
	return p.MaxDiskUsageBytes > 0 && p.RunDir != ""
}

func (p ResourceGuardPolicy) enabled() bool { return p.memoryEnabled() || p.diskEnabled() }

// ResourceGuardError 是资源保护触发时的 context 取消原因。它只用于生成 finished
// Event 的 message 与本机日志，不再定义执行阶段或结果码。
type ResourceGuardError struct {
	Resource  string
	Kind      string
	Observed  uint64
	Threshold uint64
	Cause     error
}

func (e *ResourceGuardError) Error() string {
	if e == nil {
		return "resource guard failed"
	}
	switch e.Kind {
	case guardKindCheckFailed:
		return fmt.Sprintf("resource guard cannot inspect %s: %v", e.Resource, e.Cause)
	case guardKindQuota:
		return fmt.Sprintf(
			"profiling temporary files reached the configured %d MB limit (used %d MB)",
			e.Threshold/bytesPerMB,
			e.Observed/bytesPerMB,
		)
	default:
		return fmt.Sprintf(
			"host available %s %d MB reached the minimum %d MB",
			e.Resource,
			e.Observed/bytesPerMB,
			e.Threshold/bytesPerMB,
		)
	}
}

// Is 让上层按触发类别匹配：可用内存低于阈值为 ErrMemoryLimit，临时文件超限为 ErrDiskLimit。
func (e *ResourceGuardError) Is(target error) bool {
	if e == nil {
		return false
	}
	return (target == ErrMemoryLimit && e.Kind == guardKindPressure) || (target == ErrDiskLimit && e.Kind == guardKindQuota)
}

func (e *ResourceGuardError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type resourceProbe interface {
	MemoryAvailableBytes() (uint64, error)
	// MemoryLimitBytes 返回 Beat 所在 cgroup 链路上最小的有限内存上限，没有上限时为 0。
	MemoryLimitBytes() uint64
	DirectoryUsageBytes(path string) (uint64, error)
}

// directoryUsageProbe 统计运行目录的实际占用，供两个平台的 probe 共用。
type directoryUsageProbe struct{}

func (directoryUsageProbe) DirectoryUsageBytes(path string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 运行目录中的临时文件随时可能被采集器删除，单个条目消失不算探测失败。
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		if size := info.Size(); size > 0 {
			total = saturatingAdd(total, uint64(size))
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure run directory %s: %w", path, err)
	}
	return total, nil
}

type guardedRun struct {
	id         string
	startedAt  time.Time
	policy     ResourceGuardPolicy
	cancel     context.CancelCauseFunc
	cancelling bool
	sequence   uint64
}

// ResourceGuard owns one lightweight monitoring goroutine and only knows
// about runs registered in the current Beat/CLI process. It never scans or
// terminates arbitrary host processes.
type ResourceGuard struct {
	probe    resourceProbe
	interval time.Duration

	mu        sync.Mutex
	runs      map[string]*guardedRun
	nextRun   uint64
	started   bool
	closed    bool
	startOnce sync.Once
	closeOnce sync.Once
	stop      chan struct{}
	done      chan struct{}

	// reservedMemory 是进程内各次执行已占住、但还未体现在可用内存里的估算内存，受 mu 保护。
	reservedMemory uint64
}

// NewResourceGuard creates the process-local guard used by daemon and the
// lightweight CLI execution path.
func NewResourceGuard() *ResourceGuard {
	return newResourceGuard(newHostResourceProbe(), defaultResourceGuardCheckInterval)
}

func newResourceGuard(probe resourceProbe, interval time.Duration) *ResourceGuard {
	if interval <= 0 {
		interval = defaultResourceGuardCheckInterval
	}
	return &ResourceGuard{
		probe:    probe,
		interval: interval,
		runs:     make(map[string]*guardedRun),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

var defaultResourceGuard = NewResourceGuard()

// resourceGuardPolicyFromConfig 把平台下发的 MB 阈值转换为字节。开关关闭时整套
// 保护关闭，Beat 不写死非零默认值。
func resourceGuardPolicyFromConfig(conf *configs.ActiveProfileTaskConfig, runDir string) ResourceGuardPolicy {
	if !conf.ResourceGuardConfig.AutoStopOnResourcePressure {
		return ResourceGuardPolicy{}
	}
	startHeadroom := perfStartHeadroomBytes
	if conf.CollectionMethod == configs.ActiveProfileMethodGolang {
		startHeadroom = goStartHeadroomBytes
	}
	return ResourceGuardPolicy{
		RunDir:             runDir,
		MinFreeMemoryBytes: megabytesToBytes(conf.ResourceGuardConfig.MinMemLevel),
		MaxDiskUsageBytes:  megabytesToBytes(conf.ResourceGuardConfig.MaxDiskUsage),
		ProtectBeat:        true,
		StartHeadroomBytes: startHeadroom,
	}
}

func saturatingAdd(left, right uint64) uint64 {
	if right > math.MaxUint64-left {
		return math.MaxUint64
	}
	return left + right
}

func megabytesToBytes(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	if uint64(value) > math.MaxUint64/bytesPerMB {
		return math.MaxUint64
	}
	return uint64(value) * bytesPerMB
}

// Register performs a fail-closed admission check and then registers a run for
// periodic monitoring. A disabled policy is a no-op. The returned function is
// idempotent and must be called when the run finishes.
func (g *ResourceGuard) Register(
	runID string,
	startedAt time.Time,
	policy ResourceGuardPolicy,
	cancel context.CancelCauseFunc,
) (func(), error) {
	if !policy.enabled() {
		return func() {}, nil
	}
	if g == nil || g.probe == nil {
		return func() {}, newResourceCheckError(resourceMemory, errors.New("resource guard is unavailable"))
	}
	if runID == "" {
		return func() {}, newResourceCheckError(resourceMemory, errors.New("run id is empty"))
	}
	if cancel == nil {
		return func() {}, newResourceCheckError(resourceMemory, errors.New("cancel callback is nil"))
	}
	g.mu.Lock()
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return func() {}, newResourceCheckError(resourceMemory, errors.New("resource guard is closed"))
	}

	policy = g.withBeatReserve(policy)
	if err := g.checkAdmission(policy); err != nil {
		return func() {}, err
	}

	run := &guardedRun{
		id:        runID,
		startedAt: startedAt,
		policy:    policy,
		cancel:    cancel,
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return func() {}, newResourceCheckError(resourceMemory, errors.New("resource guard is closed"))
	}
	if _, exists := g.runs[runID]; exists {
		g.mu.Unlock()
		return func() {}, newResourceCheckError(resourceMemory, fmt.Errorf("run %s is already registered", runID))
	}
	g.nextRun++
	run.sequence = g.nextRun
	g.runs[runID] = run
	g.started = true
	g.mu.Unlock()
	g.startOnce.Do(func() { go g.loop() })

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.runs, runID)
			g.mu.Unlock()
		})
	}, nil
}

// withBeatReserve 在 ProtectBeat 时把内存低水位抬到 Beat 自身的保留余量。
func (g *ResourceGuard) withBeatReserve(policy ResourceGuardPolicy) ResourceGuardPolicy {
	if !policy.ProtectBeat {
		return policy
	}
	reserve := beatMemoryReserveFloor
	if proportional := g.probe.MemoryLimitBytes() / beatMemoryReserveDivisor; proportional > reserve {
		reserve = proportional
	}
	if policy.MinFreeMemoryBytes < reserve {
		policy.MinFreeMemoryBytes = reserve
	}
	return policy
}

// checkAdmission 只检查宿主可用内存。磁盘配额限制的是本次采集自身的产物占用，
// 启动前必然为 0，因此没有启动前判据。
func (g *ResourceGuard) checkAdmission(policy ResourceGuardPolicy) error {
	if !policy.memoryEnabled() {
		return nil
	}
	memoryAvailable, err := g.probe.MemoryAvailableBytes()
	if err != nil {
		return newResourceCheckError(resourceMemory, err)
	}
	threshold := saturatingAdd(policy.MinFreeMemoryBytes, policy.StartHeadroomBytes)
	if memoryAvailable <= threshold {
		return newResourcePressureError(resourceMemory, memoryAvailable, threshold)
	}
	return nil
}

// memoryHeadroom 返回已登记的执行在内存低水位与进程内已占住的估算之上还能使用的内存。
// 未登记或未开启内存保护时 protected 为 false。
func (g *ResourceGuard) memoryHeadroom(runID string) (headroom uint64, protected bool, err error) {
	g.mu.Lock()
	run, ok := g.runs[runID]
	reserved := g.reservedMemory
	g.mu.Unlock()
	if !ok || !run.policy.memoryEnabled() {
		return 0, false, nil
	}
	available, err := g.probe.MemoryAvailableBytes()
	if err != nil {
		return 0, true, newResourceCheckError(resourceMemory, err)
	}
	floor := saturatingAdd(run.policy.MinFreeMemoryBytes, reserved)
	if available <= floor {
		return 0, true, newResourcePressureError(resourceMemory, available, floor)
	}
	return available - floor, true, nil
}

// checkMemory 供进程内的长耗时步骤在两次定时复查之间同步检查内存。触发时与定时复查一样
// 取消本次执行，并返回取消原因。
func (g *ResourceGuard) checkMemory(runID string) error {
	_, _, err := g.memoryHeadroom(runID)
	if err != nil && g.cancelRun(runID, err) {
		logger.Warnf("[active_profile] run %s stopped by resource guard: %v", runID, err)
	}
	return err
}

// reserveMemory 在一次性分配之前占住估算的内存：可用内存扣掉低水位与其他执行已占住的
// 部分后放不下时拒绝。返回的释放函数幂等。未开启内存保护时不占用。
func (g *ResourceGuard) reserveMemory(runID string, bytes uint64) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	run, ok := g.runs[runID]
	if !ok || !run.policy.memoryEnabled() {
		return func() {}, nil
	}
	available, err := g.probe.MemoryAvailableBytes()
	if err != nil {
		return func() {}, newResourceCheckError(resourceMemory, err)
	}
	needed := saturatingAdd(saturatingAdd(run.policy.MinFreeMemoryBytes, g.reservedMemory), bytes)
	if available <= needed {
		return func() {}, newResourcePressureError(resourceMemory, available, needed)
	}
	g.reservedMemory += bytes
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.reservedMemory -= bytes
			g.mu.Unlock()
		})
	}, nil
}

func (g *ResourceGuard) loop() {
	ticker := time.NewTicker(g.interval)
	defer func() {
		ticker.Stop()
		close(g.done)
	}()
	for {
		select {
		case <-ticker.C:
			g.checkRunning()
		case <-g.stop:
			return
		}
	}
}

// checkRunning cancels at most one run per pass. The newest protected run is
// selected first; the next tick re-reads host state before considering another
// run, so cancellation cannot cascade from one stale sample.
func (g *ResourceGuard) checkRunning() {
	runs := g.runningSnapshot()
	if len(runs) == 0 {
		return
	}

	memoryAvailable, memoryErr := g.hostMemory(runs)
	for _, run := range runs {
		guardErr := g.evaluateRun(run, memoryAvailable, memoryErr)
		if guardErr == nil || !g.cancelRun(run.id, guardErr) {
			continue
		}
		logger.Warnf("[active_profile] run %s stopped by resource guard: %v", run.id, guardErr)
		return
	}
}

// hostMemory 只在确有 run 需要内存保护时读取宿主内存。
func (g *ResourceGuard) hostMemory(runs []*guardedRun) (uint64, error) {
	for _, run := range runs {
		if run.policy.memoryEnabled() {
			return g.probe.MemoryAvailableBytes()
		}
	}
	return 0, nil
}

func (g *ResourceGuard) evaluateRun(run *guardedRun, memoryAvailable uint64, memoryErr error) *ResourceGuardError {
	if run.policy.memoryEnabled() {
		if memoryErr != nil {
			return newResourceCheckError(resourceMemory, memoryErr)
		}
		if memoryAvailable <= run.policy.MinFreeMemoryBytes {
			return newResourcePressureError(resourceMemory, memoryAvailable, run.policy.MinFreeMemoryBytes)
		}
	}
	if !run.policy.diskEnabled() {
		return nil
	}
	used, err := g.probe.DirectoryUsageBytes(run.policy.RunDir)
	if err != nil {
		return newResourceCheckError(resourceDisk, err)
	}
	if used >= run.policy.MaxDiskUsageBytes {
		return newResourceQuotaError(resourceDisk, used, run.policy.MaxDiskUsageBytes)
	}
	return nil
}

func (g *ResourceGuard) runningSnapshot() []*guardedRun {
	g.mu.Lock()
	defer g.mu.Unlock()
	runs := make([]*guardedRun, 0, len(g.runs))
	for _, run := range g.runs {
		if run.cancelling {
			continue
		}
		snapshot := *run
		runs = append(runs, &snapshot)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].startedAt.Equal(runs[j].startedAt) {
			return runs[i].sequence > runs[j].sequence
		}
		return runs[i].startedAt.After(runs[j].startedAt)
	})
	return runs
}

// cancelRun invokes the context cancel function while holding the registry
// lock. This is intentionally synchronized with Unregister: either the guard
// publishes the cancellation cause before executePerf reads it, or the run is
// already unregistered and cannot be cancelled after it completed.
func (g *ResourceGuard) cancelRun(runID string, cause error) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	run, ok := g.runs[runID]
	if !ok || run.cancelling {
		return false
	}
	run.cancelling = true
	run.cancel(cause)
	return true
}

// Close is intended for tests and the lightweight CLI lifecycle. The daemon's
// process-wide default guard lives until process exit.
func (g *ResourceGuard) Close() {
	if g == nil {
		return
	}
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		started := g.started
		g.mu.Unlock()
		close(g.stop)
		// The goroutine only starts after the first successful registration.
		if started {
			<-g.done
		}
	})
}

func newResourcePressureError(resource string, observed, threshold uint64) *ResourceGuardError {
	return &ResourceGuardError{
		Resource:  resource,
		Kind:      guardKindPressure,
		Observed:  observed,
		Threshold: threshold,
	}
}

func newResourceQuotaError(resource string, observed, threshold uint64) *ResourceGuardError {
	return &ResourceGuardError{
		Resource:  resource,
		Kind:      guardKindQuota,
		Observed:  observed,
		Threshold: threshold,
	}
}

func newResourceCheckError(resource string, cause error) *ResourceGuardError {
	return &ResourceGuardError{
		Resource: resource,
		Kind:     guardKindCheckFailed,
		Cause:    cause,
	}
}

func resourceGuardError(err error) *ResourceGuardError {
	var guardErr *ResourceGuardError
	if errors.As(err, &guardErr) {
		return guardErr
	}
	return nil
}
