// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A move decided while another worker holds the lease is the leader's
// decision at once -- desired_worker_id names the new worker, so every
// reader of the record sees it -- and the outgoing holder keeps its lease
// until it releases or the lease runs out (design 02 §6.2: the old lease is
// the only valid owner until the handover completes; decision-016: an owner
// change takes effect no earlier than the record's T plus the margin). The
// record names the holder in releasing_worker_id and the instant its grace
// ends in effective_at_ms; the grace skips the desired-worker comparison
// and nothing else, so the holder still passes the owner, epoch, token and
// deadline comparisons on Redis's clock like any writer.

const handoverQueryGroup = execution.QueryGroupIdentity("query-group-1")

func assignmentField(t *testing.T, store *RedisStore, field string) (string, bool) {
	t.Helper()
	value, err := store.client.HGet(context.Background(), store.assignmentKey(handoverQueryGroup), field).Result()
	if errors.Is(err, redis.Nil) {
		return "", false
	}
	if err != nil {
		t.Fatalf("HGET %s: %v", field, err)
	}
	return value, true
}

// lapseLeaseOnRedis ends the lease on Redis's clock and nothing else: the
// deadline is put one millisecond behind the server's now, and the record's
// effective time is left where the move put it, still ahead.
func lapseLeaseOnRedis(t *testing.T, store *RedisStore) {
	t.Helper()
	past := serverNow(t, store).Add(-time.Millisecond).UnixMilli()
	if err := store.client.HSet(context.Background(), store.ownershipKey(handoverQueryGroup), "deadline_ms", past).Err(); err != nil {
		t.Fatalf("HSET deadline_ms: %v", err)
	}
}

// handoverStart is a worker-old lease with one Progress write landed, and
// the leader's authority to move it.
func handoverStart(t *testing.T, store *RedisStore, now time.Time) (PublicationAuthority, AssignmentRecord, Lease) {
	t.Helper()
	ctx := context.Background()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	first, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: handoverQueryGroup, DesiredWorkerID: "worker-old", PlacementReason: PlacementRendezvous, DecidedAt: now,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(old) error = %v", err)
	}
	old, err := store.Acquire(ctx, handoverQueryGroup, "worker-old", now, time.Minute)
	if err != nil {
		t.Fatalf("Acquire(old) error = %v", err)
	}
	if result, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: old.Fence, Namespace: "progress", ExpectedMissing: true, Value: []byte("cursor-1"),
	}); err != nil || result != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(old, first write) = (%s, %v)", result, err)
	}
	return authority, first, old
}

func moveTo(t *testing.T, store *RedisStore, authority PublicationAuthority, revision uint64, worker string, at time.Time) AssignmentRecord {
	t.Helper()
	moved, err := store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: handoverQueryGroup, DesiredWorkerID: worker, ExpectedRecordRevision: revision,
		PlacementReason: PlacementRebalance, DecidedAt: at,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(%s) error = %v", worker, err)
	}
	return moved
}

func casProgress(t *testing.T, store *RedisStore, fence execution.OwnerFence, expected, value string) (FencedCASStatus, error) {
	t.Helper()
	return store.FencedCompareAndSet(context.Background(), FencedCASRequest{
		Fence: fence, Namespace: "progress", Expected: []byte(expected), Value: []byte(value),
	})
}

func progressValue(t *testing.T, store *RedisStore) string {
	t.Helper()
	value, missing, err := store.ReadControl(context.Background(), handoverQueryGroup, "progress")
	if err != nil || missing {
		t.Fatalf("ReadControl(progress) = (%q, %t, %v)", value, missing, err)
	}
	return string(value)
}

// The planned handover: the holder finishes its commit boundary under the
// lease it holds, releases, and only then can the new owner take the next
// epoch. The holder's renewal is capped at the end of its grace, never past.
func TestAMovedHolderKeepsItsLeaseUntilItReleases(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, first, old := handoverStart(t, store, now)

	wantEffective := serverDeadline(t, store, handoverQueryGroup).Add(ContentSwitchMargin)
	moved := moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
	if moved.DesiredWorkerID != "worker-new" || moved.AssignmentGeneration != first.AssignmentGeneration+1 {
		t.Fatalf("record after the move = %+v, want worker-new desired at once with the generation bumped", moved)
	}
	if releasing, _ := assignmentField(t, store, "releasing_worker_id"); releasing != "worker-old" {
		t.Fatalf("releasing_worker_id after the move = %q, want the holder worker-old", releasing)
	}
	if effective, _ := assignmentField(t, store, "effective_at_ms"); effective != formatMillis(wantEffective) {
		t.Fatalf("effective_at_ms after the move = %s, want the holder's deadline plus the margin %s", effective, formatMillis(wantEffective))
	}

	// The holder's commit boundary lands.
	if err := store.CheckFence(ctx, old.Fence); err != nil {
		t.Fatalf("CheckFence(holder after the move) = %v, want valid until it releases", err)
	}
	if result, err := casProgress(t, store, old.Fence, "cursor-1", "cursor-old-2"); err != nil || result != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(holder after the move) = (%s, %v), want applied", result, err)
	}
	// Its renewal is capped at the end of the grace: ten minutes asked for,
	// the grace's end written.
	renewed, err := store.Renew(ctx, old.Fence, now.Add(11*time.Second), 10*time.Minute)
	if err != nil {
		t.Fatalf("Renew(holder after the move) = %v, want a capped renewal", err)
	}
	if minted := serverDeadline(t, store, handoverQueryGroup); !minted.Equal(wantEffective) {
		t.Fatalf("server deadline after the holder's renewal = %v, want the grace's end %v", minted, wantEffective)
	}
	if !renewed.Deadline.Before(now.Add(11 * time.Second).Add(10 * time.Minute)) {
		t.Fatalf("Renew(holder) deadline = %v, want it capped well short of the ten minutes asked", renewed.Deadline)
	}
	if _, err := store.Acquire(ctx, handoverQueryGroup, "worker-new", now.Add(12*time.Second), time.Minute); !errors.Is(err, ErrLeaseBusy) {
		t.Fatalf("Acquire(new while the holder drains) = %v, want ErrLeaseBusy", err)
	}

	if err := store.Release(ctx, old.Fence); err != nil {
		t.Fatalf("Release(holder) = %v", err)
	}
	replacement, err := store.Acquire(ctx, handoverQueryGroup, "worker-new", now.Add(13*time.Second), time.Minute)
	if err != nil || replacement.Fence.OwnerEpoch != old.Fence.OwnerEpoch+1 {
		t.Fatalf("Acquire(new after the release) = (%+v, %v), want the next epoch", replacement, err)
	}
	// The grant ends the grace in the same script.
	if releasing, present := assignmentField(t, store, "releasing_worker_id"); present {
		t.Fatalf("releasing_worker_id after the new owner's grant = %q, want it cleared", releasing)
	}
	if effective, present := assignmentField(t, store, "effective_at_ms"); present {
		t.Fatalf("effective_at_ms after the new owner's grant = %s, want it cleared with nothing pending", effective)
	}
	if result, err := casProgress(t, store, old.Fence, "cursor-old-2", "cursor-old-3"); !errors.Is(err, ErrStaleFence) || result != FencedCASStaleOwner {
		t.Fatalf("FencedCompareAndSet(old holder after the takeover) = (%s, %v), want stale owner", result, err)
	}
	if err := store.CheckFence(ctx, old.Fence); err == nil {
		t.Fatal("CheckFence(old holder after the takeover) = valid, want refused")
	}
	if result, err := casProgress(t, store, replacement.Fence, "cursor-old-2", "cursor-2"); err != nil || result != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(new owner) = (%s, %v), want applied over the holder's last write", result, err)
	}
}

// The grace is the desired-worker comparison skipped, not the lease
// extended: a holder whose lease lapsed before the grace's end is refused
// like any lapsed lease, and the new owner may take the next epoch at once.
// No instant has two writers.
func TestAMovedHolderIsRefusedOnceItsLeaseLapsesBeforeTheGraceEnds(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, first, old := handoverStart(t, store, now)
	moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))

	lapseLeaseOnRedis(t, store)
	effectiveText, _ := assignmentField(t, store, "effective_at_ms")
	if effective, _ := strconv.ParseInt(effectiveText, 10, 64); effective <= serverNow(t, store).UnixMilli() {
		t.Fatalf("effective_at_ms = %s, want it still ahead of the server's now for this test to mean anything", effectiveText)
	}
	if err := store.CheckFence(ctx, old.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(holder with a lapsed lease, inside the grace) = %v, want ErrStaleFence", err)
	}
	replacement, err := store.Acquire(ctx, handoverQueryGroup, "worker-new", now.Add(71*time.Second), time.Minute)
	if err != nil || replacement.Fence.OwnerEpoch != old.Fence.OwnerEpoch+1 {
		t.Fatalf("Acquire(new once the lease lapsed, before the grace's end) = (%+v, %v), want the next epoch", replacement, err)
	}
	if result, err := casProgress(t, store, old.Fence, "cursor-1", "cursor-old-2"); !errors.Is(err, ErrStaleFence) || result != FencedCASStaleOwner {
		t.Fatalf("FencedCompareAndSet(old holder after the new grant) = (%s, %v), want stale owner", result, err)
	}
	if got := progressValue(t, store); got != "cursor-1" {
		t.Fatalf("progress after the refused write = %q, want cursor-1 untouched", got)
	}
}

// Renewing again does not move the grace: every renewal of the holder is
// capped at the same instant, and past it the holder is no longer desired.
func TestAMovedHoldersRenewalNeverPassesTheGrace(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, first, old := handoverStart(t, store, now)
	wantEffective := serverDeadline(t, store, handoverQueryGroup).Add(ContentSwitchMargin)
	moved := moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
	for round := 0; round < 3; round++ {
		if _, err := store.Renew(ctx, old.Fence, now.Add(time.Duration(11+round)*time.Second), 10*time.Minute); err != nil {
			t.Fatalf("Renew(holder, round %d) = %v", round, err)
		}
		if minted := serverDeadline(t, store, handoverQueryGroup); !minted.Equal(wantEffective) {
			t.Fatalf("server deadline after renewal %d = %v, want the grace's end %v", round, minted, wantEffective)
		}
	}
	// A second decision for the same new owner does not extend it either.
	moveTo(t, store, authority, moved.RecordRevision, "worker-new", now.Add(20*time.Second))
	if effective, _ := assignmentField(t, store, "effective_at_ms"); effective != formatMillis(wantEffective) {
		t.Fatalf("effective_at_ms after a repeated decision = %s, want %s unchanged", effective, formatMillis(wantEffective))
	}
	// Nor does a decision moving it on to a third worker while the same
	// holder drains.
	again, err := store.ReadAssignment(ctx, handoverQueryGroup)
	if err != nil {
		t.Fatal(err)
	}
	moveTo(t, store, authority, again.RecordRevision, "worker-third", now.Add(21*time.Second))
	if effective, _ := assignmentField(t, store, "effective_at_ms"); effective != formatMillis(wantEffective) {
		t.Fatalf("effective_at_ms after a move on to a third worker = %s, want %s unchanged", effective, formatMillis(wantEffective))
	}
	if releasing, _ := assignmentField(t, store, "releasing_worker_id"); releasing != "worker-old" {
		t.Fatalf("releasing_worker_id after a move on = %q, want the holder that still drains", releasing)
	}
	elapseOnRedis(t, store, handoverQueryGroup, time.Minute+ContentSwitchMargin+time.Second)
	if _, err := store.Renew(ctx, old.Fence, now.Add(2*time.Minute), time.Minute); !errors.Is(err, ErrNotDesired) {
		t.Fatalf("Renew(holder past the grace) = %v, want ErrNotDesired", err)
	}
}

// A move that also changes the content does not refuse the holder its own
// content while it drains: the content goes pending with the same instant.
// The new owner, granted a fresh lease once the holder released, starts on
// the new content and is not capped -- nobody is left on the old content to
// protect.
func TestAMoveWithAContentChangeLeavesTheHolderOnItsContent(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, handoverQueryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantEffective := serverDeadline(t, store, handoverQueryGroup).Add(ContentSwitchMargin)
	moved := publishScope(t, store, authority, first.RecordRevision, "worker-2", "view-b", now.Add(time.Second))
	if moved.DesiredWorkerID != "worker-2" || moved.ContentScope != "view-a" || moved.PendingContentScope != "view-b" ||
		!moved.EffectiveAt.Equal(wantEffective) || moved.AssignmentGeneration != first.AssignmentGeneration+1 {
		t.Fatalf("record after a move with a content change = %+v, want worker-2 desired, view-a current, view-b pending at %v",
			moved, wantEffective)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); err != nil {
		t.Fatalf("CheckFenceForContentScope(holder, view-a) while it drains = %v, want valid", err)
	}
	if err := store.Release(ctx, lease.Fence); err != nil {
		t.Fatal(err)
	}
	next, err := store.Acquire(ctx, handoverQueryGroup, "worker-2", now.Add(2*time.Second), time.Minute)
	if err != nil || next.ContentScope != "view-b" || next.ContentChangePending() {
		t.Fatalf("Acquire(worker-2 after the release) = (%+v, %v), want a lease on view-b with nothing pending", next, err)
	}
	record, err := store.ReadAssignment(ctx, handoverQueryGroup)
	if err != nil || record.ContentScope != "view-b" || record.ContentChangePending() {
		t.Fatalf("record after the grant = (%+v, %v), want view-b current and nothing pending", record, err)
	}
	renewed, err := store.Renew(ctx, next.Fence, now.Add(3*time.Second), time.Minute)
	if err != nil || renewed.ContentChangePending() || !renewed.Deadline.Equal(now.Add(63*time.Second)) {
		t.Fatalf("Renew(worker-2) = (%+v, %v), want an uncapped renewal", renewed, err)
	}
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-a"); err == nil {
		t.Fatal("CheckFenceForContentScope(old holder after the grant) = valid, want refused")
	}
}

// A live holder that is the new decision -- the leader moved the Query Group
// back before the holder let go -- is simply the desired owner again, with
// no grace left behind to name it.
func TestAMoveBackToTheDrainingHolderEndsItsGrace(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, first, old := handoverStart(t, store, now)
	moved := moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
	moveTo(t, store, authority, moved.RecordRevision, "worker-old", now.Add(11*time.Second))
	if releasing, present := assignmentField(t, store, "releasing_worker_id"); present {
		t.Fatalf("releasing_worker_id after a move back = %q, want it cleared", releasing)
	}
	renewed, err := store.Renew(ctx, old.Fence, now.Add(12*time.Second), time.Minute)
	if err != nil || !renewed.Deadline.Equal(now.Add(72*time.Second)) {
		t.Fatalf("Renew(holder after the move back) = (%+v, %v), want an uncapped renewal", renewed, err)
	}
}

// legacyScripts are the fence and scripts of the binary before the grace,
// run with the same keys and arguments the store sends.
type legacyScripts struct {
	store      *RedisStore
	acquire    *redis.Script
	renew      *redis.Script
	checkFence *redis.Script
	publish    *redis.Script
}

func newLegacyScripts(store *RedisStore) legacyScripts {
	return legacyScripts{
		store:      store,
		acquire:    redis.NewScript(legacyFenceLua + legacyAcquireLua),
		renew:      redis.NewScript(legacyFenceLua + legacyRenewLua),
		checkFence: redis.NewScript(legacyFenceLua + legacyCheckFenceLua),
		publish:    redis.NewScript(legacyFenceLua + legacyPublishLua),
	}
}

func (legacy legacyScripts) status(t *testing.T, script *redis.Script, keys []string, args ...interface{}) []interface{} {
	t.Helper()
	result, err := script.Run(context.Background(), legacy.store.client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("legacy script: %v", err)
	}
	return result
}

func (legacy legacyScripts) acquireStatus(t *testing.T, worker string) string {
	t.Helper()
	reply := legacy.status(t, legacy.acquire, []string{
		legacy.store.assignmentKey(handoverQueryGroup), legacy.store.ownershipKey(handoverQueryGroup),
	}, "1", worker, time.Minute.Milliseconds(), "legacy-token-"+worker)
	return scriptText(reply[0])
}

func (legacy legacyScripts) renewStatus(t *testing.T, fence execution.OwnerFence) string {
	t.Helper()
	reply := legacy.status(t, legacy.renew, []string{
		legacy.store.assignmentKey(handoverQueryGroup), legacy.store.ownershipKey(handoverQueryGroup),
	}, "1", fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, time.Minute.Milliseconds())
	return scriptText(reply[0])
}

func (legacy legacyScripts) checkFenceStatus(t *testing.T, fence execution.OwnerFence) string {
	t.Helper()
	reply := legacy.status(t, legacy.checkFence, []string{
		legacy.store.assignmentKey(handoverQueryGroup), legacy.store.ownershipKey(handoverQueryGroup),
	}, "1", fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, "")
	return scriptText(reply[0])
}

func (legacy legacyScripts) move(t *testing.T, authority PublicationAuthority, revision uint64, worker string, at time.Time) {
	t.Helper()
	reply := legacy.status(t, legacy.publish, []string{
		legacy.store.ownershipKey(ControlLeaderIdentity), legacy.store.assignmentKey(handoverQueryGroup),
		legacy.store.ownershipKey(handoverQueryGroup),
	}, authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken,
		revision, string(handoverQueryGroup), worker, string(PlacementRebalance), at.UnixMilli(),
		"", ContentSwitchMargin.Milliseconds(), "0", 0)
	if scriptText(reply[0]) != worker {
		t.Fatalf("legacy publish reply = %v, want %s desired", reply, worker)
	}
}

// Rollout, old leader and new workers: a leader from before the grace
// writes a move the way it always did, with no grace pair, and the holder
// -- on the new fence -- is refused at once, as before. A grace pair a new
// leader wrote earlier does not survive an old leader's move: the old
// script deletes effective_at_ms on every move, and a releasing worker with
// no instant has no grace. Either way the holder's next renewal is refused,
// so its lease ends and the new owner acquires: nothing waits on a lease
// nobody will give up.
func TestAnOldLeadersMoveRefusesTheHolderAtOnce(t *testing.T) {
	t.Run("a move with no grace", func(t *testing.T) {
		store := newIntegrationStore(t)
		ctx := context.Background()
		now := time.UnixMilli(1_700_000_000_000)
		authority, first, old := handoverStart(t, store, now)
		newLegacyScripts(store).move(t, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
		if err := store.CheckFence(ctx, old.Fence); !errors.Is(err, ErrNotDesired) {
			t.Fatalf("CheckFence(holder after an old leader's move) = %v, want ErrNotDesired at once", err)
		}
		if _, err := store.Renew(ctx, old.Fence, now.Add(11*time.Second), time.Minute); !errors.Is(err, ErrNotDesired) {
			t.Fatalf("Renew(holder after an old leader's move) = %v, want ErrNotDesired", err)
		}
		if err := store.Release(ctx, old.Fence); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Acquire(ctx, handoverQueryGroup, "worker-new", now.Add(12*time.Second), time.Minute); err != nil {
			t.Fatalf("Acquire(new after the holder let go) = %v", err)
		}
	})
	t.Run("an old leader moves on from a graced move", func(t *testing.T) {
		store := newIntegrationStore(t)
		ctx := context.Background()
		now := time.UnixMilli(1_700_000_000_000)
		authority, first, old := handoverStart(t, store, now)
		moved := moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
		newLegacyScripts(store).move(t, authority, moved.RecordRevision, "worker-third", now.Add(11*time.Second))
		if _, present := assignmentField(t, store, "effective_at_ms"); present {
			t.Fatal("effective_at_ms after an old leader's move is present; the old script deletes it")
		}
		if err := store.CheckFence(ctx, old.Fence); !errors.Is(err, ErrNotDesired) {
			t.Fatalf("CheckFence(holder named by a grace with no instant) = %v, want ErrNotDesired", err)
		}
		// The holder vanishes without releasing: its lease runs out, and the
		// new owner acquires and clears the leftover name.
		elapseOnRedis(t, store, handoverQueryGroup, time.Minute+time.Second)
		if _, err := store.Acquire(ctx, handoverQueryGroup, "worker-third", now.Add(72*time.Second), time.Minute); err != nil {
			t.Fatalf("Acquire(third once the lease ran out) = %v", err)
		}
		if releasing, present := assignmentField(t, store, "releasing_worker_id"); present {
			t.Fatalf("releasing_worker_id after the grant = %q, want it cleared", releasing)
		}
	})
}

// Rollout, new leader and old workers: the old fence does not know the
// grace pair, so an old holder is refused at once -- today's behaviour --
// and its renewal is refused, so it lets go. An old worker that is the new
// owner waits for that like it always did, and acquires once the holder
// released or its lease ran out.
func TestANewLeadersMoveRefusesAnOldHolderAtOnce(t *testing.T) {
	for _, handover := range []struct {
		name  string
		letGo func(t *testing.T, store *RedisStore, old Lease)
	}{
		{name: "the old holder releases", letGo: func(t *testing.T, store *RedisStore, old Lease) {
			if err := store.Release(context.Background(), old.Fence); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the old holder vanishes", letGo: func(t *testing.T, store *RedisStore, _ Lease) {
			elapseOnRedis(t, store, handoverQueryGroup, time.Minute+ContentSwitchMargin+time.Second)
		}},
	} {
		t.Run(handover.name, func(t *testing.T) {
			store := newIntegrationStore(t)
			now := time.UnixMilli(1_700_000_000_000)
			authority, first, old := handoverStart(t, store, now)
			moveTo(t, store, authority, first.RecordRevision, "worker-new", now.Add(10*time.Second))
			legacy := newLegacyScripts(store)
			if status := legacy.checkFenceStatus(t, old.Fence); status != "NOT_DESIRED" {
				t.Fatalf("old fence check of the holder after a new leader's move = %s, want NOT_DESIRED at once", status)
			}
			if status := legacy.renewStatus(t, old.Fence); status != "NOT_DESIRED" {
				t.Fatalf("old renewal of the holder after a new leader's move = %s, want NOT_DESIRED", status)
			}
			if status := legacy.acquireStatus(t, "worker-new"); status != "BUSY" {
				t.Fatalf("old acquire by the new owner while the holder lives = %s, want BUSY", status)
			}
			handover.letGo(t, store, old)
			if status := legacy.acquireStatus(t, "worker-new"); status != "OWNED" {
				t.Fatalf("old acquire by the new owner after the holder let go = %s, want OWNED", status)
			}
			if got := progressValue(t, store); got != "cursor-1" {
				t.Fatalf("progress = %q, want cursor-1", got)
			}
		})
	}
}

func formatMillis(at time.Time) string {
	return strconv.FormatInt(at.UnixMilli(), 10)
}
