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

// lapseWorkerSession puts the session's deadline in the server's past, as if
// its TTL had run out without a renewal.
func lapseWorkerSession(t *testing.T, store *RedisStore, workerID string) {
	t.Helper()
	if err := store.client.HSet(context.Background(), store.workerSessionKey(workerID), "deadline_ms", "1").Err(); err != nil {
		t.Fatal(err)
	}
}

// A worker id has one live session at a time; a session that lapsed is never
// renewed, and the next one gets a larger epoch. Released, the next is granted
// at once.
func TestAWorkerSessionIsOneAtATimeAndALapsedOneStaysLapsed(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now()
	first, err := store.AcquireWorkerSession(ctx, "worker-1", now, time.Minute)
	if err != nil || first.Epoch == 0 || first.Token == "" || !first.Deadline.After(now) {
		t.Fatalf("AcquireWorkerSession() = (%+v, %v)", first, err)
	}
	if _, err := store.AcquireWorkerSession(ctx, "worker-1", now, time.Minute); !errors.Is(err, ErrLeaseBusy) {
		t.Fatalf("second acquisition while alive = %v, want ErrLeaseBusy", err)
	}
	renewed, err := store.RenewWorkerSession(ctx, first, time.Now(), time.Minute)
	if err != nil || renewed.Epoch != first.Epoch || renewed.Deadline.Before(first.Deadline) {
		t.Fatalf("RenewWorkerSession() = (%+v, %v)", renewed, err)
	}
	stolen := first
	stolen.Token = "another-process"
	if _, err := store.RenewWorkerSession(ctx, stolen, time.Now(), time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("renewal with another token = %v, want ErrStaleFence", err)
	}

	lapseWorkerSession(t, store, "worker-1")
	if _, err := store.RenewWorkerSession(ctx, first, time.Now(), time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("renewal of a lapsed session = %v, want ErrStaleFence", err)
	}
	second, err := store.AcquireWorkerSession(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil || second.Epoch <= first.Epoch {
		t.Fatalf("acquisition after the lapse = (%+v, %v), want an epoch past %d", second, err, first.Epoch)
	}
	impostor := second
	impostor.Token = "another-process"
	if err := store.ReleaseWorkerSession(ctx, impostor); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("release with the epoch but another token = %v, want ErrStaleFence", err)
	}
	if _, err := store.RenewWorkerSession(ctx, second, time.Now(), time.Minute); err != nil {
		t.Fatalf("the session after a refused release = %v, want it still renewable", err)
	}
	if err := store.ReleaseWorkerSession(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseWorkerSession(ctx, first); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("release of an older session = %v, want ErrStaleFence", err)
	}
	third, err := store.AcquireWorkerSession(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil || third.Epoch <= second.Epoch {
		t.Fatalf("acquisition after a release = (%+v, %v), want an epoch past %d", third, err, second.Epoch)
	}
}

// The epoch never comes back to a value it gave: a record that expired, or a
// failover that lost it, starts again from the server's clock, past every
// epoch issued before.
func TestAWorkerSessionEpochDoesNotGoBackWhenItsRecordIsGone(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	first, err := store.AcquireWorkerSession(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.client.Del(ctx, store.workerSessionKey("worker-1")).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // fixed-wait: the next epoch is read from the server's clock in milliseconds
	second, err := store.AcquireWorkerSession(ctx, "worker-1", time.Now(), time.Minute)
	if err != nil || second.Epoch <= first.Epoch {
		t.Fatalf("acquisition after the record was gone = (%+v, %v), want an epoch past %d", second, err, first.Epoch)
	}
	ttl, err := store.client.PTTL(ctx, store.workerSessionKey("worker-1")).Result()
	if err != nil || ttl <= time.Minute || ttl > workerSessionRetention {
		t.Fatalf("record retention = %v (%v), want it kept past the session for up to %v", ttl, err, workerSessionRetention)
	}
}

// The Control Leader reads which sessions are proven to have lapsed: all up
// to a lapsed record's epoch, all before a live record's, none for a record
// that is not there.
func TestTheLeaderReadsWhichWorkerSessionsHaveLapsed(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	live, err := store.AcquireWorkerSession(ctx, "worker-live", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	lapsed, err := store.AcquireWorkerSession(ctx, "worker-lapsed", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	lapseWorkerSession(t, store, "worker-lapsed")
	facts, err := store.ReadWorkerSessions(ctx, []string{"worker-live", "worker-lapsed", "worker-absent"})
	if err != nil {
		t.Fatal(err)
	}
	if through, ok := facts["worker-live"].LapsedThrough(); !ok || through != live.Epoch-1 {
		t.Fatalf("live session lapsed through (%d, %t), want every epoch before %d", through, ok, live.Epoch)
	}
	if through, ok := facts["worker-lapsed"].LapsedThrough(); !ok || through != lapsed.Epoch {
		t.Fatalf("lapsed session lapsed through (%d, %t), want through %d", through, ok, lapsed.Epoch)
	}
	if fact := facts["worker-absent"]; fact.Present {
		t.Fatalf("absent session read as %+v", fact)
	} else if _, ok := fact.LapsedThrough(); ok {
		t.Fatal("an absent session proved a lapse")
	}
}
