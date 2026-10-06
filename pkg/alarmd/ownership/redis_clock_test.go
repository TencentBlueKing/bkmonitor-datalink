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

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// decision-016, batch 1b: every deadline a fenced script mints and every
// expiry it judges is on Redis's clock. A real redis-server has no clock a
// test can set, so "time passed" is produced the only way the fence can
// tell it happened: the absolute server instants a record holds are moved
// back. The tests below pin that the callers' instants take no part.

// elapseOnRedis makes one identity's records look as they would after d had
// passed on Redis's clock: the lease deadline in its ownership hash and a
// pending change's effective time in its assignment hash are moved back by
// d. Nothing else is touched -- not the epoch, token, owner or scope -- so
// what the fence sees afterwards is exactly a record d older, and nothing
// a passing d would not have produced.
func elapseOnRedis(t *testing.T, store *RedisStore, identity execution.QueryGroupIdentity, d time.Duration) {
	t.Helper()
	ctx := context.Background()
	for _, field := range []struct{ key, name string }{
		{key: store.ownershipKey(identity), name: "deadline_ms"},
		{key: store.assignmentKey(identity), name: "effective_at_ms"},
	} {
		present, err := store.client.HExists(ctx, field.key, field.name).Result()
		if err != nil {
			t.Fatalf("HEXISTS %s %s: %v", field.key, field.name, err)
		}
		if !present {
			continue
		}
		if err := store.client.HIncrBy(ctx, field.key, field.name, -d.Milliseconds()).Err(); err != nil {
			t.Fatalf("HINCRBY %s %s: %v", field.key, field.name, err)
		}
	}
}

// serverDeadline reads the lease deadline as Redis holds it, on Redis's
// clock: the instant a pending content change is measured from.
func serverDeadline(t *testing.T, store *RedisStore, identity execution.QueryGroupIdentity) time.Time {
	t.Helper()
	millis, err := store.client.HGet(context.Background(), store.ownershipKey(identity), "deadline_ms").Int64()
	if err != nil {
		t.Fatalf("HGET deadline_ms: %v", err)
	}
	return time.UnixMilli(millis)
}

// serverNow is Redis's own reading of its clock, the one the fence uses,
// at the fence's resolution: whole milliseconds, rounded down.
func serverNow(t *testing.T, store *RedisStore) time.Time {
	t.Helper()
	now, err := store.client.Time(context.Background()).Result()
	if err != nil {
		t.Fatalf("TIME: %v", err)
	}
	return now.Truncate(time.Millisecond)
}

// The caller's instant is an anchor for the reply and nothing more. A lease
// asked for at an instant three years in the past is a lease for one minute
// from now on the server, and reads as one minute from the anchor on the
// caller's clock; a renewal anchored an hour ahead is not an hour longer on
// the server, and a check made from any instant at all sees the lease the
// server sees.
func TestTheFenceJudgesExpiryOnRedisClockNotTheCallers(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	anchor := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", anchor, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", PlacementReason: PlacementRendezvous, DecidedAt: anchor,
	}); err != nil {
		t.Fatal(err)
	}
	before := serverNow(t, store)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", anchor, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !lease.Deadline.Equal(anchor.Add(time.Minute)) {
		t.Fatalf("lease deadline on the caller's clock = %v, want the anchor plus the lifetime, %v", lease.Deadline, anchor.Add(time.Minute))
	}
	minted := serverDeadline(t, store, "query-group-1")
	if minted.Before(before.Add(time.Minute)) || minted.After(serverNow(t, store).Add(time.Minute)) {
		t.Fatalf("deadline minted on the server = %v, want the server's now plus one minute (server now was %v before the call)", minted, before)
	}
	// The anchor is not the caller telling the server what time it is.
	farAhead := anchor.Add(time.Hour)
	if err := store.CheckFence(ctx, lease.Fence); err != nil {
		t.Fatalf("CheckFence() from an instant past the deadline on the caller's clock = %v, want valid: the server's clock decides", err)
	}
	renewed, err := store.Renew(ctx, lease.Fence, farAhead, time.Minute)
	if err != nil || !renewed.Deadline.Equal(farAhead.Add(time.Minute)) {
		t.Fatalf("Renew() anchored an hour ahead = (%+v, %v), want a minute from that anchor on the caller's clock", renewed, err)
	}
	if after := serverDeadline(t, store, "query-group-1"); after.After(serverNow(t, store).Add(time.Minute)) {
		t.Fatalf("a renewal anchored an hour ahead moved the server deadline to %v, want no later than the server's now plus one minute", after)
	}
	// And a lease that has run out on the server is stale from every
	// anchor, including the earliest one the caller ever used.
	elapseOnRedis(t, store, "query-group-1", time.Minute+time.Second)
	for _, at := range []time.Time{anchor, farAhead} {
		if err := store.CheckFence(ctx, lease.Fence); !errors.Is(err, ErrStaleFence) {
			t.Fatalf("CheckFence() anchored at %v after the server deadline passed = %v, want ErrStaleFence", at, err)
		}
		if _, err := store.Renew(ctx, lease.Fence, at, time.Minute); !errors.Is(err, ErrStaleFence) {
			t.Fatalf("Renew() anchored at %v after the server deadline passed = %v, want ErrStaleFence", at, err)
		}
	}
	next, err := store.Acquire(ctx, "query-group-1", "worker-1", anchor, time.Minute)
	if err != nil || next.Fence.OwnerEpoch != lease.Fence.OwnerEpoch+1 {
		t.Fatalf("Acquire() after the server deadline passed = (%+v, %v), want the next epoch", next, err)
	}
}

// Under a pending content change the renewal is capped at the change's
// effective time, which the server holds on its clock; the holder gets both
// the deadline and the effective time on its own clock from one anchor, so
// under the cap they are one instant. Before the cap binds, a renewal is
// uncapped and still names the change.
func TestACappedRenewalPutsTheDeadlineAndTheEffectiveTimeOnOneClock(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	anchor := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", anchor, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", anchor)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", anchor, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	changed := publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", anchor.Add(time.Second))
	if !changed.ContentChangePending() {
		t.Fatalf("record = %+v, want a pending change to set up", changed)
	}
	// Short of the effective time: not capped, change named.
	short, err := store.Renew(ctx, lease.Fence, anchor.Add(2*time.Second), 30*time.Second)
	if err != nil || !short.Deadline.Equal(anchor.Add(32*time.Second)) || short.PendingContentScope != "view-b" ||
		!short.EffectiveAt.After(short.Deadline) {
		t.Fatalf("Renew(30s) under a pending change = (%+v, %v), want uncapped at %v with the change named and its effective time after the deadline",
			short, err, anchor.Add(32*time.Second))
	}
	// Past it: capped there, both instants one.
	capped, err := store.Renew(ctx, lease.Fence, anchor.Add(3*time.Second), 2*time.Minute)
	if err != nil || !capped.Deadline.Equal(capped.EffectiveAt) || capped.PendingContentScope != "view-b" {
		t.Fatalf("Renew(2m) under a pending change = (%+v, %v), want the deadline capped at the effective time", capped, err)
	}
	remaining := capped.Deadline.Sub(anchor.Add(3 * time.Second))
	if remaining <= time.Minute || remaining > time.Minute+ContentSwitchMargin {
		t.Fatalf("capped remaining = %v, want within (1m, 1m+%v]: the original lease deadline plus the margin, less what really elapsed", remaining, ContentSwitchMargin)
	}
	if minted := serverDeadline(t, store, "query-group-1"); !minted.Equal(changed.EffectiveAt) {
		t.Fatalf("server deadline after the capped renewal = %v, want the effective time %v", minted, changed.EffectiveAt)
	}
}

// The probe is what readiness runs: the fence's own sequence, TIME then a
// write, on a key that is never created. It reads the server's clock.
func TestProbeFenceClockReadsTheServersClockAndLeavesNoKey(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	before := serverNow(t, store)
	read, err := ProbeFenceClock(ctx, store.client, store.prefix)
	if err != nil {
		t.Fatalf("ProbeFenceClock() error = %v", err)
	}
	if read.Before(before) || read.After(serverNow(t, store)) {
		t.Fatalf("ProbeFenceClock() = %v, want between the server's readings %v and now", read, before)
	}
	if exists := store.client.Exists(ctx, store.prefix+":fence-clock-probe").Val(); exists != 0 {
		t.Fatal("the probe created its key")
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping() = %v, want the probe to pass on this server", err)
	}
	if _, err := ProbeFenceClock(ctx, nil, store.prefix); err == nil {
		t.Fatal("ProbeFenceClock(nil client) = nil, want an error")
	}
	if _, err := ProbeFenceClock(ctx, redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), store.prefix); err == nil {
		t.Fatal("ProbeFenceClock(unreachable server) = nil, want an error")
	}
}
