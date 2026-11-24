// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	"linkd/internal/telemetry"
	"testing/synctest"
)

func TestMergeLoopFinishesUnstartedWindowsBeforeLoadingNextPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		scan := &mergeWaitingScan{due: &scheduledMergeDue{}}
		page := store.MergeWorkPage{Next: store.MergeWorkCursor{TenantID: "tenant", AlertID: "127"}}
		for i := range store.MergeWorkBatchSize {
			page.Items = append(page.Items, store.MergeWorkItem{
				WindowID: fmt.Sprintf("%064x", i+1),
				Alert:    store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant", AlertID: fmt.Sprintf("%03d", i)}},
			})
		}
		var calls [store.MergeWorkBatchSize]atomic.Int32
		loaded := 0
		started := time.Now()
		load := func(ctx context.Context) ([]mergeJob, bool, error) {
			loaded++
			if loaded > 1 {
				cancel()
				return nil, false, nil
			}
			round := scan.round
			items, err := scan.selectWork(ctx, page, time.Now())
			if err != nil {
				return nil, false, err
			}
			jobs, err := mergeWaitingJobs(items, round, func(ctx context.Context, batch []store.MergeWorkItem, _ time.Time) error {
				var i int
				if _, err := fmt.Sscanf(batch[0].Alert.Alert.AlertID, "%d", &i); err != nil {
					return err
				}
				calls[i].Add(1)
				if i < 40 {
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			})
			return jobs, true, err
		}
		locker := &mergeTestLocker{}
		runner := &mergeJobRunner{slots: make(chan struct{}, 8), locker: locker}
		registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if err := runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeJudge, 30*time.Second, load, runner, logger, registry, nil); err != nil {
			t.Fatal(err)
		}
		for i := range calls {
			if got := calls[i].Load(); got != 1 {
				t.Fatalf("window %d executed %d times before loading the next page", i, got)
			}
		}
		if loaded != 2 || time.Since(started) != 75*time.Second {
			t.Fatal("unfinished page was reloaded or failure backoff changed", loaded, time.Since(started))
		}
		if locker.acquired.Load() != locker.released.Load() || len(runner.slots) != 0 {
			t.Fatal("page continuation retained a lease or slot")
		}
	})
}

func TestMergeLoopResumesWindowWaitingForSharedSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: &mergeTestLocker{}}
		runner.slots <- struct{}{}
		timer := time.AfterFunc(46*time.Second, func() { <-runner.slots })
		defer timer.Stop()
		loaded, called := 0, 0
		load := func(context.Context) ([]mergeJob, bool, error) {
			loaded++
			return []mergeJob{{tenant: "tenant", window: fmt.Sprintf("%064x", 1), run: func(context.Context) error {
				called++
				cancel()
				return nil
			}}}, false, nil
		}
		registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if err := runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeJudge, 30*time.Second, load, runner, logger, registry, nil); err != nil {
			t.Fatal(err)
		}
		if loaded != 1 || called != 1 || len(runner.slots) != 0 {
			t.Fatal("window waiting for a shared slot was lost or reloaded", loaded, called)
		}
	})
}

func TestMergeLoopCancellationDoesNotResumeUnstartedWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		timer := time.AfterFunc(time.Second, cancel)
		defer timer.Stop()
		loaded := 0
		load := func(context.Context) ([]mergeJob, bool, error) {
			loaded++
			return []mergeJob{
				{tenant: "tenant", window: fmt.Sprintf("%064x", 1), run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
				{tenant: "tenant", window: fmt.Sprintf("%064x", 2), run: func(context.Context) error { t.Error("canceled window executed"); return nil }},
			}, true, nil
		}
		locker := &mergeTestLocker{}
		runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: locker}
		registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if err := runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeJudge, 30*time.Second, load, runner, logger, registry, nil); err != nil {
			t.Fatal(err)
		}
		if loaded != 1 || locker.acquired.Load() != 1 || locker.released.Load() != 1 || len(runner.slots) != 0 {
			t.Fatal("cancellation resumed work or retained a lease")
		}
	})
}

func TestMergePageRetainsOnlyUnstartedWindowsAndPreservesErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("storage unavailable")
		locker := &mergeTestLocker{}
		runner := &mergeJobRunner{slots: make(chan struct{}, 1), locker: locker}
		var calls [4]int
		jobs := []mergeJob{}
		for i := range calls {
			jobs = append(jobs, mergeJob{tenant: "tenant", window: fmt.Sprintf("%064x", i+1), run: func(ctx context.Context) error {
				calls[i]++
				switch i {
				case 0:
					return failure
				case 1:
					return scheduler.ErrLockBusy
				case 2:
					<-ctx.Done()
					return ctx.Err()
				default:
					return nil
				}
			}})
		}
		page, cancel := context.WithTimeout(t.Context(), time.Second)
		failed, progressed, pending, err := runMergeJobs(page, runner, jobs)
		cancel()
		if failed != 2 || progressed || len(pending) != 1 || !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("page lost failures or retained attempted windows", failed, progressed, len(pending), err)
		}
		failed, _, pending, err = runMergeJobs(t.Context(), runner, pending)
		if failed != 0 || err != nil || len(pending) != 0 || calls != [4]int{1, 1, 1, 1} {
			t.Fatal("continuation repeated attempted windows or skipped the tail", calls, err)
		}
		if locker.acquired.Load() != locker.released.Load() || len(runner.slots) != 0 {
			t.Fatal("continuation retained a lease or slot")
		}
	})
}
