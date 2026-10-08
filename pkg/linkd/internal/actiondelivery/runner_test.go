// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type intentFunc func(context.Context, string, string) (store.StoredAlert, error)

func (f intentFunc) FinishActionDelivery(c context.Context, t, id string) (store.StoredAlert, error) {
	return f(c, t, id)
}

type deliverFunc func(context.Context, string, string) (StoredTask, error)

func (f deliverFunc) Deliver(c context.Context, t, id string) (StoredTask, error) { return f(c, t, id) }

type intentWork func(context.Context, store.ActionWorkCursor, int) (store.ActionWorkPage, error)

func (f intentWork) ListActionWork(c context.Context, a store.ActionWorkCursor, n int) (store.ActionWorkPage, error) {
	return f(c, a, n)
}

func intentRows(t *testing.T, n int) *memory.Repository {
	t.Helper()
	repo := memory.New()
	for i := range n {
		a := actionAlert("tenant", 1)
		a.AlertID = fmt.Sprintf("alert-%03d", i)
		a.Fingerprint = a.AlertID
		v := a.Projection.Targets["kac"]
		v.ActionEnabled = true
		a.Projection.Targets["kac"] = v
		var err error
		a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func mustRunner(t *testing.T, work store.ActionWorkStore, tasks WorkReader, p IntentProducer, d Deliverer) *Runner {
	t.Helper()
	r, err := NewRunner(work, tasks, p, d, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func unusedDelivery(context.Context, string, string) (StoredTask, error) {
	return StoredTask{}, errors.New("unexpected delivery")
}

func unusedIntent(context.Context, string, string) (store.StoredAlert, error) {
	return store.StoredAlert{}, errors.New("unexpected intent")
}

func TestActionRunnerPagesPassFailedRowsAndKeepFailedScanCursor(t *testing.T) {
	repo := intentRows(t, 17)
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	var calls atomic.Int64
	p := intentFunc(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		calls.Add(1)
		if id == "alert-000" {
			return store.StoredAlert{}, errors.New("private driver error")
		}
		if id == "alert-001" {
			return store.StoredAlert{}, ErrCapacity
		}
		row, err := repo.GetAlert(ctx, tenant, id)
		if err != nil {
			return row, err
		}
		next := row.Alert.Clone()
		next.ActionPending = nil
		return repo.CompareAndSetAlert(ctx, tenant, id, row.Version, next)
	})
	r := mustRunner(t, repo, tasks, p, deliverFunc(unusedDelivery))
	next, result := r.enqueuePage(t.Context(), store.ActionWorkCursor{})
	if result.Scanned != 16 || result.Visited != 16 || result.Outcomes[OutcomeEnqueued] != 14 || result.Outcomes[OutcomeFailed] != 1 || result.Outcomes[OutcomeCapacity] != 1 || next.AlertID != "alert-015" || len(result.Failures) != 1 || result.Failures[0].Code != "execution_failed" {
		t.Fatal(next, result)
	}
	end, result := r.enqueuePage(t.Context(), next)
	if result.Outcomes[OutcomeEnqueued] != 1 || end != (store.ActionWorkCursor{}) || calls.Load() != 17 {
		t.Fatal("failure starved later page", end, result)
	}
	r.work = intentWork(func(context.Context, store.ActionWorkCursor, int) (store.ActionWorkPage, error) {
		return store.ActionWorkPage{}, errors.New("private scan")
	})
	again, result := r.enqueuePage(t.Context(), next)
	if again != next || result.ErrorCode != "scan_failed" {
		t.Fatal("scan failure moved cursor", again, result)
	}
	row, _ := repo.GetAlert(t.Context(), "tenant", "alert-000")
	r.work = intentWork(func(context.Context, store.ActionWorkCursor, int) (store.ActionWorkPage, error) {
		return store.ActionWorkPage{Items: []store.StoredAlert{row}, Next: store.ActionWorkCursor{TenantID: "tenant", AlertID: "alert-010"}}, nil
	})
	_, result = r.enqueuePage(t.Context(), store.ActionWorkCursor{})
	if result.ErrorCode != "invalid_page" || calls.Load() != 17 {
		t.Fatal("invalid page executed", result)
	}
}

func TestActionRunnerCancellationOnlyAdvancesVisitedPrefix(t *testing.T) {
	repo := intentRows(t, 3)
	page, err := repo.ListActionWork(t.Context(), store.ActionWorkCursor{}, 16)
	if err != nil {
		t.Fatal(err)
	}
	last := page.Items[2]
	last.Alert.BKTenantID = "tenant-z"
	page.Items[2] = last
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	p := intentFunc(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		if id == "alert-000" {
			close(entered)
			<-ctx.Done()
			return store.StoredAlert{}, ctx.Err()
		}
		if id == "alert-001" {
			t.Error("unstarted prefix item ran")
			return store.StoredAlert{}, ErrInvalid
		}
		<-entered
		cancel()
		next := last
		next.Alert = last.Alert.Clone()
		next.Alert.ActionPending = nil
		return next, nil
	})
	r := mustRunner(t, intentWork(func(context.Context, store.ActionWorkCursor, int) (store.ActionWorkPage, error) { return page, nil }), &taskMemory{rows: map[string]StoredTask{}}, p, deliverFunc(unusedDelivery))
	next, result := r.enqueuePage(ctx, store.ActionWorkCursor{})
	if next.AlertID != "alert-000" || result.Visited != 2 || result.Outcomes[OutcomeUnstarted] != 1 || result.Outcomes[OutcomeEnqueued] != 1 || result.ErrorCode != "cancelled" {
		t.Fatal("cursor crossed unstarted item", next, result)
	}
}

func TestActionRunnerOutcomesIncludeWaitingRetryBlockedAndSuperseded(t *testing.T) {
	gate := gateFunc(func(_ context.Context, t Task) (projection.Receipt, error) {
		if t.Request.TenantID == "waiting" {
			return projection.Receipt{}, ErrBusy
		}
		p := visible(t.Request)
		if t.Request.TenantID == "skip" {
			p.AppliedRevision++
			p.AppliedStatus = domain.AlertStatusClosed
		}
		return p, nil
	})
	var sends atomic.Int64
	sender := sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		sends.Add(1)
		switch q.TenantID {
		case "retry":
			return Receipt{}, Failure{Code: "transport_failed", Retryable: true}
		case "failed":
			return Receipt{}, Failure{Code: "remote_unauthorized"}
		}
		return confirmed(q), nil
	})
	svc, tasks, now := serviceFixture(t, gate, sender)
	for _, tenant := range []string{"queued", "waiting", "skip", "retry", "failed", "blocked", "future"} {
		a := actionAlert(tenant, 1)
		if tenant == "blocked" {
			a.Revision = 2
			v := a.Projection.Targets["kac"]
			v.RequiredRevision = 2
			a.Projection.Targets["kac"] = v
		}
		row := recordAction(t, svc, a, Cause{Type: "source_event", ID: a.LatestEventID})
		if tenant == "future" {
			v := row.Task.Clone()
			v.Progress.State = "retry"
			v.Progress.Attempts = 1
			v.Progress.TotalAttempts = 1
			v.Progress.ErrorCode = "remote_unavailable"
			due := now.Add(time.Second)
			v.Progress.DueAt = &due
			tasks.rows[tenant+":"+v.ID] = StoredTask{Task: v, Version: "v"}
		}
	}
	head := actionTask(t, "blocked", 1)
	head.Progress.State = "failed"
	head.Progress.Attempts = 1
	head.Progress.TotalAttempts = 1
	head.Progress.DueAt = nil
	head.Progress.ErrorCode = "remote_unauthorized"
	tasks.rows["blocked:"+head.ID] = StoredTask{Task: head, Version: "v"}
	r := mustRunner(t, memory.New(), tasks, intentFunc(unusedIntent), svc)
	r.now = func() time.Time { return *now }
	next, result := r.deliverPage(t.Context(), "")
	if next != "" || result.Scanned != 7 || result.Visited != 7 || sends.Load() != 3 {
		t.Fatal(next, result, sends.Load())
	}
	for _, o := range []WorkOutcome{OutcomeQueued, OutcomeWaitingProjection, OutcomeSkipped, OutcomeRetrying, OutcomeFailed, OutcomeBlocked, OutcomeDeferred} {
		if result.Outcomes[o] != 1 {
			t.Fatal("wrong outcome", o, result)
		}
	}
	if result.Unconfirmed != 1 {
		t.Fatal("unknown transport result lost", result)
	}
}

func TestActionRunnerSharedExecutionLimitAcrossLoops(t *testing.T) {
	r := mustRunner(t, memory.New(), &taskMemory{rows: map[string]StoredTask{}}, intentFunc(unusedIntent), deliverFunc(unusedDelivery))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var active, peak atomic.Int64
	started := make(chan struct{}, 8)
	execute := func(ctx context.Context, _ int) itemResult {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if old >= n || peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-ctx.Done()
		return (itemResult{}).withError(ctx.Err())
	}
	done := make(chan RoundResult, 2)
	for range 2 {
		go func() { result, _ := r.runItems(ctx, []string{"a", "b", "c", "d"}, execute); done <- result }()
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("shared execution failed to start")
		}
	}
	select {
	case <-started:
		t.Fatal("fifth execution bypassed budget")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	for range 2 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancel leaked worker")
		}
	}
	if peak.Load() != 4 || active.Load() != 0 {
		t.Fatal(peak.Load(), active.Load())
	}
}

type runnerObserver struct {
	mu      sync.Mutex
	active  map[RunnerPhase]bool
	started chan RunnerPhase
}

func (o *runnerObserver) SetRunning(_ context.Context, p RunnerPhase, v bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active[p] = v
}

func (o *runnerObserver) RoundStarted(_ context.Context, p RunnerPhase) {
	select {
	case o.started <- p:
	default:
	}
}

func (*runnerObserver) RoundFinished(context.Context, RunnerPhase, RoundResult) {}

func TestActionRunnerRunIsSingleAndStopsBothLoops(t *testing.T) {
	r := mustRunner(t, memory.New(), &taskMemory{rows: map[string]StoredTask{}}, intentFunc(unusedIntent), deliverFunc(unusedDelivery))
	obs := &runnerObserver{active: map[RunnerPhase]bool{}, started: make(chan RunnerPhase, 4)}
	r.observer = obs
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	seen := map[RunnerPhase]bool{}
	for len(seen) < 2 {
		select {
		case phase := <-obs.started:
			seen[phase] = true
		case <-time.After(time.Second):
			t.Fatal("missing initial loop")
		}
	}
	if err := r.Run(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal("duplicate runner allowed", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not exit")
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.active[PhaseEnqueue] || obs.active[PhaseDeliver] {
		t.Fatal("observer still active")
	}
}

type actionWorkFunc func(context.Context, Query) ([]StoredTask, error)

func (f actionWorkFunc) List(ctx context.Context, q Query) ([]StoredTask, error) { return f(ctx, q) }

func TestActionRunnerRejectsForeignResultsAndInvalidPages(t *testing.T) {
	input := actionTask(t, "tenant", 1)
	// 固定在任务到期之后，避免测试依赖墙钟绕过执行。
	now := input.CreatedAt.Add(time.Hour)
	for _, kind := range []string{"tenant", "alert", "target", "revision"} {
		t.Run(kind, func(t *testing.T) {
			a := actionAlert("tenant", 1)
			switch kind {
			case "tenant":
				a.BKTenantID = "other"
			case "alert":
				a.AlertID = "other"
			case "target":
				a.Projection.Targets["other"] = a.Projection.Targets["kac"]
				delete(a.Projection.Targets, "kac")
			case "revision":
				a.Revision = 2
				ref := a.Projection.Targets["kac"]
				ref.RequiredRevision = 2
				a.Projection.Targets["kac"] = ref
			}
			target := "kac"
			if kind == "target" {
				target = "other"
			}
			foreign, err := NewTask(a, target, Cause{Type: "source_event", ID: a.LatestEventID}, now)
			if err != nil {
				t.Fatal(err)
			}
			r := mustRunner(t, memory.New(), actionWorkFunc(func(context.Context, Query) ([]StoredTask, error) {
				return []StoredTask{{Task: input, Version: "input"}}, nil
			}), intentFunc(unusedIntent), deliverFunc(func(context.Context, string, string) (StoredTask, error) {
				return StoredTask{Task: foreign, Version: "foreign"}, ErrBusy
			}))
			r.now = func() time.Time { return now }
			_, result := r.deliverPage(t.Context(), "")
			if result.Outcomes[OutcomeFailed] != 1 || len(result.Failures) != 1 || result.Failures[0].Code != "invalid_result" {
				t.Fatal("foreign result accepted", result)
			}
		})
	}
	for _, kind := range []string{"duplicate", "oversized", "missing_version", "settled"} {
		t.Run(kind, func(t *testing.T) {
			page := []StoredTask{{Task: input, Version: "v"}}
			switch kind {
			case "duplicate":
				page = append(page, page[0])
			case "oversized":
				for len(page) < 17 {
					page = append(page, page[0])
				}
			case "missing_version":
				page[0].Version = ""
			case "settled":
				page[0].Task.Progress.State = "succeeded"
			}
			var calls atomic.Int64
			r := mustRunner(t, memory.New(), actionWorkFunc(func(context.Context, Query) ([]StoredTask, error) { return page, nil }), intentFunc(unusedIntent), deliverFunc(func(context.Context, string, string) (StoredTask, error) { calls.Add(1); return StoredTask{}, ErrBusy }))
			_, result := r.deliverPage(t.Context(), "")
			if result.ErrorCode != "invalid_page" || calls.Load() != 0 || result.Scanned != 0 {
				t.Fatal("invalid page executed", result)
			}
		})
	}
}

func TestActionRunnerCancelledScanKeepsCursorAndExitClassification(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		} else {
			ctx, cancel = context.WithCancel(t.Context())
			cancel()
		}
		defer cancel()
		r := mustRunner(t, intentWork(func(ctx context.Context, _ store.ActionWorkCursor, _ int) (store.ActionWorkPage, error) {
			return store.ActionWorkPage{}, ctx.Err()
		}), actionWorkFunc(func(ctx context.Context, _ Query) ([]StoredTask, error) { return nil, ctx.Err() }), intentFunc(unusedIntent), deliverFunc(unusedDelivery))
		cursor := store.ActionWorkCursor{TenantID: "tenant", AlertID: "last"}
		after, enqueue := r.enqueuePage(ctx, cursor)
		next, delivery := r.deliverPage(ctx, "last")
		want := "cancelled"
		if deadline {
			want = "round_timeout"
		}
		if after != cursor || next != "last" || enqueue.ErrorCode != want || delivery.ErrorCode != want || !enqueue.ObservedAt.IsZero() || !delivery.ObservedAt.IsZero() {
			t.Fatal(after, next, enqueue, delivery)
		}
	}
}

func TestActionRunnerLogsActualEarlierTaskAfterValidatedDelivery(t *testing.T) {
	input, earlier := actionTask(t, "tenant", 2), actionTask(t, "tenant", 1)
	earlier.Progress.State = "failed"
	earlier.Progress.DueAt = nil
	earlier.Progress.Attempts = 1
	earlier.Progress.TotalAttempts = 1
	earlier.Progress.ErrorCode = "remote_unauthorized"
	if err := earlier.Validate(); err != nil {
		t.Fatal(err)
	}
	r := mustRunner(t, memory.New(), actionWorkFunc(func(context.Context, Query) ([]StoredTask, error) {
		return []StoredTask{{Task: input, Version: "input"}}, nil
	}), intentFunc(unusedIntent), deliverFunc(func(context.Context, string, string) (StoredTask, error) {
		return StoredTask{Task: earlier, Version: "earlier"}, nil
	}))
	r.now = func() time.Time { return input.CreatedAt.Add(time.Hour) }
	_, result := r.deliverPage(t.Context(), "")
	if len(result.Failures) != 1 || result.Failures[0].TaskID != earlier.ID || result.Failures[0].Code != "remote_unauthorized" {
		t.Fatal("failure log points to the requested later task", result)
	}
}
