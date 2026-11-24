// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The Control Leader's hash is written only under its live lease: sets and
// deletes land together and renew the hash, and once the lease lapsed and
// another replica took the term, the old leader's write is refused whole
// and the new one's lands.
func TestFencedHashUpdateWritesOnlyUnderTheLiveControlLease(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	old, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "absent_close_memory"
	if err := store.FencedHashUpdate(ctx, FencedHashRequest{Fence: old.Fence, Namespace: namespace,
		Set: map[string][]byte{"a": []byte("1"), "b": []byte("2")}, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := store.FencedHashUpdate(ctx, FencedHashRequest{Fence: old.Fence, Namespace: namespace,
		Set: map[string][]byte{"c": []byte("3")}, Delete: []string{"a"}, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	fields, err := store.ReadControlHash(ctx, ControlLeaderIdentity, namespace)
	if err != nil || len(fields) != 2 || fields["b"] != "2" || fields["c"] != "3" {
		t.Fatalf("hash %v (%v), want b and c", fields, err)
	}
	if ttl := store.client.PTTL(ctx, store.controlKey(ControlLeaderIdentity, namespace)).Val(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl %v, want the hash renewed to an hour", ttl)
	}

	elapseOnRedis(t, store, ControlLeaderIdentity, 2*time.Minute)
	next, err := store.AcquireControlLeader(ctx, "control-2", now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FencedHashUpdate(ctx, FencedHashRequest{Fence: old.Fence, Namespace: namespace,
		Set: map[string][]byte{"b": []byte("stale")}, Delete: []string{"c"}}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("the old leader's write = %v, want ErrStaleFence", err)
	}
	if err := store.FencedHashUpdate(ctx, FencedHashRequest{Fence: next.Fence, Namespace: namespace,
		Set: map[string][]byte{"d": []byte("4")}}); err != nil {
		t.Fatal(err)
	}
	fields, _ = store.ReadControlHash(ctx, ControlLeaderIdentity, namespace)
	if len(fields) != 3 || fields["b"] != "2" || fields["c"] != "3" || fields["d"] != "4" {
		t.Fatalf("hash %v: the old leader's write landed, or the new one's did not", fields)
	}
	empty, err := store.ReadControlHash(ctx, ControlLeaderIdentity, "nothing_here")
	if err != nil || len(empty) != 0 {
		t.Fatalf("an absent hash read %v (%v), want empty", empty, err)
	}
	if err := store.FencedHashUpdate(ctx, FencedHashRequest{Fence: next.Fence, Namespace: namespace,
		Delete: make([]string, MaxControlHashWrite+1)}); err == nil {
		t.Fatal("a write past MaxControlHashWrite was taken")
	}
}
