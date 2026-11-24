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
	failed, progressed, _, err := runMergeJobs(t.Context(), runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), run: func(context.Context) error { t.Fatal("busy window executed"); return nil }}})
	if err != nil || failed != 0 || progressed {
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
	if _, _, _, err := runMergeJobs(t.Context(), runner, make([]mergeJob, 129)); err == nil || locker.acquired.Load() != 1 {
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
	failed, progressed, _, err := runMergeJobs(t.Context(), runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), run: func(context.Context) error { return scheduler.ErrLockBusy }}})
	if err == nil || failed != 1 || progressed {
		t.Fatal("mixed busy/release failure swallowed", failed, err)
	}
}

func TestMergeRelationBatchesRotatePageAndReacquireLease(t *testing.T) {
	locker := &mergeTestLocker{}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker}
	var initial atomic.Int32
	var calls [16]atomic.Int32
	jobs := make([]mergeJob, len(calls))
	for i := range jobs {
		jobs[i] = mergeJob{tenant: "tenant", window: fmt.Sprintf("%064x", i), batch: func(ctx context.Context) (bool, error) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
				t.Error("batch exceeded per-lease deadline")
			}
			n := calls[i].Add(1)
			if n == 1 {
				initial.Add(1)
			} else if int(initial.Load()) != len(jobs) {
				t.Error("continued one relation before other page members ran")
			}
			return n < 3, nil
		}}
	}
	failed, progressed, _, err := runMergeJobs(t.Context(), runner, jobs)
	if failed != 0 || err != nil || !progressed || locker.acquired.Load() != 48 || locker.released.Load() != 48 || len(runner.slots) != 0 {
		t.Fatal("batch continuation did not reacquire/release per batch", failed, err, locker.acquired.Load(), locker.released.Load())
	}
	for i := range calls {
		if calls[i].Load() != 3 {
			t.Fatal("page relation not drained", i, calls[i].Load())
		}
	}
}

func TestMergeRelationBatchesStopIdleBusyErrorAndUncertainRelease(t *testing.T) {
	for _, test := range []struct {
		name           string
		locker         scheduler.Locker
		more           bool
		err            error
		failed, called int
	}{
		{name: "active without progress", locker: &mergeTestLocker{}, called: 1},
		{name: "lock busy", locker: &mergeBusyLocker{}},
		{name: "operation busy", locker: &mergeTestLocker{}, more: true, err: scheduler.ErrLockBusy, called: 1},
		{name: "operation failure", locker: &mergeTestLocker{}, more: true, err: errors.New("source unavailable"), failed: 1, called: 1},
		{name: "release uncertainty", locker: &mergeTestLocker{releaseErr: errors.New("release failed")}, more: true, failed: 1, called: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: test.locker}
			called := 0
			jobs := []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(context.Context) (bool, error) {
				called++
				return test.more, test.err
			}}}
			failed, progressed, _, err := runMergeJobs(t.Context(), runner, jobs)
			if progressed || failed != test.failed || (err != nil) != (test.failed > 0) || called != test.called || len(runner.slots) != 0 {
				t.Fatal("non-continuable relation was repeated", failed, err, called)
			}
		})
	}
}

func TestMergeRelationBatchFailureCountedOnceAcrossOtherContinuations(t *testing.T) {
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{}}
	var failedCalls, progressCalls atomic.Int32
	jobs := []mergeJob{
		{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(context.Context) (bool, error) {
			failedCalls.Add(1)
			return true, errors.New("dependency failed")
		}},
		{tenant: "tenant", window: strings.Repeat("b", 64), batch: func(context.Context) (bool, error) { return progressCalls.Add(1) < 5, nil }},
	}
	failed, progressed, _, err := runMergeJobs(t.Context(), runner, jobs)
	if failed != 1 || err == nil || !progressed || failedCalls.Load() != 1 || progressCalls.Load() != 5 {
		t.Fatal("failed relation repeated or stopped unrelated progress", failed, err, failedCalls.Load(), progressCalls.Load())
	}
}

func TestMergeRelationBatchPageBudgetAndCancellationStopContinuation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
			}
			defer cancel()
			locker := &mergeTestLocker{}
			runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker}
			calls := 0
			failed, progressed, _, err := runMergeJobs(ctx, runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(call context.Context) (bool, error) {
				calls++
				if deadline {
					<-call.Done()
				} else if calls == 3 {
					cancel()
				}
				return true, nil
			}}})
			want := context.Canceled
			wantCalls := 3
			if deadline {
				want, wantCalls = context.DeadlineExceeded, 1
			}
			if progressed == deadline || !errors.Is(err, want) || failed != 1 || calls != wantCalls || locker.acquired.Load() != int64(wantCalls) || locker.released.Load() != int64(wantCalls) || len(runner.slots) != 0 {
				t.Fatal("page budget/cancel did not stop continuation", failed, err, calls, locker.acquired.Load(), locker.released.Load())
			}
		})
	}
}

func TestMergePageDeadlineContinuesOnlyAfterSuccessfulProgress(t *testing.T) {
	for _, test := range []struct {
		name     string
		progress bool
		failure  error
		want     bool
	}{
		{name: "progress then page deadline", progress: true, want: true},
		{name: "deadline without progress"},
		{name: "ordinary failure and progress", progress: true, failure: errors.New("storage unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			locker := &mergeTestLocker{}
			runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: locker}
			calls := 0
			jobs := []mergeJob{}
			if test.failure != nil {
				jobs = append(jobs, mergeJob{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(context.Context) (bool, error) {
					return false, test.failure
				}})
			}
			jobs = append(jobs, mergeJob{tenant: "tenant", window: strings.Repeat("b", 64), batch: func(call context.Context) (bool, error) {
				calls++
				if test.progress && calls == 1 {
					return true, nil
				}
				<-call.Done()
				return false, call.Err()
			}})
			failed, progressed, _, err := runMergeJobs(page, runner, jobs)
			if !errors.Is(err, context.DeadlineExceeded) || progressed != test.progress {
				t.Fatal("page result lost progress or deadline", failed, progressed, err)
			}
			wantFailed := 1
			if test.failure != nil {
				wantFailed++
				if !errors.Is(err, test.failure) {
					t.Fatal("page deadline hid ordinary failure", err)
				}
			}
			if failed != wantFailed || mergePageCanContinue(t.Context(), page, progressed, err) != test.want {
				t.Fatal("wrong page continuation decision", failed, progressed, err)
			}
			if locker.acquired.Load() != locker.released.Load() || len(runner.slots) != 0 {
				t.Fatal("page continuation retained a lease or slot")
			}
		})
	}
}

func TestMergePageContinuationKeepsMixedFailuresAndParentCancellation(t *testing.T) {
	page, cancelPage := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancelPage()
	<-page.Done()
	for _, err := range []error{
		errors.New("dependency unavailable"),
		errors.Join(context.DeadlineExceeded, errors.New("lease release uncertain")),
		errors.Join(context.DeadlineExceeded, scheduler.ErrLockBusy),
		errors.Join(context.DeadlineExceeded, context.DeadlineExceeded),
		context.Canceled,
		nil,
	} {
		if mergePageCanContinue(t.Context(), page, true, err) {
			t.Fatal("page continued after non-budget failure", err)
		}
	}
	wrapped := fmt.Errorf("batch: %w", errors.Join(context.DeadlineExceeded, nil))
	if !mergePageCanContinue(t.Context(), page, true, wrapped) {
		t.Fatal("wrapped page deadline was not recognized")
	}
	if mergePageCanContinue(t.Context(), t.Context(), true, context.DeadlineExceeded) {
		t.Fatal("single step deadline bypassed page backoff")
	}
	parent, cancelParent := context.WithCancel(t.Context())
	cancelParent()
	if mergePageCanContinue(parent, page, true, wrapped) {
		t.Fatal("cancelled parent continued scanning")
	}
}

type mergeSecondReleaseDeadlineLocker struct{ mergeTestLocker }

func (l *mergeSecondReleaseDeadlineLocker) Release(context.Context, scheduler.Lease) error {
	if l.released.Add(1) == 2 {
		return context.DeadlineExceeded
	}
	return nil
}

func TestMergePageDoesNotContinueAfterOperationAndWindowReleaseTimeout(t *testing.T) {
	page, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	locker := &mergeSecondReleaseDeadlineLocker{}
	runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: locker}
	calls := 0
	failed, progressed, _, err := runMergeJobs(page, runner, []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(ctx context.Context) (bool, error) {
		calls++
		if calls == 1 {
			return true, nil
		}
		<-ctx.Done()
		return false, ctx.Err()
	}}})
	if failed != 1 || !progressed || !errors.Is(err, context.DeadlineExceeded) || calls != 2 || locker.released.Load() != 2 {
		t.Fatal("operation/release timeout fixture failed", failed, progressed, calls, err)
	}
	if mergePageCanContinue(t.Context(), page, progressed, err) {
		t.Fatal("window release uncertainty was treated as a pure page deadline", err)
	}
}

func TestMergePageReservedTailContinuesOnlyWithProgressAndNoFailure(t *testing.T) {
	page, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if !mergePageCanContinue(t.Context(), page, true, nil) {
		t.Fatal("reserved checkpoint tail restarted the 30-second wait")
	}
	if mergePageCanContinue(t.Context(), page, false, nil) {
		t.Fatal("idle page repeatedly scanned its work collection")
	}
	for _, err := range []error{errors.New("dependency unavailable"), context.DeadlineExceeded, scheduler.ErrLockBusy} {
		if mergePageCanContinue(t.Context(), page, true, err) {
			t.Fatal("reserved tail hid a real operation failure", err)
		}
	}
	long, cancelLong := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelLong()
	if mergePageCanContinue(t.Context(), long, true, nil) || mergePageCanContinue(t.Context(), t.Context(), true, nil) {
		t.Fatal("ordinary progress forced an unnecessary full rescan")
	}
	parent, cancelParent := context.WithCancel(t.Context())
	cancelParent()
	if mergePageCanContinue(parent, page, true, nil) {
		t.Fatal("reserved tail ignored parent cancellation")
	}
}

func TestMergePageReservedTailLeavesWorkForNextScanWithoutBusyLoop(t *testing.T) {
	runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: &mergeTestLocker{}}
	checkpoint, called := 0, 0
	job := mergeJob{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(ctx context.Context) (bool, error) {
		called++
		if checkpoint == 0 {
			checkpoint++
			return true, nil
		}
		deadline, _ := ctx.Deadline()
		if time.Until(deadline) <= time.Second {
			return false, nil // 已有进度，页尾只保留未完成work，不再启动成员。
		}
		checkpoint++
		return false, nil // 下一轮完成后应恢复普通扫描周期。
	}}
	page, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	failed, progressed, _, err := runMergeJobs(page, runner, []mergeJob{job})
	if failed != 0 || err != nil || checkpoint != 1 || called != 2 || !mergePageCanContinue(t.Context(), page, progressed, err) {
		t.Fatal("page tail lost pending work or selected failure backoff", checkpoint, called, err)
	}
	nextPage, cancelNext := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelNext()
	failed, progressed, _, err = runMergeJobs(nextPage, runner, []mergeJob{job})
	if failed != 0 || err != nil || checkpoint != 2 || called != 3 || mergePageCanContinue(t.Context(), nextPage, progressed, err) {
		t.Fatal("next scan failed to finish or kept rescheduling completed work", checkpoint, called, err)
	}
}

func TestMergeRelationLoopAdvancesNextPageAndDoesNotRescanIdleRows(t *testing.T) {
	registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var loaded, firstCalls, idleCalls atomic.Int32
	secondPage := make(chan struct{})
	load := func(call context.Context) ([]mergeJob, bool, error) {
		if deadline, ok := call.Deadline(); !ok || time.Until(deadline) > 45*time.Second {
			t.Error("page budget missing")
		}
		if loaded.Add(1) == 1 {
			return []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(context.Context) (bool, error) { return firstCalls.Add(1) < 3, nil }}}, true, nil
		}
		return []mergeJob{{tenant: "tenant", window: strings.Repeat("b", 64), batch: func(context.Context) (bool, error) {
			idleCalls.Add(1)
			close(secondPage)
			return false, nil
		}}}, false, nil
	}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{}}
	done := make(chan error, 1)
	go func() {
		done <- runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeRelations, time.Hour, load, runner, logger, registry, nil)
	}()
	select {
	case <-secondPage:
	case <-time.After(time.Second):
		t.Fatal("progress prevented advancing the next scan page")
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle relation loop did not stop")
	}
	if loaded.Load() != 2 || firstCalls.Load() != 3 || idleCalls.Load() != 1 {
		t.Fatal("active row made full scan run at high frequency", loaded.Load(), firstCalls.Load(), idleCalls.Load())
	}
}

func TestMergeRelationLoopBacksOffAfterSlowFailedPage(t *testing.T) {
	registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const interval = 100 * time.Millisecond
	var loaded atomic.Int32
	var finished time.Time
	rescan := make(chan time.Duration, 1)
	load := func(context.Context) ([]mergeJob, bool, error) {
		if loaded.Add(1) > 1 {
			rescan <- time.Since(finished)
			cancel()
			return nil, false, nil
		}
		return []mergeJob{{tenant: "tenant", window: strings.Repeat("a", 64), batch: func(context.Context) (bool, error) {
			time.Sleep(150 * time.Millisecond) // 旧 ticker 已到期，不应绕过本轮失败后的退避。
			finished = time.Now()
			return true, errors.New("dependency unavailable")
		}}}, true, nil
	}
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{}}
	done := make(chan error, 1)
	go func() {
		done <- runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeRelations, interval, load, runner, logger, registry, nil)
	}()
	select {
	case elapsed := <-rescan:
		if elapsed < interval {
			t.Fatal("expired page ticker skipped error backoff", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed page did not resume after its backoff")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
