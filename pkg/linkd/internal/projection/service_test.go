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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type taskMemory struct {
	mu        sync.Mutex
	rows      map[string]StoredTask
	failState string
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

type testResolver struct {
	calls    int
	versions []int64
	err      error
}

func (r *testResolver) ResolveProjection(_ context.Context, _, _ string, v int64, _ string) (Destination, error) {
	r.calls++
	r.versions = append(r.versions, v)
	return Destination{TargetID: "kac"}, r.err
}

type testSender struct {
	calls  int
	err    error
	mutate func(Request) Receipt
	cancel func()
}

func (s *testSender) Send(_ context.Context, _ Destination, q Request) (Receipt, error) {
	s.calls++
	if s.cancel != nil {
		s.cancel()
	}
	if s.err != nil {
		return Receipt{}, s.err
	}
	if s.mutate != nil {
		return s.mutate(q), nil
	}
	return receiptFor(q), nil
}

func receiptFor(q Request) Receipt {
	var body struct{ Status domain.AlertStatus }
	_ = json.Unmarshal(q.Alert, &body)
	return Receipt{SchemaVersion: SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: q.Revision, ContentHash: q.ContentHash, AppliedStatus: body.Status, SearchVisible: true, DocumentRef: "alarm-history-000001/" + q.AlarmID}
}

type ackRepository struct {
	*memory.Repository
	fail bool
}

func (r *ackRepository) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return r.GetAlert(ctx, tenant, id)
}

func (r *ackRepository) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, a domain.Alert) (store.StoredAlert, error) {
	if r.fail {
		r.fail = false
		return store.StoredAlert{}, errors.New("ACK storage failed")
	}
	return r.Repository.CompareAndSetAlert(ctx, tenant, id, version, a)
}

func projectionFixture(t *testing.T) (*Service, *taskMemory, *ackRepository, *testResolver, *testSender, *time.Time, domain.Alert) {
	t.Helper()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	repo := &ackRepository{Repository: memory.New()}
	a := storetest.Alert("tenant-a", "alert-a", "opening", "fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 3, RequiredRevision: 1}, "audit": {SourceVersion: 2, RequiredRevision: 1}}}
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	tasks := &taskMemory{rows: map[string]StoredTask{}}
	resolver, sender := &testResolver{}, &testSender{}
	s, err := New(tasks, repo, resolver, sender, &testTargetLocker{held: map[string]bool{}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s, tasks, repo, resolver, sender, &now, a
}

func TestProduceAndDeliverAreStableAndTargetScoped(t *testing.T) {
	s, tasks, repo, resolver, sender, now, a := projectionFixture(t)
	first, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	again, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("duplicate production changed task", err)
	}
	saved, err := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if err != nil || saved.Task.Progress.State != "succeeded" || sender.calls != 1 || !slices.Equal(resolver.versions, []int64{3}) {
		t.Fatal("delivery", saved.Task.Progress, err)
	}
	current, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || current.Alert.Revision != 1 || current.Alert.Projection.Targets["kac"].SyncedRevision != 1 || current.Alert.Projection.Targets["audit"].SyncedRevision != 0 {
		t.Fatal("ACK crossed target/version", err)
	}
	repeated, err := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if err != nil || repeated.Version != saved.Version || sender.calls != 1 {
		t.Fatal("completed task resent", err)
	}
	work, err := tasks.List(t.Context(), Query{WorkOnly: true, Limit: 16})
	if err != nil || len(work) != 0 {
		t.Fatal("completed task remained work")
	}
	if _, err := tasks.Get(t.Context(), "tenant-b", first.Task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross tenant task")
	}
}

func TestRemoteReceiptSurvivesLocalACKFailure(t *testing.T) {
	s, tasks, repo, _, sender, _, a := projectionFixture(t)
	pending, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	if _, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); err == nil {
		t.Fatal("ACK failure ignored")
	}
	delivered, err := tasks.Get(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || delivered.Task.Progress.State != "delivered" {
		t.Fatal("durable receipt lost", err)
	}
	sender.err = errors.New("receiver now unreachable")
	done, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || done.Task.Progress.State != "succeeded" || sender.calls != 1 {
		t.Fatal("ACK repair resent external request", err)
	}
}

func TestRetryBudgetAndManualRetryPreserveFrozenTask(t *testing.T) {
	s, tasks, _, _, sender, now, a := projectionFixture(t)
	pending, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	sender.err = Failure{"remote_unavailable", true}
	var failed StoredTask
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		row, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
		if err != nil || row.Task.Progress.Attempts != attempt {
			t.Fatal("attempt", attempt, err)
		}
		if attempt < MaxAttempts {
			if row.Task.Progress.State != "retry" || row.Task.Progress.DueAt == nil {
				t.Fatal("retry state")
			}
			if _, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); !errors.Is(err, ErrBusy) {
				t.Fatal("backoff bypass", err)
			}
			*now = *row.Task.Progress.DueAt
		} else {
			failed = row
		}
	}
	if failed.Task.Progress.State != "failed" || sender.calls != MaxAttempts {
		t.Fatal("retry budget")
	}
	if rows, err := tasks.List(t.Context(), Query{WorkOnly: true, Limit: 16}); err != nil || len(rows) != 0 {
		t.Fatal("failed work still auto runs")
	}
	if rows, err := tasks.List(t.Context(), Query{TenantID: a.BKTenantID, Limit: 16}); err != nil || len(rows) != 1 {
		t.Fatal("failed task discarded")
	}
	retried, err := s.Retry(t.Context(), RetryCommand{TenantID: a.BKTenantID, TaskID: pending.Task.ID, ExpectedVersion: failed.Version, OperationID: "manual-1", OperatorID: "tester", Reason: "恢复测试投影"})
	if err != nil || retried.Task.Progress.Generation != 2 || retried.Task.Progress.Attempts != 0 || !reflect.DeepEqual(retried.Task.Request, pending.Task.Request) {
		t.Fatal("manual retry reset identity", err)
	}
	duplicate, err := s.Retry(t.Context(), RetryCommand{TenantID: a.BKTenantID, TaskID: pending.Task.ID, ExpectedVersion: failed.Version, OperationID: "manual-1", OperatorID: "tester", Reason: "恢复测试投影"})
	if err != nil || duplicate.Version != retried.Version {
		t.Fatal("manual operation repeated", err)
	}
	sender.err = nil
	done, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || done.Task.Progress.TotalAttempts != MaxAttempts+1 {
		t.Fatal("retry completion", err)
	}
}

func TestCancelledAttemptWaitsForPersistentLeaseAndUsesSameIdentity(t *testing.T) {
	s, tasks, _, _, sender, now, a := projectionFixture(t)
	pending, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sender.cancel = cancel
	if _, err := s.Deliver(ctx, a.BKTenantID, pending.Task.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current, err := tasks.Get(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || current.Task.Progress.State != "sending" {
		t.Fatal("attempt reservation lost", err)
	}
	sender.cancel = nil
	if _, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("live attempt duplicated", err)
	}
	*now = *current.Task.Progress.LeaseUntil
	done, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || done.Task.ID != pending.Task.ID || done.Task.Progress.Attempts != 2 {
		t.Fatal("expired attempt recovery", err)
	}
}

func TestTaskCreationConflictCannotOverwriteSameRevisionContent(t *testing.T) {
	s, tasks, _, _, _, now, a := projectionFixture(t)
	first, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	changed := a.Clone()
	changed.Title = "different"
	other, err := NewTask(changed, "kac", *now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Task.ID != other.ID || first.Task.Request.ContentHash == other.Request.ContentHash {
		t.Fatal("identity includes mutable content")
	}
	if _, err := tasks.Put(t.Context(), other, first.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("same version payload overwritten", err)
	}
}

func TestProtocolIdentityHashAndReceiptBoundaries(t *testing.T) {
	_, _, _, _, _, now, a := projectionFixture(t)
	a.ExtraData = domain.JSONObject{"object": json.RawMessage(`{"b":2,"a":1}`)}
	q, err := BuildRequest(a, "kac")
	if err != nil {
		t.Fatal(err)
	}
	normalized := a.Clone()
	normalized.ExtraData = domain.JSONObject{"object": json.RawMessage(`{ "a":1, "b":2 }`)}
	normalized.Projection, _, err = normalized.Projection.Acknowledge("kac", 3, 1, *now)
	if err != nil {
		t.Fatal(err)
	}
	same, err := BuildRequest(normalized, "kac")
	if err != nil || !reflect.DeepEqual(q, same) {
		t.Fatal("ACK or JSON key order changed snapshot", err)
	}
	next := a.Clone()
	next.Revision++
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.Status = domain.AlertStatusClosed
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeUser
	next.EndReason = "manual"
	terminal, err := BuildRequest(next, "kac")
	if err != nil || terminal.AlarmID != q.AlarmID || terminal.ContentHash == q.ContentHash {
		t.Fatal("terminal changed stable KAC identity", err)
	}
	otherTenant := a.Clone()
	otherTenant.BKTenantID = "tenant-b"
	other, err := BuildRequest(otherTenant, "kac")
	if err != nil || other.AlarmID == q.AlarmID {
		t.Fatal("tenant not part of identity", err)
	}
	for _, edit := range []func(*Receipt){
		func(r *Receipt) { r.TenantID = "tenant-b" }, func(r *Receipt) { r.TargetID = "audit" },
		func(r *Receipt) { r.SearchVisible = false }, func(r *Receipt) { r.AppliedRevision = 0 },
		func(r *Receipt) { r.ContentHash = strings.Repeat("a", 64) }, func(r *Receipt) { r.AlarmID = "other" },
	} {
		ack := receiptFor(q)
		edit(&ack)
		if ack.ValidateFor(q) == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
}

type testTargetLocker struct {
	mu   sync.Mutex
	held map[string]bool
}

func (l *testTargetLocker) Acquire(ctx context.Context, key string) (func(context.Context) error, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return nil, ErrBusy
	}
	l.held[key] = true
	return func(context.Context) error { l.mu.Lock(); defer l.mu.Unlock(); delete(l.held, key); return nil }, nil
}

type resolverFunc func(context.Context, string, string, int64, string) (Destination, error)

func (f resolverFunc) ResolveProjection(c context.Context, t, s string, v int64, id string) (Destination, error) {
	return f(c, t, s, v, id)
}

type senderFunc func(context.Context, Destination, Request) (Receipt, error)

func (f senderFunc) Send(c context.Context, d Destination, q Request) (Receipt, error) {
	return f(c, d, q)
}

func TestDifferentVersionsOfOneTargetCannotSendConcurrently(t *testing.T) {
	s, _, repo, _, _, _, a := projectionFixture(t)
	s.resolver = resolverFunc(func(context.Context, string, string, int64, string) (Destination, error) { return Destination{}, nil })
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	s.sender = senderFunc(func(ctx context.Context, _ Destination, q Request) (Receipt, error) {
		calls.Add(1)
		if q.Revision == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return Receipt{}, ctx.Err()
			}
		}
		return receiptFor(q), nil
	})
	first, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	row, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	next := row.Alert.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, row.Version, next); err != nil {
		t.Fatal(err)
	}
	second, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID); done <- err }()
	<-entered
	if _, err := s.Deliver(t.Context(), a.BKTenantID, second.Task.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("same target sent concurrently", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deliver(t.Context(), a.BKTenantID, second.Task.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("busy dispatch consumed an attempt")
	}
}

func TestDeliveryConcurrencyBoundCancellationAndRelease(t *testing.T) {
	s, _, repo, _, _, _, a := projectionFixture(t)
	s.resolver = resolverFunc(func(context.Context, string, string, int64, string) (Destination, error) { return Destination{}, nil })
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var concurrent, peak atomic.Int64
	s.sender = senderFunc(func(ctx context.Context, _ Destination, q Request) (Receipt, error) {
		active := concurrent.Add(1)
		defer concurrent.Add(-1)
		for old := peak.Load(); active > old; old = peak.Load() {
			if peak.CompareAndSwap(old, active) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		}
		return receiptFor(q), nil
	})
	tasks := make([]StoredTask, 0, 5)
	for i := range 5 {
		next := a.Clone()
		next.AlertID = fmt.Sprint("bounded-", i)
		next.Fingerprint = next.AlertID
		if _, err := repo.CreateAlert(t.Context(), next); err != nil {
			t.Fatal(err)
		}
		row, err := s.Produce(t.Context(), next.BKTenantID, next.AlertID, "kac")
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, row)
	}
	result := make(chan error, 4)
	for _, task := range tasks[:4] {
		go func() { _, err := s.Deliver(t.Context(), a.BKTenantID, task.Task.ID); result <- err }()
	}
	for range 4 {
		<-entered
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Deliver(ctx, a.BKTenantID, tasks[4].Task.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("waiting work ignored cancellation", err)
	}
	close(release)
	for range 4 {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() != 4 || concurrent.Load() != 0 {
		t.Fatal("concurrency/exit bound")
	}
	if _, err := s.Deliver(t.Context(), a.BKTenantID, tasks[4].Task.ID); err != nil {
		t.Fatal("slots or locks leaked", err)
	}
}

func TestLostReceiptPersistenceRetriesOriginalRequestAfterLease(t *testing.T) {
	s, tasks, repo, _, sender, now, a := projectionFixture(t)
	pending, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	tasks.failState = "delivered"
	if _, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); err == nil {
		t.Fatal("receipt persistence failure ignored")
	}
	row, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || row.Alert.Projection.Targets["kac"].SyncedRevision != 0 {
		t.Fatal("ACK preceded durable receipt", err)
	}
	current, err := tasks.Get(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || current.Task.Progress.State != "sending" {
		t.Fatal("uncertain attempt discarded", err)
	}
	*now = *current.Task.Progress.LeaseUntil
	done, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || done.Task.Progress.State != "succeeded" || sender.calls != 2 || !reflect.DeepEqual(done.Task.Request, pending.Task.Request) {
		t.Fatal("retry changed frozen content", err)
	}
}

type failedReleaseLocker struct{}

func (failedReleaseLocker) Acquire(context.Context, string) (func(context.Context) error, error) {
	return func(context.Context) error { return errors.New("release uncertain") }, nil
}

func TestReleaseUncertaintyIsReportedWithoutDiscardingSuccess(t *testing.T) {
	s, tasks, _, _, sender, _, a := projectionFixture(t)
	s.locker = failedReleaseLocker{}
	pending, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err == nil || pending.Task.Validate() != nil {
		t.Fatal("uncertain admission release must preserve saved task", err)
	}
	if _, err := s.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); err == nil {
		t.Fatal("uncertain release reported successful scheduling")
	}
	current, err := tasks.Get(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || current.Task.Progress.State != "succeeded" || sender.calls != 1 {
		t.Fatal("successful receipt/ACK discarded", err)
	}
}

func TestTerminalReceiptCannotReopenLifecycle(t *testing.T) {
	_, _, _, _, _, _, a := projectionFixture(t)
	a.Status = domain.AlertStatusClosed
	a.EndAt = &a.UpdateAt
	a.EndType = domain.AlertEndTypeUser
	a.EndReason = "manual"
	q, err := BuildRequest(a, "kac")
	if err != nil {
		t.Fatal(err)
	}
	ack := receiptFor(q)
	ack.AppliedRevision++
	ack.AppliedStatus = domain.AlertStatusActive
	if ack.ValidateFor(q) == nil {
		t.Fatal("remote active overwrote closed lifecycle")
	}
}

func TestProjectionSnapshotExcludesRecheckMetadata(t *testing.T) {
	_, _, _, _, _, now, a := projectionFixture(t)
	a.Shield = domain.AlertShield{
		Active: true, NextCheckAt: now,
		Bindings: []domain.ShieldBinding{{
			BindingID: strings.Repeat("a", 64), ActivationID: strings.Repeat("b", 64),
			Policy: domain.PolicyVersion{ID: "maintenance", Version: 1, Digest: strings.Repeat("c", 64)},
			Type:   "time_shield", SourceEventID: "opening", Severity: "warning", BoundAt: *now,
		}},
	}
	first, err := BuildRequest(a, "kac")
	if err != nil {
		t.Fatal(err)
	}
	later := a.Clone()
	nextCheck := now.Add(time.Minute)
	later.Shield.NextCheckAt = &nextCheck
	second, err := BuildRequest(later, "kac")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("recheck metadata changed business digest", err)
	}
	if strings.Contains(string(first.Alert), "next_check_at") || strings.Contains(string(first.Alert), "\"projection\"") {
		t.Fatal("internal metadata on wire")
	}
	if !strings.Contains(string(first.Alert), "maintenance") {
		t.Fatal("business binding omitted")
	}
}

func TestTaskAndQueryBudgetsRejectInvalidBoundaries(t *testing.T) {
	_, _, _, _, _, now, a := projectionFixture(t)
	task, err := NewTask(a, "kac", *now)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*Task){
		func(t *Task) { t.Progress.DueAt = nil },
		func(t *Task) { v := t.Progress.UpdatedAt.Add(time.Hour); t.Progress.DueAt = &v },
		func(t *Task) { t.Progress.Attempts = MaxAttempts + 1 },
		func(t *Task) {
			t.Progress.State = "sending"
			t.Progress.Attempts = 1
			t.Progress.TotalAttempts = 1
			t.Progress.DueAt = nil
			until := t.Progress.UpdatedAt.Add(time.Hour)
			t.Progress.LeaseUntil = &until
		},
		func(t *Task) { t.SourceVersion = 0 },
	} {
		bad := task.Clone()
		edit(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid task budget accepted")
		}
	}
	for _, q := range []Query{{WorkOnly: true, Limit: 0}, {WorkOnly: true, Limit: 17}, {Limit: 1}, {TenantID: "tenant", After: "invalid", Limit: 1}} {
		if q.Validate() == nil {
			t.Fatal("invalid query budget/scope accepted")
		}
	}
}

func TestRetryAuditIsFrozenAndChangedCommandConflicts(t *testing.T) {
	service, tasks, _, _, sender, _, a := projectionFixture(t)
	pending, err := service.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	sender.err = Failure{"remote_unauthorized", false}
	failed, err := service.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	retrier, err := NewRetrier(tasks, service.locker, service.now)
	if err != nil {
		t.Fatal(err)
	}
	c := RetryCommand{TenantID: a.BKTenantID, TaskID: failed.Task.ID, ExpectedVersion: failed.Version, OperationID: "explicit", OperatorID: "user", Reason: " original reason "}
	first, err := retrier.Retry(t.Context(), c)
	if err != nil || first.Task.Progress.LastRetry == nil || first.Task.Progress.LastRetry.Command != c {
		t.Fatal(first, err)
	}
	before := first.Task.Clone()
	again, err := retrier.Retry(t.Context(), c)
	if err != nil || !reflect.DeepEqual(again, first) {
		t.Fatal("replay changed record", err)
	}
	for _, mutate := range []func(*RetryCommand){func(v *RetryCommand) { v.Reason = "other" }, func(v *RetryCommand) { v.OperatorID = "other" }, func(v *RetryCommand) { v.ExpectedVersion = "different" }} {
		changed := c
		mutate(&changed)
		if _, err := retrier.Retry(t.Context(), changed); !errors.Is(err, ErrConflict) {
			t.Fatal("same operation accepted different command", err)
		}
	}
	first.Task.Progress.LastRetry.Command.Reason = "mutated"
	stored, err := tasks.Get(t.Context(), c.TenantID, c.TaskID)
	if err != nil || !reflect.DeepEqual(stored.Task, before) {
		t.Fatal("retry record alias leaked", err)
	}
}
