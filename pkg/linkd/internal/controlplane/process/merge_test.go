// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

func TestMergeSnapshotFailureIsNotReportedAsDependencyOutage(t *testing.T) {
	if got := mergeStepCode(fmt.Errorf("invalid merge snapshot: %w", store.ErrInvalidArgument)); got != "invalid_state" {
		t.Fatal(got)
	}
}

type mergeTestLocker struct {
	acquired, released atomic.Int64
	releaseErr         error
}

type mergeBusyLocker struct{ mergeTestLocker }

func (*mergeBusyLocker) Acquire(context.Context, string) (scheduler.Lease, error) {
	return scheduler.Lease{}, scheduler.ErrLockBusy
}

func TestMergePageDefersBusyWindowWithoutReportingDependencyFailure(t *testing.T) {
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeBusyLocker{}}
	failed, err := runMergeJobs(t.Context(), runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), run: func(context.Context) error { t.Fatal("busy window executed"); return nil }}})
	if err != nil || failed != 0 {
		t.Fatal("ordinary contention counted as failure", err)
	}
}

func (l *mergeTestLocker) Acquire(ctx context.Context, _ string) (scheduler.Lease, error) {
	if ctx.Err() != nil {
		return scheduler.Lease{}, ctx.Err()
	}
	l.acquired.Add(1)
	return scheduler.Lease{}, nil
}

func (l *mergeTestLocker) Renew(context.Context, scheduler.Lease) error { return nil }

func (l *mergeTestLocker) Release(context.Context, scheduler.Lease) error {
	l.released.Add(1)
	return l.releaseErr
}

func TestMergeLoopsShareBoundedSlotsAndCancelQueuedWork(t *testing.T) {
	locker := &mergeTestLocker{}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker}
	entered := make(chan struct{}, 4)
	leave := make(chan struct{})
	var running, maxRunning atomic.Int64
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			err := runner.run(t.Context(), mergeJob{tenant: "tenant", window: fmt.Sprintf("%064x", i), run: func(ctx context.Context) error {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
					t.Error("step deadline missing")
				}
				n := running.Add(1)
				defer running.Add(-1)
				for {
					old := maxRunning.Load()
					if n <= old || maxRunning.CompareAndSwap(old, n) {
						break
					}
				}
				entered <- struct{}{}
				<-leave
				return nil
			}})
			if err != nil {
				t.Error(err)
			}
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("jobs did not enter")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.run(ctx, mergeJob{tenant: "tenant", window: strings.Repeat("f", 64), run: func(context.Context) error { t.Error("cancelled job ran"); return nil }}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(leave)
	wg.Wait()
	if maxRunning.Load() != 4 || locker.acquired.Load() != 4 || locker.released.Load() != 4 || len(runner.slots) != 0 {
		t.Fatal("shared budget or lease cleanup failed")
	}
}

func TestMergeJobFailureReleasesResourcesAndNeverHidesUncertainRelease(t *testing.T) {
	failure := errors.New("release uncertain")
	locker := &mergeTestLocker{releaseErr: failure}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker}
	called := false
	err := runner.run(t.Context(), mergeJob{tenant: "tenant", window: strings.Repeat("a", 64), run: func(context.Context) error { called = true; return nil }})
	if !called || !errors.Is(err, failure) || len(runner.slots) != 0 {
		t.Fatal("partial success hidden", err)
	}
	if _, err := runMergeJobs(t.Context(), runner, make([]mergeJob, 17)); err == nil || locker.acquired.Load() != 1 {
		t.Fatal("oversized page executed")
	}
}

func TestMergeLoopCancellationUpdatesTaskStatus(t *testing.T) {
	registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	load := func(context.Context) ([]mergeJob, bool, error) {
		return []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), run: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}}, false, nil
	}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{}}
	done := make(chan error, 1)
	go func() {
		done <- runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeJudge, time.Second, load, runner, logger, registry, nil)
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("merge loop did not stop")
	}
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "merge-judge" && (task.Execution.Running || task.Execution.Canceled != 1 || task.Execution.Failed != 0) {
			t.Fatal(task)
		}
	}
}

func TestRedisMergeRunnerSerializesWindowAcrossControlInstances(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	cfg := scheduler.DefaultConfig()
	cfg.LockKeyPrefix = fmt.Sprintf("linkd-merge-control-test:%d:%d", os.Getpid(), time.Now().UnixNano())
	locker, err := scheduler.NewRedisLocker(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	window := strings.Repeat("a", 64)
	key, err := domain.MergeDecisionID("tenant", window)
	if err != nil {
		t.Fatal(err)
	}
	held, err := locker.Acquire(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Del(ctx, cfg.LockKeyPrefix+":"+key).Err()
	})
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker}
	called := 0
	job := mergeJob{tenant: "tenant", window: window, run: func(context.Context) error { called++; return nil }}
	if err := runner.run(t.Context(), job); !errors.Is(err, scheduler.ErrLockBusy) || called != 0 {
		t.Fatal("parallel window operation entered", err)
	}
	other := job
	other.tenant = "another"
	if err := runner.run(t.Context(), other); err != nil || called != 1 {
		t.Fatal("tenant lease collision", err)
	}
	if err := locker.Release(t.Context(), held); err != nil {
		t.Fatal(err)
	}
	if err := runner.run(t.Context(), job); err != nil || called != 2 {
		t.Fatal("released window did not continue", err)
	}
	if n, err := client.Exists(t.Context(), cfg.LockKeyPrefix+":"+key).Result(); err != nil || n != 0 {
		t.Fatal("window lease leaked", err)
	}
}

func TestMergeBusyStepDoesNotHideLeaseReleaseFailure(t *testing.T) {
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{releaseErr: errors.New("lease lost")}}
	failed, err := runMergeJobs(t.Context(), runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), run: func(context.Context) error { return scheduler.ErrLockBusy }}})
	if err == nil || failed != 1 {
		t.Fatal("mixed busy/release failure swallowed", failed, err)
	}
}
