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
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
)

type taskMemory struct {
	mu            sync.Mutex
	rows          map[string]StoredTask
	failState     string
	visibilityErr error
}

func (m *taskMemory) Get(ctx context.Context, tenant, id string) (StoredTask, error) {
	if ctx.Err() != nil {
		return StoredTask{}, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[tenant+":"+id]
	if !ok {
		return StoredTask{}, ErrNotFound
	}
	row.Task = row.Task.Clone()
	return row, nil
}

func (m *taskMemory) Put(ctx context.Context, t Task, expected string) (StoredTask, error) {
	if ctx.Err() != nil {
		return StoredTask{}, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failState == t.Progress.State {
		m.failState = ""
		return StoredTask{}, errors.New("storage unavailable")
	}
	key := t.Request.TenantID + ":" + t.ID
	old, ok := m.rows[key]
	if ok && expected != old.Version || !ok && expected != "" {
		return StoredTask{}, ErrConflict
	}
	var current *Task
	if ok {
		current = &old.Task
	}
	if err := ValidateWrite(current, t); err != nil {
		return StoredTask{}, err
	}
	next := StoredTask{Task: t.Clone()}
	// 可识别的递增 token，无当前时间或随机身份兜底。
	next.Version = old.Version + "v"
	m.rows[key] = next
	next.Task = next.Task.Clone()
	return next, nil
}

func (m *taskMemory) List(ctx context.Context, q Query) ([]StoredTask, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if q.Validate() != nil {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []StoredTask{}
	for _, row := range m.rows {
		if row.Task.ID > q.After && (q.TenantID == "" || q.TenantID == row.Task.Request.TenantID) && (!q.WorkOnly || row.Task.HasWork()) {
			result = append(result, StoredTask{Task: row.Task.Clone(), Version: row.Version})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Task.ID < result[j].Task.ID })
	return result[:min(q.Limit, len(result))], nil
}

func (m *taskMemory) CountWork(ctx context.Context, tenant string, limit int) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		if row.Task.Request.TenantID == tenant && row.Task.HasWork() {
			n++
			if n == limit {
				break
			}
		}
	}
	return n, nil
}

func (m *taskMemory) ConfirmVisible(ctx context.Context, t Task) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.visibilityErr != nil {
		return m.visibilityErr
	}
	row, ok := m.rows[t.Request.TenantID+":"+t.ID]
	if !ok {
		return ErrBusy
	}
	if row.Task.Request.Hash() != t.Request.Hash() {
		return ErrConflict
	}
	return nil
}

func (m *taskMemory) OldestUnsettled(ctx context.Context, tenant, alert, target string) (StoredTask, error) {
	if e := ctx.Err(); e != nil {
		return StoredTask{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var head StoredTask
	for _, r := range m.rows {
		q := r.Task.Request
		if q.TenantID == tenant && q.AlertID == alert && q.TargetID == target && r.Task.Unsettled() && (head.Version == "" || q.Revision < head.Task.Request.Revision) {
			head = r
		}
	}
	if head.Version == "" {
		return head, ErrNotFound
	}
	head.Task = head.Task.Clone()
	return head, nil
}

type gateFunc func(context.Context, Task) (projection.Receipt, error)

func (f gateFunc) Check(c context.Context, t Task) (projection.Receipt, error) { return f(c, t) }

type resolveFunc func(context.Context, string, string, int64, string) (Destination, error)

func (f resolveFunc) ResolveAction(c context.Context, t, s string, v int64, id string) (Destination, error) {
	return f(c, t, s, v, id)
}

type sendFunc func(context.Context, Destination, Request) (Receipt, error)

func (f sendFunc) Send(c context.Context, d Destination, q Request) (Receipt, error) {
	return f(c, d, q)
}

type targetLocker struct {
	mu         sync.Mutex
	held       map[string]bool
	releaseErr error
}

func (l *targetLocker) Acquire(ctx context.Context, key string) (func(context.Context) error, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return nil, projection.ErrBusy
	}
	l.held[key] = true
	return func(context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.held, key)
		return l.releaseErr
	}, nil
}

func serviceFixture(t *testing.T, gate Gate, sender Sender) (*Service, *taskMemory, *time.Time) {
	t.Helper()
	m := &taskMemory{rows: map[string]StoredTask{}}
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if gate == nil {
		gate = gateFunc(func(_ context.Context, task Task) (projection.Receipt, error) { return visible(task.Request), nil })
	}
	if sender == nil {
		sender = sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) { return confirmed(q), nil })
	}
	resolver := resolveFunc(func(_ context.Context, _ string, source string, version int64, target string) (Destination, error) {
		if source != "source" || version != 4 || target != "kac" {
			return Destination{}, ErrInvalid
		}
		return Destination{Endpoint: "http://receiver/action", JWTSecretKey: "secret"}, nil
	})
	s, e := New(m, gate, resolver, sender, &targetLocker{held: map[string]bool{}}, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	return s, m, &now
}

func recordAction(t *testing.T, s *Service, a domain.Alert, cause Cause) StoredTask {
	t.Helper()
	r, e := s.Record(t.Context(), a, "kac", cause)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestActionWaitsForVisibleProjectionWithoutConsumingAttempts(t *testing.T) {
	ready := false
	calls := 0
	s, _, now := serviceFixture(t, gateFunc(func(_ context.Context, t Task) (projection.Receipt, error) {
		if !ready {
			return projection.Receipt{}, ErrBusy
		}
		return visible(t.Request), nil
	}), sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) { calls++; return confirmed(q), nil }))
	a := actionAlert("tenant", 1)
	first := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	for range 3 {
		got, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
		if !CanDefer(e) || got.Task.Progress.State != "waiting_projection" || got.Task.Progress.Attempts != 0 || calls != 0 {
			t.Fatal(got, e)
		}
		*now = now.Add(time.Second)
	}
	ready = true
	done, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if e != nil || done.Task.Progress.State != "succeeded" || done.Task.Progress.Receipt == nil || calls != 1 {
		t.Fatal(done, e)
	}
	again, e := s.Record(t.Context(), a, "kac", Cause{Type: "source_event", ID: a.LatestEventID})
	if e != nil || !reflect.DeepEqual(done, again) {
		t.Fatal("same recorded task reset", e)
	}
	if _, e = s.Deliver(t.Context(), a.BKTenantID, first.Task.ID); e != nil || calls != 1 {
		t.Fatal("completed task resent", e)
	}
}

func TestActionUnknownAcceptanceReplaysSameIdentityAndFrozenRequest(t *testing.T) {
	requests := []Request{}
	effects := map[string]bool{}
	s, m, now := serviceFixture(t, nil, sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		requests = append(requests, q.Clone())
		effects[q.ActionID] = true
		return confirmed(q), nil
	}))
	a := actionAlert("tenant", 1)
	first := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	m.failState = "succeeded"
	if _, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID); e == nil {
		t.Fatal("missing result persistence failure")
	}
	row, e := m.Get(t.Context(), a.BKTenantID, first.Task.ID)
	if e != nil || row.Task.Progress.State != "sending" {
		t.Fatal(row, e)
	}
	if _, e = s.Deliver(t.Context(), a.BKTenantID, first.Task.ID); !CanDefer(e) {
		t.Fatal("sending lease bypassed", e)
	}
	*now = now.Add(31 * time.Second)
	restarted, e := New(m, s.gate, s.resolver, s.sender, s.locker, func() time.Time { return *now })
	if e != nil {
		t.Fatal(e)
	}
	done, e := restarted.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if e != nil || done.Task.Progress.State != "succeeded" || !done.Task.Progress.PreviousUnconfirmed || done.Task.Progress.Attempts != 2 || len(effects) != 1 || len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatal(done, e)
	}
}

func TestActionOrderIncludesFailedHeadAndNewTerminalCanSupersedeIt(t *testing.T) {
	var latest *Request
	sent := []string{}
	s, m, now := serviceFixture(t, gateFunc(func(_ context.Context, t Task) (projection.Receipt, error) {
		if latest != nil {
			return visible(*latest), nil
		}
		return visible(t.Request), nil
	}), sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		sent = append(sent, q.Action)
		if q.Action == "firing" {
			return Receipt{}, Failure{Code: "remote_unauthorized"}
		}
		return confirmed(q), nil
	}))
	a := actionAlert("tenant", 1)
	first := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	failed, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if e != nil || failed.Task.Progress.State != "failed" || failed.Task.Progress.PreviousUnconfirmed {
		t.Fatal(failed, e)
	}
	b := a.Clone()
	b.Revision = 2
	b.UpdateAt = b.UpdateAt.Add(time.Second)
	b.Status = domain.AlertStatusRecovered
	b.EndType = domain.AlertEndTypeSource
	b.EndReason = "source_recovered"
	end := b.UpdateAt
	b.EndAt = &end
	b.LatestEventID = "recovery"
	ref := b.Projection.Targets["kac"]
	ref.RequiredRevision = 2
	b.Projection.Targets["kac"] = ref
	second := recordAction(t, s, b, Cause{Type: "source_event", ID: b.LatestEventID})
	blocked, e := s.Deliver(t.Context(), b.BKTenantID, second.Task.ID)
	if !CanDefer(e) || blocked.Task.ID != first.Task.ID || len(sent) != 1 {
		t.Fatal("newer action bypassed failed head", blocked, e)
	}
	latest = &second.Task.Request
	*now = now.Add(time.Second)
	skipped, e := s.Deliver(t.Context(), b.BKTenantID, second.Task.ID)
	if e != nil || skipped.Task.ID != first.Task.ID || skipped.Task.Progress.State != "skipped" || len(sent) != 1 {
		t.Fatal(skipped, e)
	}
	done, e := s.Deliver(t.Context(), b.BKTenantID, second.Task.ID)
	if e != nil || done.Task.Progress.State != "succeeded" || len(sent) != 2 || sent[1] != "resolved" {
		t.Fatal(done, e)
	}
	if _, e = m.OldestUnsettled(t.Context(), b.BKTenantID, b.AlertID, "kac"); !errors.Is(e, ErrNotFound) {
		t.Fatal("settled actions still block order", e)
	}
}

func TestActionRetryBudgetAndManualRecoveryPreserveOriginalContent(t *testing.T) {
	fail := true
	calls := 0
	s, m, now := serviceFixture(t, nil, sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		calls++
		if fail {
			return Receipt{}, Failure{Code: "remote_unavailable", Retryable: true}
		}
		return confirmed(q), nil
	}))
	a := actionAlert("tenant", 1)
	first := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	current := first
	for n := 1; n <= MaxAttempts; n++ {
		var e error
		current, e = s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.Task.Progress.Attempts != n || current.Task.Request.Hash() != first.Task.Request.Hash() {
			t.Fatal(current)
		}
		if current.Task.Progress.DueAt != nil {
			*now = *current.Task.Progress.DueAt
		}
	}
	if current.Task.Progress.State != "failed" || calls != 8 {
		t.Fatal(current, calls)
	}
	retrier, e := NewRetrier(m, s.locker, func() time.Time { return *now })
	if e != nil {
		t.Fatal(e)
	}
	c := RetryCommand{TenantID: a.BKTenantID, TaskID: first.Task.ID, ExpectedVersion: current.Version, OperationID: "recover", OperatorID: "tester", Reason: "receiver restored"}
	pending, e := retrier.Retry(t.Context(), c)
	if e != nil || pending.Task.Progress.Generation != 2 || pending.Task.Progress.TotalAttempts != 8 || pending.Task.Progress.Attempts != 0 {
		t.Fatal(pending, e)
	}
	duplicate, e := retrier.Retry(t.Context(), c)
	if e != nil || !reflect.DeepEqual(duplicate, pending) {
		t.Fatal("repeat recovery reset budget", e)
	}
	fail = false
	done, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if e != nil || done.Task.Progress.State != "succeeded" || done.Task.Progress.TotalAttempts != 9 {
		t.Fatal(done, e)
	}
}

func TestActionRecordRequiresSearchVisibilityAndAtomicCASBeforeSend(t *testing.T) {
	calls := 0
	s, m, _ := serviceFixture(t, nil, sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) { calls++; return confirmed(q), nil }))
	a := actionAlert("tenant", 1)
	cause := Cause{Type: "source_event", ID: a.LatestEventID}
	m.visibilityErr = ErrBusy
	row, e := s.Record(t.Context(), a, "kac", cause)
	if !CanDefer(e) || row.Task.ID == "" {
		t.Fatal("unknown visibility acknowledged", e)
	}
	m.visibilityErr = nil
	first := recordAction(t, s, a, cause)
	if first.Task.ID != row.Task.ID {
		t.Fatal("visibility retry changed identity")
	}
	m.failState = "sending"
	if _, e := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID); e == nil || calls != 0 {
		t.Fatal("send preceded durable claim", e)
	}
}

func TestActionConcurrentDeliveryCancellationAndMixedLeaseFailure(t *testing.T) {
	var calls atomic.Int64
	s, _, _ := serviceFixture(t, nil, sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		calls.Add(1)
		return confirmed(q), nil
	}))
	a := actionAlert("tenant", 1)
	r := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, e := s.Deliver(t.Context(), a.BKTenantID, r.Task.ID)
			if e != nil && !CanDefer(e) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if CanDefer(errors.Join(ErrBusy, errors.New("release failed"))) {
		t.Fatal("mixed failure swallowed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := s.Deliver(ctx, a.BKTenantID, r.Task.ID); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	key, e := projection.TargetLockKey(a.BKTenantID, a.AlertID, "kac")
	if e != nil || key == "" {
		t.Fatal(e)
	}
	held, _ := s.locker.Acquire(t.Context(), key)
	defer func() { _ = held(t.Context()) }()
	other := actionAlert("tenant", 2)
	other.LatestEventID = "next"
	other.Admission.CauseID = "next"
	next := recordAction(t, s, other, Cause{Type: "source_event", ID: "next"})
	if _, e := s.Deliver(t.Context(), other.BKTenantID, next.Task.ID); !CanDefer(e) {
		t.Fatal("projection-shared lock bypassed", e)
	}
}

func TestActionPendingCapacityAndProjectionMismatch(t *testing.T) {
	s, m, _ := serviceFixture(t, nil, nil)
	a := actionAlert("tenant", 1)
	cause := Cause{Type: "source_event", ID: a.LatestEventID}
	first := recordAction(t, s, a, cause)
	for n := 1; n < MaxPendingPerTenant; n++ {
		b := a.Clone()
		b.AlertID = fmt.Sprintf("alert-%d", n)
		task, e := NewTask(b, "kac", cause, first.Task.CreatedAt)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.Put(t.Context(), task, ""); e != nil {
			t.Fatal(e)
		}
	}
	b := a.Clone()
	b.AlertID = "overflow"
	if _, e := s.Record(t.Context(), b, "kac", cause); !errors.Is(e, ErrCapacity) {
		t.Fatal("pending capacity bypassed", e)
	}
	if _, e := s.Record(t.Context(), a, "kac", cause); e != nil {
		t.Fatal("same task rejected at capacity", e)
	}
	foreign := a.Clone()
	foreign.BKTenantID = "other"
	if _, e := s.Record(t.Context(), foreign, "kac", cause); e != nil {
		t.Fatal("tenant capacity leaked", e)
	}
	calls := 0
	bad, _, _ := serviceFixture(t, gateFunc(func(_ context.Context, t Task) (projection.Receipt, error) {
		p := visible(t.Request)
		p.TargetID = "other"
		return p, nil
	}), sendFunc(func(context.Context, Destination, Request) (Receipt, error) { calls++; return Receipt{}, nil }))
	q := recordAction(t, bad, a, cause)
	failed, e := bad.Deliver(t.Context(), a.BKTenantID, q.Task.ID)
	if e != nil || failed.Task.Progress.State != "failed" || calls != 0 {
		t.Fatal("foreign projection permitted send", failed, e)
	}
}

func TestActionServiceBoundsAndCancelsInFlightCalls(t *testing.T) {
	entered := make(chan struct{}, 4)
	var active, maxSeen atomic.Int64
	s, m, _ := serviceFixture(t, nil, sendFunc(func(ctx context.Context, _ Destination, _ Request) (Receipt, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxSeen.Load()
			if old >= n || maxSeen.CompareAndSwap(old, n) {
				break
			}
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
			t.Error("delivery deadline missing")
		}
		entered <- struct{}{}
		<-ctx.Done()
		return Receipt{}, ctx.Err()
	}))
	var tasks []StoredTask
	for n := range 5 {
		a := actionAlert(fmt.Sprintf("tenant-%d", n), 1)
		tasks = append(tasks, recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID}))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 5)
	for _, task := range tasks {
		go func() { _, e := s.Deliver(ctx, task.Task.Request.TenantID, task.Task.ID); done <- e }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("four senders did not start")
		}
	}
	cancel()
	for range 5 {
		select {
		case e := <-done:
			if !errors.Is(e, context.Canceled) {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled sender did not exit")
		}
	}
	if maxSeen.Load() != 4 || active.Load() != 0 {
		t.Fatal("concurrency budget or shutdown", maxSeen.Load(), active.Load())
	}
	claimed := 0
	for _, task := range tasks {
		row, e := m.Get(t.Context(), task.Task.Request.TenantID, task.Task.ID)
		if e != nil {
			t.Fatal(e)
		}
		if row.Task.Progress.State == "sending" {
			claimed++
		} else if row.Task.Progress.State != "pending" {
			t.Fatal("cancellation invented a result", row)
		}
	}
	if claimed != 4 {
		t.Fatal("unstarted action consumed an attempt", claimed)
	}
}

func TestActionWaitDoesNotSwallowLeaseReleaseFailure(t *testing.T) {
	s, m, _ := serviceFixture(t, gateFunc(func(context.Context, Task) (projection.Receipt, error) { return projection.Receipt{}, ErrBusy }), nil)
	a := actionAlert("tenant", 1)
	r := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	lock, ok := s.locker.(*targetLocker)
	if !ok {
		t.Fatal("unexpected fixture locker")
	}
	failure := errors.New("release failed")
	lock.releaseErr = failure
	result, e := s.Deliver(t.Context(), a.BKTenantID, r.Task.ID)
	if !errors.Is(e, failure) || CanDefer(e) || result.Task.Progress.State != "waiting_projection" {
		t.Fatal("mixed release error lost", e)
	}
	stored, e := m.Get(t.Context(), a.BKTenantID, r.Task.ID)
	if e != nil || stored.Task.Progress.Attempts != 0 {
		t.Fatal("wait consumed attempts", e)
	}
}

func TestActionStaleSkipRetainsUnknownEarlierAcceptance(t *testing.T) {
	terminal := false
	calls := 0
	s, m, now := serviceFixture(t, gateFunc(func(_ context.Context, task Task) (projection.Receipt, error) {
		receipt := visible(task.Request)
		if terminal {
			receipt.AppliedRevision++
			receipt.AppliedStatus = domain.AlertStatusClosed
		}
		return receipt, nil
	}), sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) { calls++; return confirmed(q), nil }))
	a := actionAlert("tenant", 1)
	task := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	m.failState = "succeeded"
	if _, e := s.Deliver(t.Context(), a.BKTenantID, task.Task.ID); e == nil {
		t.Fatal("missing persistence fault")
	}
	terminal = true
	*now = now.Add(31 * time.Second)
	result, e := s.Deliver(t.Context(), a.BKTenantID, task.Task.ID)
	if e != nil || result.Task.Progress.State != "skipped" || !result.Task.Progress.PreviousUnconfirmed || result.Task.Progress.Receipt != nil || calls != 1 {
		t.Fatal("stale skip invented a never-sent history", result, e)
	}
}

func TestRecordActionConfirmsEveryTargetBeforeCompletingIntent(t *testing.T) {
	s, m, now := serviceFixture(t, nil, nil)
	a := actionAlert("tenant-a", 1)
	a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {ActionEnabled: true, SourceVersion: 4, RequiredRevision: 1}, "second": {ActionEnabled: true, SourceVersion: 4, RequiredRevision: 1}, "audit": {SourceVersion: 4, RequiredRevision: 1}}
	var err error
	a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
	if err != nil {
		t.Fatal(err)
	}
	m.visibilityErr = ErrBusy
	if err = s.RecordAction(t.Context(), a, *a.ActionPending); !errors.Is(err, ErrBusy) {
		t.Fatal("invisible first task counted as complete", err)
	}
	partial, err := m.List(t.Context(), Query{TenantID: a.BKTenantID, Limit: 16})
	if err != nil || len(partial) != 1 {
		t.Fatal("partial enqueue not preserved", err)
	}
	original := partial[0]
	m.visibilityErr = nil
	*now = now.Add(time.Second)
	a.Projection, _, err = a.Projection.Acknowledge("kac", 4, 1, *now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordAction(t.Context(), a, *a.ActionPending); err != nil {
		t.Fatal(err)
	}
	all, err := m.List(t.Context(), Query{TenantID: a.BKTenantID, Limit: 16})
	if err != nil || len(all) != 2 {
		t.Fatal("pure projection sent or target omitted", len(all), err)
	}
	for _, row := range all {
		if row.Task.Request.TargetID == "kac" && (!row.Task.CreatedAt.Equal(original.Task.CreatedAt) || row.Task.Request.Hash() != original.Task.Request.Hash()) {
			t.Fatal("ACK/retry changed original task")
		}
	}
	wrong := a.ActionPending.Clone()
	wrong.CauseID = "another"
	if err = s.RecordAction(t.Context(), a, *wrong); !errors.Is(err, ErrInvalid) {
		t.Fatal("different intent accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.RecordAction(ctx, a, *a.ActionPending); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
}

func TestCeleryRetryMayReturnDifferentTaskID(t *testing.T) {
	calls := 0
	hash := ""
	s, m, now := serviceFixture(t, nil, sendFunc(func(_ context.Context, _ Destination, q Request) (Receipt, error) {
		calls++
		if hash != "" && hash != q.Hash() {
			t.Fatal("retry changed request")
		}
		hash = q.Hash()
		ack := confirmed(q)
		if calls == 1 {
			ack.TaskID = "first-task"
		} else {
			ack.TaskID = "second-task"
		}
		return ack, nil
	}))
	a := actionAlert("tenant", 1)
	task := recordAction(t, s, a, Cause{Type: "source_event", ID: a.LatestEventID})
	m.failState = "succeeded"
	if _, err := s.Deliver(t.Context(), "tenant", task.Task.ID); err == nil {
		t.Fatal("local save fault hidden")
	}
	*now = now.Add(31 * time.Second)
	done, err := s.Deliver(t.Context(), "tenant", task.Task.ID)
	if err != nil || done.Task.Progress.State != "succeeded" || done.Task.Progress.Receipt.TaskID != "second-task" || !done.Task.Progress.PreviousUnconfirmed || calls != 2 {
		t.Fatal("fresh Celery reference rejected", done, err)
	}
}
