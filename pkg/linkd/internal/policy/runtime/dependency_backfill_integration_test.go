// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// 使用真实 Redis，并通过屏障固定“读旧快照、实时清理、迟到回填”的顺序，避免依赖调度时机。
func TestRedisDependencyLateBackfillUsesCurrentAlert(t *testing.T) {
	for _, batched := range []bool{false, true} {
		reader := "single_read"
		if batched {
			reader = "batch_read"
		}
		for _, transition := range []string{"closed", "closed_without_fallback", "merge_wait", "merge_released", "canceled"} {
			t.Run(reader+"/"+transition, func(t *testing.T) {
				repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
				s.Index = realSuppressionState(t)
				processor := dependencyProcessor(t, repo, clock, s, state, action)
				var older domain.Alert
				if transition != "closed_without_fallback" {
					older = admittedMainFixture(t, repo, clock, "older", s)
				}
				clock.at = clock.at.Add(time.Minute)
				latest := admittedMainFixture(t, repo, clock, "latest", s)
				r := dependencyRelease(t, "custom_shield")
				compiled, err := policy.Compile(r.Kind, r.Spec)
				if err != nil {
					t.Fatal(err)
				}
				f := policy.FrozenPolicy{Release: r, Compiled: compiled}
				ref := s.dependencyIndexRef(f)
				if err := s.Index.MarkDependencyIndexReady(t.Context(), "tenant", ref); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				var workers sync.WaitGroup
				t.Cleanup(func() { cancel(); workers.Wait() })
				snapshotRead, resume := make(chan struct{}), make(chan struct{})
				wait := func(ctx context.Context) error {
					close(snapshotRead)
					select {
					case <-resume:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				backfill := *s
				if batched {
					backfill.CurrentAlerts = func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
						batch := store.AlertBatch{}
						for _, id := range ids {
							row, err := repo.GetAlert(ctx, tenant, id)
							if err != nil {
								return batch, err
							}
							batch.Alerts = append(batch.Alerts, row)
						}
						return batch, wait(ctx)
					}
				} else {
					backfill.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
						row, err := repo.GetAlert(ctx, tenant, id)
						if err == nil && id == latest.AlertID {
							err = wait(ctx)
						}
						return row, err
					}
				}
				done := make(chan error, 1)
				workers.Go(func() { done <- backfill.RebuildDependencyIndex(ctx, "tenant", clock.at, nil) })
				select {
				case <-snapshotRead:
				case <-ctx.Done():
					t.Fatal("backfill did not reach snapshot barrier", ctx.Err())
				}
				if transition == "canceled" {
					cancel()
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Fatal("blocked backfill did not exit on cancellation", err)
					}
					return
				}

				current, err := repo.GetAlert(ctx, "tenant", latest.AlertID)
				if err != nil {
					t.Fatal(err)
				}
				next := current.Alert.Clone()
				window := fmt.Sprintf("%064d", 1)
				closed := transition == "closed" || transition == "closed_without_fallback"
				if !closed {
					next.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: window, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: fmt.Sprintf("%064d", 2)}, GroupKey: fmt.Sprintf("%064d", 3), MemberEventID: next.TriggerEventID, Severity: next.Severity, Groups: []int{0}, StartedAt: next.UpdateAt, Deadline: next.UpdateAt.Add(time.Minute)}}}
				}
				next.UpdateAt = clock.at.Add(time.Second)
				if closed {
					next.Status, next.EndType = domain.AlertStatusClosed, domain.AlertEndTypeSystem
					next.EndAt = &next.UpdateAt
				}
				current, err = repo.CompareAndSetAlert(ctx, "tenant", latest.AlertID, current.Version, next)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.ObserveDependencyAlert(ctx, current.Alert, nil); err != nil {
					t.Fatal(err)
				}
				if row, _, err := s.Index.TopDependencyCandidate(ctx, "tenant", ref); err != nil || row.AlertID != older.AlertID {
					t.Fatal("clear did not expose the remaining eligible main", row, err)
				}
				if transition == "merge_released" {
					current, err = processor.ReleaseMergeWindow(ctx, "tenant", latest.AlertID, window)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.ObserveDependencyAlert(ctx, current.Alert, nil); err != nil {
						t.Fatal(err)
					}
				}
				close(resume)
				if err := <-done; err != nil {
					t.Fatal(err)
				}

				row, _, err := s.Index.TopDependencyCandidate(ctx, "tenant", ref)
				wantRevision := latest.Revision
				wantMain := older.AlertID
				if transition == "merge_released" {
					wantRevision, wantMain = current.Alert.Revision, latest.AlertID
				}
				if err != nil || row.AlertID != latest.AlertID || row.Revision != wantRevision {
					t.Fatalf("unexpected index after late backfill: %+v revision=%d err=%v", row, wantRevision, err)
				}
				// 索引只是定位入口：已失效候选必须由现有当前读取剔除，不能错选，也不能漏掉仍有效的旧主。
				s.Candidates = failedCandidates{err: errors.New("selection must not scan active alerts")}
				main, found, err := s.admittedMain(ctx, f, s.Loader.Targets, clock.at, nil)
				if err != nil || found != (wantMain != "") || main.alert.AlertID != wantMain {
					t.Fatalf("selected stale main or lost valid main: %+v found=%t err=%v", main.alert, found, err)
				}
				row, _, err = s.Index.TopDependencyCandidate(ctx, "tenant", ref)
				if err != nil || row.AlertID != wantMain {
					t.Fatal("selection did not repair the stale candidate", row, err)
				}
			})
		}
	}
}

func TestRedisDependencySelectionDoesNotBypassCurrentReadFailure(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Index = realSuppressionState(t)
	a := admittedMainFixture(t, repo, clock, "main", s)
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f := policy.FrozenPolicy{Release: r, Compiled: compiled}
	ref := s.dependencyIndexRef(f)
	if err := s.Index.MarkDependencyIndexReady(t.Context(), "tenant", ref); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("current alert unavailable")
	s.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) { return store.StoredAlert{}, failure }
	if _, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil); found || !errors.Is(err, failure) {
		t.Fatalf("read failure admitted a cached candidate: found=%t err=%v", found, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, found, err := s.admittedMain(ctx, f, s.Loader.Targets, clock.at, nil); found || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection admitted a candidate: found=%t err=%v", found, err)
	}
	s.CurrentAlert = repo.GetAlert
	if main, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil); err != nil || !found || main.alert.AlertID != a.AlertID {
		t.Fatal("failure removed the valid candidate", found, err)
	}
	s.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, store.ErrNotFound
	}
	if _, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil); err != nil || found {
		t.Fatal("missing current alert remained eligible", found, err)
	}
	if row, ready, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || !ready || row.AlertID != "" {
		t.Fatal("missing current alert was not removed from candidates", row, ready, err)
	}
}
