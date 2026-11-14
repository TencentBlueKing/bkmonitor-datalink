// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.
package ownership

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// holdUnderSession makes the lease of handoverQueryGroup one held under a
// worker session, as an acquisition under A2 leaves it: the holder's session
// epoch and the lease TTL it holds by, and no deadline of its own.
func holdUnderSession(t *testing.T, store *RedisStore, session WorkerSession, ttl time.Duration) {
	t.Helper()
	ctx := context.Background()
	key := store.ownershipKey(handoverQueryGroup)
	if err := store.client.HSet(ctx, key, "owner_session_epoch", strconv.FormatUint(session.Epoch, 10),
		"owner_lease_ttl_ms", strconv.FormatInt(ttl.Milliseconds(), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.client.HDel(ctx, key, "deadline_ms").Err(); err != nil {
		t.Fatal(err)
	}
}

func publishMove(t *testing.T, store *RedisStore, authority PublicationAuthority, revision uint64, holder, worker string, at time.Time) AssignmentRecord {
	t.Helper()
	moved, err := store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: handoverQueryGroup, DesiredWorkerID: worker, HolderWorkerID: holder, ExpectedRecordRevision: revision,
		PlacementReason: PlacementRebalance, DecidedAt: at,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(%s) error = %v", worker, err)
	}
	return moved
}

// A publication writes the Leader's lease TTL to the record, for the next
// owner to hold its lease by.
func TestAPublicationCarriesTheLeadersLeaseTTL(t *testing.T) {
	store := newIntegrationStore(t)
	if err := store.ConfigureLeaseTTL(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	handoverStart(t, store, time.Now())
	if value, ok := assignmentField(t, store, "lease_ttl_ms"); !ok || value != "30000" {
		t.Fatalf("lease_ttl_ms = (%q, %t), want 30000", value, ok)
	}
}

// A move away from a lease held under a session waits out a grace only while
// that holder may still be acting: a session proven gone hands the Query
// Group over at once; a live one, a session record that cannot be read, or a
// holder other than the one the Leader expected each get the grace, which
// ends one lease TTL and the margin from the move.
func TestAMoveAwayFromASessionHolderWaitsOnlyWhileItMayStillAct(t *testing.T) {
	const ttl = 20 * time.Second
	for _, holder := range []struct {
		name     string
		prepare  func(t *testing.T, store *RedisStore, session WorkerSession)
		expected string
		grace    bool
	}{
		{"its session lapsed", func(t *testing.T, store *RedisStore, session WorkerSession) {
			if err := store.ReleaseWorkerSession(context.Background(), session); err != nil {
				t.Fatal(err)
			}
		}, "worker-old", false},
		{"its session is alive", func(*testing.T, *RedisStore, WorkerSession) {}, "worker-old", true},
		{"its session record is gone", func(t *testing.T, store *RedisStore, _ WorkerSession) {
			if err := store.client.Del(context.Background(), store.workerSessionKey("worker-old")).Err(); err != nil {
				t.Fatal(err)
			}
		}, "worker-old", true},
		{"another holder was expected", func(t *testing.T, store *RedisStore, session WorkerSession) {
			if err := store.ReleaseWorkerSession(context.Background(), session); err != nil {
				t.Fatal(err)
			}
		}, "worker-other", true},
	} {
		t.Run(holder.name, func(t *testing.T) {
			store := newIntegrationStore(t)
			now := time.Now()
			authority, first, _ := handoverStart(t, store, now)
			session, err := store.AcquireWorkerSession(context.Background(), "worker-old", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			holdUnderSession(t, store, session, ttl)
			holder.prepare(t, store, session)
			before := serverNow(t, store)
			publishMove(t, store, authority, first.RecordRevision, holder.expected, "worker-new", now)
			releasing, draining := assignmentField(t, store, "releasing_worker_id")
			if draining != holder.grace {
				t.Fatalf("releasing_worker_id = (%q, %t), want a grace %t", releasing, draining, holder.grace)
			}
			if !holder.grace {
				return
			}
			effective, _ := assignmentField(t, store, "effective_at_ms")
			at, _ := strconv.ParseInt(effective, 10, 64)
			if floor := before.Add(ttl + ContentSwitchMargin).UnixMilli(); at < floor || at > floor+5_000 {
				t.Fatalf("grace ends at %d, want one TTL and the margin from the move, %d", at, floor)
			}
		})
	}
}

// A grace never ends before a deadline the lease still carries. A lease held
// under a session has none of its own, but one can be left beside the session
// fields -- by an acquisition of a version that knows nothing of them, during
// a rolling release -- and its holder acts until it: a grace of one TTL from
// the move would hand the Query Group over while it still may.
func TestAGraceIsNeverShorterThanADeadlineTheLeaseStillCarries(t *testing.T) {
	const ttl = 20 * time.Second
	store := newIntegrationStore(t)
	now := time.Now()
	authority, first, _ := handoverStart(t, store, now)
	session, err := store.AcquireWorkerSession(context.Background(), "worker-old", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holdUnderSession(t, store, session, ttl)
	deadline := serverNow(t, store).Add(2 * time.Minute)
	if err := store.client.HSet(context.Background(), store.ownershipKey(handoverQueryGroup), "deadline_ms",
		strconv.FormatInt(deadline.UnixMilli(), 10)).Err(); err != nil {
		t.Fatal(err)
	}
	publishMove(t, store, authority, first.RecordRevision, "worker-old", "worker-new", now)
	effective, _ := assignmentField(t, store, "effective_at_ms")
	at, _ := strconv.ParseInt(effective, 10, 64)
	if want := deadline.Add(ContentSwitchMargin).UnixMilli(); at != want {
		t.Fatalf("grace ends at %d, want the deadline the lease carries and the margin, %d", at, want)
	}
}

// A content change under a session holder proven gone is written directly:
// there is no holder of the old content to protect.
func TestAContentChangeUnderAGoneSessionHolderIsWrittenDirectly(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now()
	authority, first, _ := handoverStart(t, store, now)
	scoped, err := store.PublishAssignment(ctx, authority, AssignmentDecision{QueryGroup: handoverQueryGroup, DesiredWorkerID: "worker-old",
		ExpectedRecordRevision: first.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now, ContentScope: "scope-a"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.AcquireWorkerSession(ctx, "worker-old", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holdUnderSession(t, store, session, 20*time.Second)
	if err := store.ReleaseWorkerSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{QueryGroup: handoverQueryGroup, DesiredWorkerID: "worker-old",
		ExpectedRecordRevision: scoped.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now, ContentScope: "scope-b"}); err != nil {
		t.Fatal(err)
	}
	if scope, _ := assignmentField(t, store, "content_scope"); scope != "scope-b" {
		t.Fatalf("content_scope = %q, want the new scope written directly", scope)
	}
	if pending, ok := assignmentField(t, store, "pending_content_scope"); ok {
		t.Fatalf("pending_content_scope = %q, want none", pending)
	}
}

// holdQueryGroupUnderSession is holdUnderSession for any Query Group.
func holdQueryGroupUnderSession(t *testing.T, store *RedisStore, queryGroup execution.QueryGroupIdentity, session WorkerSession) {
	t.Helper()
	ctx := context.Background()
	key := store.ownershipKey(queryGroup)
	if err := store.client.HSet(ctx, key, "owner_session_epoch", strconv.FormatUint(session.Epoch, 10), "owner_lease_ttl_ms", "20000").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.client.HDel(ctx, key, "deadline_ms").Err(); err != nil {
		t.Fatal(err)
	}
}

// The sweep keeps a retired Query Group whose holder's session may still be
// acting, and reclaims one whose holder's session is proven gone: a session
// holder has no deadline of its own to lapse.
func TestASweepReclaimsASessionHeldQueryGroupOnlyOnceItsSessionIsGone(t *testing.T) {
	store := newIntegrationStore(t)
	if err := store.ConfigureEpochRetention(time.Hour); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, queryGroup := range []execution.QueryGroupIdentity{"qg-alive", "qg-gone"} {
		worker := "worker-" + string(queryGroup)
		if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{QueryGroup: queryGroup, DesiredWorkerID: worker,
			PlacementReason: PlacementRendezvous, DecidedAt: now}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Acquire(ctx, queryGroup, worker, now, time.Minute); err != nil {
			t.Fatal(err)
		}
		session, err := store.AcquireWorkerSession(ctx, worker, now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		holdQueryGroupUnderSession(t, store, queryGroup, session)
		if queryGroup == "qg-gone" {
			if err := store.ReleaseWorkerSession(ctx, session); err != nil {
				t.Fatal(err)
			}
		}
	}
	sweep, err := store.SweepAssignments(ctx, authority, map[execution.QueryGroupIdentity]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Retired != 2 || sweep.Reclaimed != 1 || sweep.HeldByLease != 1 {
		t.Fatalf("sweep = %+v, want the gone holder's Query Group reclaimed and the live one held", sweep)
	}
	if _, ok, err := store.ReadQueryGroupOwner(ctx, "qg-alive"); err != nil || !ok {
		t.Fatalf("owner of the held Query Group = (%t, %v), want its live session holder", ok, err)
	}
}

// The owner read routes to a session holder while its session is alive, with
// the session's deadline, and to nobody once the session is gone.
func TestTheOwnerReadOfASessionHolderFollowsItsSession(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now()
	handoverStart(t, store, now)
	session, err := store.AcquireWorkerSession(ctx, "worker-old", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holdUnderSession(t, store, session, 20*time.Second)
	owner, ok, err := store.ReadQueryGroupOwner(ctx, handoverQueryGroup)
	if err != nil || !ok || owner.OwnerID != "worker-old" || !owner.Deadline.After(owner.ObservedAt) {
		t.Fatalf("owner under a live session = (%+v, %t, %v), want worker-old until its session's deadline", owner, ok, err)
	}
	if err := store.ReleaseWorkerSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ReadQueryGroupOwner(ctx, handoverQueryGroup); err != nil || ok {
		t.Fatalf("owner once the session is gone = (%t, %v), want none", ok, err)
	}
	// The worker has a session again, a newer one: the lease was taken under
	// the old one, and its holder is still gone.
	newer, err := store.AcquireWorkerSession(ctx, "worker-old", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ReadQueryGroupOwner(ctx, handoverQueryGroup); err != nil || ok {
		t.Fatalf("owner under a session older than the worker's live one = (%t, %v), want none", ok, err)
	}
	// The live one only one epoch on, as a session taken again within the
	// same millisecond is: the lease's session is still the one before it.
	if err := store.client.HSet(ctx, store.workerSessionKey("worker-old"), "session_epoch",
		strconv.FormatUint(session.Epoch+1, 10)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ReadQueryGroupOwner(ctx, handoverQueryGroup); err != nil || ok {
		t.Fatalf("owner under the session just before the worker's live one = (%t, %v), want none", ok, err)
	}
	if err := store.client.HSet(ctx, store.workerSessionKey("worker-old"), "session_epoch",
		strconv.FormatUint(newer.Epoch, 10)).Err(); err != nil {
		t.Fatal(err)
	}
	holdUnderSession(t, store, newer, 20*time.Second)
	if owner, ok, err := store.ReadQueryGroupOwner(ctx, handoverQueryGroup); err != nil || !ok || owner.OwnerID != "worker-old" {
		t.Fatalf("owner under the worker's live session = (%+v, %t, %v), want worker-old", owner, ok, err)
	}
}

// A publication says what held the lease it was published over: a holder
// under a worker session, one renewing its own lease, or none.
func TestAPublicationSaysWhatHeldTheLease(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now()
	authority, first, _ := handoverStart(t, store, now)
	_, kind, err := store.PublishAssignmentReporting(ctx, authority, AssignmentDecision{QueryGroup: handoverQueryGroup,
		DesiredWorkerID: "worker-old", ExpectedRecordRevision: first.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now})
	if err != nil || kind != HolderRenewing {
		t.Fatalf("over a renewing holder = (%q, %v), want %q", kind, err, HolderRenewing)
	}
	session, err := store.AcquireWorkerSession(ctx, "worker-old", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holdUnderSession(t, store, session, 20*time.Second)
	current, err := store.ReadAssignment(ctx, handoverQueryGroup)
	if err != nil {
		t.Fatal(err)
	}
	if _, kind, err = store.PublishAssignmentReporting(ctx, authority, AssignmentDecision{QueryGroup: handoverQueryGroup,
		DesiredWorkerID: "worker-old", ExpectedRecordRevision: current.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now}); err != nil || kind != HolderUnderSession {
		t.Fatalf("over a session holder = (%q, %v), want %q", kind, err, HolderUnderSession)
	}
	if err := store.client.HDel(ctx, store.ownershipKey(handoverQueryGroup), "owner_id").Err(); err != nil {
		t.Fatal(err)
	}
	if _, kind, err = store.PublishAssignmentReporting(ctx, authority, AssignmentDecision{QueryGroup: handoverQueryGroup,
		DesiredWorkerID: "worker-old", ExpectedRecordRevision: current.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now}); err != nil || kind != "" {
		t.Fatalf("over no holder = (%q, %v), want none", kind, err)
	}
}

// A sweep reads a Query Group's holder before its script runs, and the holder
// may change in between. The script then holds the Query Group rather than
// judge the lease by the session of a worker that no longer holds it: here the
// worker read lapsed, while the one holding now took its session before that,
// so its epoch is below what the read worker's lapse proves gone.
func TestASweepHoldsAQueryGroupWhoseHolderChangedSinceItWasRead(t *testing.T) {
	store := newIntegrationStore(t)
	if err := store.ConfigureEpochRetention(time.Hour); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	const queryGroup = execution.QueryGroupIdentity("qg-moved")
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{QueryGroup: queryGroup, DesiredWorkerID: "worker-now",
		PlacementReason: PlacementRendezvous, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(ctx, queryGroup, "worker-now", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	holding, err := store.AcquireWorkerSession(ctx, "worker-now", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holdQueryGroupUnderSession(t, store, queryGroup, holding)
	// Taken, let go and taken again, so its epoch is above the holder's even
	// within one millisecond.
	read, err := store.AcquireWorkerSession(ctx, "worker-read", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseWorkerSession(ctx, read); err != nil {
		t.Fatal(err)
	}
	if read, err = store.AcquireWorkerSession(ctx, "worker-read", time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if read.Epoch <= holding.Epoch {
		t.Fatalf("the read worker's session epoch %d is not above the holder's %d; the case needs it above", read.Epoch, holding.Epoch)
	}
	if err := store.ReleaseWorkerSession(ctx, read); err != nil {
		t.Fatal(err)
	}
	result, err := reclaimAssignmentScript.Run(ctx, store.client,
		[]string{store.ownershipKey(ControlLeaderIdentity), store.assignmentKey(queryGroup), store.ownershipKey(queryGroup), store.workerSessionKey("worker-read")},
		authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken, string(queryGroup),
		store.epochRetention.Milliseconds(), "worker-read").Text()
	if err != nil || result != "HELD" {
		t.Fatalf("reclaim with the holder changed since the read = (%q, %v), want HELD", result, err)
	}
}
