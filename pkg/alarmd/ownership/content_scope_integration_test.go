// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"
)

// decision-016, batch 1: the Assignment record names the content a lease is
// admitted to execute, a content change under a live lease waits for that
// lease's deadline, and the fence compares the scope only for writers that
// declare one. Every test runs the real scripts against a real server;
// deadlines and effective times are on that server's clock, so "the lease
// lapses" is produced with elapseOnRedis and expected instants are read
// from what the server holds (redis_clock_test.go).

func publishScope(t *testing.T, store *RedisStore, authority PublicationAuthority, revision uint64, worker, scope string, at time.Time) AssignmentRecord {
	t.Helper()
	record, err := store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: worker, ExpectedRecordRevision: revision,
		PlacementReason: PlacementRendezvous, DecidedAt: at, ContentScope: scope,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(%s, %q) error = %v", worker, scope, err)
	}
	return record
}

func TestAPublishedContentScopeIsWhatALeaseIsAdmittedTo(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	record := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	if record.ContentScope != "view-a" || record.ContentChangePending() {
		t.Fatalf("published record = %+v, want content scope view-a and nothing pending", record)
	}
	read, err := store.ReadAssignment(ctx, "query-group-1")
	if err != nil || read.ContentScope != "view-a" {
		t.Fatalf("ReadAssignment() = (%+v, %v), want content scope view-a", read, err)
	}
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil || lease.ContentScope != "view-a" {
		t.Fatalf("Acquire() = (%+v, %v), want the record's content scope on the lease", lease, err)
	}
	at := now.Add(time.Second)
	if err := store.CheckFence(ctx, lease.Fence); err != nil {
		t.Fatalf("CheckFence() without a scope = %v, want the fence as it always was", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-a) = %v, want valid", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-b"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("CheckFenceForContentScope(view-b) = %v, want ErrContentScopeMoved", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-b"); errors.Is(err, ErrStaleFence) {
		t.Fatal("a moved content scope was reported as a stale fence; the lease is live and the two must not be confused")
	}
	renewed, err := store.Renew(ctx, lease.Fence, at, time.Minute)
	if err != nil || renewed.ContentScope != "view-a" || renewed.ContentChangePending() || !renewed.Deadline.Equal(at.Add(time.Minute)) {
		t.Fatalf("Renew() = (%+v, %v), want view-a, nothing pending and the full deadline", renewed, err)
	}
	status, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: lease.Fence, Namespace: "progress", ExpectedMissing: true, Value: []byte("p1"), ContentScope: "view-b",
	})
	if status != FencedCASContentMoved || !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("FencedCompareAndSet(view-b) = (%s, %v), want CONTENT_MOVED", status, err)
	}
	if _, missing, _ := store.ReadControl(ctx, "query-group-1", "progress"); !missing {
		t.Fatal("a fenced write refused for a moved scope still wrote its value")
	}
	status, err = store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: lease.Fence, Namespace: "progress", ExpectedMissing: true, Value: []byte("p1"), ContentScope: "view-a",
	})
	if status != FencedCASApplied || err != nil {
		t.Fatalf("FencedCompareAndSet(view-a) = (%s, %v), want applied", status, err)
	}
}

func TestAContentChangeUnderALiveLeaseWaitsForItsDeadline(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The change is decided while worker-1 holds a lease for a minute; the
	// effective time is that lease's deadline, as the server holds it, plus
	// the margin.
	wantEffective := serverDeadline(t, store, "query-group-1").Add(ContentSwitchMargin)
	changed := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", now.Add(time.Second))
	if changed.ContentScope != "view-a" || changed.PendingContentScope != "view-b" || !changed.EffectiveAt.Equal(wantEffective) {
		t.Fatalf("record after the change = %+v, want view-a current, view-b pending, effective at %v", changed, wantEffective)
	}
	if changed.RecordRevision != first.RecordRevision+1 || changed.AssignmentGeneration != first.AssignmentGeneration {
		t.Fatalf("revision/generation = %d/%d, want revision bumped to %d and generation unchanged at %d",
			changed.RecordRevision, changed.AssignmentGeneration, first.RecordRevision+1, first.AssignmentGeneration)
	}
	// Renewal is capped at the effective time and says why: two minutes are
	// asked for, and the lease ends at the effective time instead.
	renewed, err := store.Renew(ctx, lease.Fence, now.Add(10*time.Second), 2*time.Minute)
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if !renewed.Deadline.Equal(renewed.EffectiveAt) || renewed.ContentScope != "view-a" || renewed.PendingContentScope != "view-b" {
		t.Fatalf("Renew() under a pending change = %+v, want deadline capped at the effective time with the pending scope named", renewed)
	}
	if minted := serverDeadline(t, store, "query-group-1"); !minted.Equal(wantEffective) {
		t.Fatalf("server deadline after the capped renewal = %v, want the effective time %v", minted, wantEffective)
	}
	// Until then the old content is still what the record authorizes -- and
	// so is the new one already: a writer on the pending content is ahead
	// of the record, not behind it. Only a third content is refused.
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-a) before the effective time = %v, want valid", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-b"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-b, the pending scope) before the effective time = %v, want valid", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-c"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("CheckFenceForContentScope(view-c) before the effective time = %v, want ErrContentScopeMoved", err)
	}
	// At the effective time the capped lease has run out; the next holder
	// is admitted to the new content, and the old content is refused.
	elapseOnRedis(t, store, "query-group-1", time.Minute+ContentSwitchMargin+time.Second)
	after := now.Add(time.Minute + ContentSwitchMargin + time.Second)
	if _, err := store.Renew(ctx, lease.Fence, after, time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Renew() past the capped deadline = %v, want ErrStaleFence", err)
	}
	next, err := store.Acquire(ctx, "query-group-1", "worker-1", after, time.Minute)
	if err != nil || next.ContentScope != "view-b" || next.Fence.OwnerEpoch != lease.Fence.OwnerEpoch+1 {
		t.Fatalf("Acquire() after the switch = (%+v, %v), want a new epoch on view-b", next, err)
	}
	promoted, err := store.ReadAssignment(ctx, "query-group-1")
	if err != nil || promoted.ContentScope != "view-b" || promoted.ContentChangePending() {
		t.Fatalf("record after the switch = (%+v, %v), want view-b current and nothing pending", promoted, err)
	}
	if err := store.CheckFenceForContentScope(ctx, next.Fence, "view-a"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("CheckFenceForContentScope(view-a) after the switch = %v, want ErrContentScopeMoved", err)
	}
	if err := store.CheckFenceForContentScope(ctx, next.Fence, "view-b"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-b) after the switch = %v, want valid", err)
	}
	renewed, err = store.Renew(ctx, next.Fence, after.Add(time.Second), time.Minute)
	if err != nil || renewed.ContentChangePending() || !renewed.Deadline.Equal(after.Add(61*time.Second)) {
		t.Fatalf("Renew() after the switch = (%+v, %v), want an uncapped renewal with nothing pending", renewed, err)
	}
}

func TestAContentChangeWithNoHolderIsWrittenDirectly(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	changed := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", now.Add(time.Second))
	if changed.ContentScope != "view-b" || changed.ContentChangePending() || changed.RecordRevision != first.RecordRevision+1 {
		t.Fatalf("record with no holder = %+v, want view-b written directly with the revision bumped", changed)
	}
	// A lease that has lapsed is no holder either.
	if _, err := store.Acquire(ctx, "query-group-1", "worker-1", now.Add(2*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	elapseOnRedis(t, store, "query-group-1", time.Minute+time.Second)
	direct := publishScope(t, store, authority, changed.RecordRevision, "worker-1", "view-c", now.Add(63*time.Second))
	if direct.ContentScope != "view-c" || direct.ContentChangePending() {
		t.Fatalf("record after a lapsed lease = %+v, want view-c written directly", direct)
	}
}

func TestAContentChangeWithAMoveIsWrittenDirectly(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	moved := publishScope(t, store, authority, first.RecordRevision, "worker-2", "view-b", now.Add(time.Second))
	if moved.DesiredWorkerID != "worker-2" || moved.ContentScope != "view-b" || moved.ContentChangePending() ||
		moved.AssignmentGeneration != first.AssignmentGeneration+1 {
		t.Fatalf("record after a move = %+v, want worker-2 on view-b directly, generation bumped", moved)
	}
	// The old holder is refused exactly as before: by desired worker, at once.
	if _, err := store.Renew(ctx, lease.Fence, now.Add(2*time.Second), time.Minute); !errors.Is(err, ErrNotDesired) {
		t.Fatalf("Renew() by the moved-away holder = %v, want ErrNotDesired", err)
	}
}

func TestRepublishingTheCurrentScopeCancelsAPendingChange(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	if _, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	pending := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", now.Add(time.Second))
	if !pending.ContentChangePending() {
		t.Fatalf("record = %+v, want a pending change to set up", pending)
	}
	// The same pending scope again changes nothing, not even the revision.
	again := publishScope(t, store, authority, pending.RecordRevision, "worker-1", "view-b", now.Add(2*time.Second))
	if again.RecordRevision != pending.RecordRevision || again.PendingContentScope != "view-b" || !again.EffectiveAt.Equal(pending.EffectiveAt) {
		t.Fatalf("republishing the pending scope = %+v, want the record left alone", again)
	}
	// A later, different scope replaces the pending one without moving the
	// effective time earlier.
	later := publishScope(t, store, authority, again.RecordRevision, "worker-1", "view-c", now.Add(3*time.Second))
	if later.PendingContentScope != "view-c" || later.EffectiveAt.Before(pending.EffectiveAt) || later.RecordRevision != again.RecordRevision+1 {
		t.Fatalf("replacing the pending scope = %+v, want view-c pending no earlier than %v", later, pending.EffectiveAt)
	}
	// Deciding the current scope again cancels the change.
	cancelled := publishScope(t, store, authority, later.RecordRevision, "worker-1", "view-a", now.Add(4*time.Second))
	if cancelled.ContentScope != "view-a" || cancelled.ContentChangePending() || cancelled.RecordRevision != later.RecordRevision+1 {
		t.Fatalf("republishing the current scope = %+v, want the pending change cleared and the revision bumped", cancelled)
	}
}

func TestAnEmptyScopeLeavesTheRecordsScopeAlone(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	// A leader that does not compute scopes publishes exactly as before.
	same := publishScope(t, store, authority, first.RecordRevision, "worker-1", "", now.Add(time.Second))
	if same.ContentScope != "view-a" || same.RecordRevision != first.RecordRevision || same.ContentChangePending() {
		t.Fatalf("publish with an empty scope = %+v, want the record untouched", same)
	}
	// And a record that never had a scope admits every writer, declared
	// scope or not: the comparison binds only once a leader has written one,
	// so a worker that starts declaring before its leader writes scopes is
	// not refused across the whole fleet.
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-2", DesiredWorkerID: "worker-1", PlacementReason: PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Acquire(ctx, "query-group-2", "worker-1", now, time.Minute)
	if err != nil || lease.ContentScope != "" {
		t.Fatalf("Acquire() on a record without a scope = (%+v, %v), want an empty scope", lease, err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-x"); err != nil {
		t.Fatalf("a declared scope against a record that names none = %v, want valid: nothing has been written to compare against", err)
	}
	if err := store.CheckFence(ctx, lease.Fence); err != nil {
		t.Fatalf("CheckFence() without a scope = %v, want valid", err)
	}
}

// The first scope a record ever gets is written directly, live lease or
// not, and binds from then on. A record that named nothing authorized
// nothing in particular -- its fence admitted every content -- so there is
// no old content whose holder a deadline would protect; and the holder is on
// the content the record now names, or it would not be the current
// publication. Written as pending it capped every lease in the fleet once on
// the round the contract started, and each Query Group lost its lease and
// held its output for the last batch bound of it.
func TestTheFirstScopeUnderALiveLeaseIsWrittenDirectlyAndDoesNotCapTheLease(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil || lease.ContentScope != "" {
		t.Fatalf("Acquire() before any scope = (%+v, %v)", lease, err)
	}
	named := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-a", now.Add(time.Second))
	if named.ContentScope != "view-a" || named.ContentChangePending() || named.RecordRevision != first.RecordRevision+1 {
		t.Fatalf("first scope under a live lease = %+v, want view-a written directly with nothing pending", named)
	}
	// The lease is not capped: a renewal for two minutes gets two minutes.
	renewed, err := store.Renew(ctx, lease.Fence, now.Add(2*time.Second), 2*time.Minute)
	if err != nil || renewed.ContentChangePending() || !renewed.Deadline.Equal(now.Add(2*time.Second+2*time.Minute)) ||
		renewed.ContentScope != "view-a" {
		t.Fatalf("Renew() after the first scope = (%+v, %v), want the full two minutes on view-a with nothing pending", renewed, err)
	}
	// And the record binds from now: the named content passes, another is
	// refused, an undeclared write is still admitted.
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-a) = %v, want valid", err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-anything-else"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("CheckFenceForContentScope(view-anything-else) = %v, want ErrContentScopeMoved", err)
	}
	if err := store.CheckFence(ctx, lease.Fence); err != nil {
		t.Fatalf("CheckFence() without a scope = %v, want valid", err)
	}
	// A pending scope counts only with its effective time: the pair the
	// promotion reads. A scope written alone is neither pending nor admitted.
	if err := store.client.HSet(ctx, store.assignmentKey("query-group-1"), "pending_content_scope", "view-orphan").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-orphan"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("a pending scope without an effective time was admitted: %v", err)
	}
}

// Both conditions false at once: the lease is gone and the scope has moved.
// The answer has to be the lease, because CONTENT_MOVED is read everywhere
// as "the lease is good, re-read" and is kept out of IsLeaseDecision for
// that reason. This is the reachable production shape: worker-1's lease
// lapses, the leader sees no live holder and writes the new scope directly,
// and worker-1 comes back with a dead lease and the old scope in hand.
func TestADeadLeaseWithAMovedScopeIsReportedAsTheLeaseNotTheScope(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The lease lapses; the leader, seeing no live holder, moves the scope
	// directly.
	elapseOnRedis(t, store, "query-group-1", time.Minute+time.Second)
	lapsed := now.Add(time.Minute + time.Second)
	moved := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", lapsed)
	if moved.ContentScope != "view-b" || moved.ContentChangePending() {
		t.Fatalf("record after the lapse = %+v, want view-b written directly", moved)
	}
	for _, declared := range []string{"view-a", "view-b", ""} {
		err := store.CheckFenceForContentScope(ctx, lease.Fence, declared)
		if !errors.Is(err, ErrStaleFence) {
			t.Fatalf("dead lease, declared %q: CheckFenceForContentScope() = %v, want ErrStaleFence", declared, err)
		}
		if errors.Is(err, ErrContentScopeMoved) {
			t.Fatalf("dead lease, declared %q: reported as a moved scope; the lost lease would go unreported", declared)
		}
	}
	status, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: lease.Fence, Namespace: "progress", ExpectedMissing: true, Value: []byte("p1"), ContentScope: "view-a",
	})
	if status != FencedCASStaleOwner || !errors.Is(err, ErrStaleFence) {
		t.Fatalf("FencedCompareAndSet() on a dead lease with a moved scope = (%s, %v), want STALE_OWNER", status, err)
	}
	// The same with the lease held by someone else: worker-2 is desired now
	// and holds it; worker-1's old fence with the old scope is STALE by the
	// lease, not moved by the scope -- and NOT_DESIRED comes before both.
	next, err := store.Acquire(ctx, "query-group-1", "worker-1", lapsed, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("superseded epoch with a moved scope: CheckFenceForContentScope() = %v, want ErrStaleFence", err)
	}
	if err := store.CheckFenceForContentScope(ctx, next.Fence, "view-a"); !errors.Is(err, ErrContentScopeMoved) {
		t.Fatalf("live lease with a moved scope: CheckFenceForContentScope() = %v, want ErrContentScopeMoved", err)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-2", ExpectedRecordRevision: moved.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: lapsed.Add(2 * time.Second), ContentScope: "view-c",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckFenceForContentScope(ctx, next.Fence, "view-b"); !errors.Is(err, ErrNotDesired) {
		t.Fatalf("not desired with a moved scope: CheckFenceForContentScope() = %v, want ErrNotDesired", err)
	}
}

// Withdrawing the content scope is the contract's rollback and what a leader
// writes when a worker that does not take part joins: it clears the scope
// and any pending change at once, because a comparison that is no longer
// made refuses nobody and needs no deadline; it bumps the revision when it
// changed anything and not when there was nothing to clear; and it goes with
// a move too, so a moved Query Group carries no stale scope along.
func TestWithdrawingTheContentScopeClearsItAtOnce(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pending := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", now.Add(time.Second))
	if !pending.ContentChangePending() {
		t.Fatalf("record = %+v, want a pending change to withdraw", pending)
	}
	withdraw := func(revision uint64, worker string) AssignmentRecord {
		record, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
			QueryGroup: "query-group-1", DesiredWorkerID: worker, ExpectedRecordRevision: revision,
			PlacementReason: PlacementRendezvous, DecidedAt: now.Add(2 * time.Second), WithdrawContentScope: true,
		})
		if err != nil {
			t.Fatalf("PublishAssignment(withdraw) error = %v", err)
		}
		return record
	}
	cleared := withdraw(pending.RecordRevision, "worker-1")
	if cleared.ContentScope != "" || cleared.ContentChangePending() || cleared.RecordRevision != pending.RecordRevision+1 ||
		cleared.DesiredWorkerID != "worker-1" || cleared.AssignmentGeneration != pending.AssignmentGeneration {
		t.Fatalf("withdrawn record = %+v, want no scope, nothing pending, revision bumped once, placement untouched", cleared)
	}
	// The live holder was not waited for: every declared scope is admitted
	// again at once, and so is an undeclared write.
	for _, declared := range []string{"view-a", "view-b", "view-anything", ""} {
		if err := store.CheckFenceForContentScope(ctx, lease.Fence, declared); err != nil {
			t.Fatalf("after the withdrawal, declared %q: CheckFenceForContentScope() = %v, want valid", declared, err)
		}
	}
	// Withdrawing again changes nothing, not even the revision.
	again := withdraw(cleared.RecordRevision, "worker-1")
	if again.RecordRevision != cleared.RecordRevision {
		t.Fatalf("a second withdrawal bumped the revision: %d -> %d", cleared.RecordRevision, again.RecordRevision)
	}
	// A withdrawal that moves the owner clears the scope the move would
	// otherwise carry along.
	// After a withdrawal the record names nothing again, so the next scope
	// is a first scope and is written directly; a change on top of it under
	// the live lease is pending, and that is what the move clears.
	named := publishScope(t, store, authority, again.RecordRevision, "worker-1", "view-c", now.Add(3*time.Second))
	if named.ContentScope != "view-c" || named.ContentChangePending() {
		t.Fatalf("record = %+v, want view-c written directly as a first scope", named)
	}
	named = publishScope(t, store, authority, named.RecordRevision, "worker-1", "view-d", now.Add(4*time.Second))
	if named.ContentScope != "view-c" || named.PendingContentScope != "view-d" {
		t.Fatalf("record = %+v, want view-d pending under the live lease", named)
	}
	moved := withdraw(named.RecordRevision, "worker-2")
	if moved.DesiredWorkerID != "worker-2" || moved.ContentScope != "" || moved.ContentChangePending() ||
		moved.AssignmentGeneration != named.AssignmentGeneration+1 {
		t.Fatalf("withdrawal with a move = %+v, want worker-2 with no scope and the generation bumped", moved)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-2", ExpectedRecordRevision: moved.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now, ContentScope: "view-d", WithdrawContentScope: true,
	}); err == nil {
		t.Fatal("a decision that both names and withdraws a scope was accepted")
	}
}
