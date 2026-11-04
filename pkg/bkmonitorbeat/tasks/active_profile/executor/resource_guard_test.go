// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

type fakeResourceProbe struct {
	mu              sync.Mutex
	memoryAvailable uint64
	memoryLimit     uint64
	diskUsage       uint64
	memoryErr       error
	diskErr         error
	memoryCalls     int
	diskCalls       int
}

func (p *fakeResourceProbe) MemoryAvailableBytes() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.memoryCalls++
	return p.memoryAvailable, p.memoryErr
}

func (p *fakeResourceProbe) MemoryLimitBytes() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.memoryLimit
}

func (p *fakeResourceProbe) DirectoryUsageBytes(string) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.diskCalls++
	return p.diskUsage, p.diskErr
}

func (p *fakeResourceProbe) set(memoryAvailable, diskUsage uint64) {
	p.mu.Lock()
	p.memoryAvailable = memoryAvailable
	p.diskUsage = diskUsage
	p.mu.Unlock()
}

func testGuardPolicy(t *testing.T) ResourceGuardPolicy {
	t.Helper()
	return ResourceGuardPolicy{
		RunDir:             t.TempDir(),
		MinFreeMemoryBytes: 1024,
		MaxDiskUsageBytes:  4096,
	}
}

func TestResourceGuardDisabledDoesNotProbe(t *testing.T) {
	probe := &fakeResourceProbe{}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()

	unregister, err := guard.Register("disabled", time.Now(), ResourceGuardPolicy{}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	unregister()
	if probe.memoryCalls != 0 || probe.diskCalls != 0 {
		t.Fatalf("disabled guard probed resources: memory=%d disk=%d", probe.memoryCalls, probe.diskCalls)
	}
}

// TestResourceGuardThresholdsAreIndependent 覆盖两项保护互不牵连：只配置一项时
// 另一项不参与判断，也不会因为对方缺省而整体失效。
func TestResourceGuardThresholdsAreIndependent(t *testing.T) {
	t.Run("only disk quota", func(t *testing.T) {
		probe := &fakeResourceProbe{memoryAvailable: 1, diskUsage: 0}
		guard := newResourceGuard(probe, time.Hour)
		defer guard.Close()
		unregister, err := guard.Register("disk-only", time.Now(), ResourceGuardPolicy{
			RunDir:            t.TempDir(),
			MaxDiskUsageBytes: 4096,
		}, func(error) {})
		if err != nil {
			t.Fatalf("disk-only admission must not read host memory: %v", err)
		}
		defer unregister()
		if probe.memoryCalls != 0 {
			t.Fatalf("disk-only policy read host memory %d times", probe.memoryCalls)
		}
	})

	t.Run("only memory watermark", func(t *testing.T) {
		probe := &fakeResourceProbe{memoryAvailable: 4096, diskUsage: 1 << 30}
		guard := newResourceGuard(probe, time.Hour)
		defer guard.Close()
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		unregister, err := guard.Register("memory-only", time.Now(), ResourceGuardPolicy{
			RunDir:             t.TempDir(),
			MinFreeMemoryBytes: 1024,
		}, cancel)
		if err != nil {
			t.Fatal(err)
		}
		defer unregister()
		guard.checkRunning()
		select {
		case <-ctx.Done():
			t.Fatalf("memory-only policy must ignore run directory usage: %v", context.Cause(ctx))
		default:
		}
	})
}

func TestResourceGuardAdmissionRejectsEqualLowWatermark(t *testing.T) {
	probe := &fakeResourceProbe{memoryAvailable: 512}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()

	policy := testGuardPolicy(t)
	policy.MinFreeMemoryBytes = 512
	_, err := guard.Register("memory-low", time.Now(), policy, func(error) {})
	var guardErr *ResourceGuardError
	if !errors.As(err, &guardErr) || guardErr.Resource != resourceMemory || guardErr.Kind != guardKindPressure {
		t.Fatalf("Register() error = %#v", err)
	}
}

// TestResourceGuardCancelsOnDiskQuota 覆盖新的磁盘语义：限制的是本次采集临时文件
// 的占用，而不是宿主剩余空间。
func TestResourceGuardCancelsOnDiskQuota(t *testing.T) {
	probe := &fakeResourceProbe{memoryAvailable: 1 << 30, diskUsage: 0}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	unregister, err := guard.Register("quota", time.Now(), testGuardPolicy(t), cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()

	guard.checkRunning()
	select {
	case <-ctx.Done():
		t.Fatal("run below its disk quota must keep going")
	default:
	}

	probe.set(1<<30, 4096)
	guard.checkRunning()
	assertGuardCause(t, ctx, resourceDisk, guardKindQuota)
}

func TestResourceGuardCancelsNewestRunThenRechecks(t *testing.T) {
	probe := &fakeResourceProbe{memoryAvailable: 4096}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	policy := testGuardPolicy(t)

	oldCtx, oldCancel := context.WithCancelCause(context.Background())
	defer oldCancel(nil)
	unregisterOld, err := guard.Register("old", time.Now().Add(-time.Minute), policy, oldCancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregisterOld()
	newCtx, newCancel := context.WithCancelCause(context.Background())
	defer newCancel(nil)
	unregisterNew, err := guard.Register("new", time.Now(), policy, newCancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregisterNew()

	probe.set(512, 0)
	guard.checkRunning()
	assertGuardCause(t, newCtx, resourceMemory, guardKindPressure)
	select {
	case <-oldCtx.Done():
		t.Fatal("one check must not cancel more than the newest run")
	default:
	}

	guard.checkRunning()
	assertGuardCause(t, oldCtx, resourceMemory, guardKindPressure)
}

func TestResourceGuardUnregisterPreventsCancellation(t *testing.T) {
	probe := &fakeResourceProbe{memoryAvailable: 4096}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	unregister, err := guard.Register("finished", time.Now(), testGuardPolicy(t), cancel)
	if err != nil {
		t.Fatal(err)
	}
	unregister()
	unregister()
	probe.set(512, 1<<30)
	guard.checkRunning()
	select {
	case <-ctx.Done():
		t.Fatalf("unregistered run was cancelled: %v", context.Cause(ctx))
	default:
	}
}

func TestResourceGuardCancelAndUnregisterAreAtomic(t *testing.T) {
	for i := 0; i < 100; i++ {
		probe := &fakeResourceProbe{memoryAvailable: 4096}
		guard := newResourceGuard(probe, time.Hour)
		ctx, cancel := context.WithCancelCause(context.Background())
		unregister, err := guard.Register("race", time.Now(), testGuardPolicy(t), cancel)
		if err != nil {
			t.Fatal(err)
		}
		cancelled := make(chan bool, 1)
		unregistered := make(chan struct{})
		cause := newResourcePressureError(resourceMemory, 512, 1024)
		go func() {
			cancelled <- guard.cancelRun("race", cause)
		}()
		go func() {
			unregister()
			close(unregistered)
		}()
		wasCancelled := <-cancelled
		<-unregistered
		if wasCancelled {
			assertGuardCause(t, ctx, resourceMemory, guardKindPressure)
		} else {
			select {
			case <-ctx.Done():
				t.Fatalf("unregistered run received a late cause: %v", context.Cause(ctx))
			default:
			}
		}
		cancel(nil)
		guard.Close()
	}
}

func TestMegabytesToBytes(t *testing.T) {
	if got, want := megabytesToBytes(1024), uint64(1024*1024*1024); got != want {
		t.Fatalf("megabytesToBytes(1024) = %d, want %d", got, want)
	}
	if got := megabytesToBytes(0); got != 0 {
		t.Fatalf("megabytesToBytes(0) = %d, want 0", got)
	}
	if got := megabytesToBytes(-5); got != 0 {
		t.Fatalf("megabytesToBytes(-5) = %d, want 0", got)
	}
}

// TestDirectoryUsageProbeMeasuresRunDir 覆盖真实的运行目录统计实现。
func TestDirectoryUsageProbeMeasuresRunDir(t *testing.T) {
	runDir := t.TempDir()
	nested := filepath.Join(runDir, "nested")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "perf.data"), make([]byte, 300), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "profile.pprof"), make([]byte, 200), 0o600); err != nil {
		t.Fatal(err)
	}

	used, err := directoryUsageProbe{}.DirectoryUsageBytes(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if used != 500 {
		t.Fatalf("run directory usage = %d, want 500", used)
	}
}

func assertGuardCause(t *testing.T, ctx context.Context, resource, kind string) {
	t.Helper()
	select {
	case <-ctx.Done():
		guardErr := resourceGuardError(context.Cause(ctx))
		if guardErr == nil || guardErr.Resource != resource || guardErr.Kind != kind {
			t.Fatalf("context cause = %#v, want %s/%s", context.Cause(ctx), resource, kind)
		}
	default:
		t.Fatalf("context was not cancelled with %s/%s", resource, kind)
	}
}

// 上层按触发类别归类 error_type：内存低于阈值匹配 ErrMemoryLimit，临时文件超限匹配 ErrDiskLimit。
func TestResourceGuardErrorMatchesLimitKind(t *testing.T) {
	pressure := newResourcePressureError(resourceMemory, 1, 2)
	quota := newResourceQuotaError(resourceDisk, 2, 1)
	checkFailed := newResourceCheckError(resourceMemory, errors.New("read /proc/meminfo"))
	if !errors.Is(pressure, ErrMemoryLimit) || errors.Is(pressure, ErrDiskLimit) {
		t.Errorf("memory pressure must match only ErrMemoryLimit")
	}
	if !errors.Is(quota, ErrDiskLimit) || errors.Is(quota, ErrMemoryLimit) {
		t.Errorf("disk quota must match only ErrDiskLimit")
	}
	if errors.Is(checkFailed, ErrMemoryLimit) || errors.Is(checkFailed, ErrDiskLimit) {
		t.Errorf("a failed resource check is not a limit")
	}
}

// 开关打开时内存低水位不低于 Beat 的保留余量 max(64MB, 上限的 20%)，min_mem_level 更高时以它为准。
func TestResourceGuardProtectBeatRaisesWatermark(t *testing.T) {
	const mb = bytesPerMB
	tests := []struct {
		name       string
		limit      uint64
		configured uint64
		want       uint64
	}{
		{name: "no cgroup limit keeps the floor", limit: 0, configured: 0, want: 64 * mb},
		{name: "small limit keeps the floor", limit: 100 * mb, configured: 0, want: 64 * mb},
		{name: "large limit reserves a fifth", limit: 1000 * mb, configured: 100 * mb, want: 200 * mb},
		{name: "higher configured watermark wins", limit: 1000 * mb, configured: 300 * mb, want: 300 * mb},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &fakeResourceProbe{memoryAvailable: 4096 * mb, memoryLimit: test.limit}
			guard := newResourceGuard(probe, time.Hour)
			defer guard.Close()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			unregister, err := guard.Register("reserve", time.Now(), ResourceGuardPolicy{
				MinFreeMemoryBytes: test.configured,
				ProtectBeat:        true,
			}, cancel)
			if err != nil {
				t.Fatal(err)
			}
			defer unregister()

			probe.set(test.want+1, 0)
			guard.checkRunning()
			if ctx.Err() != nil {
				t.Fatalf("run above the watermark was cancelled: %v", context.Cause(ctx))
			}
			probe.set(test.want, 0)
			guard.checkRunning()
			assertGuardCause(t, ctx, resourceMemory, guardKindPressure)
		})
	}
}

func TestResourceGuardAdmissionRequiresStartHeadroom(t *testing.T) {
	const mb = bytesPerMB
	policy := ResourceGuardPolicy{ProtectBeat: true, StartHeadroomBytes: perfStartHeadroomBytes}
	probe := &fakeResourceProbe{memoryAvailable: 64*mb + perfStartHeadroomBytes}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()

	_, err := guard.Register("tight", time.Now(), policy, func(error) {})
	var guardErr *ResourceGuardError
	if !errors.As(err, &guardErr) || guardErr.Kind != guardKindPressure ||
		guardErr.Threshold != 64*mb+perfStartHeadroomBytes {
		t.Fatalf("Register() error = %#v, want a pressure error at reserve + start headroom", err)
	}

	probe.set(64*mb+perfStartHeadroomBytes+1, 0)
	unregister, err := guard.Register("enough", time.Now(), policy, func(error) {})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	unregister()
}

// checkMemory 与定时复查走同一条取消路径：触发时取消本次执行并返回同一个原因。
func TestResourceGuardCheckMemoryCancelsRun(t *testing.T) {
	const mb = bytesPerMB
	probe := &fakeResourceProbe{memoryAvailable: 1024 * mb}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	unregister, err := guard.Register("convert", time.Now(), ResourceGuardPolicy{ProtectBeat: true}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()

	if err := guard.checkMemory("convert"); err != nil {
		t.Fatalf("checkMemory() error = %v", err)
	}
	probe.set(64*mb, 0)
	err = guard.checkMemory("convert")
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("checkMemory() error = %v, want ErrMemoryLimit", err)
	}
	assertGuardCause(t, ctx, resourceMemory, guardKindPressure)
	if err := guard.checkMemory("unregistered"); err != nil {
		t.Fatalf("checkMemory() on an unregistered run = %v", err)
	}
}

// 进程内并发的一次性分配按账本累计：前一份占住的估算没有释放时，后一份按剩余部分判断。
func TestResourceGuardReserveMemoryAccountsConcurrentRuns(t *testing.T) {
	const mb = bytesPerMB
	probe := &fakeResourceProbe{memoryAvailable: 1000 * mb}
	guard := newResourceGuard(probe, time.Hour)
	defer guard.Close()
	for _, id := range []string{"first", "second"} {
		unregister, err := guard.Register(id, time.Now(), ResourceGuardPolicy{ProtectBeat: true}, func(error) {})
		if err != nil {
			t.Fatal(err)
		}
		defer unregister()
	}

	releaseFirst, err := guard.reserveMemory("first", 500*mb)
	if err != nil {
		t.Fatal(err)
	}
	if headroom, protected, err := guard.memoryHeadroom("second"); err != nil || !protected ||
		headroom != 1000*mb-64*mb-500*mb {
		t.Fatalf("memoryHeadroom() = %d, %t, %v", headroom, protected, err)
	}
	if _, err := guard.reserveMemory("second", 500*mb); !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("reserveMemory() error = %v, want ErrMemoryLimit while the first estimate is held", err)
	}
	releaseFirst()
	releaseFirst()
	releaseSecond, err := guard.reserveMemory("second", 500*mb)
	if err != nil {
		t.Fatalf("reserveMemory() after release error = %v", err)
	}
	releaseSecond()
	if guard.reservedMemory != 0 {
		t.Fatalf("reserved memory = %d after releasing everything", guard.reservedMemory)
	}
}

func TestResourceGuardPolicyFromConfigProtectsBeat(t *testing.T) {
	conf := configs.NewActiveProfileTaskConfig()
	conf.CollectionMethod = configs.ActiveProfileMethodPerf
	if policy := resourceGuardPolicyFromConfig(conf, "/run"); policy.enabled() {
		t.Fatalf("switch off must disable the guard: %+v", policy)
	}
	conf.ResourceGuardConfig.AutoStopOnResourcePressure = true
	policy := resourceGuardPolicyFromConfig(conf, "/run")
	if !policy.ProtectBeat || !policy.memoryEnabled() || policy.StartHeadroomBytes != perfStartHeadroomBytes {
		t.Fatalf("perf policy = %+v", policy)
	}
	conf.CollectionMethod = configs.ActiveProfileMethodGolang
	if policy := resourceGuardPolicyFromConfig(conf, "/run"); policy.StartHeadroomBytes != goStartHeadroomBytes {
		t.Fatalf("golang policy = %+v", policy)
	}
}
