// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type runnerExecutor struct {
	produce func(context.Context, string, string, string) (StoredTask, error)
	deliver func(context.Context, string, string) (StoredTask, error)
}

func (e runnerExecutor) Produce(c context.Context, t, a, k string) (StoredTask, error) {
	return e.produce(c, t, a, k)
}

func (e runnerExecutor) Deliver(c context.Context, t, id string) (StoredTask, error) {
	return e.deliver(c, t, id)
}

type runnerWork func(context.Context, store.ProjectionWorkCursor, int) (store.ProjectionWorkPage, error)

func (f runnerWork) ListProjectionWork(c context.Context, a store.ProjectionWorkCursor, n int) (store.ProjectionWorkPage, error) {
	return f(c, a, n)
}

func runnerRows(t *testing.T, count int) *memory.Repository {
	t.Helper()
	repo := memory.New()
	for i := range count {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%03d", i), "opening", fmt.Sprint(i), "warning")
		a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}}}
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestRunnerProductionPagesRemainBoundedAndPassFailedRows(t *testing.T) {
	repo := runnerRows(t, 17)
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	var active, peak, calls atomic.Int64
	executor := runnerExecutor{produce: func(ctx context.Context, tenant, id, target string) (StoredTask, error) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for {
			old := peak.Load()
			if old >= n || peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return StoredTask{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
		if id == "alert-000" {
			return StoredTask{}, errors.New("private database")
		}
		if id == "alert-001" {
			return StoredTask{}, ErrCapacity
		}
		row, err := repo.GetAlert(ctx, tenant, id)
		if err != nil {
			return StoredTask{}, err
		}
		task, err := NewTask(row.Alert, target, time.Now())
		if err != nil {
			return StoredTask{}, err
		}
		return tasks.Put(ctx, task, "")
	}}
	r, err := NewRunner(repo, tasks, executor, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	next, result := r.producePage(t.Context(), store.ProjectionWorkCursor{})
	if result.Scanned != 16 || result.Failed != 1 || result.CapacityDeferred != 1 || result.Deferred != 1 || result.Advanced != 14 || next.AlertID != "alert-015" || peak.Load() > 4 {
		t.Fatal(next, result, peak.Load())
	}
	end, result := r.producePage(t.Context(), next)
	if result.Scanned != 1 || result.Failed != 0 || end.TenantID != "" || calls.Load() != 17 {
		t.Fatal("row failure starved later work", end, result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, result = r.producePage(ctx, store.ProjectionWorkCursor{})
	if result.ErrorCode == "" || calls.Load() != 17 {
		t.Fatal("cancel did not stop work", result)
	}
}

func TestRunnerSkipsFutureDueAndRejectsInvalidScanWithoutEffects(t *testing.T) {
	repo := runnerRows(t, 1)
	row, err := repo.GetAlert(t.Context(), "tenant", "alert-000")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := NewTask(row.Alert, "kac", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	if _, err := tasks.Put(t.Context(), task, ""); err != nil {
		t.Fatal(err)
	}
	calls := 0
	executor := runnerExecutor{produce: func(context.Context, string, string, string) (StoredTask, error) { calls++; return StoredTask{}, nil }, deliver: func(context.Context, string, string) (StoredTask, error) { calls++; return StoredTask{}, nil }}
	r, _ := NewRunner(repo, tasks, executor, nil, func() time.Time { return now })
	after, result := r.deliverPage(t.Context(), "")
	if after != "" || result.Deferred != 1 || calls != 0 || !result.ObservedAt.Equal(now) || result.OldestObservedAge != 0 {
		t.Fatal(result, calls)
	}
	r.work = runnerWork(func(context.Context, store.ProjectionWorkCursor, int) (store.ProjectionWorkPage, error) {
		return store.ProjectionWorkPage{Items: []store.ProjectionWorkItem{{Alert: row, TargetID: "missing"}}}, nil
	})
	_, result = r.producePage(t.Context(), store.ProjectionWorkCursor{})
	if result.ErrorCode != "invalid_page" || calls != 0 || !result.ObservedAt.IsZero() {
		t.Fatal("invalid work executed", result)
	}
}

func TestRunnerPageObservationUsesBusinessUpdateAndFirstTaskTime(t *testing.T) {
	repo := runnerRows(t, 1)
	row, err := repo.GetAlert(t.Context(), "tenant", "alert-000")
	if err != nil {
		t.Fatal(err)
	}
	at := row.Alert.UpdateAt.Add(2 * time.Minute)
	task, err := NewTask(row.Alert, "kac", at.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	saved, err := tasks.Put(t.Context(), task, "")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(repo, tasks, runnerExecutor{
		produce: func(context.Context, string, string, string) (StoredTask, error) { return saved, nil },
		deliver: func(context.Context, string, string) (StoredTask, error) { return saved, ErrBusy },
	}, nil, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	_, produced := runner.producePage(t.Context(), store.ProjectionWorkCursor{})
	_, delivered := runner.deliverPage(t.Context(), "")
	if !produced.ObservedAt.Equal(at) || produced.OldestObservedAge != 2*time.Minute || produced.Advanced != 1 {
		t.Fatal(produced)
	}
	if !delivered.ObservedAt.Equal(at) || delivered.OldestObservedAge != 30*time.Second || delivered.Deferred != 1 {
		t.Fatal(delivered)
	}
}

type runnerObserver struct {
	started chan RunnerPhase
	mu      sync.Mutex
	results []RoundResult
	running map[RunnerPhase]bool
}

func (o *runnerObserver) SetRunning(_ context.Context, p RunnerPhase, active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.running == nil {
		o.running = map[RunnerPhase]bool{}
	}
	o.running[p] = active
}

func (o *runnerObserver) RoundStarted(_ context.Context, p RunnerPhase) {
	select {
	case o.started <- p:
	default:
	}
}

func (o *runnerObserver) RoundFinished(_ context.Context, _ RunnerPhase, r RoundResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.results = append(o.results, r)
}

func TestRunnerCancellationJoinsLoopsAndCanRestart(t *testing.T) {
	repo := runnerRows(t, 0)
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	observer := &runnerObserver{started: make(chan RunnerPhase, 4)}
	r, err := NewRunner(repo, tasks, runnerExecutor{}, observer, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		phases := map[RunnerPhase]bool{}
		for len(phases) < 2 {
			select {
			case phase := <-observer.started:
				phases[phase] = true
			case <-time.After(5 * time.Second):
				t.Fatal("loops not started")
			}
		}
		if err := r.Run(t.Context()); !errors.Is(err, ErrBusy) {
			t.Fatal("duplicate runner allowed", err)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runner did not join cancellation")
		}
		observer.mu.Lock()
		if len(observer.running) != 2 || observer.running[PhaseProduce] || observer.running[PhaseDeliver] {
			t.Error("runner observers remained active after cancellation")
		}
		observer.mu.Unlock()
	}
}

type slowAdmissionAlerts struct{ Alerts }

func (a slowAdmissionAlerts) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	select {
	case <-ctx.Done():
		return store.StoredAlert{}, ctx.Err()
	case <-time.After(2 * time.Millisecond):
	}
	return a.Alerts.GetAlertCurrent(ctx, tenant, id)
}

func TestProducerRoundDoesNotContendWithItsOwnTenantAdmission(t *testing.T) {
	repo := runnerRows(t, 16)
	alerts := &ackRepository{Repository: repo}
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	service, err := New(tasks, slowAdmissionAlerts{alerts}, &testResolver{}, &testSender{}, &testTargetLocker{held: map[string]bool{}}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(repo, tasks, service, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, result := runner.producePage(t.Context(), store.ProjectionWorkCursor{})
	if result.Advanced != 16 || result.Deferred != 0 || result.Failed != 0 {
		t.Fatal("single producer competed with its own tenant lock", result)
	}
}

func TestProducerDeadlineOnlyAdvancesAttemptedPrefix(t *testing.T) {
	repo := runnerRows(t, 16)
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	executor := runnerExecutor{produce: func(ctx context.Context, tenant, id, target string) (StoredTask, error) {
		calls++
		if calls == 3 {
			cancel()
			return StoredTask{}, ctx.Err()
		}
		row, err := repo.GetAlert(ctx, tenant, id)
		if err != nil {
			return StoredTask{}, err
		}
		task, err := NewTask(row.Alert, target, time.Now())
		if err != nil {
			return StoredTask{}, err
		}
		return tasks.Put(ctx, task, "")
	}}
	r, _ := NewRunner(repo, tasks, executor, nil, time.Now)
	next, result := r.producePage(ctx, store.ProjectionWorkCursor{})
	if calls != 3 || next.AlertID != "alert-002" || result.Advanced != 2 {
		t.Fatal("unstarted work skipped", calls, next, result)
	}
	last, result := r.producePage(t.Context(), next)
	if last.TenantID != "" || result.Advanced != 13 || result.Failed != 0 || calls != 16 {
		t.Fatal("later rows not resumed", last, result, calls)
	}
}

func TestRunnerDoesNotHideLeaseFailureBehindBusyResult(t *testing.T) {
	result, _ := runProjectionItems(t.Context(), 1, nil, func(int) (StoredTask, error) {
		return StoredTask{}, errors.Join(ErrBusy, errors.New("release uncertain"))
	}, false)
	if result.Failed != 1 || result.Deferred != 0 {
		t.Fatal("lease failure treated as ordinary delay", result)
	}
}
